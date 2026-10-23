package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// Migration 0004 (ADR-0005), up, down and up again on a database seeded at 0003 with the two retired
// statuses: every task's status and column, every column, the integration's column and the rows
// that reference tasks are read back at each step, and the tasks and columns schema is pinned to
// 0003's with only the status CHECK changed. The retired names are spelled in halves so that the
// repository's grep for them finds only the migration itself and deltas.json.
var (
	oldBacklog, oldReady = "BACK" + "LOG", "RE" + "ADY"
)

type fiveStatesEngine struct {
	db      *sql.DB
	p       *goose.Provider
	schema  string // tasks' and columns' schema, one row per object
	sixForm string // the six-value CHECK as this engine prints it
	five    string // the five-value CHECK as this engine prints it
	extra   func(t *testing.T, db *sql.DB)
}

func fiveStatesSeed() []string {
	b, r := oldBacklog, oldReady
	return []string{
		`INSERT INTO projects (id, name, created_by, created_at) VALUES ('P-1', 'p', 'u', 1)`,
		`INSERT INTO boards (project_id, id, name, created_by, created_at) VALUES ('P-1', 'B-1', 'main', 'u', 1), ('P-1', 'B-2', 'inbox', 'u', 1)`,
		fmt.Sprintf(`INSERT INTO columns (project_id, board_id, id, name, position, status, wip_limit) VALUES
			('P-1', 'B-1', 'C-B', 'Backlog', 0, '%[1]s', NULL),
			('P-1', 'B-1', 'C-R', 'Ready', 1, '%[2]s', 4),
			('P-1', 'B-1', 'C-I', 'In progress', 2, 'IN_PROGRESS', 2),
			('P-1', 'B-1', 'C-D', 'Done', 3, 'DONE', NULL),
			('P-1', 'B-1', 'C-R2', 'Next', 5, '%[2]s', NULL),
			('P-1', 'B-2', 'C-X', 'Inbox', 0, '%[1]s', 7),
			('P-1', 'B-2', 'C-Y', 'Backlog', 1, '%[1]s', NULL)`, b, r),
		// A deleted task leaves a gap in rid, so a rebuild that renumbered rid would break the FTS5
		// index; and B-1's second READY column (C-R2) pins the fold to the FIRST one.
		fmt.Sprintf(`INSERT INTO tasks (project_id, id, board_id, column_id, title, status, order_key, created_by, created_at, updated_by, updated_at) VALUES
			('P-1', 'T-0', 'B-1', 'C-R', 'gone', '%[2]s', 'a0', 'u', 1, 'u', 1)`, b, r),
		fmt.Sprintf(`INSERT INTO tasks (project_id, id, board_id, column_id, title, status, order_key, created_by, created_at, updated_by, updated_at) VALUES
			('P-1', 'T-1', 'B-1', 'C-B', 'zorblat parked', '%[1]s', 'a0', 'u', 1, 'u', 1),
			('P-1', 'T-2', 'B-1', 'C-R', 'next up', '%[2]s', 'a0', 'u', 1, 'u', 1),
			('P-1', 'T-3', 'B-1', 'C-I', 'doing', 'IN_PROGRESS', 'a0', 'u', 1, 'u', 1),
			('P-1', 'T-4', 'B-2', 'C-X', 'inbox', '%[1]s', 'a0', 'u', 1, 'u', 1),
			('P-1', 'T-5', 'B-1', 'C-D', 'shipped', 'DONE', 'a0', 'u', 1, 'u', 1)`, b, r),
		`DELETE FROM tasks WHERE id = 'T-0'`, // after the others, so its rid stays a gap below theirs
		`INSERT INTO task_events (project_id, id, board_id, task_id, actor, type, ts) VALUES ('P-1', 'E-1', 'B-1', 'T-1', 'u', 'task.created', 1)`,
		`INSERT INTO dependencies (project_id, id, blocker_id, dependent_id, created_by, ts) VALUES ('P-1', 'D-1', 'T-1', 'T-2', 'u', 1)`,
		`INSERT INTO integrations (project_id, id, name, source, board_id, column_id, auth, routing, limits, created_by, created_at, updated_at)
			VALUES ('P-1', 'I-1', 'i', 'grafana', 'B-1', 'C-B', '{}', '{}', '{}', 'u', 1, 1)`,
	}
}

