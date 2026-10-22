package auth

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

func TestTokenPlaintextIsShownOnceAndListShowsThePrefix(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	u := f.addUser(t, "alice", "pw")
	plain, info, err := f.svc.Tokens.Create(ctx, u.ID, u.Email, "laptop", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, info.Display()+"_") || len(plain) != len("bd_")+16+1+43 {
		t.Fatalf("token shape: %d chars, display %q", len(plain), info.Display())
	}
	if got := info.ExpiresAt.Sub(info.CreatedAt); got != DefaultTokenTTL {
		t.Fatalf("ttl %v", got)
	}
	secret := plain[len(info.Display())+1:]
	row, err := f.st.GetToken(ctx, info.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprintf("%+v", row), secret) || row.SecretHash != hashSecret(secret) {
		t.Fatal("the stored row holds the secret, or not its SHA-256")
	}
	list, err := f.svc.Tokens.List(ctx, u.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list %v %v", list, err)
	}
	if s := fmt.Sprintf("%+v", list); strings.Contains(s, secret) || !strings.Contains(s, info.ID) {
		t.Fatalf("list shows %s", s)
	}
}

func TestTokenCreateBounds(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for _, c := range []struct {
		label string
		ttl   time.Duration
	}{{"", 0}, {strings.Repeat("x", 101), 0}, {"ok", time.Second}, {"ok", MaxTokenTTL + time.Hour}} {
		_, _, err := f.svc.Tokens.Create(ctx, "U-1", "a", c.label, c.ttl)
		wantCode(t, err, domain.CodeValidation)
	}
	if _, _, err := f.svc.Tokens.Create(ctx, "U-1", "a", "ok", MaxTokenTTL); err != nil {
		t.Fatal(err)
	}
}

func TestRefusalRevokedToken(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	u := f.addUser(t, "alice", "pw")
	revoked, info, _ := f.svc.Tokens.Create(ctx, u.ID, u.Email, "old", 0)
	f.clk.add(time.Millisecond)
	live, _, _ := f.svc.Tokens.Create(ctx, u.ID, u.Email, "new", 0)
	if _, err := f.svc.ResolveCaller(ctx, revoked); err != nil {
		t.Fatalf("before revoke: %v", err)
	}
	if _, err := f.svc.Tokens.Revoke(ctx, info.Display()); err != nil {
		t.Fatal(err)
	}
	t.Run("Refused", func(t *testing.T) {
		_, err := f.svc.ResolveCaller(ctx, revoked)
		wantCode(t, err, domain.CodeUnauthenticated)
	})
	t.Run("Control", func(t *testing.T) {
		c, err := f.svc.ResolveCaller(ctx, "Bearer "+live)
		if err != nil || c.Identity != u.ID || c.TokenScope != u.ID {
			t.Fatalf("%+v %v", c, err)
		}
	})
	list, _ := f.svc.Tokens.List(ctx, "")
	if list[0].Status != "revoked" || list[1].Status != "live" {
		t.Fatalf("statuses %s %s", list[0].Status, list[1].Status)
	}
	_, err := f.svc.Tokens.Revoke(ctx, "0000000000000000")
	wantCode(t, err, domain.CodeNotFound)
}

// A whole token pasted into revoke is refused as VALIDATION without repeating it: the secret must
// not reach the error a second time.
func TestRevokeRefusesAWholeTokenWithoutEchoingIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	plain, info, _ := f.svc.Tokens.Create(ctx, "U-1", "a", "x", 0)
	secret := plain[len(info.Display())+1:]
	for _, bad := range []string{plain, info.ID + "_" + secret, strings.ToUpper(info.ID), "bd_xyz"} {
		_, err := f.svc.Tokens.Revoke(ctx, bad)
		wantCode(t, err, domain.CodeValidation)
		e, _ := domain.AsBatonDeckError(err)
		if s := fmt.Sprintf("%v %+v", err, e.Details); strings.Contains(s, secret) || strings.Contains(s, bad) {
			t.Fatalf("the refusal repeats its input: %s", s)
		}
	}
	if row, _ := f.st.GetToken(ctx, info.ID); row.RevokedAt != 0 {
		t.Fatal("a refused revoke revoked the token")
	}
}

func TestTokenExpiredWrongSecretAndMalformedAreUnauthenticated(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	u := f.addUser(t, "alice", "pw")
	plain, _, _ := f.svc.Tokens.Create(ctx, u.ID, u.Email, "short", time.Hour)
	if _, err := f.svc.ResolveCaller(ctx, plain); err != nil {
		t.Fatal(err)
	}
	wrong := plain[:len(plain)-1] + map[bool]string{true: "A", false: "B"}[plain[len(plain)-1] != 'A']
	for name, b := range map[string]string{
		"wrong secret": wrong, "empty": "", "no prefix": plain[3:], "upper id": "bd_" + strings.ToUpper(plain[3:19]) + plain[19:],
		"short secret": plain[:20] + "abc", "oversized": plain + strings.Repeat("a", 300),
	} {
		_, err := f.svc.ResolveCaller(ctx, b)
		if e, ok := domain.AsBatonDeckError(err); !ok || e.Code != domain.CodeUnauthenticated {
			t.Fatalf("%s: %v", name, err)
		}
	}
	f.clk.add(time.Hour)
	_, err := f.svc.ResolveCaller(ctx, plain)
	wantCode(t, err, domain.CodeUnauthenticated)
}
