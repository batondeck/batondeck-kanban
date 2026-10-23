package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// StatementsIssued counts what the context counter counts, without the context: a read on the pool,
// an auth-area read (its own handles, users.go) and an AppendChange inside Atomically (the
// transaction's copy shares the engine's count), each under a context that carries no counter.
func TestStatementsIssuedSeesEveryContext(t *testing.T) {
	for _, c := range []struct {
		name string
		open func(t *testing.T) Store
		want int64 // ReadChanges, GetUser and AppendChange (two on Postgres: the INSERT and pg_notify)
	}{
		{"SQLite", func(t *testing.T) Store { return openSQLiteStore(t, filepath.Join(t.TempDir(), "issued.db")) }, 3},
		{"Postgres", openPostgresStore, 4},
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
			if _, err := s.GetUser(counted, "U-none"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("GetUser of no user: %v, want ErrNotFound", err)
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
			if _, err := s.GetUser(context.Background(), "U-none"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("GetUser of no user: %v, want ErrNotFound", err)
			}
			if err := s.Atomically(context.Background(), func(tx Store) error {
				_, err := tx.AppendChange(context.Background(), ChangeInput{Scope: "project:P-1", Kind: "changed", Ts: 2})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			got := sc.StatementsIssued() - before
			t.Logf("%s: the context counter saw %d; StatementsIssued saw %d for the same calls twice, the second time under a bare context", c.name, want, got)
			if want != c.want || got != 2*want {
				t.Fatalf("the context counter counted %d (want %d) and StatementsIssued %d (want %d)", want, c.want, got, 2*c.want)
			}
		})
	}
}
