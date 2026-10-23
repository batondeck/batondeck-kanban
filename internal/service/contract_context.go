package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Context is the per-task context service: items, the composed briefing, the summary, memory,
// dependencies and subtasks.
type Context interface {
	// AddItem is add_context_item.
	// Tier: write. Event: context.added. Audit: none. Doorbell: none. Replays on idempotencyKey.
	AddItem(ctx context.Context, caller domain.Caller, req AddContextItemReq) (AddContextItemResp, error)

	// GetTaskContext is get_task_context.
	// Tier: read. Writes nothing. The briefing carries no id, title or description, and its items
	// come back under items (a contract quirk kept as-is).
	GetTaskContext(ctx context.Context, caller domain.Caller, req GetTaskContextReq) (GetTaskContextResp, error)

	// SetSummary is set_summary.
	// Tier: write; version-checked (STALE). Event: summary.set. Audit: none. Doorbell: none.
	SetSummary(ctx context.Context, caller domain.Caller, req SetSummaryReq) (SetSummaryResp, error)

	// WriteMemory is write_memory.
	// Tier: write. Event: memory.written. Audit: none. Doorbell: none.
	WriteMemory(ctx context.Context, caller domain.Caller, req WriteMemoryReq) (WriteMemoryResp, error)

	// ReadMemory is read_memory.
	// Tier: read. Writes nothing.
	ReadMemory(ctx context.Context, caller domain.Caller, req ReadMemoryReq) (ReadMemoryResp, error)

	// RecallMemory is recall_memory.
	// Not tenant-scoped (the scopes the caller can read). Writes nothing.
	RecallMemory(ctx context.Context, caller domain.Caller, req RecallMemoryReq) (RecallMemoryResp, error)

	// AddDependency is add_dependency.
	// Tier: master; CYCLE_DETECTED. Event: dependency.added. Audit: none. Doorbell: none.
	AddDependency(ctx context.Context, caller domain.Caller, req AddDependencyReq) (AddDependencyResp, error)

	// RemoveDependency is remove_dependency.
	// Tier: master. Event: dependency.removed, plus task.dependency_cleared when it unblocks the
	// dependent. Audit: none. Doorbell: when it unblocks the dependent, assigned to its assignee if it
	// has one, else broadcast. The dependency_cleared event and the doorbell are a Go choice: conductor
	// emits only dependency.removed.
	RemoveDependency(ctx context.Context, caller domain.Caller, req RemoveDependencyReq) (RemoveDependencyResp, error)

	// AddSubtask is add_subtask.
	// Tier: master. Creates the child with parentTaskId in the board's first column; adds no edge and
	// leaves the parent as it is. Event: task.created (the child, in the board's first column). Audit:
	// none. Doorbell: broadcast only if that column is TODO. Replays on idempotencyKey.
	AddSubtask(ctx context.Context, caller domain.Caller, req AddSubtaskReq) (AddSubtaskResp, error)

	// ListSubtasks is list_subtasks.
	// Tier: read. Writes nothing.
	ListSubtasks(ctx context.Context, caller domain.Caller, req ListSubtasksReq) (ListSubtasksResp, error)
}

// AddContextItemReq is add_context_item's input.
type AddContextItemReq struct {
	Body           string      `json:"body"`
	Kind           ContextKind `json:"kind"`
	ProjectID      string      `json:"projectId"`
	TaskID         string      `json:"taskId"`
	IdempotencyKey string      `json:"idempotencyKey,omitzero"`
}

// AddContextItemResp is add_context_item's output.
type AddContextItemResp struct {
	Item     ContextItem `json:"item"`
	Warnings []string    `json:"warnings,omitzero"`
}

// GetTaskContextReq is get_task_context's input.
type GetTaskContextReq struct {
	ProjectID            string           `json:"projectId"`
	TaskID               string           `json:"taskId"`
	Cursor               string           `json:"cursor,omitzero"`
	Include              []ContextSection `json:"include,omitzero"`
	IncludeProjectMemory bool             `json:"includeProjectMemory,omitzero"`
	IncludeUpstream      bool             `json:"includeUpstream,omitzero"`
	Limit                int64            `json:"limit,omitzero"`
}

// GetTaskContextResp is get_task_context's output.
type GetTaskContextResp struct {
	Artifacts         []Artifact            `json:"artifacts"`
	Attachments       []AttachmentBrief     `json:"attachments"`
	Deps              TaskDeps              `json:"deps"`
	Fields            map[string]FieldValue `json:"fields"`
	Items             []ContextItem         `json:"items"`
	Memory            []MemoryEntry         `json:"memory"`
	ModelHint         *ModelHint            `json:"modelHint"`
	OpenFollowUpCount float64               `json:"openFollowUpCount"`
	OpenFollowUps     []OpenFollowUp        `json:"openFollowUps"`
	Resume            *ResumeBriefing       `json:"resume"`
	Review            *TaskReview           `json:"review"`
	Summary           string                `json:"summary"`
	Workflow          Workflow              `json:"workflow"`
	Deliverable       *string               `json:"deliverable,omitzero"`
	NextCursor        string                `json:"nextCursor,omitzero"`
	ProjectMemory     []MemoryEntry         `json:"projectMemory,omitzero"`
	Upstream          []UpstreamTask        `json:"upstream,omitzero"`
}

