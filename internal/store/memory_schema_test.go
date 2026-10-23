package store

import (
	"slices"
	"strings"
	"testing"
)

// Migration 0003 (P-65): memory's only foreign keys are its project's and its blob's, so a deleted
// task takes no memory row with it; and the SQLite rebuild left memory nothing to lose: its table
// and its primary key's index, no other index, trigger or view, with every column, the scope CHECK
// and the remaining keys' ON DELETE actions kept.

func TestMemoryKeepsNoKeyToTasksSQLite(t *testing.T) {
	db := openSQLiteFile(t, migratedSQLite(t))
	parents := sqliteStrings(t, db, `SELECT "table" FROM pragma_foreign_key_list('memory') ORDER BY "table"`)
	if !slices.Equal(parents, []string{"blobs", "projects"}) {
		t.Errorf("memory's foreign keys reference %v, want [blobs projects]", parents)
	}
	objects := sqliteStrings(t, db, `SELECT type || ' ' || name FROM sqlite_master WHERE tbl_name = 'memory' OR sql LIKE '%memory%' ORDER BY name`)
	if !slices.Equal(objects, []string{"table memory", "index sqlite_autoindex_memory_1"}) {
		t.Errorf("schema objects on memory %v, want the table and its primary key's index", objects)
	}
	// The rebuild keeps every column, the scope CHECK and both remaining keys' ON DELETE actions.
	cols := sqliteStrings(t, db, `SELECT name || ' ' || type || ' ' || "notnull" || ' ' || pk || ' ' || coalesce(dflt_value, '-') FROM pragma_table_info('memory') ORDER BY cid`)
	wantCols := []string{"id TEXT 0 1 -", "project_id TEXT 0 0 -", "task_id TEXT 0 0 -", "scope TEXT 1 0 -", "key TEXT 1 0 -", "value TEXT 0 0 -", "blob_sha256 TEXT 0 0 -", "author TEXT 1 0 -", "ts INTEGER 1 0 -", "expires_at INTEGER 0 0 -"}
	if !slices.Equal(cols, wantCols) {
		t.Errorf("memory's columns %v, want %v", cols, wantCols)
	}
	keys := sqliteStrings(t, db, `SELECT "from" || ' ' || "table" || '(' || "to" || ') ' || on_delete FROM pragma_foreign_key_list('memory') ORDER BY "table"`)
	if want := []string{"blob_sha256 blobs(sha256) NO ACTION", "project_id projects(id) CASCADE"}; !slices.Equal(keys, want) {
		t.Errorf("memory's foreign keys %v, want %v", keys, want)
	}
	ddl := sqliteStrings(t, db, `SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'memory'`)
	if len(ddl) != 1 || !strings.Contains(ddl[0], "CHECK (scope IN ('task', 'agent', 'shared', 'project', 'agent_global'))") {
		t.Errorf("memory's DDL %q lost the scope CHECK", ddl)
	}
}

func TestMemoryKeepsNoKeyToTasksPostgres(t *testing.T) {
	c := migratedPostgres(t)
	parents := pgStrings(t, c, `SELECT confrelid::regclass::text FROM pg_constraint WHERE conrelid = 'memory'::regclass AND contype = 'f' ORDER BY 1`)
	if !slices.Equal(parents, []string{"blobs", "projects"}) {
		t.Errorf("memory's foreign keys reference %v, want [blobs projects]", parents)
	}
	actions := pgStrings(t, c, `SELECT confrelid::regclass::text || ' ' || confdeltype::text FROM pg_constraint WHERE conrelid = 'memory'::regclass AND contype = 'f' ORDER BY 1`)
	if want := []string{"blobs a", "projects c"}; !slices.Equal(actions, want) {
		t.Errorf("memory's foreign keys' ON DELETE %v, want %v (a no action, c cascade)", actions, want)
	}
	checks := pgStrings(t, c, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid = 'memory'::regclass AND contype = 'c' ORDER BY 1`)
	if want := []string{"CHECK ((scope = ANY (ARRAY['task'::text, 'agent'::text, 'shared'::text, 'project'::text, 'agent_global'::text])))"}; !slices.Equal(checks, want) {
		t.Errorf("memory's CHECK constraints %v, want %v", checks, want)
	}
}
