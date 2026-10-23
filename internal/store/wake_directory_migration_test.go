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

// Migration 0005 (ADR-0004, R0.25), up, down and up again on a database seeded at 0004 with a
// legacy /wake row: the row survives every step, the five columns and the pid index exist only when
// up, the down schema is 0004's exactly, one pid holds one entry while rows with no pid are many,
// and the role CHECK takes master and worker alone.
type wakeDirEngine struct {
	db      *sql.DB
	p       *goose.Provider
	schema  string // wake_sessions' schema, one row per object
	columns string // wake_sessions' column names
	ph      func(n int) string
}

func runWakeDirectory(t *testing.T, e wakeDirEngine) {
	ctx := context.Background()
	if _, err := e.p.UpTo(ctx, 4); err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.ExecContext(ctx, `INSERT INTO wake_sessions (id, identity_id, agent_id, created_at, last_seen_at, expires_at) VALUES ('W-old', 'U-1', 'a1', 1, 1, 2)`); err != nil {
		t.Fatal(err)
	}
	at4 := sqliteStrings(t, e.db, e.schema)
	slices.Sort(at4)
	newCols := []string{"bearer_sealed", "client_session_id", "pid", "proc_start", "role"}
	kept := func(step string) {
		t.Helper()
		if ids := sqliteStrings(t, e.db, `SELECT id FROM wake_sessions`); !slices.Equal(ids, []string{"W-old"}) {
			t.Fatalf("%s: rows %v, want the legacy W-old kept", step, ids)
		}
	}
	insert := func(id string, pid any, role any) error {
		_, err := e.db.ExecContext(ctx, `INSERT INTO wake_sessions (id, identity_id, agent_id, created_at, last_seen_at, expires_at, pid, role) VALUES (`+e.ph(1)+`, 'U-1', 'a1', 1, 1, 2, `+e.ph(2)+`, `+e.ph(3)+`)`, id, pid, role)
		return err
	}
	up := func(step string) {
		t.Helper()
		if _, err := e.p.UpTo(ctx, 5); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		kept(step)
		cols := sqliteStrings(t, e.db, e.columns)
		for _, c := range newCols {
			if !slices.Contains(cols, c) {
				t.Fatalf("%s: no column %s in %v", step, c, cols)
			}
		}
		if err := insert("W-a", 4242, "worker"); err != nil {
			t.Fatalf("%s: a worker row: %v", step, err)
		}
		if err := insert("W-b", 4242, "master"); err == nil {
			t.Fatalf("%s: a second row took pid 4242", step)
		}
		if err := insert("W-c", nil, nil); err != nil {
			t.Fatalf("%s: a second row with no pid: %v", step, err)
		}
		if err := insert("W-d", 4343, "admin"); err == nil {
			t.Fatalf("%s: the role CHECK took admin", step)
		}
		if _, err := e.db.ExecContext(ctx, `DELETE FROM wake_sessions WHERE id IN ('W-a', 'W-c')`); err != nil {
			t.Fatal(err)
		}
	}
	up("0005 up")
	if _, err := e.p.DownTo(ctx, 4); err != nil {
		t.Fatalf("0005 down: %v", err)
	}
	kept("0005 down")
	got := sqliteStrings(t, e.db, e.schema)
	slices.Sort(got)
	if !slices.Equal(got, at4) {
		t.Fatalf("0005 down: the schema is not 0004's:\n got %v\nwant %v", got, at4)
	}
	up("0005 up again")
}

func TestWakeDirectoryMigrationSQLite(t *testing.T) {
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "wake.db"), "FULL")
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
	runWakeDirectory(t, wakeDirEngine{
		db: s.w, p: p,
		schema:  `SELECT type || ' ' || name || ' ' || coalesce(sql, '') FROM sqlite_master WHERE tbl_name = 'wake_sessions'`,
		columns: `SELECT name FROM pragma_table_info('wake_sessions')`,
		ph:      func(int) string { return "?" },
	})
}

func TestWakeDirectoryMigrationPostgres(t *testing.T) {
	if os.Getenv("BATONDECK_TEST_POSTGRES_DSN") == "" {
		t.Skip("BATONDECK_TEST_POSTGRES_DSN is not set: migration 0005 is not judged on Postgres")
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
	runWakeDirectory(t, wakeDirEngine{
		db: db, p: p,
		schema: `SELECT 'attr ' || column_name || ' ' || data_type || ' ' || is_nullable || ' ' || coalesce(column_default, '-')
				FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'wake_sessions'
			UNION ALL SELECT 'con ' || conname || ' ' || pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid = 'wake_sessions'::regclass
			UNION ALL SELECT 'idx ' || indexdef FROM pg_indexes WHERE schemaname = 'public' AND tablename = 'wake_sessions'`,
		columns: `SELECT column_name::text FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'wake_sessions'`,
		ph:      func(n int) string { return "$" + string(rune('0'+n)) },
	})
}
