package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Sprints is the sprint service (conductor's sprints.ts): a named cross-ticket objective a fresh
// session resumes from.
type Sprints interface {
	// Create is create_sprint.
	// Tier: master; a board that is not the project's NOT_FOUND. Event: sprint.created (board-level).
	// Audit: none. Doorbell: none (a change row). Replays on idempotencyKey.
	Create(ctx context.Context, caller domain.Caller, req CreateSprintReq) (CreateSprintResp, error)

	// Update is update_sprint.
	// Tier: master; version-checked on the sprint (STALE); the fixed sprint table and the activation
	// gates (INVALID_TRANSITION, FORBIDDEN); a change of proposalTaskId outside PROPOSED, or of
	// name, goal or acceptance on a CLOSED or ABANDONED sprint, VALIDATION. Event: sprint.activated,
	// sprint.closed, sprint.abandoned, sprint.checkpoint or sprint.updated (board-level), and
	// sprint.self_approved under the "warn" rung. Audit: none. Doorbell: none.
	Update(ctx context.Context, caller domain.Caller, req UpdateSprintReq) (UpdateSprintResp, error)

	// Set is set_sprint.
	// Tier: master; version-checked on the task (STALE) unless it is a retry-safe no-op; a sprint
	// that is neither PROPOSED nor ACTIVE INVALID_TRANSITION; another sprint under exclusivity
	// CONFLICT_LOCKED; another board or a cap VALIDATION. Event: sprint.task_added or
	// sprint.task_removed on the task. Audit: none. Doorbell: none.
	Set(ctx context.Context, caller domain.Caller, req SetSprintReq) (SetSprintResp, error)

	// Get is get_sprint.
	// Tier: read. Writes nothing.
	Get(ctx context.Context, caller domain.Caller, req GetSprintReq) (GetSprintResp, error)

	// List is list_sprints.
	// Tier: read. Writes nothing.
	List(ctx context.Context, caller domain.Caller, req ListSprintsReq) (ListSprintsResp, error)
}

// CreateSprintReq is create_sprint's input.
type CreateSprintReq struct {
	Acceptance     string `json:"acceptance"`
	BoardID        string `json:"boardId"`
	Goal           string `json:"goal"`
	Name           string `json:"name"`
	ProjectID      string `json:"projectId"`
	IdempotencyKey string `json:"idempotencyKey,omitzero"`
}

// CreateSprintResp is create_sprint's output.
type CreateSprintResp struct {
	Sprint SprintDoc `json:"sprint"`
}

// UpdateSprintReq is update_sprint's input.
type UpdateSprintReq struct {
	Patch     SprintPatchIn `json:"patch"`
	ProjectID string        `json:"projectId"`
	SprintID  string        `json:"sprintId"`
	Version   int64         `json:"version"`
}

// SprintPatchIn is update_sprint's input patch. It is an open object: conductor's schema allows
// properties it does not name.
type SprintPatchIn struct {
	Acceptance     Optional[string]       `json:"acceptance,omitzero"`
	Checkpoint     Nullable[string]       `json:"checkpoint,omitzero"`
	EventCursor    Nullable[string]       `json:"eventCursor,omitzero"`
	Goal           Optional[string]       `json:"goal,omitzero"`
	Name           Optional[string]       `json:"name,omitzero"`
	ProposalTaskID Nullable[string]       `json:"proposalTaskId,omitzero"`
	Status         Optional[SprintStatus] `json:"status,omitzero"`
}

// UpdateSprintResp is update_sprint's output.
type UpdateSprintResp struct {
	Sprint   SprintDoc `json:"sprint"`
	Warnings []string  `json:"warnings,omitzero"`
}

// SetSprintReq is set_sprint's input.
type SetSprintReq struct {
	Action    SprintAction `json:"action"`
	ProjectID string       `json:"projectId"`
	SprintID  string       `json:"sprintId"`
	TaskID    string       `json:"taskId"`
	Version   int64        `json:"version"`
}

// SetSprintResp is set_sprint's output.
type SetSprintResp struct {
	Task Task `json:"task"`
}

// GetSprintReq is get_sprint's input.
type GetSprintReq struct {
	ProjectID string          `json:"projectId"`
	SprintID  string          `json:"sprintId"`
	Include   []SprintInclude `json:"include,omitzero"`
}

// GetSprintResp is get_sprint's output.
type GetSprintResp struct {
	Attention    []SprintAttention          `json:"attention"`
	Counts       SprintCounts               `json:"counts"`
	Frontier     []string                   `json:"frontier"`
	Policies     SprintPolicies             `json:"policies"`
	Resume       map[string]*ResumeBriefing `json:"resume"`
	Sprint       SprintDoc                  `json:"sprint"`
	Tasks        []SprintMember             `json:"tasks"`
	Truncated    bool                       `json:"truncated"`
	Workflow     SprintWorkflow             `json:"workflow"`
	Deliverables []SprintDeliverable        `json:"deliverables,omitzero"`
	Memory       []MemoryEntry              `json:"memory,omitzero"`
}

// ListSprintsReq is list_sprints's input.
type ListSprintsReq struct {
	ProjectID string       `json:"projectId"`
	BoardID   string       `json:"boardId,omitzero"`
	Cursor    string       `json:"cursor,omitzero"`
	Limit     int64        `json:"limit,omitzero"`
	Status    SprintStatus `json:"status,omitzero"`
}

// ListSprintsResp is list_sprints's output.
type ListSprintsResp struct {
	Sprints    []SprintDoc `json:"sprints"`
	NextCursor string      `json:"nextCursor,omitzero"`
}

// SprintDoc is a sprint as every sprint tool returns it (conductor's SprintSchema).
type SprintDoc struct {
	Acceptance     string       `json:"acceptance"`
	ApprovedBy     *string      `json:"approvedBy"`
	BoardID        string       `json:"boardId"`
	Checkpoint     *string      `json:"checkpoint"`
	ClosedAt       *string      `json:"closedAt"`
	CreatedAt      string       `json:"createdAt"`
	CreatedBy      string       `json:"createdBy"`
	EventCursor    *string      `json:"eventCursor"`
	Goal           string       `json:"goal"`
	ID             string       `json:"id"`
	MemberCount    float64      `json:"memberCount"`
	Name           string       `json:"name"`
	ProjectID      string       `json:"projectId"`
	ProposalTaskID *string      `json:"proposalTaskId"`
	StartedAt      *string      `json:"startedAt"`
	Status         SprintStatus `json:"status"`
	UpdatedAt      string       `json:"updatedAt"`
	UpdatedBy      string       `json:"updatedBy"`
	Version        float64      `json:"version"`
}

// SprintMember is get_sprint's output tasks[]: a member as the cold-resume read shows it; claim is
// the live lease only.
type SprintMember struct {
	Assignee       *string         `json:"assignee"`
	BlockedBy      []string        `json:"blockedBy"`
	Blocks         []string        `json:"blocks"`
	Claim          *SprintClaim    `json:"claim"`
	HasDeliverable bool            `json:"hasDeliverable"`
	ID             string          `json:"id"`
	Priority       domain.Priority `json:"priority"`
	Status         domain.Status   `json:"status"`
	Title          string          `json:"title"`
}

// SprintClaim is get_sprint's output tasks[].claim.
type SprintClaim struct {
	Agent     *string `json:"agent"`
	ExpiresAt string  `json:"expiresAt"`
}

// SprintCounts is get_sprint's output counts. backlog and ready are conductor's names: backlog is
// always 0 (ADR-0005 has no parking state) and ready counts TODO; poisoned replaces deadLetter
// (plan §Fold 1) and blocked counts the BLOCKED members that are not poisoned.
type SprintCounts struct {
	Backlog    float64 `json:"backlog"`
	Blocked    float64 `json:"blocked"`
	Done       float64 `json:"done"`
	InProgress float64 `json:"inProgress"`
	Poisoned   float64 `json:"poisoned"`
	Ready      float64 `json:"ready"`
	Review     float64 `json:"review"`
	Total      float64 `json:"total"`
}

// SprintAttention is get_sprint's output attention[].
type SprintAttention struct {
	TaskID string       `json:"taskId"`
	Why    AttentionWhy `json:"why"`
}

// SprintPolicies is get_sprint's output policies.
type SprintPolicies struct {
	ArtifactPolicy     PolicyMode `json:"artifactPolicy"`
	DefaultReviewer    *string    `json:"defaultReviewer"`
	SelfApprovalPolicy PolicyMode `json:"selfApprovalPolicy"`
}

// SprintWorkflow is get_sprint's output workflow: the board's lanes and the fixed table.
type SprintWorkflow struct {
	Lanes       []Column                   `json:"lanes"`
	Transitions map[string][]domain.Status `json:"transitions"`
}

// SprintDeliverable is get_sprint's output deliverables[].
type SprintDeliverable struct {
	Deliverable string `json:"deliverable"`
	TaskID      string `json:"taskId"`
	Title       string `json:"title"`
}

// SprintAction is set_sprint's action.
type SprintAction string

// SprintActionValues are SprintAction's values, in the schema's order.
var SprintActionValues = []SprintAction{"add", "remove"}

// SprintInclude is a get_sprint include section.
type SprintInclude string

// SprintIncludeValues are SprintInclude's values, in the schema's order.
var SprintIncludeValues = []SprintInclude{"deliverables", "memory"}

// AttentionWhy is why get_sprint names a member under attention.
type AttentionWhy string

// AttentionWhyValues are AttentionWhy's values, in the schema's order.
var AttentionWhyValues = []AttentionWhy{"blocked", "poisoned", "review", "stale_lease"}
