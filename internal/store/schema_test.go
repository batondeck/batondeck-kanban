package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// baseTables is every table 0001_base.sql makes on both engines.
var baseTables = []string{
	"projects", "project_members", "capability_grants", "project_counters",
	"boards", "columns",
	"tasks", "artifacts", "summaries", "runs",
	"task_events", "change_log",
	"dependencies",
	"context_items", "blobs", "memory",
	"comments", "follow_ups", "notifications",
	"sprints", "attachments",
	"identities", "agent_sessions", "agent_profiles", "skill_stats", "wake_sessions",
	"users", "sessions", "tokens",
	"oauth_clients", "oauth_codes", "oauth_refresh_families", "oauth_tokens",
	"audit_log", "tool_calls", "idempotency_keys",
	"chronicle_pages", "chronicle_versions", "chronicle_blobs",
	"integrations", "alert_states", "deliveries", "delivery_raw_bodies",
}

// baseIndexes is every access-path index 0001_base.sql names on both engines: the claim frontier,
// the board feed, the change log by scope, and the two directions of a dependency.
var baseIndexes = []string{
	"tasks_claim_frontier",
	"task_events_feed",
	"change_log_scope",
	"dependencies_blocker",
	"dependencies_dependent",
}

// alertStatesKey is alert_states' primary key, in order: one fingerprint's state per integration.
var alertStatesKey = []string{"project_id", "integration_id", "fingerprint_hash"}

// missing is what of want is not in have.
func missing(want, have []string) []string {
	var out []string
	for _, w := range want {
		if !slices.Contains(have, w) {
			out = append(out, w)
		}
	}
	return out
}

func sqliteStrings(t *testing.T, db *sql.DB, q string, args ...any) []string {
	t.Helper()
	rows, err := db.Query(q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// migratedSQLite migrates a fresh SQLite file through the Store and returns its path.
func migratedSQLite(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "schema.db")
	s, err := OpenSQLite(path, "FULL")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// openSQLiteFile is a connection of the test's own on the file, independent of the Store's.
func openSQLiteFile(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// The migrated SQLite schema holds every table and index by name, the FTS5 index over tasks and the
// three triggers that keep it in step, and change_log keyed by id.
func TestSchemaSQLite(t *testing.T) {
	db := openSQLiteFile(t, migratedSQLite(t))
	tables := sqliteStrings(t, db, "SELECT name FROM sqlite_master WHERE type = 'table'")
	if m := missing(append(slices.Clone(baseTables), "tasks_fts"), tables); len(m) > 0 {
		t.Errorf("tables missing: %v", m)
	}
	indexes := sqliteStrings(t, db, "SELECT name FROM sqlite_master WHERE type = 'index'")
	if m := missing(baseIndexes, indexes); len(m) > 0 {
		t.Errorf("indexes missing: %v", m)
	}
	triggers := sqliteStrings(t, db, "SELECT name FROM sqlite_master WHERE type = 'trigger'")
	if m := missing([]string{"tasks_fts_ai", "tasks_fts_ad", "tasks_fts_au"}, triggers); len(m) > 0 {
		t.Errorf("FTS triggers missing: %v", m)
	}
	if got := sqliteStrings(t, db, "SELECT type FROM pragma_table_info('identities') WHERE name = 'starred_assets' AND \"notnull\" = 1"); !slices.Equal(got, []string{"TEXT"}) {
		t.Errorf("identities.starred_assets: %v, want a NOT NULL TEXT column", got)
	}
	if got := sqliteStrings(t, db, "SELECT type || ' ' || dflt_value FROM pragma_table_info('users') WHERE name = 'workspace_admin' AND \"notnull\" = 1"); !slices.Equal(got, []string{"INTEGER 0"}) {
		t.Errorf("users.workspace_admin: %v, want a NOT NULL INTEGER column defaulting to 0", got)
	}
	if _, err := db.Exec("INSERT INTO users (id, email, display_name, password_hash, created_at, workspace_admin) VALUES ('U-x', 'x', 'x', 'h', 1, 2)"); err == nil {
		t.Errorf("users.workspace_admin took 2: its CHECK does not hold it to 0 and 1")
	}
	if _, err := db.Exec("INSERT INTO users (id, email, display_name, password_hash, created_at) VALUES ('U-y', 'y', 'y', 'h', 1)"); err != nil {
		t.Errorf("a user without workspace_admin: %v", err)
	} else if got := sqliteStrings(t, db, "SELECT CAST(workspace_admin AS TEXT) FROM users WHERE id = 'U-y'"); !slices.Equal(got, []string{"0"}) {
		t.Errorf("a user without workspace_admin reads %v, want 0", got)
	}
	pk := sqliteStrings(t, db, "SELECT name FROM pragma_table_info('change_log') WHERE pk > 0")
	if !slices.Equal(pk, []string{"id"}) {
		t.Errorf("change_log's primary key is %v, want [id]", pk)
	}
	pk = sqliteStrings(t, db, "SELECT name FROM pragma_table_info('alert_states') WHERE pk > 0 ORDER BY pk")
	if want := alertStatesKey; !slices.Equal(pk, want) {
		t.Errorf("alert_states' primary key is %v, want %v in that order", pk, want)
	}
}

// postgresDB creates a fresh database on BATONDECK_TEST_POSTGRES_DSN's server and returns its DSN.
// Unset, the test skips and says so; set and unreachable, it fails. The database is dropped when
// the test ends.
func postgresDB(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("BATONDECK_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("BATONDECK_TEST_POSTGRES_DSN is not set: the Postgres schema is not judged (set it to a postgres:// URL whose user may CREATE DATABASE)")
	}
	ctx := context.Background()
	name := fmt.Sprintf("bk_schema_%d_%d", time.Now().UnixNano(), pgSeq.Add(1))
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("BATONDECK_TEST_POSTGRES_DSN is set and its server does not answer: %v", err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), dsn)
		if err != nil {
			t.Errorf("dropping %s: %v", name, err)
			return
		}
		defer c.Close(context.Background())
		if _, err := c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("dropping %s: %v", name, err)
		}
	})
	i := strings.LastIndex(dsn, "/")
	rest := dsn[i+1:]
	if j := strings.Index(rest, "?"); j >= 0 {
		return dsn[:i+1] + name + rest[j:]
	}
	return dsn[:i+1] + name
}

