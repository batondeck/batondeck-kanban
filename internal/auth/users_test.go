package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
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
	u, err := f.svc.Accounts.Add(ctx, "  Alice@Example.Test ", "", "pw", false)
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "alice@example.test" || u.DisplayName != "alice" || !strings.HasPrefix(u.ID, "U-") || u.DisabledAt != 0 {
		t.Fatalf("user %+v", u)
	}
	if strings.Contains(u.PasswordHash, "pw") || !VerifyPassword("pw", u.PasswordHash) {
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