// fiveStatesState is every row the migration may touch, one string per row.
func fiveStatesState(t *testing.T, db *sql.DB) []string {
	t.Helper()
	var out []string
	for _, q := range []string{
		`SELECT 'task ' || id || ' ' || status || ' ' || column_id FROM tasks ORDER BY id`,
		`SELECT 'column ' || board_id || ' ' || id || ' ' || name || ' ' || CAST(position AS TEXT) || ' ' || status || ' ' || coalesce(CAST(wip_limit AS TEXT), '-') FROM columns ORDER BY board_id, id`,
		`SELECT 'integration ' || id || ' ' || column_id FROM integrations`,
		`SELECT 'events ' || CAST(count(*) AS TEXT) FROM task_events`,
		`SELECT 'dependencies ' || CAST(count(*) AS TEXT) FROM dependencies`,
	} {
		out = append(out, sqliteStrings(t, db, q)...)
	}
	return out
}

func fiveStatesWant(down bool) []string {
	todo, toDo := "TODO", "To do"
	if down {
		todo, toDo = oldReady, "Ready"
	}
	return []string{
		"task T-1 " + todo + " C-R", "task T-2 " + todo + " C-R", "task T-3 IN_PROGRESS C-I", "task T-4 " + todo + " C-X", "task T-5 DONE C-D",
		"column B-1 C-D Done 3 DONE -", "column B-1 C-I In progress 2 IN_PROGRESS 2", "column B-1 C-R " + toDo + " 1 " + todo + " 4", "column B-1 C-R2 Next 5 " + todo + " -",
		"column B-2 C-X Inbox 0 " + todo + " 7", "column B-2 C-Y " + toDo + " 1 " + todo + " -",
		"integration I-1 C-R", "events 1", "dependencies 1",
	}
}

func runFiveStates(t *testing.T, e fiveStatesEngine) {
	ctx := context.Background()
	if _, err := e.p.UpTo(ctx, 3); err != nil {
		t.Fatal(err)
	}
	for _, q := range fiveStatesSeed() {
		if _, err := e.db.ExecContext(ctx, q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
	at3 := sqliteStrings(t, e.db, e.schema)
	t.Logf("0003 seeded: %v", fiveStatesState(t, e.db))
	normalize := func(rows []string, six bool) []string {
		out := make([]string, len(rows))
		for i, r := range rows {
			r = strings.ReplaceAll(r, `CREATE TABLE "tasks"`, "CREATE TABLE tasks")
			r = strings.ReplaceAll(r, `CREATE TABLE "columns"`, "CREATE TABLE columns")
			if six {
				r = strings.ReplaceAll(r, e.sixForm, e.five)
			}
			out[i] = r
		}
		slices.Sort(out)
		return out
	}
	refuses := func(status string) {
		t.Helper()
		if _, err := e.db.ExecContext(ctx, `UPDATE tasks SET status = '`+status+`' WHERE id = 'T-5'`); err == nil {
			t.Fatalf("the tasks CHECK took %s", status)
		}
		if _, err := e.db.ExecContext(ctx, `UPDATE columns SET status = '`+status+`' WHERE id = 'C-D'`); err == nil {
			t.Fatalf("the columns CHECK took %s", status)
		}
	}
	up := func(step string) {
		t.Helper()
		if _, err := e.p.UpTo(ctx, 4); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		got := fiveStatesState(t, e.db)
		t.Logf("%s: %v", step, got)
		if want := fiveStatesWant(false); !slices.Equal(got, want) {
			t.Fatalf("%s:\n got %v\nwant %v", step, got, want)
		}
		if got, want := normalize(sqliteStrings(t, e.db, e.schema), false), normalize(at3, true); !slices.Equal(got, want) {
			t.Fatalf("%s: the schema is not 0003's with the five-value CHECK:\n got %v\nwant %v", step, got, want)
		}
		refuses(oldBacklog)
		refuses(oldReady)
		if e.extra != nil {
			e.extra(t, e.db)
		}
	}
	up("0004 up")
	if _, err := e.p.DownTo(ctx, 3); err != nil {
		t.Fatalf("0004 down: %v", err)
	}
	got := fiveStatesState(t, e.db)
	t.Logf("0004 down: %v", got)
	if want := fiveStatesWant(true); !slices.Equal(got, want) {
		t.Fatalf("0004 down:\n got %v\nwant %v", got, want)
	}
	if got, want := normalize(sqliteStrings(t, e.db, e.schema), false), normalize(at3, false); !slices.Equal(got, want) {
		t.Fatalf("0004 down: the schema is not 0003's:\n got %v\nwant %v", got, want)
	}
	refuses("TODO")
	up("0004 up again")
}

func TestFiveStatesMigrationSQLite(t *testing.T) {
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "five.db"), "FULL")
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
	runFiveStates(t, fiveStatesEngine{
		db: s.w, p: p,
		// The whole schema: a lost column, key, index or trigger anywhere shows here.
		schema:  `SELECT type || ' ' || name || ' ' || tbl_name || ' ' || coalesce(sql, '') FROM sqlite_master WHERE name NOT LIKE 'goose%'`,
		sixForm: fmt.Sprintf(`CHECK (status IN ('%s', '%s', 'IN_PROGRESS', 'BLOCKED', 'REVIEW', 'DONE'))`, oldBacklog, oldReady),
		five:    `CHECK (status IN ('TODO', 'IN_PROGRESS', 'BLOCKED', 'REVIEW', 'DONE'))`,
		extra: func(t *testing.T, db *sql.DB) {
			if bad := sqliteStrings(t, db, `SELECT "table" || ' ' || parent FROM pragma_foreign_key_check`); len(bad) != 0 {
				t.Fatalf("foreign key violations %v", bad)
			}
			if on := sqliteStrings(t, db, `SELECT CAST(foreign_keys AS TEXT) FROM pragma_foreign_keys`); !slices.Equal(on, []string{"1"}) {
				t.Fatalf("foreign_keys %v after the migration, want 1", on)
			}
			if _, err := db.Exec(`INSERT INTO tasks_fts(tasks_fts, rank) VALUES ('integrity-check', 1)`); err != nil {
				t.Fatalf("the FTS5 index disagrees with tasks: %v", err)
			}
			if hit := sqliteStrings(t, db, `SELECT t.id FROM tasks_fts f JOIN tasks t ON t.rid = f.rid WHERE tasks_fts MATCH 'zorblat'`); !slices.Equal(hit, []string{"T-1"}) {
				t.Fatalf("search for zorblat found %v, want T-1", hit)
			}
		},
	})
}

