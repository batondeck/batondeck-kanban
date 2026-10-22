// Package conformance is the single test suite every Store engine must pass: the engines cannot
// drift apart because they are judged by the same tests. Engine tests call Run with a fresh-store
// factory: TestSQLiteConformance and TestPostgresConformance, beside this file.
package conformance

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/store"
)

// Migratable is what the suite needs beyond store.Store: down-migration for the
// up/down/up cleanliness check.
type Migratable interface {
	store.Store
	// MigrateDown rolls the schema back to version 0.
	MigrateDown(ctx context.Context) error
}

// Factory returns a NEW, empty, unmigrated store. The suite migrates it. Clean-up is the factory's.
type Factory func(t *testing.T) Migratable

// Run judges one engine by the whole suite, area by area, in one fixed order. An area is a function
// of this package, `func <area>(t *testing.T, newStore Factory)`, in its own file, and one line
// here.
func Run(t *testing.T, newStore Factory) {
	lifecycle(t, newStore)
	projects(t, newStore)
	boards(t, newStore)
	counters(t, newStore)
	idempotency(t, newStore)
}

func migrated(t *testing.T, newStore Factory) Migratable {
	t.Helper()
	s := newStore(t)
	if _, err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

// lifecycle is the suite for what every engine does before it holds an area: migrate up, down and up
// again, report the schema current, read its version from the connection, and transact.
func lifecycle(t *testing.T, newStore Factory) {
	t.Run("MigrateUpDownUp", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		if _, err := s.Migrate(ctx); err != nil {
			t.Fatalf("up: %v", err)
		}
		if err := s.MigrateDown(ctx); err != nil {
			t.Fatalf("down: %v", err)
		}
		if _, err := s.Migrate(ctx); err != nil {
			t.Fatalf("re-up: %v", err)
		}
	})

	t.Run("ASecondMigrateAppliesNothing", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		first, err := s.Migrate(ctx)
		if err != nil {
			t.Fatalf("first: %v", err)
		}
		second, err := s.Migrate(ctx)
		if err != nil {
			t.Fatalf("second: %v", err)
		}
		if second.Applied != 0 || second.Version != first.Version || second.None != first.None {
			t.Fatalf("a second Migrate did %+v after the first did %+v", second, first)
		}
	})

	t.Run("TheSchemaIsCurrentOnceMigrated", func(t *testing.T) {
		s := migrated(t, newStore)
		if err := s.SchemaCurrent(context.Background()); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("ServerVersionIsReadFromTheConnection", func(t *testing.T) {
		s := newStore(t)
		v, err := s.ServerVersion(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(v) == "" {
			t.Fatal("the connection reported an empty version")
		}
	})

	t.Run("AtomicallyReturnsItsCallbacksErrorAndNests", func(t *testing.T) {
		s := migrated(t, newStore)
		ctx := context.Background()
		boom := errors.New("boom")
		if err := s.Atomically(ctx, func(store.Store) error { return boom }); !errors.Is(err, boom) {
			t.Fatalf("Atomically returned %v, want the callback's error", err)
		}
		inner := 0
		err := s.Atomically(ctx, func(tx store.Store) error {
			return tx.Atomically(ctx, func(store.Store) error { inner++; return nil })
		})
		if err != nil || inner != 1 {
			t.Fatalf("a nested Atomically: err %v, ran %d times", err, inner)
		}
	})
}
