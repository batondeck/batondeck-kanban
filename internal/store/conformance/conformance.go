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

// Run judges one engine by the whole suite: lifecycle, then every area of areas in that order, each
// as a subtest named for it. An area is a function of this package, `func <area>(t *testing.T,
// newStore Factory)`, in its own file conformance_<area>.go, which hands it to register from an
// init. ADR-0003 B2's areas have their lines in areas already, so their lanes add a file and no
// line; an area outside B2 (oauth, R0.13) adds its line too. An area whose file has not landed yet
// SKIPS, naming the task that writes it.
func Run(t *testing.T, newStore Factory) {
	lifecycle(t, newStore)
	for _, a := range areas {
		t.Run(a.name, func(t *testing.T) {
			battery, ok := batteries[a.name]
			if !ok {
				t.Skipf("no battery for %s yet: %s writes it", a.name, a.owner)
			}
			battery(t, newStore)
		})
	}
}

// areas is every area of the Store (ADR-0003 B2), in the order Run judges them, with the task that
// owns its contract and battery.
var areas = []struct{ name, owner string }{
	{"projects", "R0.8"},
	{"members", "R0.8"}, // conformance_projects.go, beside projects
	{"boards", "R0.8"},
	{"counters", "R0.8"},
	{"idempotency", "R0.8"},
	{"users", "R0.12"},
	{"sessions", "R0.12"},
	{"tokens", "R0.12"},
	{"oauth", "R0.13"},
	{"tasks", "R0.18"},
	{"claims", "R0.18"},
	{"events", "R0.18"},
	{"changelog", "R0.18"},
	{"context", "R0.19"},
	{"memory", "R0.19"},
	{"summaries", "R0.19"},
	{"dependencies", "R0.19"},
	{"subtasks", "R0.19"},
	{"audit", "R0.19"},
	{"toolcalls", "R0.19"},
	{"agentsessions", "R0.19"},
	{"wakesessions", "R0.19"},
	{"attachments", "R0.19"},
	{"blobs", "R0.19"},
	{"search", "R0.19"},
	{"artifacts", "R2.1"},
	{"comments", "R2.1"},
	{"followups", "R2.1"},
	{"chronicle", "R2.6"},
	{"oncall", "R2.8"},
}

// batteries is each registered area's battery, by name.
var batteries = map[string]func(t *testing.T, newStore Factory){
	// The areas whose batteries predate register.
	"projects":    projects,
	"members":     members,
	"boards":      boards,
	"counters":    counters,
	"idempotency": idempotency,
	"users":       users,
	"sessions":    sessions,
	"tokens":      tokens,
}

// register hands an area's battery to Run. A name that is not in areas, or registered twice,
// panics at init: a battery Run would never call is a battery that never ran.
func register(name string, battery func(t *testing.T, newStore Factory)) {
	if _, dup := batteries[name]; dup {
		panic("conformance: area " + name + " registered twice")
	}
	for _, a := range areas {
		if a.name == name {
			batteries[name] = battery
			return
		}
	}
	panic("conformance: area " + name + " is not in areas")
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

	t.Run("AStoreUsedAfterItsAtomicallyReturnedWritesNothing", func(t *testing.T) {
		s := migrated(t, newStore)
		ctx := context.Background()
		for _, fail := range []bool{false, true} {
			var escaped store.Store
			_ = s.Atomically(ctx, func(tx store.Store) error {
				escaped = tx
				if fail {
					return errors.New("boom")
				}
				return nil
			})
			if _, err := escaped.CreateProject(ctx, store.CreateProjectInput{Name: "late", CreatedBy: "mallory"}, store.WriteOptions{Identity: "mallory"}); err == nil {
				t.Fatalf("a write through a Store that escaped its Atomically (rolled back: %v) succeeded", fail)
			}
		}
		if ps, err := s.ListProjects(ctx, "mallory"); err != nil || len(ps) != 0 {
			t.Fatalf("after the late writes: %d projects, %v", len(ps), err)
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
