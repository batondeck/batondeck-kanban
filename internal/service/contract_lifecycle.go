package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Lifecycle is the lease lifecycle: claim, heartbeat, release, the outcomes, the reaper,
// handoff, cancel, and the next-task advisor.
type Lifecycle interface {
	// Claim is claim_task.
	// Tier: write; version-checked (STALE); CONFLICT_LOCKED on a live lease. Event: task.claimed.
	// Audit: none. Doorbell: assigned (the task gains the caller as assignee).
	Claim(ctx context.Context, caller domain.Caller, req ClaimTaskReq) (ClaimTaskResp, error)

	// ClaimNext is claim_next.
	// Tier: write. Event: task.claimed. Audit: none. Doorbell: assigned.
	ClaimNext(ctx context.Context, caller domain.Caller, req ClaimNextReq) (ClaimNextResp, error)

	// Heartbeat is heartbeat_task.
	// Lease-guarded (the held leaseId; LEASE_EXPIRED). Event: none. Audit: none. Doorbell: none.
	Heartbeat(ctx context.Context, caller domain.Caller, req HeartbeatTaskReq) (HeartbeatTaskResp, error)

	// Release is release_task.
	// Lease-guarded. Event: task.released. Audit: none. Doorbell: broadcast (READY, no assignee).
	Release(ctx context.Context, caller domain.Caller, req ReleaseTaskReq) (ReleaseTaskResp, error)

	// Next is next_task.
	// Tier: read. Writes nothing.
	Next(ctx context.Context, caller domain.Caller, req NextTaskReq) (NextTaskResp, error)

	// Complete is complete_task.
	// Lease-guarded. Event: task.completed, plus task.review_requested when review sends it to
	// REVIEW, task.artifact_missing / task.checkpoint_missing under a warn policy, and
	// task.dependency_cleared on each dependent DONE unblocks. Audit: none. Doorbell: assigned to the
	// reviewer when the task lands REVIEW; for each dependent DONE unblocks, assigned to its assignee
	// if it has one, else broadcast when it lands READY.
	Complete(ctx context.Context, caller domain.Caller, req CompleteTaskReq) (CompleteTaskResp, error)

	// Block is block_task.
	// Lease-guarded. Event: task.blocked. Audit: none. Doorbell: none.
	Block(ctx context.Context, caller domain.Caller, req BlockTaskReq) (BlockTaskResp, error)

	// Fail is fail_task.
	// Lease-guarded. Event: task.failed, plus task.poisoned when the retry policy poisons it (BLOCKED,
	// blockedReason "poison", label poisoned). Audit: none. Doorbell: broadcast when it returns to READY
	// past its backoff (open frontier); none for READY with a backoff pending, or poisoned.
	Fail(ctx context.Context, caller domain.Caller, req FailTaskReq) (FailTaskResp, error)

	// Reap is reap_stale_leases.
	// Tier: write. Event: task.lease_reaped per reaped task, task.poisoned on each it poisons.
	// Audit: none. Doorbell: none (conductor rings nothing on a reap).
	Reap(ctx context.Context, caller domain.Caller, req ReapStaleLeasesReq) (ReapStaleLeasesResp, error)

	// Handoff is handoff_task.
	// Lease-guarded. Event: task.handoff. Audit: none. Doorbell: assigned (to the new assignee).
	Handoff(ctx context.Context, caller domain.Caller, req HandoffTaskReq) (HandoffTaskResp, error)

	// Cancel is cancel_task.
	// The master role or the task's current owner (ADR-0001); version-checked (STALE); CONFLICT_LOCKED
	// on another identity's live lease. Moves the task to DONE with the system label cancelled and the
	// reason as a context note. Event: task.moved (data {to: DONE, cancelled: true, reason}),
	// context.added for the note, and task.dependency_cleared per dependent it unblocks; no event type
	// of its own. Audit: none. Doorbell: as Move to DONE (each unblocked dependent: assigned to its
	// assignee if it has one, else broadcast when it lands READY).
	Cancel(ctx context.Context, caller domain.Caller, req CancelTaskReq) (CancelTaskResp, error)
}

// ClaimTaskReq is claim_task's input.
type ClaimTaskReq struct {
	ProjectID      string `json:"projectId"`
	TaskID         string `json:"taskId"`
	IdempotencyKey string `json:"idempotencyKey,omitzero"`
	LeaseSeconds   int64  `json:"leaseSeconds,omitzero"`
}

// ClaimTaskResp is claim_task's output.
type ClaimTaskResp struct {
	LeaseID string          `json:"leaseId"`
	Resume  *ResumeBriefing `json:"resume"`
	Sprint  *Sprint         `json:"sprint"`
	Task    Task            `json:"task"`
}

