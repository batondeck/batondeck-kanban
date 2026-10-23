package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Tasks is the task CRUD, move and delete service.
type Tasks interface {
	// Create is create_task.
	// Tier: master. Event: task.created. Audit: none. Doorbell: broadcast (it lands TODO, ADR-0005, with
	// no assignee on create). Replays on idempotencyKey.
	Create(ctx context.Context, caller domain.Caller, req CreateTaskReq) (CreateTaskResp, error)

	// Get is get_task.
	// Tier: read. Writes nothing.
	Get(ctx context.Context, caller domain.Caller, req GetTaskReq) (GetTaskResp, error)

	// Update is update_task.
	// Tier: master; version-checked (STALE). Event: task.updated. Audit: none. Doorbell: assigned when
	// the patch gives the task a new assignee; broadcast when it clears the assignee of a TODO task.
	Update(ctx context.Context, caller domain.Caller, req UpdateTaskReq) (UpdateTaskResp, error)

	// Move is move_task.
	// Tier: master; version-checked (STALE), the fixed table (INVALID_TRANSITION), the WIP limit
	// (WIP_EXCEEDED), another identity's live lease (CONFLICT_LOCKED). Event: task.moved, plus
	// task.dependency_cleared on each dependent a move to DONE unblocks. Audit: none. Doorbell: broadcast for
	// every task that lands TODO with no assignee (the moved one and the unblocked dependents), and
	// assigned to the assignee of each unblocked dependent that has one.
	Move(ctx context.Context, caller domain.Caller, req MoveTaskReq) (MoveTaskResp, error)

	// List is list_tasks.
	// Tier: read. Writes nothing.
	List(ctx context.Context, caller domain.Caller, req ListTasksReq) (ListTasksResp, error)

	// Delete is delete_task.
	// Tier: master; the plan §Fold 2 cascade. Event: task.deleted (board-level; the task's own log
	// goes with it). Audit: task.deleted. Doorbell: none.
	Delete(ctx context.Context, caller domain.Caller, req DeleteTaskReq) (DeleteTaskResp, error)
}

// CreateTaskReq is create_task's input.
type CreateTaskReq struct {
	BoardID              string                `json:"boardId"`
	ProjectID            string                `json:"projectId"`
	Title                string                `json:"title"`
	ColumnID             string                `json:"columnId,omitzero"`
	CustomFields         map[string]FieldValue `json:"customFields,omitzero"`
	Description          string                `json:"description,omitzero"`
	IdempotencyKey       string                `json:"idempotencyKey,omitzero"`
	Labels               []string              `json:"labels,omitzero"`
	ModelHint            ModelHintInput        `json:"modelHint,omitzero"`
	ParentTaskID         *string               `json:"parentTaskId,omitzero"`
	Priority             domain.Priority       `json:"priority,omitzero"`
	RequiredCapabilities []string              `json:"requiredCapabilities,omitzero"`
	SprintID             string                `json:"sprintId,omitzero"`
}

// CreateTaskResp is create_task's output.
type CreateTaskResp struct {
	Task Task `json:"task"`
}

// GetTaskReq is get_task's input.
type GetTaskReq struct {
	ProjectID string `json:"projectId"`
	TaskID    string `json:"taskId"`
}

// GetTaskResp is get_task's output.
type GetTaskResp struct {
	Task Task `json:"task"`
}

// UpdateTaskReq is update_task's input.
type UpdateTaskReq struct {
	Patch      TaskPatch `json:"patch"`
	ProjectID  string    `json:"projectId"`
	TaskID     string    `json:"taskId"`
	Version    int64     `json:"version"`
	AutoAssign bool      `json:"autoAssign,omitzero"`
}

// UpdateTaskResp is update_task's output.
type UpdateTaskResp struct {
	Task Task `json:"task"`
}

