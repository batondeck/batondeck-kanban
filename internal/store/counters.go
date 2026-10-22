package store

import (
	"context"
	"fmt"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

type counterQueries interface {
	NextTaskNumber(ctx context.Context, projectID string) (int64, error)
}

func (s *SQLite) NextTaskID(ctx context.Context, projectID string) (string, error) {
	return nextTaskID(ctx, s, projectID)
}

func (s *Postgres) NextTaskID(ctx context.Context, projectID string) (string, error) {
	return nextTaskID(ctx, s, projectID)
}

// nextTaskID is one UPSERT on project_counters, on the write side: the increment and the read of
// the new value are one statement, so concurrent callers cannot be handed the same number.
func nextTaskID(ctx context.Context, e engine, projectID string) (string, error) {
	n, err := e.queries(true).NextTaskNumber(ctx, projectID)
	switch {
	case isForeignKeyViolation(err):
		return "", domain.NotFound("Project", projectID)
	case err != nil:
		return "", fmt.Errorf("store: task counter: %w", err)
	}
	return fmt.Sprintf("T-%d", n), nil
}

func (p pgQueries) NextTaskNumber(ctx context.Context, projectID string) (int64, error) {
	return p.q.NextTaskNumber(ctx, projectID)
}
