package auth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/memstore"
)

func TestPasswordHashIsArgon2idAndVerifies(t *testing.T) {
	h, err := HashPassword("correct horse", testArgon)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatalf("encoded form %q", h)
	}
	if !VerifyPassword("correct horse", h) {
		t.Fatal("the right password did not verify")
	}
	if VerifyPassword("correct horsE", h) {
		t.Fatal("a wrong password verified")
	}
	h2, _ := HashPassword("correct horse", testArgon)
	if h2 == h {
		t.Fatal("two hashes of one password are equal: the salt is not random")
	}
	for _, bad := range []string{"", "$argon2i$v=19$m=64,t=1,p=1$AAAA$AAAA", "$argon2id$v=18$m=64,t=1,p=1$AAAA$AAAA", "$argon2id$v=19$m=64,t=0,p=1$AAAA$AAAA", "plaintext"} {
		if VerifyPassword("x", bad) {
			t.Fatalf("malformed hash %q verified", bad)
		}
	}
	if DefaultArgon2 != (Argon2Params{Time: 3, Memory: 64 * 1024, Threads: 4}) {
		t.Fatalf("DefaultArgon2 %+v", DefaultArgon2)
	}
}

func TestAddNormalizesAndRefusesADuplicateLogin(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	// The password has a space, which base64 never holds: "the hash does not contain the password"
	// cannot then pass or fail by chance on a random salt and digest (a two-letter "pw" appears in
	// about one hash in sixty).
	const password = "pw secret"
	u, err := f.svc.Accounts.Add(ctx, "  Alice@Example.Test ", "", password, false)
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "alice@example.test" || u.DisplayName != "alice" || !strings.HasPrefix(u.ID, "U-") || u.DisabledAt != 0 {
		t.Fatalf("user %+v", u)
	}
	if strings.Contains(u.PasswordHash, password) || !VerifyPassword(password, u.PasswordHash) {
		t.Fatal("the stored hash is not the password's argon2id")
	}
	_, err = f.svc.Accounts.Add(ctx, "ALICE@example.test", "", "pw2", false)
	wantCode(t, err, domain.CodeValidation)
	for _, bad := range []string{"", "a b", strings.Repeat("a", 255)} {
		_, err := f.svc.Accounts.Add(ctx, bad, "", "pw", false)
		wantCode(t, err, domain.CodeValidation)
	}
	_, err = f.svc.Accounts.Add(ctx, "bob", "", "", false)
	wantCode(t, err, domain.CodeValidation)
}

