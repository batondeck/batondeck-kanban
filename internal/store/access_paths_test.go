package store

import (
	"database/sql"
	"slices"
	"strings"
	"testing"
)

// accessPaths is each statement that must reach its rows through an index rather than a scan of the
// whole table (or the whole project), with the index its SQLite plan must name.
var accessPaths = []struct{ name, query, index string }{
	// The head of a column: every write that moves a task into another column reads it.
	{"column head", "SELECT order_key FROM tasks WHERE project_id = 'p' AND board_id = 'b' AND column_id = 'c' ORDER BY order_key, id LIMIT 1", "tasks_column_order"},
}

// The migrated SQLite schema serves each access path through its index (EXPLAIN QUERY PLAN names
// it), with no sort of its own.
func TestSQLiteAccessPathsUseTheirIndexes(t *testing.T) {
	db := openSQLiteFile(t, migratedSQLite(t))
	for _, p := range accessPaths {
		plan := sqlitePlan(t, db, p.query)
		if !strings.Contains(plan, p.index) || strings.Contains(plan, "TEMP B-TREE") {
			t.Errorf("%s: plan %q, want it through %s with no sort", p.name, plan, p.index)
		}
	}
}

// The migrated Postgres schema holds each access path's index.
func TestPostgresAccessPathIndexes(t *testing.T) {
	c := migratedPostgres(t)
	indexes := pgStrings(t, c, "SELECT indexname::text FROM pg_indexes WHERE schemaname = 'public'")
	for _, p := range accessPaths {
		if !slices.Contains(indexes, p.index) {
			t.Errorf("%s: no index %s", p.name, p.index)
		}
	}
}

// sqlitePlan is query's EXPLAIN QUERY PLAN, its steps' details joined.
func sqlitePlan(t *testing.T, db *sql.DB, query string) string {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN " + query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var steps []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		steps = append(steps, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(steps, " | ")
}
