package store

import (
	"context"
	"fmt"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The subtask read of contract_subtasks.go, on both engines: one statement, FROM the parent.

type subtaskQueries interface {
	ListSubtaskIDs(ctx context.Context, arg sqlitedb.ListSubtaskIDsParams) ([]sqlitedb.ListSubtaskIDsRow, error)
}

func (s *SQLite) ListSubtasks(ctx context.Context, projectID, parentTaskID string) ([]string, error) {
	return listSubtasks(ctx, s, projectID, parentTaskID)
}

func (s *Postgres) ListSubtasks(ctx context.Context, projectID, parentTaskID string) ([]string, error) {
	return listSubtasks(ctx, s, projectID, parentTaskID)
}

func listSubtasks(ctx context.Context, e engine, projectID, parentTaskID string) ([]string, error) {
	rows, err := e.queries(false).ListSubtaskIDs(ctx, sqlitedb.ListSubtaskIDsParams{ProjectID: projectID, ID: parentTaskID})
	if err != nil {
		return nil, fmt.Errorf("store: list subtasks: %w", err)
	}
	if len(rows) == 0 {
		return nil, domain.NotFound("Task", parentTaskID)
	}
	out := []string{}
	for _, r := range rows {
		if r.ID.Valid {
			out = append(out, r.ID.String)
		}
	}
	return out, nil
}

func (p pgQueries) ListSubtaskIDs(ctx context.Context, arg sqlitedb.ListSubtaskIDsParams) ([]sqlitedb.ListSubtaskIDsRow, error) {
	rows, err := p.q.ListSubtaskIDs(ctx, pgdb.ListSubtaskIDsParams(arg))
	return convert(rows, func(r pgdb.ListSubtaskIDsRow) sqlitedb.ListSubtaskIDsRow { return sqlitedb.ListSubtaskIDsRow(r) }), err
}