func TestRefusalWrongPassword(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "alice", "right")
	ctx := context.Background()
	t.Run("Refused", func(t *testing.T) {
		_, err := f.svc.Accounts.Authenticate(ctx, "192.0.2.1", "alice", "wrong")
		wantCode(t, err, domain.CodeUnauthenticated)
		_, err = f.svc.Accounts.Authenticate(ctx, "192.0.2.1", "nobody", "right")
		wantCode(t, err, domain.CodeUnauthenticated)
	})
	t.Run("Control", func(t *testing.T) {
		if _, err := f.svc.Accounts.Authenticate(ctx, "192.0.2.1", "ALICE", "right"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestRefusalDisabledUser(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.addUser(t, "alice", "pw")
	f.addUser(t, "bob", "pw")
	if _, err := f.svc.Accounts.Disable(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	t.Run("Refused", func(t *testing.T) {
		_, err := f.svc.Accounts.Authenticate(ctx, "192.0.2.1", "alice", "pw")
		wantCode(t, err, domain.CodeUnauthenticated)
	})
	t.Run("Control", func(t *testing.T) {
		if _, err := f.svc.Accounts.Authenticate(ctx, "192.0.2.1", "bob", "pw"); err != nil {
			t.Fatal(err)
		}
	})
}

// Enable is the way back from Disable (P-46): the password works again, and a token or session
// the user held before resolves again once the account cache entry expires.
func TestEnableUndoesDisable(t *testing.T) {
	f := newFixture(t)
	f.svc.AccountCacheTTL = 50 * time.Millisecond
	ctx := context.Background()
	alice := f.addUser(t, "alice", "pw")
	tok, _, err := f.svc.Tokens.Create(ctx, alice.ID, alice.Email, "a", 0)
	if err != nil {
		t.Fatal(err)
	}
	sess, _, err := f.svc.Login(ctx, "192.0.2.1", "alice", "pw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Accounts.Disable(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	f.clk.add(50 * time.Millisecond)
	_, err = f.svc.Accounts.Authenticate(ctx, "192.0.2.1", "alice", "pw")
	wantCode(t, err, domain.CodeUnauthenticated)
	_, err = f.svc.ResolveCaller(ctx, tok)
	wantCode(t, err, domain.CodeUnauthenticated)
	_, _, err = f.svc.ResolveSession(ctx, sess)
	wantCode(t, err, domain.CodeUnauthenticated)

	u, was, err := f.svc.Accounts.Enable(ctx, "ALICE")
	if err != nil {
		t.Fatal(err)
	}
	if !was || u.ID != alice.ID || u.DisabledAt != 0 {
		t.Fatalf("Enable: was %v, id %s, disabled_at %d", was, u.ID, u.DisabledAt)
	}
	if _, err := f.svc.Accounts.Authenticate(ctx, "192.0.2.1", "alice", "pw"); err != nil {
		t.Fatalf("the password after Enable: %v", err)
	}
	// The cached disabled state answers until its entry expires; then the account is read again.
	f.clk.add(50 * time.Millisecond)
	if _, err := f.svc.ResolveCaller(ctx, tok); err != nil {
		t.Fatalf("the token after Enable and a fresh account read: %v", err)
	}
	if _, _, err := f.svc.ResolveSession(ctx, sess); err != nil {
		t.Fatalf("the session after Enable and a fresh account read: %v", err)
	}

	// An enabled user: nothing changes, and was is false.
	u, was, err = f.svc.Accounts.Enable(ctx, "alice")
	if err != nil || was || u.DisabledAt != 0 {
		t.Fatalf("enabling an enabled user: was %v, disabled_at %d, err %v", was, u.DisabledAt, err)
	}
	_, _, err = f.svc.Accounts.Enable(ctx, "nobody")
	wantCode(t, err, domain.CodeNotFound)
}

func TestSetPasswordReplacesTheOldOne(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.addUser(t, "alice", "old")
	if _, err := f.svc.Accounts.SetPassword(ctx, "alice", "new"); err != nil {
		t.Fatal(err)
	}
	_, err := f.svc.Accounts.Authenticate(ctx, "192.0.2.1", "alice", "old")
	wantCode(t, err, domain.CodeUnauthenticated)
	if _, err := f.svc.Accounts.Authenticate(ctx, "192.0.2.1", "alice", "new"); err != nil {
		t.Fatal(err)
	}
	_, err = f.svc.Accounts.SetPassword(ctx, "nobody", "x")
	wantCode(t, err, domain.CodeNotFound)
	_, err = f.svc.Accounts.SetPassword(ctx, "alice", "")
	wantCode(t, err, domain.CodeValidation)
	_, err = f.svc.Accounts.Disable(ctx, "nobody")
	wantCode(t, err, domain.CodeNotFound)
}

// A password change ends every browser session of that user (P-51) and nothing else: another
// user's session and the user's own API token still resolve, because a token is a separate
// credential that `token revoke` ends.
func TestSetPasswordEndsTheUsersSessionsOnly(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	alice := f.addUser(t, "alice", "pw")
	f.addUser(t, "bob", "pw")
	a1, _ := f.session(t, "alice")
	a2, _ := f.session(t, "alice")
	b1, _ := f.session(t, "bob")
	tok, _, err := f.svc.Tokens.Create(ctx, alice.ID, alice.Email, "a", 0)
	if err != nil {
		t.Fatal(err)
	}
	ended, err := f.svc.Accounts.SetPassword(ctx, "alice", "new-password")
	if err != nil || ended != 2 {
		t.Fatalf("SetPassword: ended %d, %v; want 2 sessions ended", ended, err)
	}
	for _, s := range []string{a1, a2} {
		_, _, err := f.svc.ResolveSession(ctx, s)
		wantCode(t, err, domain.CodeUnauthenticated)
	}
	if _, _, err := f.svc.ResolveSession(ctx, b1); err != nil {
		t.Fatalf("bob's session after alice's password change: %v", err)
	}
	if c, err := f.svc.ResolveCaller(ctx, tok); err != nil || c.Identity != alice.ID {
		t.Fatalf("alice's API token after her password change: %+v %v, want it to resolve", c, err)
	}
	_, err = f.svc.Accounts.Authenticate(ctx, "192.0.2.1", "alice", "pw")
	wantCode(t, err, domain.CodeUnauthenticated)
	if _, err := f.svc.Accounts.Authenticate(ctx, "192.0.2.1", "alice", "new-password"); err != nil {
		t.Fatal(err)
	}
}

// Accounts without Sessions refuses a password change before writing the hash, so no caller can
// change a password and leave the old sessions open.
func TestSetPasswordWithoutSessionsChangesNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.addUser(t, "alice", "pw")
	acc := *f.svc.Accounts
	acc.Sessions = nil
	if _, err := acc.SetPassword(ctx, "alice", "new-password"); err == nil {
		t.Fatal("SetPassword without Sessions: no error")
	}
	if _, err := f.svc.Accounts.Authenticate(ctx, "192.0.2.1", "alice", "pw"); err != nil {
		t.Fatalf("the old password after a refused change: %v, want it unchanged", err)
	}
}

// Name is the display name, else the login, and "" with no error for an id with no account (P-314:
// /api/session's displayName). The login-only row is written to the store directly, since Add
// always fills the display name; it stands for an account made before names were required.
func TestAccountName(t *testing.T) {
	mem := memstore.New()
	if _, err := mem.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	engines := map[string]store.Users{"sqlite": newFixture(t).st, "memstore": mem}
	for engine, users := range engines {
		t.Run(engine, func(t *testing.T) {
			ctx := context.Background()
			a := &Accounts{Store: users, Params: testArgon}
			alice, err := a.Add(ctx, "alice@example.test", "Alice Doe", "pw", false)
			if err != nil {
				t.Fatal(err)
			}
			hash, err := HashPassword("pw", testArgon)
			if err != nil {
				t.Fatal(err)
			}
			const bob = "U-0000000000000b0b"
			if err := users.CreateUser(ctx, store.User{ID: bob, Email: "bob", PasswordHash: hash, CreatedAt: 1}); err != nil {
				t.Fatal(err)
			}
			for _, c := range []struct{ id, want string }{{alice.ID, "Alice Doe"}, {bob, "bob"}, {"U-ffffffffffffffff", ""}} {
				got, err := a.Name(ctx, c.id)
				if err != nil || got != c.want {
					t.Fatalf("Name(%s) = %q, %v; want %q, nil", c.id, got, err, c.want)
				}
			}
		})
	}
}
