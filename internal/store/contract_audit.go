package store

import (
	"context"
	"encoding/json"
)

// AuditResult is an audited call's outcome.
type AuditResult string

const (
	AuditOK    AuditResult = "ok"
	AuditError AuditResult = "error"
)

// AuditRow is one audit_log row: conductor's AUDIT_LOG_FIELDS minus tenantId. ID is assigned by
// AppendAudit. TS is Unix milliseconds, stamped by the caller. "" is "nothing to record" in every
// string field but Action, Actor and Result. Data is the descriptor's curated JSON object
// (domain.DescribeAudit), nil for {}.
type AuditRow struct {
	ID        int64
	TS        int64
	Action    string
	Actor     string
	Agent     string
	Subject   string
	ProjectID string
	Result    AuditResult
	ErrorCode string
	Data      json.RawMessage
}

// AuditFilter narrows a listing: each non-empty field must match exactly, Since and Until bound TS
// (inclusive, 0 is unbounded). The zero filter is every row.
type AuditFilter struct {
	ProjectID string
	Actor     string
	Action    string
	Subject   string
	Since     int64
	Until     int64
}

// AuditPage is one page of rows; NextCursor is "" on the last page.
type AuditPage struct {
	Rows       []AuditRow
	NextCursor string
}

// Audit is the audit log: append-only, one row per sensitive mutation, written by service.mutate
// (ADR-0003). Rows are never updated and outlive the project they name. Errors: an empty action or
// actor, a result other than ok or error, Data that is not a JSON object and a malformed cursor are
// domain.Validation. Order is newest first, (ts, id) descending; a cursor is base64url JSON {order,
// id} with order the ts.
type Audit interface {
	// AppendAudit writes the row and returns its id.
	AppendAudit(ctx context.Context, row AuditRow) (int64, error)
	// ListAuditLog is one page of the rows matching f. Limit <= 0 is 50 and a limit above 500 is 500.
	ListAuditLog(ctx context.Context, f AuditFilter, cursor string, limit int) (AuditPage, error)
}

var (
	_ Audit = (*SQLite)(nil)
	_ Audit = (*Postgres)(nil)
)
