package conformance_test

import (
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/conformance"
	"github.com/batondeck/batondeck-kanban/internal/store/memstore"
)

// The compile assertion of AC5: the in-memory store implements the whole Store.
var _ store.Store = (*memstore.Store)(nil)

// TestMemstoreConformance judges the in-memory store by the suite the engines pass: the fake the
// service lanes build on answers as SQLite and Postgres do wherever the suite looks.
func TestMemstoreConformance(t *testing.T) {
	conformance.Run(t, func(t *testing.T) conformance.Migratable {
		s := memstore.New()
		t.Cleanup(func() { s.Close() })
		return conformance.WithSeeder(s)
	})
}
