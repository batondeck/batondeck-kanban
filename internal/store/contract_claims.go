package store

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// ClaimInput is a claim by one agent. LeaseID is the caller's new lease id, ExpiresAt when the
// lease runs out, Now the time the claim is judged at and recorded under (UpdatedAt). ColumnID is
// the IN_PROGRESS column the task lands in ("" keeps its column): the service resolves it from the
// board. Agent and AgentID are nil for a claim that did not come through an agent.
type ClaimInput struct {
	Owner     string
	Agent     *string
	AgentID   *string
	LeaseID   string
	ExpiresAt int64
	Now       int64
	ColumnID  string
}

// FrontierQuery is the claim frontier of one board: its claimable tasks (domain.IsClaimable at Now:
// TODO, nothing in BlockedBy, not poisoned, not racing, no live lease, past NextEligibleAt), with
// Assignee set only those assigned to it, without it only the unassigned ones and Caller's (assigned
// to Caller's id, or to its account's email in any case), so nobody takes work routed to another
// (Caller "" is the unassigned ones alone), and with Capabilities not empty only those whose
// RequiredCapabilities are all among them. The frontier is best first: Score descending, then
// priority (urgent, high, normal, low), then order key, then id, the two keys compared as bytes
// (Postgres: COLLATE "C"), so T-10 comes before T-9.
type FrontierQuery struct {
	ProjectID    string
	BoardID      string
	Assignee     *string
	Caller       string
	Capabilities []string
	Now          int64
}

// Claims is the lease area: who works a task, until when. Keys: (project_id, task_id). A claim
// writes status IN_PROGRESS, the lease, next_eligible_at NULL, UpdatedBy = Owner and the version one
// higher; a heartbeat or a release changes only the lease and never the version (the service's
// move around a release bumps it once). Errors, the same shapes as domain's on every engine: a
// missing task is domain.NotFound("Task", id); another holder's live lease is
// domain.ConflictLocked(taskID, holder) — another owner, or the same owner under another AgentID —
// and a racing parent ConflictLocked(taskID, "race"); a lease that is missing, not the caller's
// LeaseID or past its ExpiresAt is LeaseExpired(taskID) (code LEASE_EXPIRED, details {taskId}); a
// task that cannot be claimed is domain.InvalidTransition(status, IN_PROGRESS, nil, nil). Where the
// task lands after a failure or reap (TODO with a backoff, or BLOCKED poison) is the service's
// SetStatus, not written here. WIP limits are the service's (CountTasks inside Atomically). An
// empty owner or lease id, ExpiresAt not after Now, or an empty board id is domain.Validation; a
// ColumnID not on the task's board is domain.NotFound("Column", id).
type Claims interface {
	// ClaimNext claims the first task of the frontier in ONE statement and returns it; ok is false
	// when the frontier is empty (an unknown board's frontier is empty: the service has read the
	// board for its column). Concurrent callers never win the same task.
	ClaimNext(ctx context.Context, q FrontierQuery, in ClaimInput) (task Task, ok bool, err error)
	// ClaimTask claims one task at expectedVersion. A task with BlockedBy not empty, or neither
	// TODO nor IN_PROGRESS with an expired lease or the caller's own, is INVALID_TRANSITION;
	// re-claiming one's own live lease replaces it. A version that is not the task's is STALE.
	ClaimTask(ctx context.Context, projectID, taskID string, expectedVersion int64, in ClaimInput) (Task, error)
	// Heartbeat moves the lease's expiry to expiresAt and returns the task; with keepLonger an
	// expiry already later than expiresAt stays. The lease must be leaseID, held by owner and live at
	// now; another owner is CONFLICT_LOCKED, an expiresAt not after now VALIDATION.
	Heartbeat(ctx context.Context, projectID, taskID, leaseID, owner string, expiresAt, now int64, keepLonger bool) (Task, error)
	// ReleaseLease drops the lease after the same checks as Heartbeat and returns the task.
	ReleaseLease(ctx context.Context, projectID, taskID, leaseID, owner string, now int64) (Task, error)
	// ListExpiredLeases is the tasks of the project (of boardID, or every board when it is "")
	// holding a lease expired at now, the oldest expiry first (then id, as bytes). A limit <= 0 is 50 and
	// above 200 is 200.
	ListExpiredLeases(ctx context.Context, projectID, boardID string, now int64, limit int) ([]Task, error)
	// PreviewFrontier is the first limit tasks of the frontier, best first, claiming nothing. A
	// limit <= 0 is 50 and above 200 is 200.
	PreviewFrontier(ctx context.Context, q FrontierQuery, limit int) ([]Task, error)
}

// LeaseExpired is the LEASE_EXPIRED error: conductor's BatonDeckError.leaseExpired, which
// internal/domain has no constructor for.
func LeaseExpired(taskID string) *domain.BatonDeckError {
	return domain.NewError(domain.CodeLeaseExpired, "Lease expired or invalid for task "+taskID, map[string]any{"taskId": taskID})
}

var (
	_ Claims = (*SQLite)(nil)
	_ Claims = (*Postgres)(nil)
)
