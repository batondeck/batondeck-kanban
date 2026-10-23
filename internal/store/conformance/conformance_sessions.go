package conformance

import (
	"context"
	"errors"
	"testing"

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

	t.Run("SessionsRefuseAnUnknownUser", func(t *testing.T) {
		s := migrated(t, newStore)
		if err := s.CreateSession(ctx, sess); err == nil {
			t.Fatal("a session for a user that does not exist was stored")
		}
	})
}
