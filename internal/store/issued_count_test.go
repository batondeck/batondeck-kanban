package store

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// StatementsIssued counts what the context counter counts, without the context: a read on the pool
// and an AppendChange inside Atomically (the transaction's copy shares the engine's count), each
// under a context that carries no counter.
func TestStatementsIssuedSeesEveryContext(t *testing.T) {
	for _, c := range []struct {
		name string
		open func(t *testing.T) Store
	}{
		{"SQLite", func(t *testing.T) Store { return openSQLiteStore(t, filepath.Join(t.TempDir(), "issued.db")) }},
		{"Postgres", openPostgresStore},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := c.open(t)
			sc, ok := s.(StatementCounter)
			if !ok {
				t.Fatalf("%T is not a StatementCounter", s)
			}
			var n atomic.Int64
			counted := countingStatements(context.Background(), &n)
			before := sc.StatementsIssued()
			if _, err := s.ReadChanges(counted, 0, 10); err != nil {
				t.Fatal(err)
			}
			if err := s.Atomically(counted, func(tx Store) error {
				_, err := tx.AppendChange(counted, ChangeInput{Scope: "project:P-1", Kind: "changed", Ts: 1})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			want := n.Load()
			if _, err := s.ReadChanges(context.Background(), 0, 10); err != nil {
				t.Fatal(err)
			}
			if err := s.Atomically(context.Background(), func(tx Store) error {
				_, err := tx.AppendChange(context.Background(), ChangeInput{Scope: "project:P-1", Kind: "changed", Ts: 2})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			got := sc.StatementsIssued() - before
			t.Logf("%s: the context counter saw %d; StatementsIssued saw %d for the same calls twice, the second time under a bare context", c.name, want, got)
			if want == 0 || got != 2*want {
				t.Fatalf("StatementsIssued counted %d, want %d (twice the context counter's %d)", got, 2*want, want)
			}
		})
	}
}