// SetSummaryReq is set_summary's input.
type SetSummaryReq struct {
	ProjectID string `json:"projectId"`
	Summary   string `json:"summary"`
	TaskID    string `json:"taskId"`
	Version   int64  `json:"version"`
}

// SetSummaryResp is set_summary's output.
type SetSummaryResp struct {
	Task Task `json:"task"`
}

// WriteMemoryReq is write_memory's input.
type WriteMemoryReq struct {
	Key            string             `json:"key"`
	ProjectID      string             `json:"projectId"`
	Scope          domain.MemoryScope `json:"scope"`
	IdempotencyKey string             `json:"idempotencyKey,omitzero"`
	LargeArtifact  LargeArtifact      `json:"largeArtifact,omitzero"`
	TaskID         string             `json:"taskId,omitzero"`
	TTL            string             `json:"ttl,omitzero"`
	Value          any                `json:"value,omitzero"`
}

// WriteMemoryResp is write_memory's output.
type WriteMemoryResp struct {
	EntryID   string   `json:"entryId"`
	UploadURL string   `json:"uploadUrl,omitzero"`
	Warnings  []string `json:"warnings,omitzero"`
}

// ReadMemoryReq is read_memory's input.
type ReadMemoryReq struct {
	ProjectID string          `json:"projectId"`
	TaskID    string          `json:"taskId"`
	Key       string          `json:"key,omitzero"`
	Scope     ReadMemoryScope `json:"scope,omitzero"`
}

// ReadMemoryResp is read_memory's output.
type ReadMemoryResp struct {
	Entries []MemoryEntry `json:"entries"`
}

// RecallMemoryReq is recall_memory's input.
type RecallMemoryReq struct {
	K         int64             `json:"k,omitzero"`
	Key       string            `json:"key,omitzero"`
	ProjectID string            `json:"projectId,omitzero"`
	Scope     RecallMemoryScope `json:"scope,omitzero"`
}

// RecallMemoryResp is recall_memory's output.
type RecallMemoryResp struct {
	Entries []MemoryEntry `json:"entries"`
}

// AddDependencyReq is add_dependency's input.
type AddDependencyReq struct {
	FromTaskID     string         `json:"fromTaskId"`
	ProjectID      string         `json:"projectId"`
	ToTaskID       string         `json:"toTaskId"`
	IdempotencyKey string         `json:"idempotencyKey,omitzero"`
	Type           DependencyType `json:"type,omitzero"`
}

// AddDependencyResp is add_dependency's output.
type AddDependencyResp struct {
	Edge DependencyEdge `json:"edge"`
}

// RemoveDependencyReq is remove_dependency's input.
type RemoveDependencyReq struct {
	EdgeID    string `json:"edgeId"`
	ProjectID string `json:"projectId"`
}

// RemoveDependencyResp is remove_dependency's output.
type RemoveDependencyResp struct {
	Ok True `json:"ok"`
}

// AddSubtaskReq is add_subtask's input.
type AddSubtaskReq struct {
	ParentTaskID   string          `json:"parentTaskId"`
	ProjectID      string          `json:"projectId"`
	Title          string          `json:"title"`
	Description    string          `json:"description,omitzero"`
	IdempotencyKey string          `json:"idempotencyKey,omitzero"`
	Priority       domain.Priority `json:"priority,omitzero"`
}

// AddSubtaskResp is add_subtask's output.
type AddSubtaskResp struct {
	Task Task `json:"task"`
}

// ListSubtasksReq is list_subtasks's input.
type ListSubtasksReq struct {
	ParentTaskID string `json:"parentTaskId"`
	ProjectID    string `json:"projectId"`
}

// ListSubtasksResp is list_subtasks's output.
type ListSubtasksResp struct {
	Tasks []Task `json:"tasks"`
}

// ContextItem is add_context_item's output item, first seen there.
type ContextItem struct {
	Author string      `json:"author"`
	Body   string      `json:"body"`
	ID     string      `json:"id"`
	Kind   ContextKind `json:"kind"`
	TaskID string      `json:"taskId"`
	Ts     string      `json:"ts"`
}