// MoveTaskReq is move_task's input.
type MoveTaskReq struct {
	ProjectID  string        `json:"projectId"`
	TaskID     string        `json:"taskId"`
	Version    int64         `json:"version"`
	Order      string        `json:"order,omitzero"`
	ToColumnID string        `json:"toColumnId,omitzero"`
	ToStatus   domain.Status `json:"toStatus,omitzero"`
}

// MoveTaskResp is move_task's output.
type MoveTaskResp struct {
	Task     Task     `json:"task"`
	Warnings []string `json:"warnings,omitzero"`
}

// ListTasksReq is list_tasks's input.
type ListTasksReq struct {
	BoardID          string           `json:"boardId"`
	ProjectID        string           `json:"projectId"`
	Assignee         Nullable[string] `json:"assignee,omitzero"`
	Cursor           string           `json:"cursor,omitzero"`
	Label            string           `json:"label,omitzero"`
	Limit            int64            `json:"limit,omitzero"`
	MissingArtifacts bool             `json:"missingArtifacts,omitzero"`
	SprintID         string           `json:"sprintId,omitzero"`
	Status           domain.Status    `json:"status,omitzero"`
	UnblockedOnly    bool             `json:"unblockedOnly,omitzero"`
	UpdatedSince     string           `json:"updatedSince,omitzero"`
}

// ListTasksResp is list_tasks's output.
type ListTasksResp struct {
	Tasks      []Task `json:"tasks"`
	NextCursor string `json:"nextCursor,omitzero"`
}

// DeleteTaskReq is delete_task's input.
type DeleteTaskReq struct {
	ProjectID string `json:"projectId"`
	TaskID    string `json:"taskId"`
	Version   int64  `json:"version"`
}

// DeleteTaskResp is delete_task's output.
type DeleteTaskResp struct {
	Ok True `json:"ok"`
}

// ModelHintInput is create_task's input modelHint, first seen there. It is an open object: conductor's schema allows properties it does not name.
type ModelHintInput struct {
	Model     string `json:"model"`
	Effort    Effort `json:"effort,omitzero"`
	Rationale string `json:"rationale,omitzero"`
}

// Task is create_task's output task, first seen there.
type Task struct {
	Assignee             *string               `json:"assignee"`
	BlockedBy            []string              `json:"blockedBy"`
	Blocks               []string              `json:"blocks"`
	BoardID              string                `json:"boardId"`
	Claim                *TaskClaim            `json:"claim"`
	ColumnID             string                `json:"columnId"`
	ContextSummary       string                `json:"contextSummary"`
	CreatedAt            string                `json:"createdAt"`
	CreatedBy            string                `json:"createdBy"`
	CustomFields         map[string]FieldValue `json:"customFields"`
	Description          string                `json:"description"`
	DueAt                *string               `json:"dueAt"`
	Estimate             *float64              `json:"estimate"`
	ID                   string                `json:"id"`
	Labels               []string              `json:"labels"`
	Order                string                `json:"order"`
	ParentTaskID         *string               `json:"parentTaskId"`
	Priority             domain.Priority       `json:"priority"`
	ProjectID            string                `json:"projectId"`
	RequiredCapabilities []string              `json:"requiredCapabilities"`
	Status               domain.Status         `json:"status"`
	Title                string                `json:"title"`
	UpdatedAt            string                `json:"updatedAt"`
	UpdatedBy            string                `json:"updatedBy"`
	Version              float64               `json:"version"`
	Artifacts            []Artifact            `json:"artifacts,omitzero"`
	Chars                ContextChars          `json:"chars,omitzero"`
	ClaimAttempts        float64               `json:"claimAttempts,omitzero"`
	CreatedByAgent       *string               `json:"createdByAgent,omitzero"`
	Deliverable          *string               `json:"deliverable,omitzero"`
	ModelHint            *ModelHint            `json:"modelHint,omitzero"`
	NextEligibleAt       *string               `json:"nextEligibleAt,omitzero"`
	RaceOpenedAt         *string               `json:"raceOpenedAt,omitzero"`
	Racing               bool                  `json:"racing,omitzero"`
	Review               *TaskReview           `json:"review,omitzero"`
	Reviewer             *string               `json:"reviewer,omitzero"`
	RunOf                *string               `json:"runOf,omitzero"`
	SprintIDs            []string              `json:"sprintIds,omitzero"`
}

