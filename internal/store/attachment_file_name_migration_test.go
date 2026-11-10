package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/pressly/goose/v3"
)

// Migration 0012 (api-http#4), up, down and up again on a database seeded at 0006 with a pending and
// a ready attachment: both rows survive every step, an old row reads the empty name, a new row keeps
// its name, and down drops only the column.
func runAttachmentFileNameMigration(t *testing.T, db *sql.DB, p *goose.Provider) {
	ctx := context.Background()
	if _, err := p.UpTo(ctx, 6); err != nil {
		t.Fatal(err)
	}
	exec := func(q string) error { _, err := db.ExecContext(ctx, q); return err }
	for _, q := range []string{
		`INSERT INTO projects (id, name, created_by, created_at) VALUES ('P-1', 'p', 'u', 1)`,
		`INSERT INTO boards (project_id, id, name, created_by, created_at) VALUES ('P-1', 'B-1', 'b', 'u', 1)`,
		`INSERT INTO columns (project_id, board_id, id, name, position, status) VALUES ('P-1', 'B-1', 'C-1', 'c', 1, 'TODO')`,
		`INSERT INTO tasks (project_id, id, board_id, column_id, title, status, order_key, created_by, created_at, updated_by, updated_at) VALUES ('P-1', 'T-1', 'B-1', 'C-1', 't', 'TODO', 'a0', 'u', 1, 'u', 1)`,
		`INSERT INTO blobs (sha256, bytes, created_at) VALUES ('` + sha64 + `', 3, 1)`,
		`INSERT INTO attachments (project_id, task_id, id, mime_type, bytes, kind, status, created_by, ts) VALUES ('P-1', 'T-1', 'A-pending', 'text/plain', 3, 'file', 'pending', 'u', 1)`,
		`INSERT INTO attachments (project_id, task_id, id, blob_sha256, mime_type, bytes, kind, status, created_by, ts) VALUES ('P-1', 'T-1', 'A-ready', '` + sha64 + `', 'text/plain', 3, 'file', 'ready', 'u', 2)`,
	} {
		if err := exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	ids := func() []string { return sqliteStrings(t, db, `SELECT id FROM attachments ORDER BY id`) }
	want := []string{"A-pending", "A-ready"}
	up := func(step string) {
		t.Helper()
		if _, err := p.UpTo(ctx, 12); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		if got := sqliteStrings(t, db, `SELECT id || '=' || file_name FROM attachments WHERE id IN ('A-pending', 'A-ready') ORDER BY id`); !slices.Equal(got, []string{"A-pending=", "A-ready="}) {
			t.Fatalf("%s: the old rows read %v, want each kept with the empty name", step, got)
		}
		if err := exec(`INSERT INTO attachments (project_id, task_id, id, mime_type, bytes, kind, status, created_by, ts, file_name) VALUES ('P-1', 'T-1', 'A-named', 'text/plain', 3, 'file', 'pending', 'u', 3, 'spec.pdf')`); err != nil {
			t.Fatalf("%s: a named row: %v", step, err)
		}
		if got := sqliteStrings(t, db, `SELECT file_name FROM attachments WHERE id = 'A-named'`); !slices.Equal(got, []string{"spec.pdf"}) {
			t.Fatalf("%s: the named row reads %v", step, got)
		}
		if err := exec(`DELETE FROM attachments WHERE id = 'A-named'`); err != nil {
			t.Fatal(err)
		}
	}
	up("0012 up")
	if _, err := p.DownTo(ctx, 6); err != nil {
		t.Fatalf("0012 down: %v", err)
	}
	if got := ids(); !slices.Equal(got, want) {
		t.Fatalf("0012 down: rows %v, want %v", got, want)
	}
	if err := exec(`SELECT file_name FROM attachments`); err == nil {
		t.Fatal("0012 down: file_name is still there")
	}
	up("0012 up again")
}

const sha64 = "abababababababababababababababababababababababababababababababab"

func TestAttachmentFileNameMigrationSQLite(t *testing.T) {
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "names.db"), "FULL")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	m, err := s.migrator()
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.provider()
	if err != nil {
		t.Fatal(err)
	}
	runAttachmentFileNameMigration(t, s.w, p)
}

func TestAttachmentFileNameMigrationPostgres(t *testing.T) {
	if os.Getenv("BATONDECK_TEST_POSTGRES_DSN") == "" {
		t.Skip("BATONDECK_TEST_POSTGRES_DSN is not set: migration 0012 is not judged on Postgres")
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
	runAttachmentFileNameMigration(t, db, p)
}
