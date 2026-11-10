package service

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// The one service test over the engines rather than memstore: R1.16's audited mutations reached
// AppendAudit's INTERNAL stub on SQLite and Postgres until R0.11 built it. Each succeeds there and
// writes its audit row. The Postgres leg skips without BATONDECK_TEST_POSTGRES_DSN.
func TestProjectMutationsOverTheEnginesWriteTheirAuditRows(t *testing.T) {
	for _, e := range []struct {
		name string
		open func(t *testing.T) store.Store
	}{
		{"sqlite", func(t *testing.T) store.Store { return storetest.SQLite(t) }},
		{"postgres", func(t *testing.T) store.Store { return storetest.Postgres(t) }},
	} {
		t.Run(e.name, func(t *testing.T) {
			ctx := context.Background()
			st := e.open(t)
			c := New(st, Options{ToolCallFlush: -1})
			t.Cleanup(func() { _ = c.Close(context.Background()) })
			carol := domain.Caller{Identity: "carol", Agent: "claude", TokenScope: "carol"}
			p, err := c.Projects().Create(ctx, carol, CreateProjectReq{Name: "Q", IdempotencyKey: "k"})
			if err != nil {
				t.Fatalf("create_project: %v", err)
			}
			id := p.ProjectID
			if _, err := c.Projects().AddMember(ctx, carol, AddMemberReq{ProjectID: id, IdentityID: "dave", Role: domain.RoleAdmin}); err != nil {
				t.Fatalf("add_member: %v", err)
			}
			if _, err := c.Boards().Create(ctx, carol, CreateBoardReq{ProjectID: id, Name: "B"}); err != nil {
				t.Fatalf("create_board: %v", err)
			}
			if _, err := c.Projects().UpdateSettings(ctx, carol, UpdateSettingsReq{ProjectID: id, MaxRunsPerTask: 7}); err != nil {
				t.Fatalf("update_settings: %v", err)
			}
			if _, err := c.Projects().RemoveMember(ctx, carol, RemoveMemberReq{ProjectID: id, IdentityID: "dave"}); err != nil {
				t.Fatalf("remove_member: %v", err)
			}
			if _, err := c.Projects().Delete(ctx, carol, DeleteProjectReq{ProjectID: id}); err != nil {
				t.Fatalf("delete_project: %v", err)
			}
			audit, err := st.ListAuditLog(ctx, store.AuditFilter{}, "", 50)
			if err != nil {
				t.Fatalf("list audit: %v", err)
			}
			var actions []string
			for _, r := range audit.Rows {
				actions = append(actions, r.Action)
			}
			slices.Sort(actions)
			want := []string{"member.added", "member.removed", "project.deleted", "settings.updated"}
			if !slices.Equal(actions, want) {
				t.Fatalf("audit actions %v, want %v", actions, want)
			}
			t.Logf("%s: audit actions %v", e.name, actions)
		})
	}
}

// A chronicle page's delete writes chronicle.page_deleted to the audit log (its event names no board
// or task, so no feed reads it), and update_settings records checkpointPolicy, tokenPolicy and
// maxTicketTokens: before, such a change stored settings.updated with data {}.
func TestChronicleDeleteAndPolicySettingsAreAudited(t *testing.T) {
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) {
			ctx := context.Background()
			st := e.open(t)
			c := New(st, Options{ToolCallFlush: -1})
			t.Cleanup(func() { _ = c.Close(context.Background()) })
			carol := domain.Caller{Identity: "carol", Agent: "claude", TokenScope: "carol"}
			p, err := c.Projects().Create(ctx, carol, CreateProjectReq{Name: "Q"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.Projects().AddMember(ctx, carol, AddMemberReq{ProjectID: p.ProjectID, IdentityID: "wes", Role: domain.RoleMaster}); err != nil {
				t.Fatal(err)
			}
			page, err := c.Chronicle().CreatePage(ctx, carol, CreateChroniclePageReq{ProjectID: p.ProjectID, Kind: "adr", Slug: "adr/x", Title: "X",
				Blocks: []ChronicleBlockInput{{ID: "a-1", Text: "the decision"}}})
			if err != nil {
				t.Fatal(err)
			}
			wes := domain.Caller{Identity: "wes", Agent: "w", TokenScope: "wes"}
			if _, err := c.Chronicle().DeletePage(ctx, wes, DeleteChroniclePageReq{ProjectID: p.ProjectID, PageID: page.PageID}); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Projects().UpdateSettings(ctx, carol, UpdateSettingsReq{ProjectID: p.ProjectID, CheckpointPolicy: "off"}); err != nil {
				t.Fatal(err)
			}
			deleted, err := st.ListAuditLog(ctx, store.AuditFilter{Action: "chronicle.page_deleted"}, "", 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(deleted.Rows) != 1 || deleted.Rows[0].Subject != page.PageID || deleted.Rows[0].Actor != "wes" {
				t.Fatalf("chronicle.page_deleted rows %+v, want one naming %s by wes", deleted.Rows, page.PageID)
			}
			settings, err := st.ListAuditLog(ctx, store.AuditFilter{Action: "settings.updated"}, "", 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(settings.Rows) != 1 || !strings.Contains(string(settings.Rows[0].Data), `"checkpointPolicy":"off"`) {
				t.Fatalf("settings.updated rows %+v, want one recording checkpointPolicy", settings.Rows)
			}
		})
	}
}
