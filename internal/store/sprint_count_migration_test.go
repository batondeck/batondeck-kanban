package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Migration 0007 recounts every sprint's member_count from the tasks that name it, as
// ListSprintMembers reads them (the sprint's board, its project): a count that deletes left too high
// before the delete cascade lowered it, or one too low, becomes the true count; down and up again
// keep it. memberCount is what SPRINT_MAX_TASKS judges, so an inflated one refused members for ever.
func TestSprintMemberCountMigrationSQLite(t *testing.T) {
	ctx := context.Background()
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "s.db"), "FULL")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	m, err := s.migrator()
	if err != nil {
		t.Fatal(err)
	}
	sprintCountMigration(t, ctx, m, s.w)
}

func TestSprintMemberCountMigrationPostgres(t *testing.T) {
	if os.Getenv("BATONDECK_TEST_POSTGRES_DSN") == "" {
		t.Skip("BATONDECK_TEST_POSTGRES_DSN is not set: migration 0007 is not judged on Postgres")
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
	sprintCountMigration(t, ctx, m, db)
}

func sprintCountMigration(t *testing.T, ctx context.Context, m migrator, db *sql.DB) {
	t.Helper()
	p, err := m.provider()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.UpTo(ctx, 6); err != nil {
		t.Fatal(err)
	}
	task := func(project, id, board, sprints string) string {
		return `INSERT INTO tasks (project_id, id, board_id, column_id, title, description, status, order_key, sprint_ids, created_by, created_at, updated_by, updated_at)
		 VALUES ('` + project + `', '` + id + `', '` + board + `', 'C-1', 't', '', 'TODO', 'a0', '` + sprints + `', 'u', 1, 'u', 1)`
	}
	sprint := func(project, id, board string, count int) string {
		return `INSERT INTO sprints (project_id, id, board_id, name, goal, acceptance, status, member_count, created_by, created_at, updated_by, updated_at)
		 VALUES ('` + project + `', '` + id + `', '` + board + `', 's', '', '', 'PROPOSED', ` + strconv.Itoa(count) + `, 'u', 1, 'u', 1)`
	}
	for _, q := range []string{
		`INSERT INTO projects (id, name, created_by, created_at) VALUES ('P-1', 'p', 'u', 1), ('P-2', 'q', 'u', 1)`,
		`INSERT INTO boards (project_id, id, name, created_by, created_at) VALUES ('P-1', 'B-1', 'b', 'u', 1), ('P-1', 'B-2', 'c', 'u', 1), ('P-2', 'B-1', 'b', 'u', 1)`,
		sprint("P-1", "S-1", "B-1", 5), // two members left: three deletes before the cascade lowered it
		sprint("P-1", "S-2", "B-1", 0), // one member it never counted
		sprint("P-1", "S-3", "B-1", 2), // none left
		sprint("P-2", "S-1", "B-1", 1), // another project's S-1: one member of its own
		task("P-1", "T-1", "B-1", `["S-1"]`),
		task("P-1", "T-2", "B-1", `["S-2","S-1"]`),
		task("P-1", "T-3", "B-2", `["S-1"]`), // another board's task names S-1: not a member, as ListSprintMembers reads
		task("P-1", "T-4", "B-1", `[]`),
		task("P-2", "T-1", "B-1", `["S-1"]`),
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	counts := func() map[string]string {
		got := map[string]string{}
		for _, row := range sqliteStrings(t, db, `SELECT project_id || '/' || id || '=' || CAST(member_count AS TEXT) FROM sprints ORDER BY project_id, id`) {
			k, v, _ := strings.Cut(row, "=")
			got[k] = v
		}
		return got
	}
	want := map[string]string{"P-1/S-1": "2", "P-1/S-2": "1", "P-1/S-3": "0", "P-2/S-1": "1"}
	for _, step := range []string{"0007 up", "0007 up again"} {
		if _, err := p.UpTo(ctx, 7); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		got := counts()
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("%s: member counts %v, want %v", step, got, want)
			}
		}
		if step == "0007 up" {
			if _, err := p.DownTo(ctx, 6); err != nil {
				t.Fatalf("0007 down: %v", err)
			}
		}
	}
}
