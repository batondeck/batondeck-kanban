package store

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The audit area of contract_audit.go, on both engines: an append is one INSERT, a page one SELECT
// newest first over (ts, id), the filters "" = any and the bounds the int64 extremes.

type auditQueries interface {
	AppendAudit(ctx context.Context, arg sqlitedb.AppendAuditParams) (int64, error)
	ListAuditLog(ctx context.Context, arg sqlitedb.ListAuditLogParams) ([]sqlitedb.AuditLog, error)
}

func (s *SQLite) AppendAudit(ctx context.Context, row AuditRow) (int64, error) {
	return appendAudit(ctx, s, row)
}

func (s *Postgres) AppendAudit(ctx context.Context, row AuditRow) (int64, error) {
	return appendAudit(ctx, s, row)
}

func (s *SQLite) ListAuditLog(ctx context.Context, f AuditFilter, cursor string, limit int) (AuditPage, error) {
	return listAuditLog(ctx, s, f, cursor, limit)
}

func (s *Postgres) ListAuditLog(ctx context.Context, f AuditFilter, cursor string, limit int) (AuditPage, error) {
	return listAuditLog(ctx, s, f, cursor, limit)
}

func appendAudit(ctx context.Context, e engine, row AuditRow) (int64, error) {
	if row.Action == "" || row.Actor == "" {
		return 0, domain.Validation("an audit row needs an action and an actor", nil)
	}
	if row.Result != AuditOK && row.Result != AuditError {
		return 0, domain.Validation("unknown audit result: "+string(row.Result), map[string]any{"result": row.Result})
	}
	data, err := object("data", row.Data)
	if err != nil {
		return 0, err
	}
	id, err := e.queries(true).AppendAudit(ctx, sqlitedb.AppendAuditParams{
		Ts: row.TS, Action: row.Action, Actor: row.Actor, Agent: row.Agent, Subject: row.Subject,
		ProjectID: row.ProjectID, Result: string(row.Result), ErrorCode: row.ErrorCode, Data: string(data),
	})
	if err != nil {
		return 0, fmt.Errorf("store: append audit: %w", err)
	}
	return id, nil
}

// auditCursor is a listing cursor, base64url {order: the decimal ts, id: the decimal id}; "" is the
// first page, read as the row after (MaxInt64, MaxInt64).
func auditCursor(cursor string) (ts, id int64, err error) {
	if cursor == "" {
		return math.MaxInt64, math.MaxInt64, nil
	}
	order, rawID, err := DecodeCursor(cursor)
	if err != nil {
		return 0, 0, err
	}
	ts, terr := strconv.ParseInt(order, 10, 64)
	id, ierr := strconv.ParseInt(rawID, 10, 64)
	if terr != nil || ierr != nil {
		return 0, 0, domain.Validation("Invalid cursor", nil)
	}
	return ts, id, nil
}

func listAuditLog(ctx context.Context, e engine, f AuditFilter, cursor string, limit int) (AuditPage, error) {
	afterTs, afterID, err := auditCursor(cursor)
	if err != nil {
		return AuditPage{}, err
	}
	limit = clamp(limit, 50, 500)
	since, until := f.Since, f.Until
	if since == 0 {
		since = math.MinInt64
	}
	if until == 0 {
		until = math.MaxInt64
	}
	rows, err := e.queries(false).ListAuditLog(ctx, sqlitedb.ListAuditLogParams{
		ProjectID: f.ProjectID, Actor: f.Actor, Action: f.Action, Subject: f.Subject, Since: since, Until: until,
		AfterTs: afterTs, AfterID: afterID, PageLimit: int64(limit + 1),
	})
	if err != nil {
		return AuditPage{}, fmt.Errorf("store: audit log: %w", err)
	}
	page := AuditPage{Rows: make([]AuditRow, 0, min(len(rows), limit))}
	for i, r := range rows {
		if i == limit {
			last := page.Rows[limit-1]
			page.NextCursor = EncodeCursor(strconv.FormatInt(last.TS, 10), strconv.FormatInt(last.ID, 10))
			break
		}
		page.Rows = append(page.Rows, AuditRow{
			ID: r.ID, TS: r.Ts, Action: r.Action, Actor: r.Actor, Agent: r.Agent, Subject: r.Subject,
			ProjectID: r.ProjectID, Result: AuditResult(r.Result), ErrorCode: r.ErrorCode, Data: json.RawMessage(r.Data),
		})
	}
	return page, nil
}

func (p pgQueries) AppendAudit(ctx context.Context, arg sqlitedb.AppendAuditParams) (int64, error) {
	return p.q.AppendAudit(ctx, pgdb.AppendAuditParams(arg))
}

func (p pgQueries) ListAuditLog(ctx context.Context, arg sqlitedb.ListAuditLogParams) ([]sqlitedb.AuditLog, error) {
	rows, err := p.q.ListAuditLog(ctx, pgdb.ListAuditLogParams(arg))
	return convert(rows, func(r pgdb.AuditLog) sqlitedb.AuditLog { return sqlitedb.AuditLog(r) }), err
}