// AttachmentBrief is get_task_context's output attachments[], first seen there.
type AttachmentBrief struct {
	Bytes         float64          `json:"bytes"`
	CreatedBy     string           `json:"createdBy"`
	ExtractedText *string          `json:"extractedText"`
	GCSURI        string           `json:"gcsUri"`
	ID            string           `json:"id"`
	Kind          MediaKind        `json:"kind"`
	MimeType      string           `json:"mimeType"`
	SHA256        *string          `json:"sha256"`
	Status        AttachmentStatus `json:"status"`
	TaskID        string           `json:"taskId"`
	ThumbURI      *string          `json:"thumbUri"`
	Ts            string           `json:"ts"`
}

// TaskDeps is get_task_context's output deps, first seen there.
type TaskDeps struct {
	BlockedBy []string         `json:"blockedBy"`
	Blocks    []string         `json:"blocks"`
	Edges     []DependencyEdge `json:"edges"`
}

// DependencyEdge is get_task_context's output deps.edges[], first seen there.
type DependencyEdge struct {
	CreatedBy  string         `json:"createdBy"`
	FromTaskID string         `json:"fromTaskId"`
	ID         string         `json:"id"`
	ProjectID  string         `json:"projectId"`
	ToTaskID   string         `json:"toTaskId"`
	Ts         string         `json:"ts"`
	Type       DependencyType `json:"type"`
}

// MemoryEntry is get_task_context's output memory[], first seen there.
type MemoryEntry struct {
	Author string             `json:"author"`
	GCSURI *string            `json:"gcsUri"`
	ID     string             `json:"id"`
	Key    string             `json:"key"`
	Scope  domain.MemoryScope `json:"scope"`
	TaskID *string            `json:"taskId"`
	Ts     string             `json:"ts"`
	TTL    *string            `json:"ttl"`
	Value  any                `json:"value"`
}

// OpenFollowUp is get_task_context's output openFollowUps[], first seen there.
type OpenFollowUp struct {
	Author    string         `json:"author"`
	Body      string         `json:"body"`
	ID        string         `json:"id"`
	ProjectID string         `json:"projectId"`
	Reopened  bool           `json:"reopened"`
	Status    FollowUpStatus `json:"status"`
	Target    *string        `json:"target"`
	TaskID    string         `json:"taskId"`
	Ts        string         `json:"ts"`
}

// Workflow is get_task_context's output workflow, first seen there.
type Workflow struct {
	AllowedMoves []domain.Status `json:"allowedMoves"`
	Lanes        []Column        `json:"lanes"`
	Status       domain.Status   `json:"status"`
}

// UpstreamTask is get_task_context's output upstream[], first seen there.
type UpstreamTask struct {
	Deliverable *string `json:"deliverable"`
	Status      string  `json:"status"`
	Summary     string  `json:"summary"`
	TaskID      string  `json:"taskId"`
	Title       string  `json:"title"`
}

// LargeArtifact is write_memory's input largeArtifact, first seen there. It is an open object: conductor's schema allows properties it does not name.
type LargeArtifact struct {
	FileName string `json:"fileName"`
	MimeType string `json:"mimeType"`
}

// ContextKind is a context item's kind.
type ContextKind string

// ContextKindValues are ContextKind's values, in the schema's order.
var ContextKindValues = []ContextKind{"decision", "field", "link", "note"}

// ContextSection names one part of the get_task_context briefing.
type ContextSection string

// ContextSectionValues are ContextSection's values, in the schema's order.
var ContextSectionValues = []ContextSection{"attachments", "deps", "fields", "followups", "items", "memory", "summary"}

// MediaKind is an attachment's media kind.
type MediaKind string

// MediaKindValues are MediaKind's values, in the schema's order.
var MediaKindValues = []MediaKind{"audio", "file", "image", "video"}

// AttachmentStatus is an attachment's processing state.
type AttachmentStatus string

// AttachmentStatusValues are AttachmentStatus's values, in the schema's order.
var AttachmentStatusValues = []AttachmentStatus{"failed", "pending", "ready"}

// DependencyType is a dependency edge's type.
type DependencyType string

// DependencyTypeValues are DependencyType's values, in the schema's order.
var DependencyTypeValues = []DependencyType{"blocks", "relates"}

// FollowUpStatus is a follow-up's state.
type FollowUpStatus string

// FollowUpStatusValues are FollowUpStatus's values, in the schema's order.
var FollowUpStatusValues = []FollowUpStatus{"acked", "open"}

// ReadMemoryScope is the subset of domain.MemoryScope read_memory takes.
type ReadMemoryScope string

// ReadMemoryScopeValues are ReadMemoryScope's values, in the schema's order.
var ReadMemoryScopeValues = []ReadMemoryScope{"agent", "shared", "task"}

// RecallMemoryScope is the subset of domain.MemoryScope recall_memory takes.
type RecallMemoryScope string

// RecallMemoryScopeValues are RecallMemoryScope's values, in the schema's order.
var RecallMemoryScopeValues = []RecallMemoryScope{"agent_global", "project"}
