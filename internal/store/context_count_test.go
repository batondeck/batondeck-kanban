package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// The primitives get_task_context composes (service.Context, R1.17) cost at most six statements
// together, the plan's get_task_context <= 6: the task (Tasks.GetTask), its summary, a page of its
// context items, its memory, and the edges on each side of it, one statement each whether the
// task holds rows or none. Counted on a task holding a row of every kind and on a bare one.
func taskContextStatements(t *testing.T, s Store) {
	ctx := context.Background()
	p, err := s.CreateProject(ctx, CreateProjectInput{Name: "p", CreatedBy: "alice"}, WriteOptions{Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBoard(ctx, CreateBoardInput{ProjectID: p.ID, Name: "b", CreatedBy: "alice"}, WriteOptions{Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	task := func(title string) Task {
		t.Helper()
		tk, err := s.CreateTask(ctx, CreateTaskInput{ProjectID: p.ID, BoardID: b.ID, ColumnID: b.Columns[1].ID, Title: title,
			Status: domain.Todo, OrderKey: "a0", CreatedBy: "alice", CreatedAt: time.Now().UnixMilli()})
		if err != nil {
			t.Fatal(err)
		}
		return tk
	}
	full, blocker, dependent, bare := task("full"), task("blocker"), task("dependent"), task("bare")
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = s.SetSummary(ctx, p.ID, full.ID, "so far", "alice", full.Version)
	must(err)
	_, err = s.AddContextItem(ctx, AddContextItemInput{ProjectID: p.ID, TaskID: full.ID, Kind: ContextNote, Body: "n", Author: "alice"})
	must(err)
	_, err = s.WriteMemory(ctx, WriteMemoryInput{ProjectID: p.ID, TaskID: full.ID, Scope: MemoryTask, Key: "k", Value: json.RawMessage(`1`), Author: "alice"})
	must(err)
	_, err = s.AddEdge(ctx, AddEdgeInput{ProjectID: p.ID, FromTaskID: blocker.ID, ToTaskID: full.ID, Type: DependencyBlocks, CreatedBy: "alice"})
	must(err)
	_, err = s.AddEdge(ctx, AddEdgeInput{ProjectID: p.ID, FromTaskID: full.ID, ToTaskID: dependent.ID, Type: DependencyBlocks, CreatedBy: "alice"})
	must(err)

	for _, c := range []struct {
		task Task
		rows int
	}{{full, 5}, {bare, 0}} {
		var total atomic.Int64
		count := func(name string, call func(ctx context.Context) (int, error)) int {
			t.Helper()
			var n atomic.Int64
			got, err := call(countingStatements(ctx, &n))
			must(err)
			if n.Load() != 1 {
				t.Errorf("%s on %s issued %d statements, want 1", name, c.task.Title, n.Load())
			}
			t.Logf("%s on %s: %d statement(s)", name, c.task.Title, n.Load())
			total.Add(n.Load())
			return got
		}
		rows := 0
		count("Tasks.GetTask", func(ctx context.Context) (int, error) { _, err := s.GetTask(ctx, p.ID, c.task.ID); return 0, err })
		rows += count("Summaries.GetSummary", func(ctx context.Context) (int, error) {
			sum, err := s.GetSummary(ctx, p.ID, c.task.ID)
			if sum.Body != "" {
				return 1, err
			}
			return 0, err
		})
		rows += count("Context.ListContextItems", func(ctx context.Context) (int, error) {
			page, err := s.ListContextItems(ctx, p.ID, c.task.ID, ContextItemsQuery{Limit: 20})
			return len(page.Items), err
		})
		rows += count("Memory.ReadMemory", func(ctx context.Context) (int, error) {
			es, err := s.ReadMemory(ctx, p.ID, c.task.ID, ReadMemoryQuery{Identity: "alice", Limit: 20})
			return len(es), err
		})
		rows += count("Dependencies.ListBlockers", func(ctx context.Context) (int, error) {
			es, err := s.ListBlockers(ctx, p.ID, c.task.ID)
			return len(es), err
		})
		rows += count("Dependencies.ListDependents", func(ctx context.Context) (int, error) {
			es, err := s.ListDependents(ctx, p.ID, c.task.ID)
			return len(es), err
		})
		if rows != c.rows {
			t.Fatalf("the primitives read %d rows on %s, want %d", rows, c.task.Title, c.rows)
		}
		if got := total.Load(); got > 6 {
			t.Fatalf("the get_task_context primitives issued %d statements on %s, want at most 6", got, c.task.Title)
		}
		t.Logf("get_task_context primitives on %s: %d statements together", c.task.Title, total.Load())
	}
}

func TestTaskContextStatementsSQLite(t *testing.T) {
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "count.db"), "FULL")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	taskContextStatements(t, s)
}

func TestTaskContextStatementsPostgres(t *testing.T) {
	ctx := context.Background()
	s, err := OpenPostgres(ctx, postgresDB(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	taskContextStatements(t, s)
}
