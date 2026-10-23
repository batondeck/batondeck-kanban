package store

import "context"

// Run is one runs row: the run task RunTaskID is run Ordinal of ParentTaskID, opened by OpenedBy at
// OpenedAt (Unix milliseconds). The run task itself is a task whose RunOf is the parent (Tasks).
type Run struct {
	ProjectID    string
	RunTaskID    string
	ParentTaskID string
	Ordinal      int64
	OpenedBy     string
	OpenedAt     int64
}

// Runs is the race bookkeeping. A run task is created through Tasks.CreateTask with RunOf set, and
// the service records its ordinal here in the same transaction; deleting the run task deletes its
// row (ON DELETE CASCADE), so a run leaves only through the delete cascade (plan §Fold 2). Keys:
// (project_id, run_task_id). Errors: a run task that does not exist is domain.NotFound("Task", id),
// one already recorded domain.Validation; an empty parent or opener, an ordinal < 1 or OpenedAt <= 0
// is domain.Validation.
type Runs interface {
	// InsertRun records r.
	InsertRun(ctx context.Context, r Run) error
	// ListRuns is the parent's run rows, ordinal ascending (then run task id, as bytes); a parent
	// with none, or that does not exist, is an empty list. At most 200 rows: a race is capped far
	// below that (maxRunsPerTask is at most 50).
	ListRuns(ctx context.Context, projectID, parentTaskID string) ([]Run, error)
}

var (
	_ Runs = (*SQLite)(nil)
	_ Runs = (*Postgres)(nil)
)
