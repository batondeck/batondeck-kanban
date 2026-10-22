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
