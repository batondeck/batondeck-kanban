package store

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// deviceMigration is the version of the migration that gives a directory row its device (P-167,
// *_wake_session_device.sql) and the version before it, read from the provider's sources so that a
// renumbering keeps the test.
func deviceMigration(t *testing.T, p *goose.Provider) (v, prev int64) {
	t.Helper()
	for _, s := range p.ListSources() {
		if strings.HasSuffix(s.Path, "_wake_session_device.sql") {
			return s.Version, prev
		}
		prev = s.Version
	}
	t.Fatal("no *_wake_session_device.sql migration")
	return 0, 0
}

// The device migration (P-167), up, down and up again on a database holding
// directory rows: the rows survive the up with no device, a pid is unique per device once up (the
// same pid on another device registers, a second on the same device is refused), the down keeps one
// row per pid and the schema before it exactly, and the up after the down holds again.
func runWakeDevice(t *testing.T, e wakeDirEngine) {
	ctx := context.Background()
	v, prev := deviceMigration(t, e.p)
	if _, err := e.p.UpTo(ctx, prev); err != nil {
		t.Fatal(err)
	}
	before := sqliteStrings(t, e.db, e.schema)
	slices.Sort(before)
	insert := func(id string, pid any, device string) error {
		cols, vals, args := "id, identity_id, agent_id, created_at, last_seen_at, expires_at, pid", e.ph(1)+", 'U-1', 'a1', 1, 1, 2, "+e.ph(2), []any{id, pid}
		if device != "-" {
			cols, vals, args = cols+", device_id", vals+", "+e.ph(3), append(args, device)
		}
		_, err := e.db.ExecContext(ctx, `INSERT INTO wake_sessions (`+cols+`) VALUES (`+vals+`)`, args...)
		return err
	}
	for id, pid := range map[string]any{"W-here": 4242, "W-nopid": nil} {
		if err := insert(id, pid, "-"); err != nil {
			t.Fatal(err)
		}
	}
	up := func(step string) {
		t.Helper()
		if _, err := e.p.UpTo(ctx, v); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		if got := sqliteStrings(t, e.db, `SELECT id || ' ' || device_id FROM wake_sessions ORDER BY id`); !slices.Equal(got, []string{"W-here ", "W-nopid "}) {
			t.Fatalf("%s: rows %q, want W-here and W-nopid kept, naming no device", step, got)
		}
		if err := insert("W-there", 4242, "dev-b"); err != nil {
			t.Fatalf("%s: pid 4242 on another device: %v", step, err)
		}
		if err := insert("W-again", 4242, ""); err == nil {
			t.Fatalf("%s: a second row of pid 4242 on the same device", step)
		}
		if err := insert("W-nopid2", nil, ""); err != nil {
			t.Fatalf("%s: a second row with no pid: %v", step, err)
		}
	}
	up("up")
	if _, err := e.p.DownTo(ctx, prev); err != nil {
		t.Fatalf("down: %v", err)
	}
	got := sqliteStrings(t, e.db, e.schema)
	slices.Sort(got)
	if !slices.Equal(got, before) {
		t.Fatalf("down: the schema is not the one before:\n got %v\nwant %v", got, before)
	}
	// Of the two rows of pid 4242 the first by id, W-here, is kept.
	if ids := sqliteStrings(t, e.db, `SELECT id FROM wake_sessions ORDER BY id`); !slices.Equal(ids, []string{"W-here", "W-nopid", "W-nopid2"}) {
		t.Fatalf("down: rows %v, want W-here (one row of pid 4242), W-nopid and W-nopid2", ids)
	}
	if _, err := e.db.ExecContext(ctx, `DELETE FROM wake_sessions WHERE id = 'W-nopid2'`); err != nil {
		t.Fatal(err)
	}
	up("up again")
}

func TestWakeDeviceMigrationSQLite(t *testing.T) {
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "device.db"), "FULL")
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
	runWakeDevice(t, wakeDirEngine{
		db: s.w, p: p,
		schema: `SELECT type || ' ' || name || ' ' || coalesce(sql, '') FROM sqlite_master WHERE tbl_name = 'wake_sessions'`,
		ph:     func(int) string { return "?" },
	})
}

func TestWakeDeviceMigrationPostgres(t *testing.T) {
	if os.Getenv("BATONDECK_TEST_POSTGRES_DSN") == "" {
		t.Skip("BATONDECK_TEST_POSTGRES_DSN is not set: the device migration is not judged on Postgres")
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
	runWakeDevice(t, wakeDirEngine{
		db: db, p: p,
		schema: `SELECT 'attr ' || column_name || ' ' || data_type || ' ' || is_nullable || ' ' || coalesce(column_default, '-')
				FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'wake_sessions'
			UNION ALL SELECT 'con ' || conname || ' ' || pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid = 'wake_sessions'::regclass
			UNION ALL SELECT 'idx ' || indexdef FROM pg_indexes WHERE schemaname = 'public' AND tablename = 'wake_sessions'`,
		ph: func(n int) string { return "$" + string(rune('0'+n)) },
	})
}