var pgSeq atomic.Int64

// migratedPostgres migrates a fresh Postgres database through the Store and returns a connection
// of the test's own to it.
func migratedPostgres(t *testing.T) *pgx.Conn {
	t.Helper()
	ctx := context.Background()
	dsn := postgresDB(t)
	s, err := OpenPostgres(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	s.Close()
	c, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	return c
}

func pgStrings(t *testing.T, c *pgx.Conn, q string) []string {
	t.Helper()
	rows, err := c.Query(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// The migrated Postgres schema holds every table and index by name, the GIN index over the tasks'
// generated tsvector, and change_log keyed by id.
func TestSchemaPostgres(t *testing.T) {
	c := migratedPostgres(t)
	tables := pgStrings(t, c, "SELECT table_name::text FROM information_schema.tables WHERE table_schema = 'public' AND table_type = 'BASE TABLE'")
	if m := missing(baseTables, tables); len(m) > 0 {
		t.Errorf("tables missing: %v", m)
	}
	indexes := pgStrings(t, c, "SELECT indexname::text FROM pg_indexes WHERE schemaname = 'public'")
	if m := missing(append(slices.Clone(baseIndexes), "tasks_search"), indexes); len(m) > 0 {
		t.Errorf("indexes missing: %v", m)
	}
	gin := pgStrings(t, c, "SELECT indexdef FROM pg_indexes WHERE indexname = 'tasks_search'")
	if len(gin) != 1 || !strings.Contains(gin[0], "USING gin (search)") {
		t.Errorf("tasks_search is %v, want a GIN index on search", gin)
	}
	gen := pgStrings(t, c, "SELECT is_generated FROM information_schema.columns WHERE table_name = 'tasks' AND column_name = 'search' AND data_type = 'tsvector'")
	if !slices.Equal(gen, []string{"ALWAYS"}) {
		t.Errorf("tasks.search: %v, want a generated tsvector column", gen)
	}
	starred := pgStrings(t, c, "SELECT data_type::text FROM information_schema.columns WHERE table_name = 'identities' AND column_name = 'starred_assets' AND is_nullable = 'NO'")
	if !slices.Equal(starred, []string{"text"}) {
		t.Errorf("identities.starred_assets: %v, want a NOT NULL text column", starred)
	}
	admin := pgStrings(t, c, "SELECT data_type::text || ' ' || column_default FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'workspace_admin' AND is_nullable = 'NO'")
	if !slices.Equal(admin, []string{"boolean false"}) {
		t.Errorf("users.workspace_admin: %v, want a NOT NULL boolean column defaulting to false", admin)
	}
	pk := pgStrings(t, c, `SELECT a.attname::text FROM pg_index i JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
		WHERE i.indrelid = 'change_log'::regclass AND i.indisprimary`)
	if !slices.Equal(pk, []string{"id"}) {
		t.Errorf("change_log's primary key is %v, want [id]", pk)
	}
	pk = pgStrings(t, c, `SELECT a.attname::text FROM pg_index i
		CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, n)
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
		WHERE i.indrelid = 'alert_states'::regclass AND i.indisprimary ORDER BY k.n`)
	if want := alertStatesKey; !slices.Equal(pk, want) {
		t.Errorf("alert_states' primary key is %v, want %v in that order", pk, want)
	}
}

// The task fixture: a project, a board, and one task whose title, description and search tokens
// each carry a word of their own.
const (
	fixtureProject = `INSERT INTO projects (id, name, created_by, created_at) VALUES ('P-1', 'p', 'u', 1)`
	fixtureBoard   = `INSERT INTO boards (project_id, id, name, created_by, created_at) VALUES ('P-1', 'B-1', 'b', 'u', 1)`
	fixtureTask    = `INSERT INTO tasks (project_id, id, board_id, column_id, title, description, status, order_key, search_tokens, created_by, created_at, updated_by, updated_at)
		VALUES ('P-1', 'T-1', 'B-1', 'C-1', 'Rotate the frobnicator', 'quuxly described', 'TODO', 'a0', 'zorblat', 'u', 1, 'u', 1)`
)

// On SQLite the FTS5 index over tasks.search_body finds a task, through the generated SearchTasks,
// by a word of its title, a word only in its description and a word only in its search tokens; the
// triggers move it on an update and remove it on a delete.
func TestSearchIndexSQLite(t *testing.T) {
	ctx := context.Background()
	db := openSQLiteFile(t, migratedSQLite(t))
	for _, q := range []string{fixtureProject, fixtureBoard, fixtureTask} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	q := sqlitedb.New(db)
	search := func(word string) []string {
		t.Helper()
		rows, err := q.SearchTasks(ctx, sqlitedb.SearchTasksParams{SearchBody: word, ProjectID: "P-1"})
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		return ids
	}
	for _, w := range []string{"frobnicator", "quuxly", "zorblat"} {
		if got := search(w); !slices.Equal(got, []string{"T-1"}) {
			t.Fatalf("%q found %v, want [T-1]", w, got)
		}
	}
	if _, err := db.Exec("UPDATE tasks SET title = 'Polish the widget' WHERE project_id = 'P-1' AND id = 'T-1'"); err != nil {
		t.Fatal(err)
	}
	if got := search("frobnicator"); len(got) != 0 {
		t.Fatalf("the old title word still finds %v after an update", got)
	}
	if got := search("widget"); !slices.Equal(got, []string{"T-1"}) {
		t.Fatalf("the new title word found %v, want [T-1]", got)
	}
	if _, err := db.Exec("DELETE FROM tasks WHERE project_id = 'P-1' AND id = 'T-1'"); err != nil {
		t.Fatal(err)
	}
	if got := search("widget"); len(got) != 0 {
		t.Fatalf("a deleted task is still found: %v", got)
	}
}

// On Postgres the generated tsvector finds the same task by the same words, a word only in its
// description included, through the generated SearchTasks, and follows an update.
func TestSearchIndexPostgres(t *testing.T) {
	ctx := context.Background()
	c := migratedPostgres(t)
	for _, q := range []string{fixtureProject, fixtureBoard, fixtureTask} {
		if _, err := c.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	q := pgdb.New(c)
	search := func(word string) []string {
		t.Helper()
		rows, err := q.SearchTasks(ctx, pgdb.SearchTasksParams{PlaintoTsquery: word, ProjectID: "P-1"})
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		return ids
	}
	for _, w := range []string{"frobnicator", "quuxly", "zorblat"} {
		if got := search(w); !slices.Equal(got, []string{"T-1"}) {
			t.Fatalf("%q found %v, want [T-1]", w, got)
		}
	}
	if _, err := c.Exec(ctx, "UPDATE tasks SET title = 'Polish the widget' WHERE project_id = 'P-1' AND id = 'T-1'"); err != nil {
		t.Fatal(err)
	}
	if got := search("frobnicator"); len(got) != 0 {
		t.Fatalf("the old title word still finds %v after an update", got)
	}
	if got := search("widget"); !slices.Equal(got, []string{"T-1"}) {
		t.Fatalf("the new title word found %v, want [T-1]", got)
	}
}

// The task counter is per project: one project's second number is 2, and another project starts at
// 1 (conductor rule 6: T-n is unique only within its project).
func countersAgree(t *testing.T, next func(project string) int64) {
	t.Helper()
	for _, step := range []struct {
		project string
		want    int64
	}{{"P-1", 1}, {"P-1", 2}, {"P-2", 1}, {"P-1", 3}} {
		if got := next(step.project); got != step.want {
			t.Fatalf("NextTaskNumber(%s) = T-%d, want T-%d", step.project, got, step.want)
		}
	}
}

func TestTaskCounterSQLite(t *testing.T) {
	ctx := context.Background()
	db := openSQLiteFile(t, migratedSQLite(t))
	for _, p := range []string{"P-1", "P-2"} {
		if _, err := db.Exec("INSERT INTO projects (id, name, created_by, created_at) VALUES (?, 'p', 'u', 1)", p); err != nil {
			t.Fatal(err)
		}
	}
	q := sqlitedb.New(db)
	countersAgree(t, func(p string) int64 {
		n, err := q.NextTaskNumber(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return n
	})
}

func TestTaskCounterPostgres(t *testing.T) {
	ctx := context.Background()
	c := migratedPostgres(t)
	for _, p := range []string{"P-1", "P-2"} {
		if _, err := c.Exec(ctx, "INSERT INTO projects (id, name, created_by, created_at) VALUES ($1, 'p', 'u', 1)", p); err != nil {
			t.Fatal(err)
		}
	}
	q := pgdb.New(c)
	countersAgree(t, func(p string) int64 {
		n, err := q.NextTaskNumber(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return n
	})
}
