package store

import (
	"slices"
	"testing"
)

// Migration 0003 (P-65): memory's only foreign keys are its project's and its blob's, so a deleted
// task takes no memory row with it; and the SQLite rebuild left memory nothing to lose: its table
// and its primary key's index, no other index, trigger or view.

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
}

func TestMemoryKeepsNoKeyToTasksPostgres(t *testing.T) {
	c := migratedPostgres(t)
	parents := pgStrings(t, c, `SELECT confrelid::regclass::text FROM pg_constraint WHERE conrelid = 'memory'::regclass AND contype = 'f' ORDER BY 1`)
	if !slices.Equal(parents, []string{"blobs", "projects"}) {
		t.Errorf("memory's foreign keys reference %v, want [blobs projects]", parents)
	}
}