// ClaimNextReq is claim_next's input.
type ClaimNextReq struct {
	BoardID        string   `json:"boardId"`
	ProjectID      string   `json:"projectId"`
	Assignee       string   `json:"assignee,omitzero"`
	Capabilities   []string `json:"capabilities,omitzero"`
	IdempotencyKey string   `json:"idempotencyKey,omitzero"`
	LeaseSeconds   int64    `json:"leaseSeconds,omitzero"`
	MaxConcurrency int64    `json:"maxConcurrency,omitzero"`
	Shard          Shard    `json:"shard,omitzero"`
	SprintID       string   `json:"sprintId,omitzero"`
	UseProfile     bool     `json:"useProfile,omitzero"`
}

// ClaimNextResp is claim_next's output.
type ClaimNextResp struct {
	Attempts int64           `json:"attempts"`
	LeaseID  *string         `json:"leaseId"`
	Resume   *ResumeBriefing `json:"resume"`
	Sprint   *Sprint         `json:"sprint"`
	Task     *Task           `json:"task"`
}

// HeartbeatTaskReq is heartbeat_task's input.
type HeartbeatTaskReq struct {
	LeaseID      string `json:"leaseId"`
	ProjectID    string `json:"projectId"`
	TaskID       string `json:"taskId"`
	LeaseSeconds int64  `json:"leaseSeconds,omitzero"`
}

// HeartbeatTaskResp is heartbeat_task's output.
type HeartbeatTaskResp struct {
	ExpiresAt string `json:"expiresAt"`
}

// ReleaseTaskReq is release_task's input.
type ReleaseTaskReq struct {
	LeaseID        string `json:"leaseId"`
	ProjectID      string `json:"projectId"`
	TaskID         string `json:"taskId"`
	IdempotencyKey string `json:"idempotencyKey,omitzero"`
}

// ReleaseTaskResp is release_task's output.
type ReleaseTaskResp struct {
	Task Task `json:"task"`
}

// NextTaskReq is next_task's input.
type NextTaskReq struct {
	BoardID      string       `json:"boardId"`
	ProjectID    string       `json:"projectId"`
	Assignee     string       `json:"assignee,omitzero"`
	Capabilities []string     `json:"capabilities,omitzero"`
	Explain      bool         `json:"explain,omitzero"`
	InboxLimit   int64        `json:"inboxLimit,omitzero"`
	IncludeInbox bool         `json:"includeInbox,omitzero"`
	SprintID     string       `json:"sprintId,omitzero"`
	Strategy     NextStrategy `json:"strategy,omitzero"`
	UseProfile   bool         `json:"useProfile,omitzero"`
}

// NextTaskResp is next_task's output.
type NextTaskResp struct {
	Resume      *ResumeBriefing `json:"resume"`
	Task        *Task           `json:"task"`
	Explanation NextExplanation `json:"explanation,omitzero"`
	Inbox       Inbox           `json:"inbox,omitzero"`
}

// CompleteTaskReq is complete_task's input.
type CompleteTaskReq struct {
	LeaseID        string          `json:"leaseId"`
	ProjectID      string          `json:"projectId"`
	TaskID         string          `json:"taskId"`
	Artifacts      []ArtifactInput `json:"artifacts,omitzero"`
	Deliverable    string          `json:"deliverable,omitzero"`
	IdempotencyKey string          `json:"idempotencyKey,omitzero"`
	Reviewer       string          `json:"reviewer,omitzero"`
}

// CompleteTaskResp is complete_task's output.
type CompleteTaskResp struct {
	Task     Task     `json:"task"`
	Handover Handover `json:"handover,omitzero"`
	Warnings []string `json:"warnings,omitzero"`
}

// BlockTaskReq is block_task's input.
type BlockTaskReq struct {
	LeaseID        string   `json:"leaseId"`
	ProjectID      string   `json:"projectId"`
	Reason         string   `json:"reason"`
	TaskID         string   `json:"taskId"`
	BlockedBy      []string `json:"blockedBy,omitzero"`
	IdempotencyKey string   `json:"idempotencyKey,omitzero"`
}

// BlockTaskResp is block_task's output.
type BlockTaskResp struct {
	Task Task `json:"task"`
}

// FailTaskReq is fail_task's input.
type FailTaskReq struct {
	LeaseID        string         `json:"leaseId"`
	ProjectID      string         `json:"projectId"`
	Reason         string         `json:"reason"`
	TaskID         string         `json:"taskId"`
	IdempotencyKey string         `json:"idempotencyKey,omitzero"`
	Retryable      Optional[bool] `json:"retryable,omitzero"`
}

// FailTaskResp is fail_task's output.
type FailTaskResp struct {
	Task Task `json:"task"`
}

// ReapStaleLeasesReq is reap_stale_leases's input.
type ReapStaleLeasesReq struct {
	BoardID   string `json:"boardId"`
	ProjectID string `json:"projectId"`
}

// ReapStaleLeasesResp is reap_stale_leases's output.
type ReapStaleLeasesResp struct {
	Reaped int64 `json:"reaped"`
}

