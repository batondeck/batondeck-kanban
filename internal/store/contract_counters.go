package store

import "context"

// Counters mints task ids. A task's key is (project_id, task_id): T-n is unique only within its
// project (conductor rule 6, T-111), so every project has a T-1.
type Counters interface {
	// NextTaskID returns the project's next id, "T-<n>": T-1 first, one higher on each call, never
	// a number a committed call handed out, on either engine, under any concurrency (one UPSERT
	// statement). Deleting a task does not lower the counter. Inside Atomically the increment is
	// part of that transaction: a rollback undoes it, and the number, which no committed row holds,
	// is handed out again. An unknown project is NOT_FOUND (domain.NotFound("Project", id)).
	NextTaskID(ctx context.Context, projectID string) (string, error)
}

var (
	_ Counters = (*SQLite)(nil)
	_ Counters = (*Postgres)(nil)
)
