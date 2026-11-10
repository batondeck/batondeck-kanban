package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The claims area of contract_claims.go, on both engines. ClaimNext is one statement over the
// frontier (claims.sql); a heartbeat and a release are one statement each while the lease is the
// caller's, and read the task only to name a refusal; ClaimTask is a versioned write (tasks.go).

type claimQueries interface {
	ClaimNext(ctx context.Context, arg sqlitedb.ClaimNextParams) (sqlitedb.ClaimNextRow, error)
	PreviewFrontier(ctx context.Context, arg sqlitedb.PreviewFrontierParams) ([]sqlitedb.PreviewFrontierRow, error)
	HeartbeatLease(ctx context.Context, arg sqlitedb.HeartbeatLeaseParams) (sqlitedb.HeartbeatLeaseRow, error)
	ReleaseTaskLease(ctx context.Context, arg sqlitedb.ReleaseTaskLeaseParams) (sqlitedb.ReleaseTaskLeaseRow, error)
	ListExpiredLeases(ctx context.Context, arg sqlitedb.ListExpiredLeasesParams) ([]sqlitedb.ListExpiredLeasesRow, error)
}

func (s *SQLite) ClaimNext(ctx context.Context, q FrontierQuery, in ClaimInput) (Task, bool, error) {
	return claimNext(ctx, s, q, in)
}

func (s *Postgres) ClaimNext(ctx context.Context, q FrontierQuery, in ClaimInput) (Task, bool, error) {
	return claimNext(ctx, s, q, in)
}

func (s *SQLite) ClaimTask(ctx context.Context, projectID, taskID string, expectedVersion int64, in ClaimInput) (Task, error) {
	return claimTask(ctx, s, projectID, taskID, expectedVersion, in)
}

func (s *Postgres) ClaimTask(ctx context.Context, projectID, taskID string, expectedVersion int64, in ClaimInput) (Task, error) {
	return claimTask(ctx, s, projectID, taskID, expectedVersion, in)
}

func (s *SQLite) Heartbeat(ctx context.Context, projectID, taskID, leaseID, owner string, expiresAt, now int64, keepLonger bool) (Task, error) {
	return heartbeat(ctx, s, projectID, taskID, leaseID, owner, expiresAt, now, keepLonger)
}

func (s *Postgres) Heartbeat(ctx context.Context, projectID, taskID, leaseID, owner string, expiresAt, now int64, keepLonger bool) (Task, error) {
	return heartbeat(ctx, s, projectID, taskID, leaseID, owner, expiresAt, now, keepLonger)
}

func (s *SQLite) ReleaseLease(ctx context.Context, projectID, taskID, leaseID, owner string, now int64) (Task, error) {
	return releaseLease(ctx, s, projectID, taskID, leaseID, owner, now)
}

func (s *Postgres) ReleaseLease(ctx context.Context, projectID, taskID, leaseID, owner string, now int64) (Task, error) {
	return releaseLease(ctx, s, projectID, taskID, leaseID, owner, now)
}

func (s *SQLite) ListExpiredLeases(ctx context.Context, projectID, boardID string, now int64, limit int) ([]Task, error) {
	return listExpiredLeases(ctx, s, projectID, boardID, now, limit)
}

func (s *Postgres) ListExpiredLeases(ctx context.Context, projectID, boardID string, now int64, limit int) ([]Task, error) {
	return listExpiredLeases(ctx, s, projectID, boardID, now, limit)
}

func (s *SQLite) PreviewFrontier(ctx context.Context, q FrontierQuery, limit int) ([]Task, error) {
	return previewFrontier(ctx, s, q, limit)
}

func (s *Postgres) PreviewFrontier(ctx context.Context, q FrontierQuery, limit int) ([]Task, error) {
	return previewFrontier(ctx, s, q, limit)
}

func validClaim(in ClaimInput) error {
	if in.Owner == "" || in.LeaseID == "" {
		return domain.Validation("a claim needs an owner and a lease id", nil)
	}
	if in.ExpiresAt <= in.Now {
		return domain.Validation("a lease must expire after now", map[string]any{"expiresAt": in.ExpiresAt, "now": in.Now})
	}
	return nil
}