// HandoffTaskReq is handoff_task's input.
type HandoffTaskReq struct {
	LeaseID    string `json:"leaseId"`
	MemoryNote string `json:"memoryNote"`
	ProjectID  string `json:"projectId"`
	TaskID     string `json:"taskId"`
	ToAgent    string `json:"toAgent"`
}

// HandoffTaskResp is handoff_task's output.
type HandoffTaskResp struct {
	Task     Task     `json:"task"`
	Warnings []string `json:"warnings,omitzero"`
}

// CancelTaskReq is cancel_task's input.
type CancelTaskReq struct {
	ProjectID string `json:"projectId"`
	Reason    string `json:"reason"`
	TaskID    string `json:"taskId"`
	Version   int64  `json:"version"`
}

// CancelTaskResp is cancel_task's output.
type CancelTaskResp struct {
	Task     Task     `json:"task"`
	Handover Handover `json:"handover,omitzero"`
	Warnings []string `json:"warnings,omitzero"`
}

// ResumeBriefing is claim_task's output resume, first seen there.
type ResumeBriefing struct {
	Branch         *string     `json:"branch"`
	HeadCommit     *string     `json:"headCommit"`
	LastCheckpoint *Checkpoint `json:"lastCheckpoint"`
}

// Checkpoint is claim_task's output resume.lastCheckpoint, first seen there.
type Checkpoint struct {
	DONE      *string `json:"DONE"`
	NEXT      *string `json:"NEXT"`
	REJECTED  *string `json:"REJECTED"`
	UNCERTAIN *string `json:"UNCERTAIN"`
}

// Sprint is claim_task's output sprint, first seen there.
type Sprint struct {
	Goal   string       `json:"goal"`
	ID     string       `json:"id"`
	Name   string       `json:"name"`
	Status SprintStatus `json:"status"`
}

// Shard is claim_next's input shard, first seen there. It is an open object: conductor's schema allows properties it does not name.
type Shard struct {
	Count int64 `json:"count"`
	Index int64 `json:"index"`
}

// NextExplanation is next_task's output explanation, first seen there.
type NextExplanation struct {
	AgedOut     bool                `json:"agedOut"`
	Factors     ScoreFactors        `json:"factors"`
	Reason      string              `json:"reason"`
	RunnerUp    *RunnerUp           `json:"runnerUp"`
	Score       float64             `json:"score"`
	Reliability float64             `json:"reliability,omitzero"`
	Tier        CapabilityMatchTier `json:"tier,omitzero"`
}

// ScoreFactors is next_task's output explanation.factors, first seen there.
type ScoreFactors struct {
	Aging      AgingFactor `json:"aging"`
	Capability AgingFactor `json:"capability"`
	Deadline   AgingFactor `json:"deadline"`
	Priority   AgingFactor `json:"priority"`
	Readiness  AgingFactor `json:"readiness"`
}

// AgingFactor is next_task's output explanation.factors.aging, first seen there.
type AgingFactor struct {
	Contribution float64 `json:"contribution"`
	Value        float64 `json:"value"`
	Weight       float64 `json:"weight"`
}

// RunnerUp is next_task's output explanation.runnerUp, first seen there.
type RunnerUp struct {
	Reason string  `json:"reason"`
	Score  float64 `json:"score"`
	TaskID string  `json:"taskId"`
}

// Inbox is next_task's output inbox, first seen there.
type Inbox struct {
	Blocked   []Task `json:"blocked"`
	Poisoned  []Task `json:"poisoned"`
	Review    []Task `json:"review"`
	Truncated bool   `json:"truncated"`
}

// ArtifactInput is complete_task's input artifacts[], first seen there. It is an open object: conductor's schema allows properties it does not name.
type ArtifactInput struct {
	Kind  ArtifactKind `json:"kind"`
	Ref   string       `json:"ref,omitzero"`
	Repo  string       `json:"repo,omitzero"`
	Title string       `json:"title,omitzero"`
	URL   string       `json:"url,omitzero"`
}

// Handover is complete_task's output handover, first seen there.
type Handover struct {
	Reviewer *string       `json:"reviewer"`
	Status   domain.Status `json:"status"`
}

// SprintStatus is a sprint's state, as claim responses report it.
type SprintStatus string

// SprintStatusValues are SprintStatus's values, in the schema's order.
var SprintStatusValues = []SprintStatus{"ABANDONED", "ACTIVE", "CLOSED", "PROPOSED"}

// NextStrategy is how next_task ranks the frontier.
type NextStrategy string

// NextStrategyValues are NextStrategy's values, in the schema's order.
var NextStrategyValues = []NextStrategy{"fifo", "score"}

// CapabilityMatchTier is how a task's required capabilities matched the caller's.
type CapabilityMatchTier string

// CapabilityMatchTierValues are CapabilityMatchTier's values, in the schema's order.
var CapabilityMatchTierValues = []CapabilityMatchTier{"exact", "none", "synonym", "tag"}
