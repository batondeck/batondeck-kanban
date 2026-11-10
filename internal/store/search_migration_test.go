package store

import (
	"context"
	"os"
	"slices"
	"testing"
)

// Migration 0007 on Postgres, on a database seeded at 0006 with a task whose title holds a path, a
// ticket reference and an e-mail address: the task survives, and its words inside them match
// after the up (the search vector is recomputed for every row); down and up again keep it.
func TestSearchMigrationPostgres(t *testing.T) {
	if os.Getenv("BATONDECK_TEST_POSTGRES_DSN") == "" {
		t.Skip("BATONDECK_TEST_POSTGRES_DSN is not set: migration 0007 is not judged on Postgres")
	}
	ctx := context.Background()
	s, err := OpenPostgres(ctx, postgresDB(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	m, db, err := s.migrator()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	p, err := m.provider()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 6); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO projects (id, name, created_by, created_at) VALUES ('P-1', 'p', 'u', 1)`,
		`INSERT INTO boards (project_id, id, name, created_by, created_at) VALUES ('P-1', 'B-1', 'b', 'u', 1)`,
		`INSERT INTO tasks (project_id, id, board_id, column_id, title, description, status, order_key, created_by, created_at, updated_by, updated_at)
		 VALUES ('P-1', 'T-1', 'B-1', 'C-1', 'Fix internal/store/tasks.go for T-123', 'ask bob@example.com', 'TODO', 'a0', 'u', 1, 'u', 1)`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	hits := func(word string) []string {
		return sqliteStrings(t, db, `SELECT id FROM tasks WHERE search @@ to_tsquery('simple', $1) ORDER BY id`, "'"+word+"'")
	}
	if got := hits("tasks"); len(got) != 0 {
		t.Fatalf("at 0006 the word inside the path already matches %v: nothing to migrate", got)
	}
	for _, step := range []string{"0007 up", "0007 up again"} {
		if _, err := p.UpTo(ctx, 7); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		for _, w := range []string{"tasks", "store", "123", "bob", "example"} {
			if got := hits(w); !slices.Equal(got, []string{"T-1"}) {
				t.Fatalf("%s: %q matches %v, want [T-1]", step, w, got)
			}
		}
		if step == "0007 up" {
			if _, err := p.DownTo(ctx, 6); err != nil {
				t.Fatalf("0007 down: %v", err)
			}
			if got := hits("fix"); !slices.Equal(got, []string{"T-1"}) {
				t.Fatalf("0007 down: %v, want the task kept and searchable", got)
			}
		}
	}
}
