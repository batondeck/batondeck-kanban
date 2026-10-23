package memstore

import (
	"context"
	"slices"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The dependency area of contract_dependencies.go. No call here changes a task's version: the
// caches are written in place on the stored task.

func (s *Store) AddEdge(ctx context.Context, in store.AddEdgeInput) (store.DependencyEdge, error) {
	if in.Type != store.DependencyBlocks && in.Type != store.DependencyRelates {
		return store.DependencyEdge{}, domain.Validation("unknown dependency type: "+string(in.Type), map[string]any{"type": in.Type})
	}
	if in.CreatedBy == "" {
		return store.DependencyEdge{}, domain.Validation("a dependency needs its creator", nil)
	}
	return get(ctx, s, func(st *state) (store.DependencyEdge, error) {
		from, err := st.task(in.ProjectID, in.FromTaskID)
		if err != nil {
			return store.DependencyEdge{}, err
		}
		to, err := st.task(in.ProjectID, in.ToTaskID)
		if err != nil {
			return store.DependencyEdge{}, err
		}
		edges := st.projectEdges(in.ProjectID)
		var blocking []domain.Edge
		for _, e := range edges {
			if e.FromTaskID == in.FromTaskID && e.ToTaskID == in.ToTaskID && e.Type == in.Type {
				return e, nil
			}
			if e.Type == store.DependencyBlocks {
				blocking = append(blocking, domain.Edge{FromTaskID: e.FromTaskID, ToTaskID: e.ToTaskID})
			}
		}
		if in.Type == store.DependencyBlocks {
			if err := domain.CheckCycle(blocking, in.FromTaskID, in.ToTaskID); err != nil {
				return store.DependencyEdge{}, err
			}
			if !slices.Contains(to.BlockedBy, from.ID) {
				to.BlockedBy = append(list(to.BlockedBy), from.ID)
			}
			st.put(to)
			if !slices.Contains(from.Blocks, to.ID) {
				from.Blocks = append(list(from.Blocks), to.ID)
			}
			st.put(from)
		}
		e := store.DependencyEdge{ProjectID: in.ProjectID, ID: newID("D-", 16), FromTaskID: in.FromTaskID, ToTaskID: in.ToTaskID, Type: in.Type, CreatedBy: in.CreatedBy, TS: nowMillis()}
		st.edges[[2]string{e.ProjectID, e.ID}] = e
		return e, nil
	})
}

func (s *Store) RemoveEdge(ctx context.Context, projectID, edgeID string) (store.DependencyEdge, error) {
	return get(ctx, s, func(st *state) (store.DependencyEdge, error) {
		k := [2]string{projectID, edgeID}
		e, ok := st.edges[k]
		if !ok {
			return store.DependencyEdge{}, domain.NotFound("DependencyEdge", edgeID)
		}
		delete(st.edges, k)
		if e.Type == store.DependencyBlocks {
			if to, err := st.task(projectID, e.ToTaskID); err == nil {
				to.BlockedBy = slices.DeleteFunc(to.BlockedBy, func(id string) bool { return id == e.FromTaskID })
				st.put(to)
			}
			if from, err := st.task(projectID, e.FromTaskID); err == nil {
				from.Blocks = slices.DeleteFunc(from.Blocks, func(id string) bool { return id == e.ToTaskID })
				st.put(from)
			}
		}
		return e, nil
	})
}

// projectEdges is the project's edges in (ts, id) order.
func (st *state) projectEdges(projectID string) []store.DependencyEdge {
	out := []store.DependencyEdge{}
	for k, e := range st.edges {
		if k[0] == projectID {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b store.DependencyEdge) int { return byTSAndID(a.TS, b.TS, a.ID, b.ID) })
	return out
}

func (s *Store) ListEdges(ctx context.Context, projectID string) ([]store.DependencyEdge, error) {
	return get(ctx, s, func(st *state) ([]store.DependencyEdge, error) {
		if _, ok := st.projects[projectID]; !ok {
			return nil, domain.NotFound("Project", projectID)
		}
		return st.projectEdges(projectID), nil
	})
}

func (s *Store) ListBlockers(ctx context.Context, projectID, taskID string) ([]store.DependencyEdge, error) {
	return s.edgesOfTask(ctx, projectID, taskID, func(e store.DependencyEdge) bool { return e.ToTaskID == taskID })
}

func (s *Store) ListDependents(ctx context.Context, projectID, taskID string) ([]store.DependencyEdge, error) {
	return s.edgesOfTask(ctx, projectID, taskID, func(e store.DependencyEdge) bool { return e.FromTaskID == taskID })
}

func (s *Store) edgesOfTask(ctx context.Context, projectID, taskID string, keep func(store.DependencyEdge) bool) ([]store.DependencyEdge, error) {
	return get(ctx, s, func(st *state) ([]store.DependencyEdge, error) {
		if _, err := st.task(projectID, taskID); err != nil {
			return nil, err
		}
		return slices.DeleteFunc(st.projectEdges(projectID), func(e store.DependencyEdge) bool { return !keep(e) }), nil
	})
}

func (s *Store) SetCaches(ctx context.Context, projectID, taskID string, blockedBy, blocks []string) error {
	return s.do(ctx, func(st *state) error {
		t, err := st.task(projectID, taskID)
		if err != nil {
			return err
		}
		t.BlockedBy, t.Blocks = list(blockedBy), list(blocks)
		st.put(t)
		return nil
	})
}
