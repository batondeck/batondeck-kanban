package store

import (
	"context"
	"path/filepath"
	"testing"
)

// AC3 of R0.11: a batch handed to a closed store is an error on both engines, never a silent
// success; that the middleware's caller never fails over it is service.ToolCalls's (R0.21 AC5).
func TestInsertToolCallsOnAClosedStoreIsAnError(t *testing.T) {
	batch := []ToolCall{{TS: 1, Tool: "claim_next", Identity: "alice", Outcome: ToolCallOK, Code: "OK"}}
	for _, c := range []struct {
		name string
		open func(t *testing.T) Store
	}{
		{"SQLite", func(t *testing.T) Store { return openSQLiteStore(t, filepath.Join(t.TempDir(), "closed.db")) }},
		{"Postgres", openPostgresStore},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := c.open(t)
			if err := s.InsertToolCalls(context.Background(), batch); err != nil {
				t.Fatalf("open store: %v", err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			err := s.InsertToolCalls(context.Background(), batch)
			t.Logf("closed store: %v", err)
			if err == nil {
				t.Fatal("InsertToolCalls on a closed store returned nil")
			}
		})
	}
}
