package service

import (
	"context"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// A member added by a login no account holds yet is kept as given: a row keyed by that login. Once
// an account takes the login, every reference to it resolves to the account id. The row stays
// removable by its own key (and Remove on it never removes the account's membership), add_member
// for the account folds it into the account's row, grant_capabilities names it by its key, and it is
// no admin the last-admin guard can lean on: no credential resolves to it.
func TestAMemberAddedByLoginBeforeItsAccount(t *testing.T) {
	const login = "dave@example.test"
	setup := func(t *testing.T, role domain.Role, grant bool) *fixture {
		t.Helper()
		f := newFixture(t, Options{})
		got, err := f.c.Projects().AddMember(f.ctx, f.alice, AddMemberReq{ProjectID: f.project.ID, IdentityID: login, Role: role})
		if err != nil || got.Members[login] != role {
			t.Fatalf("add_member by a login with no account: %+v (err %v)", got.Members, err)
		}
		if grant {
			if _, err := f.c.Grants().Grant(f.ctx, f.alice, GrantCapabilitiesReq{ProjectID: f.project.ID, IdentityID: login}); err != nil {
				t.Fatalf("grant_capabilities on the login row: %v", err)
			}
		}
		if err := f.st.CreateUser(f.ctx, store.User{ID: "U-dave", Email: login, DisplayName: "Dave", PasswordHash: "x", CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
		return f
	}
	members := func(t *testing.T, f *fixture) map[string]domain.Role {
		t.Helper()
		p, err := f.c.Projects().Get(f.ctx, f.alice, GetProjectReq{ProjectID: f.project.ID})
		if err != nil {
			t.Fatal(err)
		}
		return p.Project.Members
	}

	t.Run("the login row is removable by its key", func(t *testing.T) {
		f := setup(t, domain.RoleAdmin, false)
		_, err := f.c.Projects().RemoveMember(f.ctx, f.alice, RemoveMemberReq{ProjectID: f.project.ID, IdentityID: login})
		t.Logf("remove_member %q after the account exists: %v", login, err)
		if err != nil {
			t.Fatal(err)
		}
		if m := members(t, f); len(m) != 1 || m["alice"] != domain.RoleAdmin {
			t.Fatalf("members after the removal %+v, want alice alone", m)
		}
	})

	t.Run("remove on the login row keeps the account's own membership", func(t *testing.T) {
		f := setup(t, domain.RoleMember, false)
		if err := f.st.Atomically(f.ctx, func(tx store.Store) error {
			_, err := tx.AddMember(f.ctx, f.project.ID, "U-dave", domain.RoleMember, "alice")
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.c.Projects().RemoveMember(f.ctx, f.alice, RemoveMemberReq{ProjectID: f.project.ID, IdentityID: login}); err != nil {
			t.Fatal(err)
		}
		if m := members(t, f); m["U-dave"] != domain.RoleMember || len(m) != 2 {
			t.Fatalf("members %+v, want alice and U-dave (the login row removed)", m)
		}
	})

	t.Run("the last real admin cannot leave it the only admin", func(t *testing.T) {
		f := setup(t, domain.RoleAdmin, false)
		_, rerr := f.c.Projects().RemoveMember(f.ctx, f.alice, RemoveMemberReq{ProjectID: f.project.ID, IdentityID: "alice"})
		_, derr := f.c.Projects().AddMember(f.ctx, f.alice, AddMemberReq{ProjectID: f.project.ID, IdentityID: "alice", Role: domain.RoleMember})
		t.Logf("alice removes herself: %v; demotes herself: %v", rerr, derr)
		wantCode(t, rerr, domain.CodeValidation)
		wantCode(t, derr, domain.CodeValidation)
		if m := members(t, f); m["alice"] != domain.RoleAdmin {
			t.Fatalf("members %+v, want alice still admin", m)
		}
	})

	t.Run("add_member for the account folds the login row into it", func(t *testing.T) {
		for _, ref := range []string{" Dave@Example.test ", "U-dave"} {
			t.Run(ref, func(t *testing.T) {
				f := setup(t, domain.RoleWorker, true)
				got, err := f.c.Projects().AddMember(f.ctx, f.alice, AddMemberReq{ProjectID: f.project.ID, IdentityID: ref, Role: domain.RoleWorker})
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("add_member %q answers %+v", ref, got.Members)
				if _, ghost := got.Members[login]; ghost || got.Members["U-dave"] != domain.RoleWorker || len(got.Members) != 2 {
					t.Fatalf("add_member answers %+v, want alice and U-dave only", got.Members)
				}
				if m := members(t, f); len(m) != 2 || m["U-dave"] != domain.RoleWorker {
					t.Fatalf("members %+v, want alice and U-dave only", m)
				}
				// The grant written for the login row narrows the account now: it is not dropped
				// with the row, which would widen what dave may do.
				g, err := f.st.GetGrant(f.ctx, f.project.ID, "U-dave")
				if err != nil {
					t.Fatalf("the login row's grant did not follow it: %v", err)
				}
				if want := domain.WorkerDefaultGrant(); len(g.Grant.Tools) != len(want.Tools) {
					t.Fatalf("grant %+v, want the worker default", g.Grant)
				}
				if _, err := f.c.Boards().List(f.ctx, domain.Caller{Identity: "U-dave"}, ListBoardsReq{ProjectID: f.project.ID}); err != nil {
					t.Fatalf("the account is not the member: %v", err)
				}
			})
		}
	})

	t.Run("grant_capabilities names the login row by its key", func(t *testing.T) {
		f := setup(t, domain.RoleWorker, false)
		got, err := f.c.Grants().Grant(f.ctx, f.alice, GrantCapabilitiesReq{ProjectID: f.project.ID, IdentityID: login})
		if err != nil || got.IdentityID != login {
			t.Fatalf("grant_capabilities: %+v (err %v), want the login row", got, err)
		}
	})
}

// engines are the two store engines the engine-level member tests run on; the Postgres leg skips
// without BATONDECK_TEST_POSTGRES_DSN.
var engines = []struct {
	name string
	open func(t *testing.T) store.Store
}{
	{"sqlite", func(t *testing.T) store.Store { return storetest.SQLite(t) }},
	{"postgres", func(t *testing.T) store.Store { return storetest.Postgres(t) }},
}

// The login-row rules over the engines: the reads the guard and the fold make run inside the
// engines' transactions.
func TestAMemberAddedByLoginBeforeItsAccountOverTheEngines(t *testing.T) {
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) {
			ctx := context.Background()
			st := e.open(t)
			c := New(st, Options{ToolCallFlush: -1})
			t.Cleanup(func() { _ = c.Close(context.Background()) })
			carol := domain.Caller{Identity: "carol", TokenScope: "carol"}
			p, err := c.Projects().Create(ctx, carol, CreateProjectReq{Name: "Q"})
			if err != nil {
				t.Fatal(err)
			}
			id := p.ProjectID
			if _, err := c.Projects().AddMember(ctx, carol, AddMemberReq{ProjectID: id, IdentityID: "Dave@Example.test", Role: domain.RoleAdmin}); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Grants().Grant(ctx, carol, GrantCapabilitiesReq{ProjectID: id, IdentityID: "Dave@Example.test"}); err != nil {
				t.Fatal(err)
			}
			if err := st.CreateUser(ctx, store.User{ID: "U-dave", Email: "dave@example.test", DisplayName: "Dave", PasswordHash: "x", CreatedAt: 1}); err != nil {
				t.Fatal(err)
			}
			_, err = c.Projects().RemoveMember(ctx, carol, RemoveMemberReq{ProjectID: id, IdentityID: "carol"})
			wantCode(t, err, domain.CodeValidation)
			got, err := c.Projects().AddMember(ctx, carol, AddMemberReq{ProjectID: id, IdentityID: "dave@example.test", Role: domain.RoleWorker})
			if err != nil {
				t.Fatal(err)
			}
			members, err := st.ListMembers(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if len(members) != 2 || got.Members["U-dave"] != domain.RoleWorker || len(got.Members) != 2 {
				t.Fatalf("members %+v (answer %+v), want carol and U-dave", members, got.Members)
			}
			if _, err := st.GetGrant(ctx, id, "U-dave"); err != nil {
				t.Fatalf("the login row's grant did not follow it: %v", err)
			}
			// A second login row, removed by its key with the account a member.
			if _, err := c.Projects().AddMember(ctx, carol, AddMemberReq{ProjectID: id, IdentityID: "erin@example.test", Role: domain.RoleMember}); err != nil {
				t.Fatal(err)
			}
			if err := st.CreateUser(ctx, store.User{ID: "U-erin", Email: "erin@example.test", DisplayName: "Erin", PasswordHash: "x", CreatedAt: 1}); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Projects().RemoveMember(ctx, carol, RemoveMemberReq{ProjectID: id, IdentityID: "erin@example.test"}); err != nil {
				t.Fatalf("remove_member by the login row's key: %v", err)
			}
		})
	}
}

// An admin row for a login no account holds yet is no admin an account can leave the project to: no
// credential resolves to it, and nothing makes it the account once one takes the login. So the last
// account admin may neither remove nor demote itself while only such a row stays admin; another
// account admin is enough. (A project of identities that are no accounts, as the fixtures', keeps
// counting every admin row: there the departing admin is no account either.)
func TestAPendingLoginIsNoAdminToLeaveTo(t *testing.T) {
	for _, e := range engines {
		t.Run(e.name, func(t *testing.T) {
			ctx := context.Background()
			st := e.open(t)
			c := New(st, Options{ToolCallFlush: -1})
			t.Cleanup(func() { _ = c.Close(context.Background()) })
			for _, u := range []store.User{
				{ID: "U-carol", Email: "carol@example.test", DisplayName: "Carol", PasswordHash: "x", CreatedAt: 1},
				{ID: "U-erin", Email: "erin@example.test", DisplayName: "Erin", PasswordHash: "x", CreatedAt: 1},
			} {
				if err := st.CreateUser(ctx, u); err != nil {
					t.Fatal(err)
				}
			}
			carol := domain.Caller{Identity: "U-carol", TokenScope: "U-carol"}
			p, err := c.Projects().Create(ctx, carol, CreateProjectReq{Name: "Q"})
			if err != nil {
				t.Fatal(err)
			}
			id := p.ProjectID
			if _, err := c.Projects().AddMember(ctx, carol, AddMemberReq{ProjectID: id, IdentityID: "dave@example.test", Role: domain.RoleAdmin}); err != nil {
				t.Fatal(err)
			}
			_, rerr := c.Projects().RemoveMember(ctx, carol, RemoveMemberReq{ProjectID: id, IdentityID: "U-carol"})
			_, derr := c.Projects().AddMember(ctx, carol, AddMemberReq{ProjectID: id, IdentityID: "carol@example.test", Role: domain.RoleMember})
			t.Logf("U-carol removes herself: %v; demotes herself: %v", rerr, derr)
			if be := wantCode(t, rerr, domain.CodeValidation); be.Message != "Cannot remove the last admin" {
				t.Fatalf("remove: %v, want 'Cannot remove the last admin'", rerr)
			}
			if be := wantCode(t, derr, domain.CodeValidation); be.Message != "Cannot demote the last admin" {
				t.Fatalf("demote: %v, want 'Cannot demote the last admin'", derr)
			}
			members, err := st.ListMembers(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if roles := memberRoles(members); roles["U-carol"] != domain.RoleAdmin || len(roles) != 2 {
				t.Fatalf("members %+v, want U-carol still admin beside the login row", roles)
			}
			// With another account admin, U-carol may go.
			if _, err := c.Projects().AddMember(ctx, carol, AddMemberReq{ProjectID: id, IdentityID: "erin@example.test", Role: domain.RoleAdmin}); err != nil {
				t.Fatal(err)
			}
			if _, err := c.Projects().AddMember(ctx, carol, AddMemberReq{ProjectID: id, IdentityID: "U-carol", Role: domain.RoleMember}); err != nil {
				t.Fatalf("demote beside another account admin: %v", err)
			}
			erin := domain.Caller{Identity: "U-erin", TokenScope: "U-erin"}
			if _, err := c.Projects().RemoveMember(ctx, erin, RemoveMemberReq{ProjectID: id, IdentityID: "U-carol"}); err != nil {
				t.Fatalf("remove beside another account admin: %v", err)
			}
			// U-erin is now the last account admin: she cannot leave to the login row either.
			_, err = c.Projects().RemoveMember(ctx, erin, RemoveMemberReq{ProjectID: id, IdentityID: "U-erin"})
			wantCode(t, err, domain.CodeValidation)
			// The login row itself stays removable: it is no account admin.
			if _, err := c.Projects().RemoveMember(ctx, erin, RemoveMemberReq{ProjectID: id, IdentityID: "dave@example.test"}); err != nil {
				t.Fatalf("remove the login row: %v", err)
			}
		})
	}
}
