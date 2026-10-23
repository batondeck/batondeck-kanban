package conformance

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("oauth", oauth) }

func oauthClient(id string, created int64) store.OAuthClient {
	return store.OAuthClient{ClientID: id, ClientName: "client " + id,
		RedirectURIs: []string{"http://localhost:3334/oauth/callback", "https://app.example/cb"}, Scope: "worker", CreatedAt: created}
}

func oauthCode(hash, client string, at int64) store.OAuthCode {
	return store.OAuthCode{CodeHash: hash, ClientID: client, IdentityID: "U-1", RedirectURI: "https://app.example/cb",
		CodeChallenge: "challenge-" + hash, Scope: "worker", Resource: "https://bk.example/mcp", CreatedAt: at, ExpiresAt: at + 600_000}
}

func oauthFamily(id, client string, at int64) store.OAuthFamily {
	return store.OAuthFamily{ID: id, ClientID: client, IdentityID: "U-1", Scope: "master", Resource: "https://bk.example/mcp", CreatedAt: at}
}

func oauthToken(hash, kind, family, client string, at int64) store.OAuthToken {
	return store.OAuthToken{TokenHash: hash, Kind: kind, FamilyID: family, ClientID: client, IdentityID: "U-1",
		Scope: "master", Resource: "https://bk.example/mcp", CreatedAt: at, ExpiresAt: at + 3_600_000}
}

