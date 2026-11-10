package store

import (
	"context"
	"fmt"
	"slices"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The dependency area of contract_dependencies.go, on both engines. An edge write locks both
// tasks (in byte order, so two writers of one pair never wait on each other in opposite orders),
// judges the edge and the caches in Go, and writes the edge and the two caches in one transaction.
// No statement here touches a task's version or updated_at: SetTaskCaches writes the two caches
// and nothing else. Each list is one statement, FROM its project or task.

type dependencyQueries interface {
	FindEdge(ctx context.Context, arg sqlitedb.FindEdgeParams) (sqlitedb.Dependency, error)
	ListGatingEdges(ctx context.Context, projectID string) ([]sqlitedb.ListGatingEdgesRow, error)
	InsertEdge(ctx context.Context, arg sqlitedb.InsertEdgeParams) (sqlitedb.Dependency, error)
	DeleteEdge(ctx context.Context, arg sqlitedb.DeleteEdgeParams) (sqlitedb.Dependency, error)
	ListProjectEdges(ctx context.Context, id string) ([]sqlitedb.ListProjectEdgesRow, error)
	ListBlockerEdges(ctx context.Context, arg sqlitedb.ListBlockerEdgesParams) ([]sqlitedb.ListBlockerEdgesRow, error)
	ListDependentEdges(ctx context.Context, arg sqlitedb.ListDependentEdgesParams) ([]sqlitedb.ListDependentEdgesRow, error)
	SetTaskCaches(ctx context.Context, arg sqlitedb.SetTaskCachesParams) (int64, error)
}

func (s *SQLite) AddEdge(ctx context.Context, in AddEdgeInput) (DependencyEdge, error) {
	return addEdge(ctx, s, in)
}

func (s *Postgres) AddEdge(ctx context.Context, in AddEdgeInput) (DependencyEdge, error) {
	return addEdge(ctx, s, in)
}

func (s *SQLite) RemoveEdge(ctx context.Context, projectID, edgeID string) (DependencyEdge, error) {
	return removeEdge(ctx, s, projectID, edgeID)
}

func (s *Postgres) RemoveEdge(ctx context.Context, projectID, edgeID string) (DependencyEdge, error) {
	return removeEdge(ctx, s, projectID, edgeID)
}

func (s *SQLite) ListEdges(ctx context.Context, projectID string) ([]DependencyEdge, error) {
	return listEdges(ctx, s, projectID)
}

func (s *Postgres) ListEdges(ctx context.Context, projectID string) ([]DependencyEdge, error) {
	return listEdges(ctx, s, projectID)
}

func (s *SQLite) ListBlockers(ctx context.Context, projectID, taskID string) ([]DependencyEdge, error) {
	return listBlockers(ctx, s, projectID, taskID)
}

func (s *Postgres) ListBlockers(ctx context.Context, projectID, taskID string) ([]DependencyEdge, error) {
	return listBlockers(ctx, s, projectID, taskID)
}

func (s *SQLite) ListDependents(ctx context.Context, projectID, taskID string) ([]DependencyEdge, error) {
	return listDependents(ctx, s, projectID, taskID)
}

func (s *Postgres) ListDependents(ctx context.Context, projectID, taskID string) ([]DependencyEdge, error) {
	return listDependents(ctx, s, projectID, taskID)
}

func (s *SQLite) SetCaches(ctx context.Context, projectID, taskID string, blockedBy, blocks []string) error {
	return setCaches(ctx, s, projectID, taskID, blockedBy, blocks)
}

func (s *Postgres) SetCaches(ctx context.Context, projectID, taskID string, blockedBy, blocks []string) error {
	return setCaches(ctx, s, projectID, taskID, blockedBy, blocks)
}

// lockPair locks the two tasks of an edge in byte order and returns them; a missing one is
// NotFound, the from task named first.
func lockPair(ctx context.Context, q sqlQueries, projectID, fromID, toID string) (from, to Task, err error) {
	got := map[string]Task{}
	ids := []string{fromID, toID}
	slices.Sort(ids)
	for _, id := range slices.Compact(ids) {
		row, err := q.LockTask(ctx, sqlitedb.LockTaskParams{ProjectID: projectID, ID: id})
		if isNoRows(err) {
			continue
		}
		if err != nil {
			return Task{}, Task{}, fmt.Errorf("store: task: %w", err)
		}
		t, err := taskFromRow(sqlitedb.GetTaskRow(row))
		if err != nil {
			return Task{}, Task{}, err
		}
		got[id] = t
	}
	from, okFrom := got[fromID]
	to, okTo := got[toID]
	switch {
	case !okFrom:
		return Task{}, Task{}, domain.NotFound("Task", fromID)
	case !okTo:
		return Task{}, Task{}, domain.NotFound("Task", toID)
	}
	return from, to, nil
}

// writeCaches writes t's two caches.
func writeCaches(ctx context.Context, q sqlQueries, t Task) error {
	if _, err := q.SetTaskCaches(ctx, sqlitedb.SetTaskCachesParams{BlockedBy: jsonList(t.BlockedBy), Blocks: jsonList(t.Blocks), ProjectID: t.ProjectID, ID: t.ID}); err != nil {
		return fmt.Errorf("store: dependency caches: %w", err)
	}
	return nil
}

func addEdge(ctx context.Context, e engine, in AddEdgeInput) (DependencyEdge, error) {
	if in.Type != DependencyBlocks && in.Type != DependencyRelates {
		return DependencyEdge{}, domain.Validation("unknown dependency type: "+string(in.Type), map[string]any{"type": in.Type})
	}
	if in.CreatedBy == "" {
		return DependencyEdge{}, domain.Validation("a dependency needs its creator", nil)
	}
	var out DependencyEdge
	err := e.Atomically(ctx, func(tx Store) error {
		// The project lock first, before either task row is locked (Locks: project then tasks), so a
		// caller that forgot mutation.LockProject still cannot reopen P-110. It is re-entrant: free
		// when the caller already holds it.
		if err := tx.LockProject(ctx, in.ProjectID); err != nil {
			return err
		}
		q := on(tx).queries(true)
		from, to, err := lockPair(ctx, q, in.ProjectID, in.FromTaskID, in.ToTaskID)
		if err != nil {
			return err
		}
		same, err := q.FindEdge(ctx, sqlitedb.FindEdgeParams{ProjectID: in.ProjectID, BlockerID: in.FromTaskID, DependentID: in.ToTaskID, Type: string(in.Type)})
		exists := err == nil
		if err != nil && !isNoRows(err) {
			return fmt.Errorf("store: dependency: %w", err)
		}
		if in.Type == DependencyBlocks {
			// blockedBy holds open blockers only: a DONE blocker's edge is recorded (upstream context
			// reads it) but gates nothing, since nothing would ever clear it again. An edge whose gate
			// cleared (its blocker reached DONE) and whose blocker is open again gates again.
			gates := from.Status != domain.Done && !slices.Contains(to.BlockedBy, from.ID)
			if exists && !gates {
				out = edgeFrom(same)
				return nil
			}
			rows, err := q.ListGatingEdges(ctx, in.ProjectID)
			if err != nil {
				return fmt.Errorf("store: dependencies: %w", err)
			}
			edges := make([]domain.Edge, len(rows))
			for i, r := range rows {
				edges[i] = domain.Edge{FromTaskID: r.BlockerID, ToTaskID: r.DependentID}
			}
			if err := domain.CheckCycle(edges, in.FromTaskID, in.ToTaskID); err != nil {
				return err
			}
			if gates {
				to.BlockedBy = append(to.BlockedBy, from.ID)
			}
			if !slices.Contains(from.Blocks, to.ID) {
				from.Blocks = append(from.Blocks, to.ID)
			}
			if err := writeCaches(ctx, q, to); err != nil {
				return err
			}
			if err := writeCaches(ctx, q, from); err != nil {
				return err
			}
		}
		if exists {
			out = edgeFrom(same)
			return nil
		}
		r, err := q.InsertEdge(ctx, sqlitedb.InsertEdgeParams{
			ProjectID: in.ProjectID, ID: newID("D-", 16), BlockerID: in.FromTaskID, DependentID: in.ToTaskID,
			Type: string(in.Type), CreatedBy: in.CreatedBy, Ts: nowMillis(),
		})
		if err != nil {
			return fmt.Errorf("store: add dependency: %w", err)
		}
		out = edgeFrom(r)
		return nil
	})
	return out, err
}

func removeEdge(ctx context.Context, e engine, projectID, edgeID string) (DependencyEdge, error) {
	var out DependencyEdge
	err := e.Atomically(ctx, func(tx Store) error {
		q := on(tx).queries(true)
		r, err := q.DeleteEdge(ctx, sqlitedb.DeleteEdgeParams{ProjectID: projectID, ID: edgeID})
		if err := found(err, "DependencyEdge", edgeID); err != nil {
			return err
		}
		out = edgeFrom(r)
		if out.Type != DependencyBlocks {
			return nil
		}
		// The edge's tasks exist: deleting a task deletes its edges (the schema's cascade).
		from, to, err := lockPair(ctx, q, projectID, out.FromTaskID, out.ToTaskID)
		if err != nil {
			return err
		}
		to.BlockedBy = slices.DeleteFunc(to.BlockedBy, func(id string) bool { return id == out.FromTaskID })
		from.Blocks = slices.DeleteFunc(from.Blocks, func(id string) bool { return id == out.ToTaskID })
		if err := writeCaches(ctx, q, to); err != nil {
			return err
		}
		return writeCaches(ctx, q, from)
	})
	return out, err
}

// edgeRow is the shape of the three list statements' rows: the starting row's id, then an edge
// whose columns are NULL when there is none.
type edgeRow = sqlitedb.ListBlockerEdgesRow

func edgesOf(rows []edgeRow, projectID string) []DependencyEdge {
	out := []DependencyEdge{}
	for _, r := range rows {
		if !r.ID.Valid {
			continue
		}
		out = append(out, DependencyEdge{
			ProjectID: projectID, ID: r.ID.String, FromTaskID: r.BlockerID.String, ToTaskID: r.DependentID.String,
			Type: DependencyType(r.Type.String), CreatedBy: r.CreatedBy.String, TS: r.Ts.Int64,
		})
	}
	return out
}

func listEdges(ctx context.Context, e engine, projectID string) ([]DependencyEdge, error) {
	rows, err := e.queries(false).ListProjectEdges(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("store: list dependencies: %w", err)
	}
	if len(rows) == 0 {
		return nil, domain.NotFound("Project", projectID)
	}
	return edgesOf(convert(rows, func(r sqlitedb.ListProjectEdgesRow) edgeRow {
		return edgeRow{TaskID: r.ProjectID, ID: r.ID, BlockerID: r.BlockerID, DependentID: r.DependentID, Type: r.Type, CreatedBy: r.CreatedBy, Ts: r.Ts}
	}), projectID), nil
}

func listBlockers(ctx context.Context, e engine, projectID, taskID string) ([]DependencyEdge, error) {
	rows, err := e.queries(false).ListBlockerEdges(ctx, sqlitedb.ListBlockerEdgesParams{ProjectID: projectID, ID: taskID})
	if err != nil {
		return nil, fmt.Errorf("store: list blockers: %w", err)
	}
	if len(rows) == 0 {
		return nil, domain.NotFound("Task", taskID)
	}
	return edgesOf(rows, projectID), nil
}

func listDependents(ctx context.Context, e engine, projectID, taskID string) ([]DependencyEdge, error) {
	rows, err := e.queries(false).ListDependentEdges(ctx, sqlitedb.ListDependentEdgesParams{ProjectID: projectID, ID: taskID})
	if err != nil {
		return nil, fmt.Errorf("store: list dependents: %w", err)
	}
	if len(rows) == 0 {
		return nil, domain.NotFound("Task", taskID)
	}
	return edgesOf(convert(rows, func(r sqlitedb.ListDependentEdgesRow) edgeRow { return edgeRow(r) }), projectID), nil
}

func setCaches(ctx context.Context, e engine, projectID, taskID string, blockedBy, blocks []string) error {
	n, err := e.queries(true).SetTaskCaches(ctx, sqlitedb.SetTaskCachesParams{BlockedBy: jsonList(blockedBy), Blocks: jsonList(blocks), ProjectID: projectID, ID: taskID})
	if err != nil {
		return fmt.Errorf("store: dependency caches: %w", err)
	}
	if n == 0 {
		return domain.NotFound("Task", taskID)
	}
	return nil
}

func edgeFrom(r sqlitedb.Dependency) DependencyEdge {
	return DependencyEdge{
		ProjectID: r.ProjectID, ID: r.ID, FromTaskID: r.BlockerID, ToTaskID: r.DependentID,
		Type: DependencyType(r.Type), CreatedBy: r.CreatedBy, TS: r.Ts,
	}
}

// --- the Postgres queries in sqlitedb's types ---

func (p pgQueries) FindEdge(ctx context.Context, arg sqlitedb.FindEdgeParams) (sqlitedb.Dependency, error) {
	r, err := p.q.FindEdge(ctx, pgdb.FindEdgeParams(arg))
	return sqlitedb.Dependency(r), err
}

func (p pgQueries) ListGatingEdges(ctx context.Context, projectID string) ([]sqlitedb.ListGatingEdgesRow, error) {
	rows, err := p.q.ListGatingEdges(ctx, projectID)
	return convert(rows, func(r pgdb.ListGatingEdgesRow) sqlitedb.ListGatingEdgesRow { return sqlitedb.ListGatingEdgesRow(r) }), err
}

func (p pgQueries) InsertEdge(ctx context.Context, arg sqlitedb.InsertEdgeParams) (sqlitedb.Dependency, error) {
	r, err := p.q.InsertEdge(ctx, pgdb.InsertEdgeParams(arg))
	return sqlitedb.Dependency(r), err
}

func (p pgQueries) DeleteEdge(ctx context.Context, arg sqlitedb.DeleteEdgeParams) (sqlitedb.Dependency, error) {
	r, err := p.q.DeleteEdge(ctx, pgdb.DeleteEdgeParams(arg))
	return sqlitedb.Dependency(r), err
}

func (p pgQueries) ListProjectEdges(ctx context.Context, id string) ([]sqlitedb.ListProjectEdgesRow, error) {
	rows, err := p.q.ListProjectEdges(ctx, id)
	return convert(rows, func(r pgdb.ListProjectEdgesRow) sqlitedb.ListProjectEdgesRow { return sqlitedb.ListProjectEdgesRow(r) }), err
}

func (p pgQueries) ListBlockerEdges(ctx context.Context, arg sqlitedb.ListBlockerEdgesParams) ([]sqlitedb.ListBlockerEdgesRow, error) {
	rows, err := p.q.ListBlockerEdges(ctx, pgdb.ListBlockerEdgesParams(arg))
	return convert(rows, func(r pgdb.ListBlockerEdgesRow) sqlitedb.ListBlockerEdgesRow { return sqlitedb.ListBlockerEdgesRow(r) }), err
}

func (p pgQueries) ListDependentEdges(ctx context.Context, arg sqlitedb.ListDependentEdgesParams) ([]sqlitedb.ListDependentEdgesRow, error) {
	rows, err := p.q.ListDependentEdges(ctx, pgdb.ListDependentEdgesParams(arg))
	return convert(rows, func(r pgdb.ListDependentEdgesRow) sqlitedb.ListDependentEdgesRow {
		return sqlitedb.ListDependentEdgesRow(r)
	}), err
}

func (p pgQueries) SetTaskCaches(ctx context.Context, arg sqlitedb.SetTaskCachesParams) (int64, error) {
	return p.q.SetTaskCaches(ctx, pgdb.SetTaskCachesParams(arg))
}
