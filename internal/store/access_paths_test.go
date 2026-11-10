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
	// A deleted task's events (the foreign key's cascade) and one task's event page.
	{"task events cascade", "DELETE FROM task_events WHERE project_id = 'p' AND task_id = 't'", "task_events_task"},
	{"task events page", "SELECT id FROM task_events WHERE project_id = 'p' AND task_id = 't' ORDER BY ts DESC, id DESC LIMIT 50", "task_events_task"},
	// The blob GC's two reference checks, per blob.
	{"attachment blob reference", "SELECT 1 FROM attachments a WHERE a.blob_sha256 = 'h'", "attachments_blob"},
	{"memory blob reference", "SELECT 1 FROM memory m WHERE m.blob_sha256 = 'h'", "memory_blob"},
	// A task's memory window and the memory expiry sweep.
	{"memory window", "SELECT id FROM memory w WHERE w.project_id = 'p' AND w.task_id = 't' AND w.scope IN ('task', 'agent', 'shared') ORDER BY w.ts DESC, w.id DESC LIMIT 20", "memory_task"},
	{"memory expiry sweep", "SELECT id FROM memory m WHERE m.expires_at IS NOT NULL AND m.expires_at <= 5 ORDER BY m.expires_at, m.id LIMIT 500", "memory_expiry"},
	// The idempotency keys past their window.
	{"idempotency sweep", "SELECT project_id, identity, key FROM idempotency_keys i WHERE i.expires_at <= 5 ORDER BY i.expires_at, i.project_id, i.identity, i.key LIMIT 500", "idempotency_keys_expiry"},
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
