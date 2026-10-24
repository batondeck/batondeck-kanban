package store

import (
	"context"
	"fmt"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The runs area of contract_runs.go, on both engines: one INSERT, its missing run task the foreign
// key's refusal, and one SELECT.

type runQueries interface {
	InsertRun(ctx context.Context, arg sqlitedb.InsertRunParams) (int64, error)
	ListRuns(ctx context.Context, arg sqlitedb.ListRunsParams) ([]sqlitedb.Run, error)
}

func (s *SQLite) InsertRun(ctx context.Context, r Run) error { return insertRun(ctx, s, r) }

func (s *Postgres) InsertRun(ctx context.Context, r Run) error { return insertRun(ctx, s, r) }

func (s *SQLite) ListRuns(ctx context.Context, projectID, parentTaskID string) ([]Run, error) {
	return listRuns(ctx, s, projectID, parentTaskID)
}

func (s *Postgres) ListRuns(ctx context.Context, projectID, parentTaskID string) ([]Run, error) {
	return listRuns(ctx, s, projectID, parentTaskID)
}

// CheckRun is the shape a Runs.InsertRun row must have, shared with memstore.
func CheckRun(r Run) error {
	if r.ParentTaskID == "" || r.OpenedBy == "" || r.Ordinal < 1 || r.OpenedAt <= 0 {
		return domain.Validation("a run needs a parent, an opener, an ordinal >= 1 and a time", nil)
	}
	return nil
}

func insertRun(ctx context.Context, e engine, r Run) error {
	if err := CheckRun(r); err != nil {
		return err
	}
	n, err := e.queries(true).InsertRun(ctx, sqlitedb.InsertRunParams{
		ProjectID: r.ProjectID, RunTaskID: r.RunTaskID, ParentTaskID: r.ParentTaskID, Ordinal: r.Ordinal,
		OpenedBy: r.OpenedBy, OpenedAt: r.OpenedAt,
	})
	if isForeignKeyViolation(err) {
		return domain.NotFound("Task", r.RunTaskID)
	}
	if err != nil {
		return fmt.Errorf("store: insert run: %w", err)
	}
	if n == 0 {
		return domain.Validation("run "+r.RunTaskID+" is already recorded", map[string]any{"runTaskId": r.RunTaskID})
	}
	return nil
}

func listRuns(ctx context.Context, e engine, projectID, parentTaskID string) ([]Run, error) {
	rows, err := e.queries(false).ListRuns(ctx, sqlitedb.ListRunsParams{ProjectID: projectID, ParentTaskID: parentTaskID})
	if err != nil {
		return nil, fmt.Errorf("store: list runs: %w", err)
	}
	out := make([]Run, len(rows))
	for i, r := range rows {
		out[i] = Run(r)
	}
	return out, nil
}

func (p pgQueries) InsertRun(ctx context.Context, arg sqlitedb.InsertRunParams) (int64, error) {
	return p.q.InsertRun(ctx, pgdb.InsertRunParams(arg))
}

func (p pgQueries) ListRuns(ctx context.Context, arg sqlitedb.ListRunsParams) ([]sqlitedb.Run, error) {
	rows, err := p.q.ListRuns(ctx, pgdb.ListRunsParams(arg))
	return convert(rows, func(r pgdb.Run) sqlitedb.Run { return sqlitedb.Run(r) }), err
}
