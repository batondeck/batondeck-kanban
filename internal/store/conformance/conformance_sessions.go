package conformance

import (
	"context"
	"errors"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// sessions judges the sessions table: create and read back, ErrNotFound for a missing hash, and the
// foreign key to users.
func sessions(t *testing.T, newStore Factory) {
	ctx := context.Background()
	sess := store.Session{IDHash: "h1", UserID: "U-1", CSRFToken: "c1", CreatedAt: 10, LastSeenAt: 10, ExpiresAt: 99}

	t.Run("SessionsCreateAndReadBack", func(t *testing.T) {
		s := migrated(t, newStore)
		if err := s.CreateUser(ctx, testUser("U-1", "a@x.test", 1)); err != nil {
			t.Fatal(err)
		}
		if err := s.CreateSession(ctx, sess); err != nil {
			t.Fatal(err)
		}
		got, err := s.GetSession(ctx, "h1")
		if err != nil || got != sess {
			t.Fatalf("%+v %v, want %+v", got, err, sess)
		}
	})

	t.Run("SessionsMissingIsErrNotFound", func(t *testing.T) {
		s := migrated(t, newStore)
		if _, err := s.GetSession(ctx, "none"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("GetSession: %v", err)
		}
	})

	t.Run("SessionsDeleteEndsTheSession", func(t *testing.T) {
		s := migrated(t, newStore)
		if err := s.CreateUser(ctx, testUser("U-1", "a@x.test", 1)); err != nil {
			t.Fatal(err)
		}
		other := sess
		other.IDHash = "h2"
		for _, x := range []store.Session{sess, other} {
			if err := s.CreateSession(ctx, x); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.DeleteSession(ctx, "h1"); err != nil {
			t.Fatalf("DeleteSession: %v", err)
		}
		if _, err := s.GetSession(ctx, "h1"); !errors.Is(err, store.ErrNotFound) {
			t.Fatalf("GetSession after DeleteSession: %v, want ErrNotFound", err)
		}
		if got, err := s.GetSession(ctx, "h2"); err != nil || got != other {
			t.Fatalf("the user's other session: %+v %v, want it kept", got, err)
		}
		if err := s.DeleteSession(ctx, "h1"); err != nil {
			t.Fatalf("DeleteSession of a missing hash: %v, want nil", err)
		}
	})

	// A password change ends every session of that user and no one else's.
	t.Run("SessionsDeleteUserEndsOnlyTheirs", func(t *testing.T) {
		s := migrated(t, newStore)
		for i, id := range []string{"U-1", "U-2"} {
			if err := s.CreateUser(ctx, testUser(id, id+"@x.test", int64(i+1))); err != nil {
				t.Fatal(err)
			}
		}
		h2, bob := sess, sess
		h2.IDHash = "h2"
		bob.IDHash, bob.UserID = "h3", "U-2"
		for _, x := range []store.Session{sess, h2, bob} {
			if err := s.CreateSession(ctx, x); err != nil {
				t.Fatal(err)
			}
		}
		if n, err := s.DeleteUserSessions(ctx, "U-1"); err != nil || n != 2 {
			t.Fatalf("DeleteUserSessions: %d %v, want 2", n, err)
		}
		for _, h := range []string{"h1", "h2"} {
			if _, err := s.GetSession(ctx, h); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("GetSession(%s) after DeleteUserSessions: %v, want ErrNotFound", h, err)
			}
		}
		if got, err := s.GetSession(ctx, "h3"); err != nil || got != bob {
			t.Fatalf("another user's session: %+v %v, want it kept", got, err)
		}
		if n, err := s.DeleteUserSessions(ctx, "U-1"); err != nil || n != 0 {
			t.Fatalf("DeleteUserSessions again: %d %v, want 0", n, err)
		}
		if n, err := s.DeleteUserSessions(ctx, "U-none"); err != nil || n != 0 {
			t.Fatalf("DeleteUserSessions of no user: %d %v, want 0", n, err)
		}
	})

	// The retention sweep (P-44): only sessions that expired before the cutoff go, at most limit a
	// statement, oldest expiry first; one expiring at the cutoff and a later one stay.
	t.Run("SessionsDeleteExpiredTakesTheOldestBeforeTheCutoff", func(t *testing.T) {
		s := migrated(t, newStore)
		if err := s.CreateUser(ctx, testUser("U-1", "a@x.test", 1)); err != nil {
			t.Fatal(err)
		}
		// The hashes sort against their expiry, so the order taken is the expiry's.
		for _, x := range []struct {
			hash    string
			expires int64
		}{{"h-v", 50}, {"h-w", 40}, {"h-x", 30}, {"h-y", 20}, {"h-z", 10}} {
			if err := s.CreateSession(ctx, store.Session{IDHash: x.hash, UserID: "U-1", CSRFToken: "c", CreatedAt: 1, LastSeenAt: 1, ExpiresAt: x.expires}); err != nil {
				t.Fatal(err)
			}
		}
		gone := func(hashes ...string) {
			t.Helper()
			for _, h := range hashes {
				if _, err := s.GetSession(ctx, h); !errors.Is(err, store.ErrNotFound) {
					t.Fatalf("session %s: %v, want it deleted", h, err)
				}
			}
		}
		kept := func(hashes ...string) {
			t.Helper()
			for _, h := range hashes {
				if _, err := s.GetSession(ctx, h); err != nil {
					t.Fatalf("session %s: %v, want it kept", h, err)
				}
			}
		}
		if n, err := s.DeleteExpiredSessions(ctx, 40, 2); err != nil || n != 2 {
			t.Fatalf("first page: %d %v, want 2", n, err)
		}
		gone("h-z", "h-y")
		kept("h-x", "h-w", "h-v")
		if n, err := s.DeleteExpiredSessions(ctx, 40, 10); err != nil || n != 1 {
			t.Fatalf("second page: %d %v, want 1", n, err)
		}
		gone("h-x")
		kept("h-w", "h-v")
		if n, err := s.DeleteExpiredSessions(ctx, 40, 10); err != nil || n != 0 {
			t.Fatalf("third page: %d %v, want 0", n, err)
		}
		for _, limit := range []int{0, -1} {
			_, err := s.DeleteExpiredSessions(ctx, 40, limit)
			wantErr(t, err, domain.CodeValidation, "")
		}
	})

	t.Run("SessionsRefuseAnUnknownUser", func(t *testing.T) {
		s := migrated(t, newStore)
		if err := s.CreateSession(ctx, sess); err == nil {
			t.Fatal("a session for a user that does not exist was stored")
		}
	})
}
