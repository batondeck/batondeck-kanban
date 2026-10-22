package store

import "context"

// Summary is a task's rolling context summary. UpdatedAt is Unix milliseconds; a task that has
// never had one reads as an empty Body with UpdatedAt 0.
type Summary struct {
	ProjectID string
	TaskID    string
	Body      string
	UpdatedBy string
	UpdatedAt int64
}

// Summaries is the summary area: one summary per task. Keys: (project_id, task_id). Errors: a
// missing task is domain.NotFound("Task", id); a stale expected version is domain.Stale(expected,
// actual) with the task's current version as actual.
type Summaries interface {
	// SetSummary replaces the task's summary when the task's version is expectedVersion, and
	// returns it. It checks the version and does not bump it: the bump is the caller's, once per
	// mutation (service.mutate, ADR-0003), so a set_summary moves the task's version by exactly one.
	// On STALE nothing is written.
	SetSummary(ctx context.Context, projectID, taskID, body, updatedBy string, expectedVersion int64) (Summary, error)
	// GetSummary is the task's summary, empty when it has none.
	GetSummary(ctx context.Context, projectID, taskID string) (Summary, error)
}

var (
	_ Summaries = (*SQLite)(nil)
	_ Summaries = (*Postgres)(nil)
)
