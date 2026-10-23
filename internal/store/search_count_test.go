package store

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// search_tasks costs at most two statements (plan §Performance's pin rule): SearchTasks is one,
// first page or a later one, and the tasks it names are one ListTasksByIDs; CountSearch, which a
// caller wanting a total adds instead, is one too.
func searchStatements(t *testing.T, s Store) {
	ctx := context.Background()
	p, err := s.CreateProject(ctx, CreateProjectInput{Name: "p", CreatedBy: "alice"}, WriteOptions{Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBoard(ctx, CreateBoardInput{ProjectID: p.ID, Name: "b", CreatedBy: "alice"}, WriteOptions{Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"deploy the api", "deploy deploy docs", "deploy web", "unrelated"} {
		if _, err := s.CreateTask(ctx, CreateTaskInput{ProjectID: p.ID, BoardID: b.ID, ColumnID: b.Columns[1].ID, Title: title,
			Status: domain.Todo, OrderKey: "a0", CreatedBy: "alice", CreatedAt: time.Now().UnixMilli()}); err != nil {
			t.Fatal(err)
		}
	}
	count := func(name string, call func(ctx context.Context) error) int64 {
		t.Helper()
		var n atomic.Int64
		if err := call(countingStatements(ctx, &n)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if n.Load() != 1 {
			t.Errorf("%s issued %d statements, want 1", name, n.Load())
		}
		t.Logf("%s: %d statement(s)", name, n.Load())
		return n.Load()
	}
	var page SearchPage
	total := count("Search.SearchTasks", func(ctx context.Context) (err error) {
		page, err = s.SearchTasks(ctx, p.ID, "deploy", SearchFilter{}, "", 2)
		return err
	})
	if len(page.Hits) != 2 || page.NextCursor == "" {
		t.Fatalf("first page %+v, want two hits and a cursor", page)
	}
	ids := []string{page.Hits[0].TaskID, page.Hits[1].TaskID}
	total += count("Tasks.ListTasksByIDs", func(ctx context.Context) error {
		got, err := s.ListTasksByIDs(ctx, p.ID, ids)
		if err == nil && len(got) != 2 {
			t.Errorf("ListTasksByIDs found %d of %v", len(got), ids)
		}
		return err
	})
	if total > 2 {
		t.Fatalf("search_tasks's page and its tasks issued %d statements, want at most 2", total)
	}
	count("Search.SearchTasks after a cursor", func(ctx context.Context) error {
		_, err := s.SearchTasks(ctx, p.ID, "deploy", SearchFilter{}, page.NextCursor, 2)
		return err
	})
	count("Search.CountSearch", func(ctx context.Context) error {
		n, err := s.CountSearch(ctx, p.ID, "deploy", SearchFilter{Statuses: []domain.Status{domain.Todo}})
		if err == nil && n != 3 {
			t.Errorf("CountSearch = %d, want 3", n)
		}
		return err
	})
}

func TestSearchStatementsSQLite(t *testing.T) {
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "count.db"), "FULL")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	searchStatements(t, s)
}

func TestSearchStatementsPostgres(t *testing.T) {
	ctx := context.Background()
	s, err := OpenPostgres(ctx, postgresDB(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	searchStatements(t, s)
}
