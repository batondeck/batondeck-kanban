package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
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
	// The retention sweep (P-44), with the cutoff at 300: an expired and a revoked token of a label
	// that has a newer row go, oldest first (created_at, then id), at most limit a statement. Kept:
	// the newest row of each (identity, label) however lapsed (label ownership reads it), per
	// identity, with a created_at tie broken by id; a live token; and tokens lapsed at or after the
	// cutoff.
	t.Run("TokensDeleteLapsedKeepsTheNewestRowOfEachLabel", func(t *testing.T) {
		s := migrated(t, newStore)
		tok := func(id, identity, label string, created, expires int64) store.Token {
			return store.Token{ID: id, IdentityID: identity, Subject: identity + "@x.test", SecretHash: "sha-" + id,
				Label: label, Permissions: "[]", CreatedAt: created, ExpiresAt: expires}
		}
		rows := []store.Token{
			tok("t-k", "U-1", "tie", 90, 100),      // kept: newest of (U-1, tie), the tie broken by id
			tok("t-j", "U-1", "tie", 90, 100),      // deleted: expired, t-k is newer by id
			tok("t-i", "U-2", "scout", 15, 100),    // kept: newest of (U-2, scout), lapsed
			tok("t-h", "U-1", "late", 65, 10_000),  // kept: revoked at 350, after the cutoff
			tok("t-g", "U-1", "late", 70, 10_000),  // kept: live, newest of (U-1, late)
			tok("t-f", "U-1", "late", 60, 400),     // kept: expired at 400, after the cutoff
			tok("t-e", "U-1", "live", 5, 120),      // deleted: expired, t-d is newer
			tok("t-d", "U-1", "live", 50, 10_000),  // kept: live
			tok("t-c", "U-1", "scout", 40, 150),    // kept: newest of (U-1, scout), lapsed
			tok("t-b", "U-1", "scout", 20, 10_000), // deleted: revoked at 30, t-c is newer
			tok("t-a", "U-1", "scout", 10, 100),    // deleted: expired, t-c is newer
		}
		for _, r := range rows {
			if err := s.CreateToken(ctx, r); err != nil {
				t.Fatal(err)
			}
		}
		for id, at := range map[string]int64{"t-b": 30, "t-h": 350} {
			if err := s.RevokeToken(ctx, id, at); err != nil {
				t.Fatal(err)
			}
		}
		left := func() map[string]bool {
			t.Helper()
			l, err := s.ListTokens(ctx, "")
			if err != nil {
				t.Fatal(err)
			}
			out := map[string]bool{}
			for _, x := range l {
				out[x.ID] = true
			}
			return out
		}
		if n, err := s.DeleteLapsedTokens(ctx, 300, 2); err != nil || n != 2 {
			t.Fatalf("first page: %d %v, want 2", n, err)
		}
		if l := left(); l["t-e"] || l["t-a"] || !l["t-b"] || !l["t-j"] {
			t.Fatalf("after the first page %v: want t-e and t-a (the oldest) gone, t-b and t-j left", l)
		}
		if n, err := s.DeleteLapsedTokens(ctx, 300, 10); err != nil || n != 2 {
			t.Fatalf("second page: %d %v, want 2", n, err)
		}
		want := map[string]bool{"t-c": true, "t-d": true, "t-f": true, "t-g": true, "t-h": true, "t-i": true, "t-k": true}
		if l := left(); len(l) != len(want) {
			t.Fatalf("left %v, want %v", l, want)
		} else {
			for id := range want {
				if !l[id] {
					t.Fatalf("left %v, want %v", l, want)
				}
			}
		}
		if n, err := s.DeleteLapsedTokens(ctx, 300, 10); err != nil || n != 0 {
			t.Fatalf("third page: %d %v, want 0", n, err)
		}
		for _, limit := range []int{0, -1} {
			_, err := s.DeleteLapsedTokens(ctx, 300, limit)
			wantErr(t, err, domain.CodeValidation, "")
		}
	})
}
