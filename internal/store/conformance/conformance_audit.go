package conformance

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("audit", auditLog) }

// auditLog is the audit area: append and read back every field, newest first, the filters, paging
// by cursor, and the refusals.
func auditLog(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.ListAuditLog(ctx, store.AuditFilter{}, "", 1)
		return err
	}
	area(t, "Audit", newStore, probe, func(t *testing.T) {
		row := func(ts int64, action, actor, subject, projectID string) store.AuditRow {
			return store.AuditRow{TS: ts, Action: action, Actor: actor, Subject: subject, ProjectID: projectID, Result: store.AuditOK}
		}
		appendAll := func(t *testing.T, s store.Store, rows ...store.AuditRow) {
			t.Helper()
			for _, r := range rows {
				_, err := s.AppendAudit(ctx, r)
				noErr(t, err)
			}
		}
		actions := func(rows []store.AuditRow) []string {
			out := make([]string, len(rows))
			for i, r := range rows {
				out[i] = r.Action
			}
			return out
		}

		t.Run("AppendReadsBackEveryField", func(t *testing.T) {
			s := migrated(t, newStore)
			in := store.AuditRow{TS: 1000, Action: "member.added", Actor: "alice", Agent: "claude", Subject: "bob", ProjectID: "P-1", Result: store.AuditError, ErrorCode: "FORBIDDEN", Data: json.RawMessage(`{"role":"worker"}`)}
			id, err := s.AppendAudit(ctx, in)
			noErr(t, err)
			page, err := s.ListAuditLog(ctx, store.AuditFilter{}, "", 0)
			noErr(t, err)
			if len(page.Rows) != 1 {
				t.Fatalf("ListAuditLog = %+v, want the one row", page.Rows)
			}
			got := page.Rows[0]
			var data map[string]any
			if err := json.Unmarshal(got.Data, &data); err != nil {
				t.Fatalf("data %q: %v", got.Data, err)
			}
			if id == 0 || got.ID != id || got.TS != 1000 || got.Action != in.Action || got.Actor != "alice" || got.Agent != "claude" || got.Subject != "bob" || got.ProjectID != "P-1" || got.Result != store.AuditError || got.ErrorCode != "FORBIDDEN" || data["role"] != "worker" {
				t.Fatalf("read back %+v (id %d), want %+v", got, id, in)
			}
		})

		t.Run("ListIsNewestFirstFilteredAndPaged", func(t *testing.T) {
			s := migrated(t, newStore)
			appendAll(t, s,
				row(100, "member.added", "alice", "bob", "P-1"),
				row(200, "member.removed", "alice", "bob", "P-1"),
				row(300, "member.added", "carol", "dan", "P-2"),
				row(400, "task.deleted", "alice", "T-1", "P-1"),
			)
			list := func(f store.AuditFilter) []string {
				t.Helper()
				page, err := s.ListAuditLog(ctx, f, "", 0)
				noErr(t, err)
				return actions(page.Rows)
			}
			for _, c := range []struct {
				name string
				f    store.AuditFilter
				want []string
			}{
				{"all", store.AuditFilter{}, []string{"task.deleted", "member.added", "member.removed", "member.added"}},
				{"project", store.AuditFilter{ProjectID: "P-2"}, []string{"member.added"}},
				{"actor", store.AuditFilter{Actor: "alice"}, []string{"task.deleted", "member.removed", "member.added"}},
				{"action", store.AuditFilter{Action: "member.added"}, []string{"member.added", "member.added"}},
				{"subject", store.AuditFilter{Subject: "bob"}, []string{"member.removed", "member.added"}},
				{"subject-alias", store.AuditFilter{Subject: "BOB", SubjectAlias: "bob"}, []string{"member.removed", "member.added"}},
				{"subject-exact-without-alias", store.AuditFilter{Subject: "BOB"}, nil},
				{"subject-or-alias", store.AuditFilter{Subject: "T-1", SubjectAlias: "dan"}, []string{"task.deleted", "member.added"}},
				{"since-until", store.AuditFilter{Since: 200, Until: 300}, []string{"member.added", "member.removed"}},
			} {
				if got := list(c.f); !slices.Equal(got, c.want) {
					t.Fatalf("%s: %v, want %v", c.name, got, c.want)
				}
			}
			var seen []int64
			cursor := ""
			for range 3 {
				page, err := s.ListAuditLog(ctx, store.AuditFilter{}, cursor, 3)
				noErr(t, err)
				for _, r := range page.Rows {
					seen = append(seen, r.TS)
				}
				cursor = page.NextCursor
				if cursor == "" {
					break
				}
			}
			if !slices.Equal(seen, []int64{400, 300, 200, 100}) || cursor != "" {
				t.Fatalf("paging by 3 read %v (last cursor %q), want [400 300 200 100] and an end", seen, cursor)
			}
		})

		t.Run("TheRefusals", func(t *testing.T) {
			s := migrated(t, newStore)
			for _, r := range []store.AuditRow{
				{TS: 1, Actor: "alice", Result: store.AuditOK},
				{TS: 1, Action: "member.added", Result: store.AuditOK},
				{TS: 1, Action: "member.added", Actor: "alice", Result: "maybe"},
				{TS: 1, Action: "member.added", Actor: "alice", Result: store.AuditOK, Data: json.RawMessage(`[1]`)},
			} {
				_, err := s.AppendAudit(ctx, r)
				wantErr(t, err, domain.CodeValidation, "")
			}
			_, err := s.ListAuditLog(ctx, store.AuditFilter{}, "not a cursor!", 0)
			wantErr(t, err, domain.CodeValidation, "")
			page, err := s.ListAuditLog(ctx, store.AuditFilter{}, "", 0)
			noErr(t, err)
			if len(page.Rows) != 0 {
				t.Fatalf("refused appends left %+v", page.Rows)
			}
		})
	})
}