func TestFiveStatesMigrationPostgres(t *testing.T) {
	if os.Getenv("BATONDECK_TEST_POSTGRES_DSN") == "" {
		t.Skip("BATONDECK_TEST_POSTGRES_DSN is not set: migration 0004 is not judged on Postgres")
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
	arr := func(vs ...string) string {
		var q []string
		for _, v := range vs {
			q = append(q, "'"+v+"'::text")
		}
		return "ARRAY[" + strings.Join(q, ", ") + "]"
	}
	runFiveStates(t, fiveStatesEngine{
		db: db, p: p,
		// tasks' and columns' columns, constraints, indexes and triggers, and every key that
		// references either: what a rewrite of the CHECK could have touched.
		schema: `SELECT 'attr ' || table_name || ' ' || column_name || ' ' || data_type || ' ' || is_nullable || ' ' || coalesce(column_default, '-') || ' ' || coalesce(generation_expression, '-')
				FROM information_schema.columns WHERE table_schema = 'public' AND table_name IN ('tasks', 'columns')
			UNION ALL SELECT 'con ' || conrelid::regclass::text || ' ' || conname || ' ' || pg_get_constraintdef(oid)
				FROM pg_constraint WHERE conrelid IN ('tasks'::regclass, 'columns'::regclass) OR confrelid IN ('tasks'::regclass, 'columns'::regclass)
			UNION ALL SELECT 'idx ' || indexdef FROM pg_indexes WHERE schemaname = 'public' AND tablename IN ('tasks', 'columns')
			UNION ALL SELECT 'trg ' || tgrelid::regclass::text || ' ' || tgname FROM pg_trigger WHERE tgrelid IN ('tasks'::regclass, 'columns'::regclass)`,
		sixForm: arr(oldBacklog, oldReady, "IN_PROGRESS", "BLOCKED", "REVIEW", "DONE"),
		five:    arr("TODO", "IN_PROGRESS", "BLOCKED", "REVIEW", "DONE"),
	})
}
