package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// One task's events (ListTaskEvents: a ticket's Recent Activity, read on every open ticket after each
// write and each board frame) are read through an index on (project_id, task_id, ts, id): no scan of
// every event of the project (on SQLite, no sort either). The database is seeded at 0006, before the index, so the
// migration that adds it is judged on a database that already holds events.
const (
	indexSeedTasks  = 60
	indexSeedEvents = 6000
)

// sqliteRecorder hands the generated statement on to the database and keeps it, with its arguments,
// for the EXPLAIN.
type sqliteRecorder struct {
	*sql.DB
	query string
	args  []any
}

func (r *sqliteRecorder) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	r.query, r.args = q, args
	return r.DB.QueryContext(ctx, q, args...)
}

type pgRecorder struct {
	*pgx.Conn
	query string
	args  []any
}

func (r *pgRecorder) Query(ctx context.Context, q string, args ...any) (pgx.Rows, error) {
	r.query, r.args = q, args
	return r.Conn.Query(ctx, q, args...)
}

// taskEventsSeed is the project, its board, indexSeedTasks tasks and indexSeedEvents events spread over
// them, in the given dialect's series syntax.
func taskEventsSeed(series func(n int) string) []string {
	return []string{
		fixtureProject,
		fixtureBoard,
		`INSERT INTO tasks (project_id, id, board_id, column_id, title, description, status, order_key, search_tokens, created_by, created_at, updated_by, updated_at)
			SELECT 'P-1', 'T-' || i, 'B-1', 'C-1', 't', '', 'TODO', 'a' || i, '', 'u', 1, 'u', 1 FROM ` + series(indexSeedTasks),
		`INSERT INTO task_events (project_id, id, board_id, task_id, actor, type, data, ts)
			SELECT 'P-1', 'E-' || i, 'B-1', 'T-' || (i % ` + strconv.Itoa(indexSeedTasks) + ` + 1), 'u', 'task.updated', '{}', i FROM ` + series(indexSeedEvents),
	}
}

// sqlitePlan is EXPLAIN QUERY PLAN's detail lines for q.
func sqlitePlan(t *testing.T, db *sql.DB, q string, args []any) []string {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		out = append(out, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// The first page and a later page (the cursor's OR), as the store asks for them.
var taskEventsPages = []sqlitedb.ListTaskEventsParams{
	{ProjectID: "P-1", TaskID: sql.NullString{String: "T-7", Valid: true}, PageLimit: 201},
	{ProjectID: "P-1", TaskID: sql.NullString{String: "T-7", Valid: true}, AfterID: "E-3006", AfterTs: 3006, PageLimit: 201},
}

func TestTaskEventsReadIsIndexedSQLite(t *testing.T) {
	ctx := context.Background()
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "events.db"), "FULL")
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
	if _, err := p.UpTo(ctx, 6); err != nil {
		t.Fatal(err)
	}
	series := func(n int) string {
		return `(WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ` + strconv.Itoa(n) + `) SELECT i FROM n)`
	}
	for _, q := range taskEventsSeed(series) {
		if _, err := s.w.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("migrating a database that holds events: %v", err)
	}
	if _, err := s.w.ExecContext(ctx, "ANALYZE"); err != nil {
		t.Fatal(err)
	}
	for _, page := range taskEventsPages {
		rec := &sqliteRecorder{DB: s.w}
		rows, err := sqlitedb.New(rec).ListTaskEvents(ctx, page)
		if err != nil {
			t.Fatal(err)
		}
		if want := indexSeedEvents / indexSeedTasks; page.AfterID == "" && len(rows) != want {
			t.Fatalf("T-7's first page holds %d events, want %d", len(rows), want)
		}
		joined := strings.Join(sqlitePlan(t, s.w, rec.query, rec.args), " | ")
		if !strings.Contains(joined, "INDEX") || !strings.Contains(joined, "task_id=?") || strings.Contains(joined, "TEMP B-TREE") {
			t.Fatalf("ListTaskEvents (after %q) is not read through an index on (project_id, task_id, ts, id): %s", page.AfterID, joined)
		}
	}
}

func TestTaskEventsReadIsIndexedPostgres(t *testing.T) {
	if os.Getenv("BATONDECK_TEST_POSTGRES_DSN") == "" {
		t.Skip("BATONDECK_TEST_POSTGRES_DSN is not set: the task events index is not judged on Postgres")
	}
	ctx := context.Background()
	dsn := postgresDB(t)
	s, err := OpenPostgres(ctx, dsn)
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
	c, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	series := func(n int) string { return "generate_series(1, " + strconv.Itoa(n) + ") AS g(i)" }
	for _, q := range taskEventsSeed(series) {
		if _, err := c.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("migrating a database that holds events: %v", err)
	}
	if _, err := c.Exec(ctx, "ANALYZE task_events"); err != nil {
		t.Fatal(err)
	}
	for _, page := range taskEventsPages {
		rec := &pgRecorder{Conn: c}
		rows, err := pgdb.New(rec).ListTaskEvents(ctx, pgdb.ListTaskEventsParams(page))
		if err != nil {
			t.Fatal(err)
		}
		if want := indexSeedEvents / indexSeedTasks; page.AfterID == "" && len(rows) != want {
			t.Fatalf("T-7's first page holds %d events, want %d", len(rows), want)
		}
		r, err := c.Query(ctx, "EXPLAIN "+rec.query, rec.args...)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := pgx.CollectRows(r, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(plan, " | ")
		// Postgres may sort the one task's rows it reads by the index (a bitmap scan when the page
		// holds them all); what it must not do is read the project's.
		if strings.Contains(joined, "Seq Scan") ||
			!strings.Contains(joined, "Index Cond: ((project_id = 'P-1'::text) AND (task_id = 'T-7'::text))") {
			t.Fatalf("ListTaskEvents (after %q) is not read through an index on (project_id, task_id, ts, id): %s", page.AfterID, joined)
		}
	}
}
