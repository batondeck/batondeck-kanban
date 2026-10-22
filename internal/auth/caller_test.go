package auth

import (
	"context"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// A disabled user's live token stops resolving once the account cache entry expires: here with
// the TTL lowered to 50 ms of the fake clock; DefaultAccountCacheTTL is the 60 s the plan names.
func TestDisabledUserTokenFailsWithinTheCacheTTL(t *testing.T) {
	if DefaultAccountCacheTTL != 60*time.Second {
		t.Fatalf("DefaultAccountCacheTTL %v", DefaultAccountCacheTTL)
	}
	f := newFixture(t)
	f.svc.AccountCacheTTL = 50 * time.Millisecond
	ctx := context.Background()
	alice := f.addUser(t, "alice", "pw")
	bob := f.addUser(t, "bob", "pw")
	at, _, _ := f.svc.Tokens.Create(ctx, alice.ID, alice.Email, "a", 0)
	bt, _, _ := f.svc.Tokens.Create(ctx, bob.ID, bob.Email, "b", 0)
	if _, err := f.svc.ResolveCaller(ctx, at); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Accounts.Disable(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	// Within the window the cached state still answers: the bound, not instant revocation.
	if _, err := f.svc.ResolveCaller(ctx, at); err != nil {
		t.Fatalf("inside the cache window: %v", err)
	}
	f.clk.add(50 * time.Millisecond)
	_, err := f.svc.ResolveCaller(ctx, at)
	wantCode(t, err, domain.CodeUnauthenticated)
	if _, err := f.svc.ResolveCaller(ctx, bt); err != nil {
		t.Fatalf("control, an enabled user: %v", err)
	}
}

func TestATokenOfAnUnknownIdentityIsUnauthenticated(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	plain, _, _ := f.svc.Tokens.Create(ctx, "U-gone", "gone", "x", 0)
	_, err := f.svc.ResolveCaller(ctx, plain)
	wantCode(t, err, domain.CodeUnauthenticated)
}

// AC3: the workspace-admin flag set at `user add --admin` reaches the Caller, by token and by
// session; a user added without it is not an admin.
func TestWorkspaceAdminFlagReachesTheCaller(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	admin, err := f.svc.Accounts.Add(ctx, "root", "", "pw", true)
	if err != nil {
		t.Fatal(err)
	}
	plain := f.addUser(t, "alice", "pw")
	for _, c := range []struct {
		id    string
		login string
		want  bool
	}{{admin.ID, "root", true}, {plain.ID, "alice", false}} {
		tok, _, _ := f.svc.Tokens.Create(ctx, c.id, c.login, "t", 0)
		got, err := f.svc.ResolveCaller(ctx, tok)
		if err != nil || got.WorkspaceAdmin != c.want || got.Identity != c.id {
			t.Fatalf("%s by token: %+v %v, want admin %v", c.login, got, err, c.want)
		}
		secret, _, err := f.svc.Login(ctx, c.login, "pw")
		if err != nil {
			t.Fatal(err)
		}
		got, _, err = f.svc.ResolveSession(ctx, secret)
		if err != nil || got.WorkspaceAdmin != c.want {
			t.Fatalf("%s by session: %+v %v, want admin %v", c.login, got, err, c.want)
		}
	}
}
