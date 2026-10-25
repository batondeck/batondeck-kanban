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
	if err := f.svc.Accounts.SetPassword(ctx, "alice", "new"); err != nil {
		t.Fatal(err)
	}
	_, err := f.svc.Accounts.Authenticate(ctx, "192.0.2.1", "alice", "old")
	wantCode(t, err, domain.CodeUnauthenticated)
	if _, err := f.svc.Accounts.Authenticate(ctx, "192.0.2.1", "alice", "new"); err != nil {
		t.Fatal(err)
	}
	wantCode(t, f.svc.Accounts.SetPassword(ctx, "nobody", "x"), domain.CodeNotFound)
	wantCode(t, f.svc.Accounts.SetPassword(ctx, "alice", ""), domain.CodeValidation)
	_, err = f.svc.Accounts.Disable(ctx, "nobody")
	wantCode(t, err, domain.CodeNotFound)
}
