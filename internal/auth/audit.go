package auth

import (
	"context"
	"encoding/json"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/store"
)

// StoreAudit is a Service.Audit that appends each event to the audit log as a row stamped with
// now (nil means time.Now). The row takes the event's fields, mapped to what the log holds: Result
// "ok" is store.AuditOK and any other (the CSRF check's and a revoked grant's "refused") is
// store.AuditError with the ErrorCode kept; an empty Actor is AnonymousActor. The event's request
// has already been answered or refused, so a failed append is reported to logf (nil reports
// nothing) and changes nothing else.
func StoreAudit(log store.Audit, now func() time.Time, logf func(format string, args ...any)) func(context.Context, AuditEvent) {
	if now == nil {
		now = time.Now
	}
	report := func(e AuditEvent, err error) {
		if logf != nil {
			logf("auth: the audit row %s %s was not written: %v", e.Action, e.Subject, err)
		}
	}
	return func(ctx context.Context, e AuditEvent) {
		row := store.AuditRow{TS: now().UnixMilli(), Action: e.Action, Actor: e.Actor, Subject: e.Subject,
			Result: store.AuditOK, ErrorCode: e.ErrorCode}
		if row.Actor == "" {
			row.Actor = AnonymousActor
		}
		if e.Result != "ok" {
			row.Result = store.AuditError
		}
		if e.Data != nil {
			data, err := json.Marshal(e.Data)
			if err != nil {
				report(e, err)
				return
			}
			row.Data = data
		}
		// The row outlives the request: a client that hangs up does not cancel it.
		if _, err := log.AppendAudit(context.WithoutCancel(ctx), row); err != nil {
			report(e, err)
		}
	}
}
