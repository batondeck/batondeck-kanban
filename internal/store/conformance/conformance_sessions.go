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

	t.Run("SessionsRefuseAnUnknownUser", func(t *testing.T) {
		s := migrated(t, newStore)
		if err := s.CreateSession(ctx, sess); err == nil {
			t.Fatal("a session for a user that does not exist was stored")
		}
	})
}
