package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// Two serve processes over one database each hold their own access cache. A membership, role or
// grant change made through one reaches the other as its change-log row, which the bus hands every
// process's Core.ObserveChange: the row drops the project's cached access there too, so the change
// holds on the identity's next call to either process, not only once the access TTL has run out.
// The bus is played here by reading the change log and handing each row to the second Core.
func TestAMembershipChangeDropsAnotherProcesssCachedAccess(t *testing.T) {
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) {
			ctx := context.Background()
			st := e.open(t)
			clk := &clock{t: time.Now()}
			a := New(st, Options{ToolCallFlush: -1, Now: clk.now})
			b := New(st, Options{ToolCallFlush: -1, Now: clk.now})
			t.Cleanup(func() { _ = a.Close(context.Background()); _ = b.Close(context.Background()) })
			var cursor int64
			deliver := func() { // the bus delivering every new row to b
				t.Helper()
				rows, err := st.ReadChanges(ctx, cursor, 1000)
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range rows {
					b.ObserveChange(r)
					cursor = r.ID
				}
			}
			carol := domain.Caller{Identity: "carol", TokenScope: "carol"}
			bob := domain.Caller{Identity: "bob", TokenScope: "bob"}
			p, err := a.Projects().Create(ctx, carol, CreateProjectReq{Name: "Q"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.Projects().AddMember(ctx, carol, AddMemberReq{ProjectID: p.ProjectID, IdentityID: "bob", Role: domain.RoleMaster}); err != nil {
				t.Fatal(err)
			}
			deliver()
			if _, err := b.Boards().Create(ctx, bob, CreateBoardReq{ProjectID: p.ProjectID, Name: "B"}); err != nil {
				t.Fatal(err) // warms b's entry for bob
			}
			// A row of another tool keeps b's entry: only membership, role and grant changes drop it.
			deliver()
			if _, _, ok := b.access.get(p.ProjectID, "bob", clk.now()); !ok {
				t.Fatal("a create_board row dropped the cached access")
			}

			if _, err := a.Projects().AddMember(ctx, carol, AddMemberReq{ProjectID: p.ProjectID, IdentityID: "bob", Role: domain.RoleViewer}); err != nil {
				t.Fatal(err)
			}
			deliver()
			_, err = b.Boards().Create(ctx, bob, CreateBoardReq{ProjectID: p.ProjectID, Name: "C"})
			t.Logf("bob, demoted to viewer through a, creates a board through b (clock unmoved): %v", err)
			wantCode(t, err, domain.CodeForbidden)

			if _, err := a.Projects().RemoveMember(ctx, carol, RemoveMemberReq{ProjectID: p.ProjectID, IdentityID: "bob"}); err != nil {
				t.Fatal(err)
			}
			deliver()
			_, err = b.Boards().List(ctx, bob, ListBoardsReq{ProjectID: p.ProjectID})
			t.Logf("bob, removed through a, lists boards through b (clock unmoved): %v", err)
			wantCode(t, err, domain.CodeForbidden)
		})
	}
}

// The tools whose change-log row drops a project's cached access, and one whose row does not.
func TestObserveChangeDropsAccessOnPolicyRows(t *testing.T) {
	for tool, drops := range map[string]bool{
		"add_member": true, "remove_member": true, "grant_capabilities": true, "delete_project": true,
		"create_task": false,
	} {
		t.Run(tool, func(t *testing.T) {
			f := newFixture(t, Options{})
			bob := f.addMember(t, "bob", domain.RoleWorker)
			if _, err := f.c.Boards().List(f.ctx, bob, ListBoardsReq{ProjectID: f.project.ID}); err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(map[string]any{"kind": "changed", "projectId": f.project.ID, "tool": tool, "actor": "alice"})
			f.c.ObserveChange(store.Change{ID: 1, Payload: payload})
			_, _, cached := f.c.access.get(f.project.ID, "bob", f.clock.now())
			if cached == drops {
				t.Fatalf("after a %s row the entry is cached: %v, want %v", tool, cached, !drops)
			}
		})
	}
}
