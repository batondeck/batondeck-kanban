package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// list_audit_log pages: every row of the window is reachable, newest first, by passing nextCursor
// back as cursor, and the last page carries none (it used to answer the newest limit rows only).
func TestListAuditLogPagesThroughTheWindow(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		admin := domain.Caller{Identity: "root", WorkspaceAdmin: true}
		now := time.Now().UnixMilli()
		var want []string
		for i := range 5 {
			subject := fmt.Sprintf("T-%d", i+1)
			if _, err := e.st.AppendAudit(e.ctx, store.AuditRow{TS: now - int64(5-i)*1000, Action: "paging.test", Actor: "alice", Subject: subject, Result: store.AuditOK}); err != nil {
				t.Fatal(err)
			}
			want = append([]string{subject}, want...)
		}
		var got []string
		cursor := ""
		for pages := 0; ; pages++ {
			if pages > 5 {
				t.Fatalf("still paging after %d pages: %v", pages, got)
			}
			r, err := e.c.Audit().ListAuditLog(e.ctx, admin, ListAuditLogReq{Action: "paging.test", Limit: 2, Cursor: cursor})
			if err != nil {
				t.Fatal(err)
			}
			for _, x := range r.Entries {
				got = append(got, x.Subject)
			}
			if r.NextCursor == "" {
				break
			}
			cursor = r.NextCursor
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("paged %v, want %v", got, want)
		}
		_, err := e.c.Audit().ListAuditLog(e.ctx, admin, ListAuditLogReq{Cursor: "%%%"})
		if tkCode(err) != domain.CodeValidation {
			t.Fatalf("a malformed cursor: %v, want VALIDATION", err)
		}
	})
}
