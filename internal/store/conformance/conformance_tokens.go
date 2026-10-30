package conformance

import (
	"context"
	"errors"
	"maps"
	"strings"
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
	// that has an older and a newer row go, oldest first (created_at, then id), at most limit a
	// statement. Kept: the oldest and the newest row of each (identity, label) however lapsed (label
	// ownership reads them, and its first minter is the earliest row: train clash T1), per identity,
	// with a created_at tie broken by id either way; a live token; and tokens lapsed at or after the
	// cutoff.
	t.Run("TokensDeleteLapsedKeepsTheOldestAndNewestRowOfEachLabel", func(t *testing.T) {
		s := migrated(t, newStore)
		tok := func(id, identity, label string, created, expires int64) store.Token {
			return store.Token{ID: id, IdentityID: identity, Subject: identity + "@x.test", SecretHash: "sha-" + id,
				Label: label, Permissions: "[]", CreatedAt: created, ExpiresAt: expires}
		}
		rows := []store.Token{
			tok("t-l", "U-1", "tie", 90, 100),      // kept: newest of (U-1, tie), the tie broken by id
			tok("t-k", "U-1", "tie", 90, 100),      // deleted: expired, between t-j and t-l by id
			tok("t-j", "U-1", "tie", 90, 100),      // kept: oldest of (U-1, tie), the tie broken by id
			tok("t-i", "U-2", "scout", 15, 100),    // kept: the only row of (U-2, scout), lapsed
			tok("t-h", "U-1", "late", 65, 10_000),  // kept: revoked at 350, after the cutoff
			tok("t-g", "U-1", "late", 70, 10_000),  // kept: live, newest of (U-1, late)
			tok("t-f", "U-1", "late", 60, 400),     // kept: expired at 400, after the cutoff
			tok("t-m", "U-1", "live", 30, 120),     // deleted: expired, t-e is older and t-d newer
			tok("t-e", "U-1", "live", 5, 120),      // kept: oldest of (U-1, live), lapsed
			tok("t-d", "U-1", "live", 50, 10_000),  // kept: live
			tok("t-c", "U-1", "scout", 40, 150),    // kept: newest of (U-1, scout), lapsed
			tok("t-n", "U-1", "scout", 25, 200),    // deleted: expired, t-a is older and t-c newer
			tok("t-b", "U-1", "scout", 20, 10_000), // deleted: revoked at 30, t-a is older and t-c newer
			tok("t-a", "U-1", "scout", 10, 100),    // kept: oldest of (U-1, scout), lapsed
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
		if l := left(); l["t-b"] || l["t-n"] || !l["t-m"] || !l["t-k"] {
			t.Fatalf("after the first page %v: want t-b and t-n (the oldest deletable) gone, t-m and t-k left", l)
		}
		if n, err := s.DeleteLapsedTokens(ctx, 300, 10); err != nil || n != 2 {
			t.Fatalf("second page: %d %v, want 2", n, err)
		}
		want := map[string]bool{"t-a": true, "t-c": true, "t-d": true, "t-e": true, "t-f": true, "t-g": true, "t-h": true, "t-i": true, "t-j": true, "t-l": true}
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
	// Train clash T1 (L5's first-minter rule reads a label's earliest row, its case folded): bob
	// mints ops-bot, carol then mints OPS-BOT, bob revokes his and mints ops-bot again, and a later
	// rotation of bob's leaves a middle row. After the sweep the earliest row of each folded label is
	// the same row, so the label's first minter is still bob, not carol.
	t.Run("TokensDeleteLapsedKeepsEachLabelsFirstMinter", func(t *testing.T) {
		s := migrated(t, newStore)
		mint := func(id, identity, label string, created, expires int64) {
			t.Helper()
			if err := s.CreateToken(ctx, store.Token{ID: id, IdentityID: identity, Subject: identity + "@x.test", SecretHash: "sha-" + id,
				Label: label, Permissions: "[]", CreatedAt: created, ExpiresAt: expires}); err != nil {
				t.Fatal(err)
			}
		}
		mint("t-bob-1", "U-bob", "ops-bot", 10, 10_000)     // bob's first ops-bot, revoked at 12
		mint("t-carol-1", "U-carol", "OPS-BOT", 11, 10_000) // carol's variant, live
		mint("t-bob-2", "U-bob", "ops-bot", 13, 100)        // bob's rotation, expired
		mint("t-bob-3", "U-bob", "ops-bot", 20, 10_000)     // bob's current ops-bot, live
		if err := s.RevokeToken(ctx, "t-bob-1", 12); err != nil {
			t.Fatal(err)
		}
		earliest := func() map[string]string {
			t.Helper()
			l, err := s.ListTokens(ctx, "")
			if err != nil {
				t.Fatal(err)
			}
			first := map[string]store.Token{}
			for _, x := range l {
				k := strings.ToLower(x.Label)
				if f, ok := first[k]; !ok || x.CreatedAt < f.CreatedAt || x.CreatedAt == f.CreatedAt && x.ID < f.ID {
					first[k] = x
				}
			}
			out := map[string]string{}
			for k, x := range first {
				out[k] = x.IdentityID + " " + x.ID
			}
			return out
		}
		before := earliest()
		if before["ops-bot"] != "U-bob t-bob-1" {
			t.Fatalf("before the sweep the earliest ops-bot row is %q, want bob's t-bob-1", before["ops-bot"])
		}
		if n, err := s.DeleteLapsedTokens(ctx, 300, 10); err != nil || n != 1 {
			t.Fatalf("the sweep: %d %v, want 1 (bob's middle row t-bob-2)", n, err)
		}
		if after := earliest(); !maps.Equal(after, before) {
			t.Fatalf("the earliest row of each label after the sweep %v, want %v unchanged (the first minter moved)", after, before)
		}
		if _, err := s.GetToken(ctx, "t-bob-2"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("bob's middle ops-bot row after the sweep: %v, want ErrNotFound", err)
		}
	})
}