// TaskClaim is create_task's output task.claim, first seen there.
type TaskClaim struct {
	ExpiresAt string  `json:"expiresAt"`
	Owner     string  `json:"owner"`
	Agent     *string `json:"agent,omitzero"`
	AgentID   *string `json:"agentId,omitzero"`
}

// Artifact is create_task's output task.artifacts[], first seen there.
type Artifact struct {
	AddedBy      string              `json:"addedBy"`
	AddedByAgent *string             `json:"addedByAgent"`
	Kind         ArtifactKind        `json:"kind"`
	Ref          *string             `json:"ref"`
	Repo         *string             `json:"repo"`
	Title        *string             `json:"title"`
	Ts           string              `json:"ts"`
	URL          *string             `json:"url"`
	Retracted    *ArtifactRetraction `json:"retracted,omitzero"`
}

// ArtifactRetraction is create_task's output task.artifacts[].retracted, first seen there.
type ArtifactRetraction struct {
	At      string  `json:"at"`
	By      string  `json:"by"`
	ByAgent *string `json:"byAgent"`
	Reason  string  `json:"reason"`
}

// ContextChars is create_task's output task.chars, first seen there.
type ContextChars struct {
	Attachments float64 `json:"attachments"`
	Comments    float64 `json:"comments"`
	Doc         float64 `json:"doc"`
	Followups   float64 `json:"followups"`
	Items       float64 `json:"items"`
	Memory      float64 `json:"memory"`
	Total       float64 `json:"total"`
}

// ModelHint is create_task's output task.modelHint, first seen there.
type ModelHint struct {
	Model     string `json:"model"`
	Effort    Effort `json:"effort,omitzero"`
	Rationale string `json:"rationale,omitzero"`
}

// TaskReview is create_task's output task.review, first seen there.
type TaskReview struct {
	CompletedBy      string  `json:"completedBy"`
	CompletedByAgent *string `json:"completedByAgent"`
	RequestedAt      string  `json:"requestedAt"`
	RequestedBy      string  `json:"requestedBy"`
	Reviewer         *string `json:"reviewer"`
}

// TaskPatch is update_task's input patch, first seen there. It is an open object: conductor's schema allows properties it does not name.
type TaskPatch struct {
	Assignee             Nullable[string]         `json:"assignee,omitzero"`
	CustomFields         map[string]FieldValue    `json:"customFields,omitzero"`
	Description          Optional[string]         `json:"description,omitzero"`
	DueAt                Nullable[string]         `json:"dueAt,omitzero"`
	Estimate             Nullable[float64]        `json:"estimate,omitzero"`
	Labels               []string                 `json:"labels,omitzero"`
	ModelHint            Nullable[ModelHintInput] `json:"modelHint,omitzero"`
	Priority             domain.Priority          `json:"priority,omitzero"`
	RequiredCapabilities []string                 `json:"requiredCapabilities,omitzero"`
	Reviewer             Nullable[string]         `json:"reviewer,omitzero"`
	Title                string                   `json:"title,omitzero"`
}

// FieldValue is a custom field's value: a string (at most 500 characters), a number or a boolean.
type FieldValue any

// Effort is a model hint's effort level.
type Effort string

// EffortValues are Effort's values, in the schema's order.
var EffortValues = []Effort{"high", "low", "medium", "xhigh"}

// ArtifactKind is what an artifact on a task points at.
type ArtifactKind string

// ArtifactKindValues are ArtifactKind's values, in the schema's order.
var ArtifactKindValues = []ArtifactKind{"attachment", "branch", "commit", "doc", "file", "pr"}