// oauth judges the OAuth 2.1 server's four tables: clients read back with their redirect URIs, the
// touch and the GC of clients never used (cascading to their codes, families and tokens); codes and
// tokens consumed once (ErrOAuthUsed after, one winner of ten concurrent calls, nothing kept by a
// rollback); the family revocation that the first time stands and every token of it reads; the
// foreign keys; ErrNotFound for every missing row.
func oauth(t *testing.T, newStore Factory) {
	ctx := context.Background()

	t.Run("ClientsReadBackTouchAndNotFound", func(t *testing.T) {
		s := migrated(t, newStore)
		c := oauthClient("C-1", 10)
		c.LastUsedAt = 99 // ignored: a client starts unused
		noErr(t, s.CreateOAuthClient(ctx, c))
		got, err := s.GetOAuthClient(ctx, "C-1")
		noErr(t, err)
		if got.ClientID != "C-1" || got.ClientName != "client C-1" || !slices.Equal(got.RedirectURIs, c.RedirectURIs) ||
			got.Scope != "worker" || got.CreatedAt != 10 || got.LastUsedAt != 0 {
			t.Fatalf("GetOAuthClient = %+v, want %+v with LastUsedAt 0", got, c)
		}
		noErr(t, s.TouchOAuthClient(ctx, "C-1", 50))
		if got, _ := s.GetOAuthClient(ctx, "C-1"); got.LastUsedAt != 50 {
			t.Fatalf("last used %d after the touch, want 50", got.LastUsedAt)
		}
		if _, err := s.GetOAuthClient(ctx, "C-9"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("GetOAuthClient(missing) = %v, want ErrNotFound", err)
		}
		if err := s.TouchOAuthClient(ctx, "C-9", 1); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("TouchOAuthClient(missing) = %v, want ErrNotFound", err)
		}
	})

	t.Run("DeleteUnusedClientsTakesOnlyOldNeverUsedOnesAndTheirRows", func(t *testing.T) {
		s := migrated(t, newStore)
		for _, c := range []store.OAuthClient{oauthClient("old-unused", 10), oauthClient("old-used", 10), oauthClient("new-unused", 500)} {
			noErr(t, s.CreateOAuthClient(ctx, c))
		}
		noErr(t, s.TouchOAuthClient(ctx, "old-used", 20))
		noErr(t, s.CreateOAuthCode(ctx, oauthCode("h-gone", "old-unused", 10)))
		noErr(t, s.CreateOAuthFamily(ctx, oauthFamily("F-gone", "old-unused", 10)))
		noErr(t, s.CreateOAuthToken(ctx, oauthToken("t-gone", store.OAuthAccess, "F-gone", "old-unused", 10)))
		noErr(t, s.CreateOAuthCode(ctx, oauthCode("h-kept", "old-used", 10)))

		n, err := s.DeleteUnusedOAuthClients(ctx, 100)
		noErr(t, err)
		if n != 1 {
			t.Fatalf("deleted %d clients, want 1 (old-unused)", n)
		}
		if _, err := s.GetOAuthClient(ctx, "old-unused"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("old-unused still there: %v", err)
		}
		for _, id := range []string{"old-used", "new-unused"} {
			if _, err := s.GetOAuthClient(ctx, id); err != nil {
				t.Fatalf("%s was deleted: %v", id, err)
			}
		}
		if _, err := s.ConsumeOAuthCode(ctx, "h-gone", 30); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("the deleted client's code: %v, want ErrNotFound", err)
		}
		if _, err := s.GetOAuthFamily(ctx, "F-gone"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("the deleted client's family: %v, want ErrNotFound", err)
		}
		if _, err := s.GetOAuthToken(ctx, "t-gone"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("the deleted client's token: %v, want ErrNotFound", err)
		}
		if _, err := s.ConsumeOAuthCode(ctx, "h-kept", 30); err != nil {
			t.Fatalf("the kept client's code: %v", err)
		}
	})

	t.Run("ACodeIsConsumedOnceAndKeepsItsFirstUse", func(t *testing.T) {
		s := migrated(t, newStore)
		noErr(t, s.CreateOAuthClient(ctx, oauthClient("C-1", 10)))
		in := oauthCode("h1", "C-1", 10)
		in.UsedAt = 5 // ignored: a code starts unused
		noErr(t, s.CreateOAuthCode(ctx, in))
		got, err := s.ConsumeOAuthCode(ctx, "h1", 40)
		noErr(t, err)
		in.UsedAt = 0
		if got != in {
			t.Fatalf("first consume = %+v, want %+v", got, in)
		}
		again, err := s.ConsumeOAuthCode(ctx, "h1", 70)
		if !errors.Is(err, store.ErrOAuthUsed) || again.UsedAt != 40 || again.ClientID != "C-1" {
			t.Fatalf("second consume = %+v %v, want the row used at 40 and ErrOAuthUsed", again, err)
		}
		if _, err := s.ConsumeOAuthCode(ctx, "h-missing", 40); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("missing code: %v, want ErrNotFound", err)
		}
		if err := s.CreateOAuthCode(ctx, oauthCode("h2", "C-none", 10)); err == nil {
			t.Fatal("a code of a missing client was accepted")
		}
	})

	t.Run("TenConcurrentConsumesHaveOneWinner", func(t *testing.T) {
		s := migrated(t, newStore)
		noErr(t, s.CreateOAuthClient(ctx, oauthClient("C-1", 10)))
		noErr(t, s.CreateOAuthCode(ctx, oauthCode("h1", "C-1", 10)))
		noErr(t, s.CreateOAuthFamily(ctx, oauthFamily("F-1", "C-1", 10)))
		noErr(t, s.CreateOAuthToken(ctx, oauthToken("r1", store.OAuthRefresh, "F-1", "C-1", 10)))
		for _, kind := range []string{"code", "token"} {
			var wg sync.WaitGroup
			var mu sync.Mutex
			won, used := 0, 0
			var other []error
			for i := range 10 {
				wg.Go(func() {
					var err error
					if kind == "code" {
						_, err = s.ConsumeOAuthCode(ctx, "h1", int64(100+i))
					} else {
						_, err = s.ConsumeOAuthToken(ctx, "r1", int64(100+i))
					}
					mu.Lock()
					defer mu.Unlock()
					switch {
					case err == nil:
						won++
					case errors.Is(err, store.ErrOAuthUsed):
						used++
					default:
						other = append(other, err)
					}
				})
			}
			wg.Wait()
			if won != 1 || used != 9 || len(other) != 0 {
				t.Fatalf("%s: %d won, %d used, other errors %v; want 1 and 9", kind, won, used, other)
			}
		}
	})

	t.Run("AConsumeRolledBackLeavesTheRowUnused", func(t *testing.T) {
		s := migrated(t, newStore)
		noErr(t, s.CreateOAuthClient(ctx, oauthClient("C-1", 10)))
		noErr(t, s.CreateOAuthCode(ctx, oauthCode("h1", "C-1", 10)))
		boom := fmt.Errorf("boom")
		err := s.Atomically(ctx, func(tx store.Store) error {
			if _, err := tx.ConsumeOAuthCode(ctx, "h1", 40); err != nil {
				return err
			}
			if _, err := tx.ConsumeOAuthCode(ctx, "h1", 41); !errors.Is(err, store.ErrOAuthUsed) {
				return fmt.Errorf("inside the transaction the second consume = %v, want ErrOAuthUsed", err)
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("Atomically = %v, want boom", err)
		}
		if got, err := s.ConsumeOAuthCode(ctx, "h1", 50); err != nil || got.UsedAt != 0 {
			t.Fatalf("after the rollback: %+v %v, want an unused code", got, err)
		}
	})

	t.Run("FamiliesRevokeOnceAndEveryTokenReadsIt", func(t *testing.T) {
		s := migrated(t, newStore)
		noErr(t, s.CreateOAuthClient(ctx, oauthClient("C-1", 10)))
		fam := oauthFamily("F-1", "C-1", 10)
		fam.RevokedAt, fam.RevokedReason = 3, "ignored"
		noErr(t, s.CreateOAuthFamily(ctx, fam))
		got, err := s.GetOAuthFamily(ctx, "F-1")
		noErr(t, err)
		fam.RevokedAt, fam.RevokedReason = 0, ""
		if got != fam {
			t.Fatalf("GetOAuthFamily = %+v, want %+v", got, fam)
		}
		access := oauthToken("a1", store.OAuthAccess, "F-1", "C-1", 20)
		access.UsedAt, access.RevokedAt, access.FamilyRevokedAt = 1, 2, 3 // ignored
		noErr(t, s.CreateOAuthToken(ctx, access))
		noErr(t, s.CreateOAuthToken(ctx, oauthToken("r1", store.OAuthRefresh, "F-1", "C-1", 20)))
		tok, err := s.GetOAuthToken(ctx, "a1")
		noErr(t, err)
		access.UsedAt, access.RevokedAt, access.FamilyRevokedAt = 0, 0, 0
		if tok != access {
			t.Fatalf("GetOAuthToken = %+v, want %+v", tok, access)
		}

		noErr(t, s.RevokeOAuthFamily(ctx, "F-1", 60, "refresh_reuse"))
		noErr(t, s.RevokeOAuthFamily(ctx, "F-1", 90, "later"))
		got, _ = s.GetOAuthFamily(ctx, "F-1")
		if got.RevokedAt != 60 || got.RevokedReason != "refresh_reuse" {
			t.Fatalf("revoked %d %q, want the first revocation (60, refresh_reuse)", got.RevokedAt, got.RevokedReason)
		}
		for _, h := range []string{"a1", "r1"} {
			if tok, err := s.GetOAuthToken(ctx, h); err != nil || tok.FamilyRevokedAt != 60 {
				t.Fatalf("token %s after the family's revocation: %+v %v, want FamilyRevokedAt 60", h, tok, err)
			}
		}
		if err := s.RevokeOAuthFamily(ctx, "F-9", 1, "x"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("RevokeOAuthFamily(missing) = %v, want ErrNotFound", err)
		}
		if _, err := s.GetOAuthFamily(ctx, "F-9"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("GetOAuthFamily(missing) = %v, want ErrNotFound", err)
		}
	})

	t.Run("ATokenIsConsumedOnceAndNeedsItsFamily", func(t *testing.T) {
		s := migrated(t, newStore)
		noErr(t, s.CreateOAuthClient(ctx, oauthClient("C-1", 10)))
		noErr(t, s.CreateOAuthFamily(ctx, oauthFamily("F-1", "C-1", 10)))
		noErr(t, s.CreateOAuthToken(ctx, oauthToken("r1", store.OAuthRefresh, "F-1", "C-1", 20)))
		first, err := s.ConsumeOAuthToken(ctx, "r1", 30)
		noErr(t, err)
		if first.UsedAt != 0 || first.Kind != store.OAuthRefresh || first.FamilyID != "F-1" {
			t.Fatalf("first consume = %+v", first)
		}
		again, err := s.ConsumeOAuthToken(ctx, "r1", 31)
		if !errors.Is(err, store.ErrOAuthUsed) || again.UsedAt != 30 {
			t.Fatalf("second consume = %+v %v, want used at 30 and ErrOAuthUsed", again, err)
		}
		if tok, _ := s.GetOAuthToken(ctx, "r1"); tok.UsedAt != 30 {
			t.Fatalf("read after the consumes: used at %d, want 30", tok.UsedAt)
		}
		if _, err := s.ConsumeOAuthToken(ctx, "r-missing", 30); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("missing token: %v, want ErrNotFound", err)
		}
		if _, err := s.GetOAuthToken(ctx, "r-missing"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("GetOAuthToken(missing) = %v, want ErrNotFound", err)
		}
		if err := s.CreateOAuthToken(ctx, oauthToken("r2", store.OAuthRefresh, "F-none", "C-1", 20)); err == nil {
			t.Fatal("a token of a missing family was accepted")
		}
		if err := s.CreateOAuthFamily(ctx, oauthFamily("F-2", "C-none", 20)); err == nil {
			t.Fatal("a family of a missing client was accepted")
		}
	})
}
