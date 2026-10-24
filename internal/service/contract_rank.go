package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Ranking is the explainable scorer over a board's claimable pool (conductor lifecycle.ts
// rank_tasks, T-1 and T-3) and the leader fan-out (orchestrate_subtasks).
type Ranking interface {
	// Rank is rank_tasks.
	// Tier: read. Writes nothing. The board's claimable TODO tasks (at most 1000, five pages),
	// scored by domain.ScoreTask under the project's selectionWeights and agingCeilingMs, highest
	// first; with useProfile and a registered profile, filtered by domain.MatchCapabilities (exact,
	// alias, tag) with the tag penalty and the reliability nudge, else strictly by capabilities.
	Rank(ctx context.Context, caller domain.Caller, req RankTasksReq) (RankTasksResp, error)

	// Orchestrate is orchestrate_subtasks.
	// Tier: master; lease-guarded on the parent (LEASE_EXPIRED). In one transaction: each child is
	// created TODO, unassigned, under parentTaskId on the parent's board, with a blocks edge to the
	// parent; the parent moves to BLOCKED (ADR-0005), its lease dropped, assigned to the caller when
	// it had no assignee, so auto-unblock rings the leader when the last child is DONE. Events:
	// task.created on each child, dependency.added and task.orchestrated on the parent. Audit: none.
	// Doorbell: broadcast for each child (open work, assigned to nobody); the parent's self-assignment
	// rings nobody. Replays on idempotencyKey.
	Orchestrate(ctx context.Context, caller domain.Caller, req OrchestrateSubtasksReq) (OrchestrateSubtasksResp, error)
}

// RankTasksReq is rank_tasks's input.
type RankTasksReq struct {
	ProjectID    string   `json:"projectId"`
	BoardID      string   `json:"boardId"`
	Assignee     string   `json:"assignee,omitzero"`
	Capabilities []string `json:"capabilities,omitzero"`
	Limit        int64    `json:"limit,omitzero"`
	SprintID     string   `json:"sprintId,omitzero"`
	UseProfile   bool     `json:"useProfile,omitzero"`
}

// RankTasksResp is rank_tasks's output.
type RankTasksResp struct {
	Ranked []RankedTask `json:"ranked"`
}

// RankedTask is rank_tasks's output ranked[]: tier and reliability only on the profile path.
type RankedTask struct {
	AgedOut     bool                `json:"agedOut"`
	Factors     ScoreFactors        `json:"factors"`
	Reason      string              `json:"reason"`
	Reliability float64             `json:"reliability,omitzero"`
	Score       float64             `json:"score"`
	Task        Task                `json:"task"`
	Tier        CapabilityMatchTier `json:"tier,omitzero"`
}

// OrchestrateSubtasksReq is orchestrate_subtasks's input.
type OrchestrateSubtasksReq struct {
	ProjectID      string             `json:"projectId"`
	ParentTaskID   string             `json:"parentTaskId"`
	LeaseID        string             `json:"leaseId"`
	Children       []OrchestrateChild `json:"children"`
	IdempotencyKey string             `json:"idempotencyKey,omitzero"`
}

// OrchestrateChild is orchestrate_subtasks's input children[].
type OrchestrateChild struct {
	Title                string          `json:"title"`
	Description          string          `json:"description,omitzero"`
	Priority             domain.Priority `json:"priority,omitzero"`
	RequiredCapabilities []string        `json:"requiredCapabilities,omitzero"`
}

// OrchestrateSubtasksResp is orchestrate_subtasks's output.
type OrchestrateSubtasksResp struct {
	Children []Task `json:"children"`
	Parent   Task   `json:"parent"`
}
