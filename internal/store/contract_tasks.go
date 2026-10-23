package store

import (
	"context"
	"encoding/json"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Task is one task row. Times are Unix milliseconds; a nil pointer is a NULL column. The JSON
// columns (CustomFields, Deliverable, Review, Chars, ModelHint) are kept as the caller wrote them.
// BlockedBy and Blocks are the caches of the dependency edges (the Dependencies area keeps them).
type Task struct {
	ProjectID            string
	ID                   string
	BoardID              string
	ColumnID             string
	Title                string
	Description          string
	Status               domain.Status
	BlockedReason        *string
	Priority             domain.Priority
	Assignee             *string
	Labels               []string
	RequiredCapabilities []string
	Estimate             *float64
	DueAt                *int64
	ParentTaskID         *string
	OrderKey             string
	CustomFields         json.RawMessage
	BlockedBy            []string
	Blocks               []string
	Deliverable          *string
	Reviewer             *string
	Review               json.RawMessage
	Chars                json.RawMessage
	ModelHint            json.RawMessage
	RunOf                *string
	SprintIDs            []string
	Racing               bool
	RaceOpenedAt         *int64
	ClaimAttempts        int64
	NextEligibleAt       *int64
	Score                float64
	SearchTokens         string
	Claim                *TaskClaim
	Version              int64
	CreatedBy            string
	CreatedByAgent       *string
	CreatedAt            int64
	UpdatedBy            string
	UpdatedAt            int64
}

// TaskClaim is the lease on a task. Owner is the identity holding it; AgentID tells two agents of
// one identity apart. A lease past ExpiresAt is expired: expiry is judged on read, never swept.
type TaskClaim struct {
	Owner     string
	Agent     *string
	AgentID   *string
	LeaseID   string
	ExpiresAt int64
}

// CreateTaskInput is a new task. The id is minted from the project's counter (Counters) in the same
// transaction. Priority "" is normal; nil lists and CustomFields are empty. CreatedAt is the time
// the row records, CreatedBy also its first UpdatedBy.
type CreateTaskInput struct {
	ProjectID            string
	BoardID              string
	ColumnID             string
	Title                string
	Description          string
	Status               domain.Status
	Priority             domain.Priority
	Assignee             *string
	Labels               []string
	RequiredCapabilities []string
	ParentTaskID         *string
	OrderKey             string
	CustomFields         json.RawMessage
	ModelHint            json.RawMessage
	RunOf                *string
	SprintIDs            []string
	CreatedBy            string
	CreatedByAgent       *string
	CreatedAt            int64
}

// Nullable is a patch field for a column that can be NULL: a nil *Nullable leaves the column as it
// is, and a Nullable whose Value is nil sets it to NULL.
type Nullable[T any] struct{ Value *T }

// TaskPatch changes the named fields of a task; a nil field is unchanged. Status, column, order,
// claim, retry and dependency-cache fields are not here: they have their own calls (SetStatus,
// SetOrderKey, Claims, Dependencies). UpdatedBy and UpdatedAt are required.
type TaskPatch struct {
	Title                *string
	Description          *string
	Priority             *domain.Priority
	Assignee             *Nullable[string]
	Labels               *[]string
	RequiredCapabilities *[]string
	Estimate             *Nullable[float64]
	DueAt                *Nullable[int64]
	CustomFields         *json.RawMessage
	Deliverable          *Nullable[string]
	Reviewer             *Nullable[string]
	Review               *Nullable[json.RawMessage]
	Chars                *Nullable[json.RawMessage]
	ModelHint            *Nullable[json.RawMessage]
	SprintIDs            *[]string
	Racing               *bool
	RaceOpenedAt         *Nullable[int64]
	Score                *float64
	SearchTokens         *string
	UpdatedBy            string
	UpdatedAt            int64
}

// StatusChange is what SetStatus writes. ColumnID "" keeps the task's column; OrderKey nil keeps
// its order. BlockedReason is written only when Status is BLOCKED ("" is no reason); any other
// status clears it. ClaimAttempts and NextEligibleAt nil keep them. Labels nil keeps them.
// ClearClaim drops the lease. Unpoison takes a poisoned task (BLOCKED, reason "poison") out of
// quarantine: claim_attempts to 0, next_eligible_at NULL, the poisoned label removed
// (domain.Unpoison); the role gate is the service's. Assignee, Deliverable and Review are written
// with the status as TaskPatch writes them (nil keeps the column, a nil Value sets it NULL), so an
// outcome that hands a task over (complete_task, handoff_task) is one versioned write; a Review that
// is not JSON is VALIDATION. Racing and RaceOpenedAt are written the same way, so a race that opens,
// resolves or ends moves its parent once (start_runs, pick_run, cancel_runs). UpdatedBy and UpdatedAt
// are required.
type StatusChange struct {
	Status         domain.Status
	ColumnID       string
	OrderKey       *string
	BlockedReason  string
	ClaimAttempts  *int64
	NextEligibleAt *Nullable[int64]
	Labels         []string
	ClearClaim     bool
	Unpoison       bool
	Assignee       *Nullable[string]
	Deliverable    *Nullable[string]
	Review         *Nullable[json.RawMessage]
	Racing         *bool
	RaceOpenedAt   *Nullable[int64]
	UpdatedBy      string
	UpdatedAt      int64
}

// TaskFilter narrows ListTasks and CountTasks to one board. Every set field must match: Status,
// ColumnID and Label exactly, Assignee exactly (Unassigned: no assignee; both set is VALIDATION),
// RunOf the parent of a run.
type TaskFilter struct {
	BoardID    string
	Status     *domain.Status
	ColumnID   string
	Assignee   *string
	Unassigned bool
	Label      string
	RunOf      *string
}

// TaskPage is one page of tasks; NextCursor is "" on the last page.
type TaskPage struct {
	Tasks      []Task
	NextCursor string
}

// Tasks is the task area. Keys: a task is (project_id, task_id); T-n is unique only within its
// project, so nothing is looked up by a task id alone. Errors: a missing board is
// domain.NotFound("Board", id), a missing column domain.NotFound("Column", id), a missing task
// domain.NotFound("Task", id) — of a project that does not exist too, except CreateTask, which
// answers domain.NotFound("Project", id); a malformed argument is domain.Validation. Versioned writes take the caller's expectedVersion and return the task at its
// new version (one higher); a version that is not the task's is domain.Stale(expected, actual) and
// changes nothing. A write never appends an event, an audit row or a change-log row: that
// composition is the service's (ADR-0003), inside Atomically. Lists are ordered by (order key, id)
// ascending, both compared as bytes (Postgres: COLLATE "C"), so "Zz" sorts before "a0" as lexorank
// needs and T-10 before T-9; paged by cursor: base64url JSON {"order","id"} of the last row (EncodeCursor),
// read strictly after; a malformed cursor is VALIDATION. A limit <= 0 is 50 and above 200 is 200.
type Tasks interface {
	// CreateTask mints the project's next T-n and inserts the task at version 1, in one
	// transaction. An empty title, creator or order key, a status or priority outside the domain's,
	// CreatedAt <= 0, CustomFields that is not a JSON object or a ModelHint that is not JSON is
	// VALIDATION; the board must be the project's and the column the board's.
	CreateTask(ctx context.Context, in CreateTaskInput) (Task, error)
	// GetTask is one task by (project, id).
	GetTask(ctx context.Context, projectID, taskID string) (Task, error)
	// UpdateTask applies patch at expectedVersion. An empty title, an unknown priority,
	// CustomFields that is not a JSON object, a Review, Chars or ModelHint that is not JSON, or no
	// UpdatedBy or UpdatedAt is VALIDATION.
	UpdateTask(ctx context.Context, projectID, taskID string, expectedVersion int64, patch TaskPatch) (Task, error)
	// SetStatus moves the task to change.Status at expectedVersion. It does not judge the
	// transition (domain.DecideMove is the service's); a status outside the six, an empty order key
	// or no UpdatedBy or UpdatedAt is VALIDATION, a column not on the task's board NOT_FOUND, Unpoison
	// on a task that is not poisoned VALIDATION.
	SetStatus(ctx context.Context, projectID, taskID string, expectedVersion int64, change StatusChange) (Task, error)
	// SetOrderKey sets the task's order key (and, with columnID not "", its column) at
	// expectedVersion. An empty key is VALIDATION, a column not on the task's board NOT_FOUND.
	SetOrderKey(ctx context.Context, projectID, taskID string, expectedVersion int64, orderKey, columnID, updatedBy string, updatedAt int64) (Task, error)
	// ListTasks is one page of the board's tasks matching f. An unknown board is NOT_FOUND, never
	// an empty page.
	ListTasks(ctx context.Context, projectID string, f TaskFilter, cursor string, limit int) (TaskPage, error)
	// CountTasks is how many of the board's tasks match f, in one statement. An unknown board is
	// NOT_FOUND.
	CountTasks(ctx context.Context, projectID string, f TaskFilter) (int64, error)
	// ListTasksByIDs is the project's tasks among ids, in the order of ids; an id with no task is
	// left out, never an error. More than 200 ids is VALIDATION.
	ListTasksByIDs(ctx context.Context, projectID string, ids []string) ([]Task, error)
	// DeleteTask deletes the task at expectedVersion, and with it every row the schema keys on it
	// (ON DELETE CASCADE: its artifacts, summary, run row, dependency edges, context items, memory,
	// comments, follow-ups, attachments and events). The other tasks' BlockedBy/Blocks caches, its
	// subtasks and its runs are the service's cascade (plan §Fold 2). The counter does not go down.
	DeleteTask(ctx context.Context, projectID, taskID string, expectedVersion int64) error
}

var (
	_ Tasks = (*SQLite)(nil)
	_ Tasks = (*Postgres)(nil)
)
