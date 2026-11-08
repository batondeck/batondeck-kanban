package service

import (
	"cmp"
	"context"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// audit is service.Audit over store.Audit.
type audit struct{ c *Core }

var _ Audit = audit{}

// Audit is the audit-log read service.
func (c *Core) Audit() Audit { return audit{c} }

// Conductor's src/domain/audit.ts: DEFAULT_QUERY_DAYS, MAX_QUERY_DAYS, MAX_ROWS; 50 is its limit default.
const (
	defaultAuditDays  = 90
	maxAuditDays      = 365
	defaultAuditLimit = 50
	maxAuditLimit     = 500
)

func (a audit) ListAuditLog(ctx context.Context, caller domain.Caller, req ListAuditLogReq) (resp ListAuditLogResp, err error) {
	defer a.c.recovered("list_audit_log", &err)
	if _, err := a.c.authorize(ctx, caller, "", "list_audit_log", domain.GrantSubject{}, false); err != nil {
		return resp, err
	}
	switch {
	case req.Days < 0 || req.Days > maxAuditDays:
		return resp, domain.Validation("days is 1 to 365", map[string]any{"field": "days", "max": maxAuditDays})
	case req.Limit < 0 || req.Limit > maxAuditLimit:
		return resp, domain.Validation("limit is 1 to 500", map[string]any{"field": "limit", "max": maxAuditLimit})
	}
	days := cmp.Or(req.Days, defaultAuditDays)
	limit := cmp.Or(req.Limit, defaultAuditLimit)
	since := a.c.now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	// An identity subject is stored canonical (domain.CanonIdentity) and the API hands its ids out as
	// "U-…", so the subject matches as given or in that form: project and task ids stay exact.
	f := store.AuditFilter{Actor: req.Actor, Action: req.Action, Subject: req.Subject, Since: since}
	if req.Subject != "" {
		f.SubjectAlias = domain.CanonIdentity(req.Subject)
	}
	page, err := a.c.store.ListAuditLog(ctx, f, "", int(limit))
	if err != nil {
		return resp, a.c.mapErr("list_audit_log", err)
	}
	resp = ListAuditLogResp{Days: float64(days), Entries: make([]AuditEntry, 0, len(page.Rows))}
	for _, r := range page.Rows {
		data := "{}"
		if len(r.Data) > 0 {
			data = string(r.Data)
		}
		resp.Entries = append(resp.Entries, AuditEntry{Action: r.Action, Actor: r.Actor, Agent: r.Agent, Data: data, ErrorCode: r.ErrorCode,
			ProjectID: r.ProjectID, Result: string(r.Result), Subject: r.Subject, TS: isoMs(r.TS)})
	}
	return resp, nil
}
