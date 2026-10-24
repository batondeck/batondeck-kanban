package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// Migration 0006 (P-275, owner-approved 2026-10-24), up, down and up again on a database seeded at
// 0005 with one row of each older type: those rows survive every step, the type CHECK takes
// 'handoff' only when up (and never an unknown type), down drops the handoff rows, and the down
// schema is 0005's exactly.
type handoffEngine struct {
	db     *sql.DB
	p      *goose.Provider
	schema string // notifications' schema, one row per object
}

func runHandoffMigration(t *testing.T, e handoffEngine) {
	ctx := context.Background()
	if _, err := e.p.UpTo(ctx, 5); err != nil {
		t.Fatal(err)
	}
	exec := func(q string) error { _, err := e.db.ExecContext(ctx, q); return err }
	row := func(id, typ string) string {
		return `INSERT INTO notifications (identity_id, id, project_id, type, actor, ts) VALUES ('U-1', '` + id + `', 'P-1', '` + typ + `', 'U-2', 1)`
	}
	for _, q := range []string{
		`INSERT INTO projects (id, name, created_by, created_at) VALUES ('P-1', 'p', 'u', 1)`,
		row("N-m", "mention"), row("N-f", "follow_up"), row("N-r", "review_request"),
	} {
		if err := exec(q); err != nil {
			t.Fatal(err)
		}
	}
	norm := func() []string {
		got := sqliteStrings(t, e.db, e.schema)
		for i, s := range got {
			got[i] = strings.ReplaceAll(s, `"notifications"`, "notifications")
		}
		slices.Sort(got)
		return got
	}
	at5 := norm()
	ids := func() []string { return sqliteStrings(t, e.db, `SELECT id FROM notifications ORDER BY id`) }
	old := []string{"N-f", "N-m", "N-r"}
	up := func(step string) {
		t.Helper()
		if _, err := e.p.UpTo(ctx, 6); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		if got := ids(); !slices.Equal(got, old) {
			t.Fatalf("%s: rows %v, want %v kept", step, got, old)
		}
		if err := exec(row("N-h", "handoff")); err != nil {
			t.Fatalf("%s: a handoff row: %v", step, err)
		}
		if err := exec(row("N-x", "assigned")); err == nil {
			t.Fatalf("%s: the type CHECK took assigned", step)
		}
	}
	up("0006 up")
	if _, err := e.p.DownTo(ctx, 5); err != nil {
		t.Fatalf("0006 down: %v", err)
	}
	if got := ids(); !slices.Equal(got, old) {
		t.Fatalf("0006 down: rows %v, want %v and the handoff row dropped", got, old)
	}
	if got := norm(); !slices.Equal(got, at5) {
		t.Fatalf("0006 down: the schema is not 0005's:\n got %v\nwant %v", got, at5)
	}
	if err := exec(row("N-h", "handoff")); err == nil {
		t.Fatal("0006 down: the type CHECK took handoff")
	}
	up("0006 up again")
}

func TestHandoffNotificationMigrationSQLite(t *testing.T) {
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "handoff.db"), "FULL")
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
	runHandoffMigration(t, handoffEngine{db: s.w, p: p,
		schema: `SELECT type || ' ' || name || ' ' || coalesce(sql, '') FROM sqlite_master WHERE tbl_name = 'notifications'`})
}

func TestHandoffNotificationMigrationPostgres(t *testing.T) {
	if os.Getenv("BATONDECK_TEST_POSTGRES_DSN") == "" {
		t.Skip("BATONDECK_TEST_POSTGRES_DSN is not set: migration 0006 is not judged on Postgres")
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
	runHandoffMigration(t, handoffEngine{db: db, p: p,
		schema: `SELECT 'attr ' || column_name || ' ' || data_type || ' ' || is_nullable || ' ' || coalesce(column_default, '-')
				FROM information_schema.columns WHERE table_schema = 'public' AND table_name = 'notifications'
			UNION ALL SELECT 'con ' || conname || ' ' || pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid = 'notifications'::regclass
			UNION ALL SELECT 'idx ' || indexdef FROM pg_indexes WHERE schemaname = 'public' AND tablename = 'notifications'`})
}
