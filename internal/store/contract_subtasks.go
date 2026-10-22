package store

import "context"

// Subtasks is the parent/child read. A child is a task whose parent_task_id is the parent; writing
// one (add_subtask, with the parent's BLOCKED landing) is the service's, over Tasks and Dependencies
// (ADR-0003). Keys: (project_id, task_id). Errors: a missing parent is domain.NotFound("Task", id).
type Subtasks interface {
	// ListSubtasks is the ids of the parent's children in task-number order (T-2 before T-10); a
	// parent with none is an empty list. The tasks themselves are read with Tasks.ListByIDs.
	ListSubtasks(ctx context.Context, projectID, parentTaskID string) ([]string, error)
}

var (
	_ Subtasks = (*SQLite)(nil)
	_ Subtasks = (*Postgres)(nil)
)
