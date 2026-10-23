package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The change log of contract_changelog.go, on both engines: change_log.id is SQLite's AUTOINCREMENT
// (never reused, a rolled-back id included) and Postgres's bigserial (taken at the INSERT, so ids
// follow appends, not commits). Every call is one statement, except Postgres's AppendChange:
// it sends the bus its NOTIFY as a second (watch.go).

type changeQueries interface {
	AppendChange(ctx context.Context, arg sqlitedb.AppendChangeParams) (int64, error)
	ReadChanges(ctx context.Context, arg sqlitedb.ReadChangesParams) ([]sqlitedb.ChangeLog, error)
	ReadChangesForScope(ctx context.Context, arg sqlitedb.ReadChangesForScopeParams) ([]sqlitedb.ChangeLog, error)
	DeleteChangesOlderThan(ctx context.Context, arg sqlitedb.DeleteChangesOlderThanParams) (int64, error)
}

// AppendChange wakes the file's in-process watchers once the change is committed (watch.go).
func (s *SQLite) AppendChange(ctx context.Context, in ChangeInput) (int64, error) {
	id, err := appendChange(ctx, s, in)
	if err == nil {
		s.noteAppended()
	}
	return id, err
}

// AppendChange sends the NOTIFY the bus LISTENs for (watch.go) as a second statement: inside
// Atomically on its transaction, delivered at that commit; outside it, after the INSERT committed.
func (s *Postgres) AppendChange(ctx context.Context, in ChangeInput) (int64, error) {
	id, err := appendChange(ctx, s, in)
	if err != nil {
		return 0, err
	}
	if err := s.notifyChanges(ctx); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *SQLite) ReadChanges(ctx context.Context, since int64, limit int) ([]Change, error) {
	return readChanges(ctx, s, since, limit)
}

func (s *Postgres) ReadChanges(ctx context.Context, since int64, limit int) ([]Change, error) {
	return readChanges(ctx, s, since, limit)
}

func (s *SQLite) ReadChangesForScope(ctx context.Context, scope string, since int64, limit int) ([]Change, error) {
	return readChangesForScope(ctx, s, scope, since, limit)
}

func (s *Postgres) ReadChangesForScope(ctx context.Context, scope string, since int64, limit int) ([]Change, error) {
	return readChangesForScope(ctx, s, scope, since, limit)
}

func (s *SQLite) DeleteChangesOlderThan(ctx context.Context, before int64, limit int) (int64, error) {
	return deleteChangesOlderThan(ctx, s, before, limit)
}

func (s *Postgres) DeleteChangesOlderThan(ctx context.Context, before int64, limit int) (int64, error) {
	return deleteChangesOlderThan(ctx, s, before, limit)
}

func appendChange(ctx context.Context, e engine, in ChangeInput) (int64, error) {
	if in.Scope == "" || in.Kind == "" || in.Ts <= 0 {
		return 0, domain.Validation("a change needs a scope, a kind and a time", nil)
	}
	payload, err := object("payload", in.Payload)
	if err != nil {
		return 0, err
	}
	var project sql.NullString
	if in.ProjectID != "" {
		project = sql.NullString{String: in.ProjectID, Valid: true}
	}
	id, err := e.queries(true).AppendChange(ctx, sqlitedb.AppendChangeParams{
		TokenScope: in.Scope, ProjectID: project, Kind: in.Kind, Payload: string(payload), Ts: in.Ts,
	})
	if err != nil {
		return 0, fmt.Errorf("store: append change: %w", err)
	}
	return id, nil
}

func readChanges(ctx context.Context, e engine, since int64, limit int) ([]Change, error) {
	rows, err := e.queries(false).ReadChanges(ctx, sqlitedb.ReadChangesParams{Since: since, PageLimit: int64(clamp(limit, 100, 1000))})
	if err != nil {
		return nil, fmt.Errorf("store: read changes: %w", err)
	}
	return convert(rows, changeFromRow), nil
}

func readChangesForScope(ctx context.Context, e engine, scope string, since int64, limit int) ([]Change, error) {
	rows, err := e.queries(false).ReadChangesForScope(ctx, sqlitedb.ReadChangesForScopeParams{
		TokenScope: scope, Since: since, PageLimit: int64(clamp(limit, 100, 1000)),
	})
	if err != nil {
		return nil, fmt.Errorf("store: read changes: %w", err)
	}
	return convert(rows, changeFromRow), nil
}

func deleteChangesOlderThan(ctx context.Context, e engine, before int64, limit int) (int64, error) {
	if limit <= 0 {
		return 0, domain.Validation("a delete needs a positive limit", map[string]any{"limit": limit})
	}
	n, err := e.queries(true).DeleteChangesOlderThan(ctx, sqlitedb.DeleteChangesOlderThanParams{Before: before, PageLimit: int64(limit)})
	if err != nil {
		return 0, fmt.Errorf("store: delete changes: %w", err)
	}
	return n, nil
}

func changeFromRow(r sqlitedb.ChangeLog) Change {
	return Change{ID: r.ID, Scope: r.TokenScope, ProjectID: strPtr(r.ProjectID), Kind: r.Kind, Payload: json.RawMessage(r.Payload), Ts: r.Ts}
}

func (p pgQueries) AppendChange(ctx context.Context, arg sqlitedb.AppendChangeParams) (int64, error) {
	return p.q.AppendChange(ctx, pgdb.AppendChangeParams(arg))
}

func (p pgQueries) ReadChanges(ctx context.Context, arg sqlitedb.ReadChangesParams) ([]sqlitedb.ChangeLog, error) {
	rows, err := p.q.ReadChanges(ctx, pgdb.ReadChangesParams(arg))
	return convert(rows, func(r pgdb.ChangeLog) sqlitedb.ChangeLog { return sqlitedb.ChangeLog(r) }), err
}

func (p pgQueries) ReadChangesForScope(ctx context.Context, arg sqlitedb.ReadChangesForScopeParams) ([]sqlitedb.ChangeLog, error) {
	rows, err := p.q.ReadChangesForScope(ctx, pgdb.ReadChangesForScopeParams(arg))
	return convert(rows, func(r pgdb.ChangeLog) sqlitedb.ChangeLog { return sqlitedb.ChangeLog(r) }), err
}

func (p pgQueries) DeleteChangesOlderThan(ctx context.Context, arg sqlitedb.DeleteChangesOlderThanParams) (int64, error) {
	return p.q.DeleteChangesOlderThan(ctx, pgdb.DeleteChangesOlderThanParams(arg))
}
