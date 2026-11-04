package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// A task's context items are strictly increasing in ts in the order they were added
// (Context.AddContextItem), so the newest-first page, and resume's last checkpoint read off it
// (P-117), is the insertion order even when the clock gives two adds one millisecond. Proved on
// each SQL engine without racing the clock: an item already stamped an hour ahead stands in for
// one stamped this millisecond, and ids are chosen so that the (ts, id) order alone would put the
// later add last.
func contextItemsKeepInsertionOrder(t *testing.T, s Store, e engine) {
	ctx := context.Background()
	p, err := s.CreateProject(ctx, CreateProjectInput{Name: "p", CreatedBy: "alice"}, WriteOptions{Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBoard(ctx, CreateBoardInput{ProjectID: p.ID, Name: "b", CreatedBy: "alice"}, WriteOptions{Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, CreateTaskInput{ProjectID: p.ID, BoardID: b.ID, ColumnID: b.Columns[1].ID, Title: "t",
		Status: domain.Todo, OrderKey: "a0", CreatedBy: "alice", CreatedAt: time.Now().UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	ahead := time.Now().Add(time.Hour).UnixMilli()
	insert := func(id string, ts int64) int64 {
		t.Helper()
		row, err := e.queries(true).InsertContextItem(ctx, sqlitedb.InsertContextItemParams{ID: id, Kind: "note", Body: id,
			Author: "alice", Ts: ts, ProjectID: p.ID, TaskID: task.ID})
		if err != nil {
			t.Fatal(err)
		}
		return row.Ts
	}
	first := insert("CI-zzzz", ahead)
	second := insert("CI-mmmm", ahead) // the same millisecond as first
	added, err := s.AddContextItem(ctx, AddContextItemInput{ProjectID: p.ID, TaskID: task.ID, Kind: ContextNote, Body: "CI-added", Author: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("stamped: first %d, second (asked %d) %d, AddContextItem now %d", first, ahead, second, added.TS)
	if first != ahead || second != ahead+1 || added.TS != ahead+2 {
		t.Fatalf("stamped %d, %d, %d; want %d, %d, %d", first, second, added.TS, ahead, ahead+1, ahead+2)
	}
	page, err := s.ListContextItems(ctx, p.ID, task.ID, ContextItemsQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range page.Items {
		got = append(got, it.Body)
	}
	if len(got) != 3 || got[0] != "CI-added" || got[1] != "CI-mmmm" || got[2] != "CI-zzzz" {
		t.Fatalf("newest first %v, want [CI-added CI-mmmm CI-zzzz]", got)
	}
}

func TestContextItemsKeepInsertionOrderSQLite(t *testing.T) {
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "order.db"), "FULL")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	contextItemsKeepInsertionOrder(t, s, s)
}

func TestContextItemsKeepInsertionOrderPostgres(t *testing.T) {
	ctx := context.Background()
	s, err := OpenPostgres(ctx, postgresDB(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	contextItemsKeepInsertionOrder(t, s, s)
}
