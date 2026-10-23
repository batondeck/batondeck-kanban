package store

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// The statements an AppendChange issues: one on SQLite (the INSERT; its wake is in-process), two
// on Postgres (the INSERT, then the pg_notify the bus LISTENs for).
func TestAppendChangeStatements(t *testing.T) {
	for _, c := range []struct {
		name string
		open func(t *testing.T) Store
		want int64
	}{
		{"SQLite", func(t *testing.T) Store { return openSQLiteStore(t, filepath.Join(t.TempDir(), "count.db")) }, 1},
		{"Postgres", openPostgresStore, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := c.open(t)
			var n atomic.Int64
			if _, err := s.AppendChange(countingStatements(context.Background(), &n), ChangeInput{Scope: "project:P-1", Kind: "changed", Ts: 1}); err != nil {
				t.Fatal(err)
			}
			t.Logf("AppendChange: %d statement(s)", n.Load())
			if n.Load() != c.want {
				t.Fatalf("AppendChange issued %d statements, want %d", n.Load(), c.want)
			}
		})
	}
}
