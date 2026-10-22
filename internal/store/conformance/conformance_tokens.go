package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/store"
)

func testToken(id, identity string, at int64) store.Token {
	return store.Token{ID: id, IdentityID: identity, Subject: identity + "@x.test", SecretHash: "sha-" + id,
		Label: "label " + id, Permissions: "[]", CreatedAt: at, ExpiresAt: at + 1000}
}

// tokens judges the tokens table: create and read back, listing all and per identity in creation
// order, revoke (the first time stands, a revoked token stays listed) and ErrNotFound for a
// missing id.
func tokens(t *testing.T, newStore Factory) {
	ctx := context.Background()

	t.Run("TokensCreateAndReadBack", func(t *testing.T) {
		s := migrated(t, newStore)
		tok := testToken("t1", "U-1", 10)
		tok.LastUsedAt, tok.RevokedAt = 7, 8 // ignored: a token starts unused and live
		if err := s.CreateToken(ctx, tok); err != nil {
			t.Fatal(err)
		}
		tok.LastUsedAt, tok.RevokedAt = 0, 0
		got, err := s.GetToken(ctx, "t1")
		if err != nil || got != tok {
			t.Fatalf("%+v %v, want %+v", got, err, tok)
		}
	})

	t.Run("TokensListAllAndPerIdentity", func(t *testing.T) {
		s := migrated(t, newStore)
		for _, tok := range []store.Token{testToken("t2", "U-1", 20), testToken("t1", "U-2", 10), testToken("t3", "U-1", 30)} {
			if err := s.CreateToken(ctx, tok); err != nil {
				t.Fatal(err)
			}
		}
		ids := func(ts []store.Token) (out []string) {
			for _, tok := range ts {
				out = append(out, tok.ID)
			}
			return out
		}
		all, err := s.ListTokens(ctx, "")
		if err != nil || len(all) != 3 || ids(all)[0] != "t1" || ids(all)[2] != "t3" {
			t.Fatalf("all: %v %v", ids(all), err)
		}
		mine, err := s.ListTokens(ctx, "U-1")
		if err != nil || len(mine) != 2 || ids(mine)[0] != "t2" || ids(mine)[1] != "t3" {
			t.Fatalf("U-1: %v %v", ids(mine), err)
		}
		none, err := s.ListTokens(ctx, "U-9")
		if err != nil || len(none) != 0 {
			t.Fatalf("U-9: %v %v", ids(none), err)
		}
	})

	t.Run("TokensRevokeKeepsTheFirstTimeAndStaysListed", func(t *testing.T) {
		s := migrated(t, newStore)
		if err := s.CreateToken(ctx, testToken("t1", "U-1", 10)); err != nil {
			t.Fatal(err)
		}
		if err := s.RevokeToken(ctx, "t1", 50); err != nil {
			t.Fatal(err)
		}
		if err := s.RevokeToken(ctx, "t1", 60); err != nil {
			t.Fatalf("revoking twice: %v", err)
		}
		got, _ := s.GetToken(ctx, "t1")
		if got.RevokedAt != 50 {
			t.Fatalf("revoked_at %d, want 50", got.RevokedAt)
		}
		if l, _ := s.ListTokens(ctx, "U-1"); len(l) != 1 || l[0].RevokedAt != 50 {
			t.Fatalf("listed: %+v", l)
		}
	})

	t.Run("TokensMissingIsErrNotFound", func(t *testing.T) {
		s := migrated(t, newStore)
		if _, err := s.GetToken(ctx, "none"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("GetToken: %v", err)
		}
		if err := s.RevokeToken(ctx, "none", 1); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("RevokeToken: %v", err)
		}
	})
}
