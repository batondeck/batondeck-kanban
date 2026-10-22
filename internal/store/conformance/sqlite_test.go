package conformance_test

import (
	"path/filepath"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/conformance"
)

// TestSQLiteConformance judges the SQLite engine by the suite: each store a fresh file under
// t.TempDir(), which the testing package removes.
func TestSQLiteConformance(t *testing.T) {
	conformance.Run(t, func(t *testing.T) conformance.Migratable {
		s, err := store.OpenSQLite(filepath.Join(t.TempDir(), "conformance.db"), "FULL")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	})
}