func validFrontier(q FrontierQuery) error {
	if q.BoardID == "" {
		return domain.Validation("a frontier is one board's", nil)
	}
	return nil
}

// capsParam is the frontier's capabilities as the statements read them: a JSON array, or JSON
// null for no filter.
func capsParam(caps []string) string {
	if len(caps) == 0 {
		return "null"
	}
	b, _ := json.Marshal(caps)
	return string(b)
}

// idsParam is FrontierQuery.TaskIDs as the statements' only_ids: JSON null for nil (no filter),
// else the JSON array, "[]" for an empty one (no task).
func idsParam(ids []string) string {
	if ids == nil {
		return "null"
	}
	b, _ := json.Marshal(ids)
	return string(b)
}

func live(c *TaskClaim, now int64) bool { return c != nil && c.ExpiresAt > now }

func sameAgent(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// claimNext is the claim statement alone when it wins. On Postgres a miss is looked at once more
// without SKIP LOCKED (waitingClaimer), so a TODO task a writer holds is waited for rather than
// reported as no work. When it matches nothing and the caller named a column, the column and then
// the frontier are read to tell a missing column (NOT_FOUND, as when it wins) from an empty frontier.
func claimNext(ctx context.Context, e engine, fq FrontierQuery, in ClaimInput) (Task, bool, error) {
	if err := validFrontier(fq); err != nil {
		return Task{}, false, err
	}
	if err := validClaim(in); err != nil {
		return Task{}, false, err
	}
	q := e.queries(true)
	var landing string
	if in.ColumnID != "" {
		// The claimed task leaves a TODO column for in.ColumnID: it lands at that column's head.
		var err error
		if landing, err = landingKey(ctx, q, fq.ProjectID, fq.BoardID, in.ColumnID, ""); err != nil {
			return Task{}, false, err // "" (the column's first key is malformed) keeps the task's own
		}
	}
	arg := sqlitedb.ClaimNextParams{
		ColumnID: in.ColumnID, LandingOrderKey: landing, Owner: in.Owner, Agent: nullStr(in.Agent), AgentID: nullStr(in.AgentID),
		LeaseID: in.LeaseID, ExpiresAt: in.ExpiresAt, Now: in.Now, ProjectID: fq.ProjectID, BoardID: fq.BoardID,
		Assignee: nullStr(fq.Assignee), Caller: fq.Caller, Caps: capsParam(fq.Capabilities), SprintID: fq.SprintID, OnlyIds: idsParam(fq.TaskIDs),
	}
	row, err := q.ClaimNext(ctx, arg)
	if w, ok := e.(waitingClaimer); ok && isNoRows(err) {
		row, err = w.claimNextWaiting(ctx, arg)
	}
	switch {
	case err == nil:
		t, err := taskFromRow(sqlitedb.GetTaskRow(row))
		return t, err == nil, err
	case !isNoRows(err):
		return Task{}, false, fmt.Errorf("store: claim next: %w", err)
	case in.ColumnID == "":
		return Task{}, false, nil
	}
	if err := columnOn(ctx, q, fq.ProjectID, fq.BoardID, in.ColumnID); err == nil || !isNotFound(err) {
		return Task{}, false, err
	}
	head, err := previewFrontier(ctx, e, fq, 1)
	if err != nil || len(head) == 0 {
		return Task{}, false, err
	}
	return Task{}, false, domain.NotFound("Column", in.ColumnID)
}

// waitingClaimer is an engine whose ClaimNext skips locked rows: Postgres. SQLite's writer holds
// the database, so nothing is ever skipped there.
type waitingClaimer interface {
	claimNextWaiting(ctx context.Context, arg sqlitedb.ClaimNextParams) (sqlitedb.ClaimNextRow, error)
}

func (s *Postgres) claimNextWaiting(ctx context.Context, arg sqlitedb.ClaimNextParams) (sqlitedb.ClaimNextRow, error) {
	r, err := s.queries(true).(pgQueries).q.ClaimNextWaiting(ctx, pgdb.ClaimNextWaitingParams(arg))
	return sqlitedb.ClaimNextRow(r), err
}

func isNotFound(err error) bool {
	e, ok := domain.AsBatonDeckError(err)
	return ok && e.Code == domain.CodeNotFound
}

func claimTask(ctx context.Context, e engine, projectID, taskID string, expectedVersion int64, in ClaimInput) (Task, error) {
	if err := validClaim(in); err != nil {
		return Task{}, err
	}
	return versioned(ctx, e, projectID, taskID, expectedVersion, func(q sqlQueries, t Task) (Task, error) {
		if t.Racing {
			return Task{}, domain.ConflictLocked(taskID, "race")
		}
		if len(t.BlockedBy) > 0 {
			return Task{}, domain.InvalidTransition(t.Status, domain.InProgress, nil, nil)
		}
		mine := t.Claim != nil && t.Claim.Owner == in.Owner && sameAgent(t.Claim.AgentID, in.AgentID)
		if live(t.Claim, in.Now) && !mine {
			return Task{}, domain.ConflictLocked(taskID, t.Claim.Owner)
		}
		reclaimable := t.Status == domain.Todo || (t.Status == domain.InProgress && (!live(t.Claim, in.Now) || mine))
		if !reclaimable {
			return Task{}, domain.InvalidTransition(t.Status, domain.InProgress, nil, nil)
		}
		if in.ColumnID != "" {
			if err := columnOn(ctx, q, projectID, t.BoardID, in.ColumnID); err != nil {
				return Task{}, err
			}
			if in.ColumnID != t.ColumnID {
				key, err := landingKey(ctx, q, projectID, t.BoardID, in.ColumnID, t.OrderKey)
				if err != nil {
					return Task{}, err
				}
				t.OrderKey = key
			}
			t.ColumnID = in.ColumnID
		}
		t.Status = domain.InProgress
		t.Claim = &TaskClaim{Owner: in.Owner, Agent: in.Agent, AgentID: in.AgentID, LeaseID: in.LeaseID, ExpiresAt: in.ExpiresAt}
		t.NextEligibleAt = nil
		return writeState(ctx, q, t, in.Owner, in.Now)
	})
}

// leaseRefusal names why a lease write matched no row, from the task as it reads now: a missing
// task, no lease, another owner's (CONFLICT_LOCKED), another lease id or an expired one.
func leaseRefusal(ctx context.Context, q sqlQueries, projectID, taskID, owner string) error {
	row, err := q.GetTask(ctx, sqlitedb.GetTaskParams{ProjectID: projectID, ID: taskID})
	if err := found(err, "Task", taskID); err != nil {
		return err
	}
	t, err := taskFromRow(row)
	if err != nil {
		return err
	}
	if t.Claim != nil && t.Claim.Owner != owner {
		return domain.ConflictLocked(taskID, t.Claim.Owner)
	}
	return LeaseExpired(taskID)
}

func heartbeat(ctx context.Context, e engine, projectID, taskID, leaseID, owner string, expiresAt, now int64, keepLonger bool) (Task, error) {
	if expiresAt <= now {
		return Task{}, domain.Validation("a lease must expire after now", map[string]any{"expiresAt": expiresAt, "now": now})
	}
	q := e.queries(true)
	row, err := q.HeartbeatLease(ctx, sqlitedb.HeartbeatLeaseParams{
		KeepLonger: boolInt(keepLonger), ExpiresAt: expiresAt, ProjectID: projectID, ID: taskID, Owner: owner, LeaseID: leaseID, Now: now,
	})
	switch {
	case isNoRows(err):
		return Task{}, leaseRefusal(ctx, q, projectID, taskID, owner)
	case err != nil:
		return Task{}, fmt.Errorf("store: heartbeat: %w", err)
	}
	return taskFromRow(sqlitedb.GetTaskRow(row))
}

func releaseLease(ctx context.Context, e engine, projectID, taskID, leaseID, owner string, now int64) (Task, error) {
	q := e.queries(true)
	row, err := q.ReleaseTaskLease(ctx, sqlitedb.ReleaseTaskLeaseParams{
		ProjectID: projectID, ID: taskID, Owner: owner, LeaseID: leaseID, Now: now,
	})
	switch {
	case isNoRows(err):
		return Task{}, leaseRefusal(ctx, q, projectID, taskID, owner)
	case err != nil:
		return Task{}, fmt.Errorf("store: release lease: %w", err)
	}
	return taskFromRow(sqlitedb.GetTaskRow(row))
}

func listExpiredLeases(ctx context.Context, e engine, projectID, boardID string, now int64, limit int) ([]Task, error) {
	rows, err := e.queries(false).ListExpiredLeases(ctx, sqlitedb.ListExpiredLeasesParams{
		ProjectID: projectID, BoardID: boardID, Now: now, PageLimit: int64(clamp(limit, 50, 200)),
	})
	if err != nil {
		return nil, fmt.Errorf("store: expired leases: %w", err)
	}
	return tasksFromRows(rows, func(r sqlitedb.ListExpiredLeasesRow) sqlitedb.GetTaskRow { return sqlitedb.GetTaskRow(r) })
}

func previewFrontier(ctx context.Context, e engine, fq FrontierQuery, limit int) ([]Task, error) {
	if err := validFrontier(fq); err != nil {
		return nil, err
	}
	rows, err := e.queries(false).PreviewFrontier(ctx, sqlitedb.PreviewFrontierParams{
		ProjectID: fq.ProjectID, BoardID: fq.BoardID, Now: fq.Now, Assignee: nullStr(fq.Assignee), Caller: fq.Caller,
		Caps: capsParam(fq.Capabilities), SprintID: fq.SprintID, OnlyIds: idsParam(fq.TaskIDs), PageLimit: int64(clamp(limit, 50, 200)),
	})
	if err != nil {
		return nil, fmt.Errorf("store: frontier: %w", err)
	}
	return tasksFromRows(rows, func(r sqlitedb.PreviewFrontierRow) sqlitedb.GetTaskRow { return sqlitedb.GetTaskRow(r) })
}

// tasksFromRows maps a list of task rows, never nil.
func tasksFromRows[R any](rows []R, row func(R) sqlitedb.GetTaskRow) ([]Task, error) {
	out := make([]Task, 0, len(rows))
	for _, r := range rows {
		t, err := taskFromRow(row(r))
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

func (p pgQueries) ClaimNext(ctx context.Context, arg sqlitedb.ClaimNextParams) (sqlitedb.ClaimNextRow, error) {
	r, err := p.q.ClaimNext(ctx, pgdb.ClaimNextParams(arg))
	return sqlitedb.ClaimNextRow(r), err
}

func (p pgQueries) PreviewFrontier(ctx context.Context, arg sqlitedb.PreviewFrontierParams) ([]sqlitedb.PreviewFrontierRow, error) {
	rows, err := p.q.PreviewFrontier(ctx, pgdb.PreviewFrontierParams(arg))
	return convert(rows, func(r pgdb.PreviewFrontierRow) sqlitedb.PreviewFrontierRow { return sqlitedb.PreviewFrontierRow(r) }), err
}

func (p pgQueries) HeartbeatLease(ctx context.Context, arg sqlitedb.HeartbeatLeaseParams) (sqlitedb.HeartbeatLeaseRow, error) {
	r, err := p.q.HeartbeatLease(ctx, pgdb.HeartbeatLeaseParams(arg))
	return sqlitedb.HeartbeatLeaseRow(r), err
}

func (p pgQueries) ReleaseTaskLease(ctx context.Context, arg sqlitedb.ReleaseTaskLeaseParams) (sqlitedb.ReleaseTaskLeaseRow, error) {
	r, err := p.q.ReleaseTaskLease(ctx, pgdb.ReleaseTaskLeaseParams(arg))
	return sqlitedb.ReleaseTaskLeaseRow(r), err
}

func (p pgQueries) ListExpiredLeases(ctx context.Context, arg sqlitedb.ListExpiredLeasesParams) ([]sqlitedb.ListExpiredLeasesRow, error) {
	rows, err := p.q.ListExpiredLeases(ctx, pgdb.ListExpiredLeasesParams(arg))
	return convert(rows, func(r pgdb.ListExpiredLeasesRow) sqlitedb.ListExpiredLeasesRow {
		return sqlitedb.ListExpiredLeasesRow(r)
	}), err
}
