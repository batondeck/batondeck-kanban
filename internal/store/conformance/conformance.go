// Package conformance is the single test suite every Store engine must pass: the engines cannot
// drift apart because they are judged by the same tests. Engine packages call Run with a
// fresh-store factory.
package conformance

import (
	"context"
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

// Run judges one engine by the whole suite, area by area, in one fixed order. It holds no area
// yet, since the Store has none, so it skips rather than pass having judged nothing.
func Run(t *testing.T, newStore Factory) {
	t.Skip("conformance: the Store holds no area yet, so there is nothing to judge")
}
