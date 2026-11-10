package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Runs is the race service (conductor's runs.ts): a task raced across N run tasks, then resolved.
// A losing run leaves by the delete cascade (plan §Fold 2), never by a status.
type Runs interface {
	// Start is start_runs.
	// Tier: master; version-checked on the parent (STALE); a racing or poisoned parent, or one with
	// an open blocker (blockedBy, named), INVALID_TRANSITION; another holder's live lease on the
	// parent CONFLICT_LOCKED (another identity's, or another agent's of the caller's, P-399); more
	// runs than maxRunsPerTask VALIDATION. Events: runs.started on the parent, task.created and
	// run.opened on each run. Audit: none. Doorbell: assigned to each named run's agent, broadcast for
	// each unnamed one. Replays on idempotencyKey.
	Start(ctx context.Context, caller domain.Caller, req StartRunsReq) (StartRunsResp, error)

	// Open is open_run.
	// Tier: master; a parent that is not racing, or has an open blocker, INVALID_TRANSITION; the run
	// cap VALIDATION. Events: task.created and run.opened on the run. Audit: none. Doorbell: assigned
	// to agent, else broadcast. Replays on idempotencyKey.
	Open(ctx context.Context, caller domain.Caller, req OpenRunReq) (OpenRunResp, error)

	// Pick is pick_run.
	// Tier: master; version-checked on the parent (STALE); a parent that is not racing, or a run that
	// is not submitted, INVALID_TRANSITION; a task that is not its run VALIDATION. Every other run is
	// deleted through the cascade (run.discarded and task.deleted each); the parent takes the winner's
	// deliverable and lands REVIEW or DONE with racing false (run.picked). Audit: none. Doorbell:
	// the cascade's, and the parent's change.
	Pick(ctx context.Context, caller domain.Caller, req PickRunReq) (PickRunResp, error)

	// Cancel is cancel_runs.
	// Tier: master; version-checked on the parent (STALE); a parent that is not racing
	// INVALID_TRANSITION. Every run is deleted through the cascade, which lands the parent TODO with
	// racing false (run.discarded each, runs.cancelled). Audit: none. Doorbell: broadcast when the
	// parent is back in the pool with nobody named.
	Cancel(ctx context.Context, caller domain.Caller, req CancelRunsReq) (CancelRunsResp, error)

	// List is list_runs.
	// Tier: read. Writes nothing.
	List(ctx context.Context, caller domain.Caller, req ListRunsReq) (ListRunsResp, error)
}

// StartRunsReq is start_runs's input.
type StartRunsReq struct {
	ProjectID      string   `json:"projectId"`
	TaskID         string   `json:"taskId"`
	Version        int64    `json:"version"`
	Agents         []string `json:"agents,omitzero"`
	Brief          string   `json:"brief,omitzero"`
	Count          int64    `json:"count,omitzero"`
	IdempotencyKey string   `json:"idempotencyKey,omitzero"`
}

// StartRunsResp is start_runs's output.
type StartRunsResp struct {
	Runs []Task `json:"runs"`
	Task Task   `json:"task"`
}

// OpenRunReq is open_run's input.
type OpenRunReq struct {
	ProjectID      string `json:"projectId"`
	TaskID         string `json:"taskId"`
	Agent          string `json:"agent,omitzero"`
	Brief          string `json:"brief,omitzero"`
	IdempotencyKey string `json:"idempotencyKey,omitzero"`
}

// OpenRunResp is open_run's output.
type OpenRunResp struct {
	Run Task `json:"run"`
}

// PickRunReq is pick_run's input.
type PickRunReq struct {
	ProjectID string `json:"projectId"`
	RunTaskID string `json:"runTaskId"`
	TaskID    string `json:"taskId"`
	Version   int64  `json:"version"`
}

// PickRunResp is pick_run's output.
type PickRunResp struct {
	Task Task `json:"task"`
}

// CancelRunsReq is cancel_runs's input.
type CancelRunsReq struct {
	ProjectID string `json:"projectId"`
	TaskID    string `json:"taskId"`
	Version   int64  `json:"version"`
}

// CancelRunsResp is cancel_runs's output.
type CancelRunsResp struct {
	Task Task `json:"task"`
}

// ListRunsReq is list_runs's input.
type ListRunsReq struct {
	ProjectID string `json:"projectId"`
	TaskID    string `json:"taskId"`
}

// ListRunsResp is list_runs's output.
type ListRunsResp struct {
	Runs []RunView `json:"runs"`
}

// RunView is list_runs's output runs[]: a compact view of one run.
type RunView struct {
	Assignee       *string       `json:"assignee"`
	HasDeliverable bool          `json:"hasDeliverable"`
	LeaseHolder    *string       `json:"leaseHolder"`
	Status         domain.Status `json:"status"`
	TaskID         string        `json:"taskId"`
	Version        float64       `json:"version"`
	Deliverable    *string       `json:"deliverable,omitzero"`
}
