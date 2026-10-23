package store

import (
	"context"
	"fmt"

	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The summary area of contract_summaries.go, on both engines. A read is one statement; a set
// locks the task, judges its version and upserts, without moving the version.

type summaryQueries interface {
	GetSummary(ctx context.Context, arg sqlitedb.GetSummaryParams) (sqlitedb.GetSummaryRow, error)
	UpsertSummary(ctx context.Context, arg sqlitedb.UpsertSummaryParams) (sqlitedb.Summary, error)
}

func (s *SQLite) SetSummary(ctx context.Context, projectID, taskID, body, updatedBy string, expectedVersion int64) (Summary, error) {
	return setSummary(ctx, s, projectID, taskID, body, updatedBy, expectedVersion)
}

func (s *Postgres) SetSummary(ctx context.Context, projectID, taskID, body, updatedBy string, expectedVersion int64) (Summary, error) {
	return setSummary(ctx, s, projectID, taskID, body, updatedBy, expectedVersion)
}

func (s *SQLite) GetSummary(ctx context.Context, projectID, taskID string) (Summary, error) {
	return getSummary(ctx, s, projectID, taskID)
}

func (s *Postgres) GetSummary(ctx context.Context, projectID, taskID string) (Summary, error) {
	return getSummary(ctx, s, projectID, taskID)
}

func setSummary(ctx context.Context, e engine, projectID, taskID, body, updatedBy string, expectedVersion int64) (Summary, error) {
	var out Summary
	err := e.Atomically(ctx, func(tx Store) error {
		q := on(tx).queries(true)
		if _, err := taskAt(ctx, q, projectID, taskID, expectedVersion); err != nil {
			return err
		}
		r, err := q.UpsertSummary(ctx, sqlitedb.UpsertSummaryParams{ProjectID: projectID, TaskID: taskID, Body: body, UpdatedBy: updatedBy, UpdatedAt: nowMillis()})
		if err != nil {
			return fmt.Errorf("store: set summary: %w", err)
		}
		out = Summary{ProjectID: r.ProjectID, TaskID: r.TaskID, Body: r.Body, UpdatedBy: r.UpdatedBy, UpdatedAt: r.UpdatedAt}
		return nil
	})
	return out, err
}

func getSummary(ctx context.Context, e engine, projectID, taskID string) (Summary, error) {
	r, err := e.queries(false).GetSummary(ctx, sqlitedb.GetSummaryParams{ProjectID: projectID, ID: taskID})
	if err := found(err, "Task", taskID); err != nil {
		return Summary{}, err
	}
	return Summary{ProjectID: projectID, TaskID: taskID, Body: r.Body.String, UpdatedBy: r.UpdatedBy.String, UpdatedAt: r.UpdatedAt.Int64}, nil
}

// --- the Postgres queries in sqlitedb's types ---

func (p pgQueries) GetSummary(ctx context.Context, arg sqlitedb.GetSummaryParams) (sqlitedb.GetSummaryRow, error) {
	r, err := p.q.GetSummary(ctx, pgdb.GetSummaryParams(arg))
	return sqlitedb.GetSummaryRow(r), err
}

func (p pgQueries) UpsertSummary(ctx context.Context, arg sqlitedb.UpsertSummaryParams) (sqlitedb.Summary, error) {
	r, err := p.q.UpsertSummary(ctx, pgdb.UpsertSummaryParams(arg))
	return sqlitedb.Summary(r), err
}
