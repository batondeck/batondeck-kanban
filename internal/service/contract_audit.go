package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Audit is the read side of the audit log service.mutate writes (store.Audit).
type Audit interface {
	// ListAuditLog is list_audit_log: the rows of the last Days days (default 90, 1 to 365)
	// matching every filter given, newest first, at most Limit (default 50, 1 to 500) a page:
	// NextCursor, when the window holds more, is the Cursor that reads the next (older) page.
	// Workspace admin only: service.authorize refuses anyone else FORBIDDEN. Not tenant-scoped.
	// Writes nothing.
	ListAuditLog(ctx context.Context, caller domain.Caller, req ListAuditLogReq) (ListAuditLogResp, error)
}

// ListAuditLogReq is list_audit_log's input (contract/admin-tools.json).
type ListAuditLogReq struct {
	Action  string `json:"action,omitzero"`
	Actor   string `json:"actor,omitzero"`
	Cursor  string `json:"cursor,omitzero"`
	Days    int64  `json:"days,omitzero"`
	Limit   int64  `json:"limit,omitzero"`
	Subject string `json:"subject,omitzero"`
}

// ListAuditLogResp is list_audit_log's output: the window queried, after the default, the rows, and
// the cursor of the next page when there is one.
type ListAuditLogResp struct {
	Days       float64      `json:"days"`
	Entries    []AuditEntry `json:"entries"`
	NextCursor string       `json:"nextCursor,omitzero"`
}

// AuditEntry is one row as conductor's AUDIT_SELECTED_FIELDS shapes it: every field a string, ""
// where the row records nothing, TS RFC 3339 and Data the row's JSON object as text.
type AuditEntry struct {
	Action    string `json:"action"`
	Actor     string `json:"actor"`
	Agent     string `json:"agent"`
	Data      string `json:"data"`
	ErrorCode string `json:"errorCode"`
	ProjectID string `json:"projectId"`
	Result    string `json:"result"`
	Subject   string `json:"subject"`
	TS        string `json:"ts"`
}
