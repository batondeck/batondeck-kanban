package memstore

import (
	"context"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The memstore half of internal/store's TestContextItemsKeepInsertionOrder*: an item already
// stamped an hour ahead stands in for one stamped this millisecond, with an id that the (ts, id)
// order alone would put after the new add; AddContextItem must still stamp the add after it.
func TestContextItemsKeepInsertionOrder(t *testing.T) {
	ctx := context.Background()
	s := New()
	t.Cleanup(func() { s.Close() })
	if _, err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject(ctx, store.CreateProjectInput{Name: "p", CreatedBy: "alice"}, store.WriteOptions{Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBoard(ctx, store.CreateBoardInput{ProjectID: p.ID, Name: "b", CreatedBy: "alice"}, store.WriteOptions{Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(ctx, store.CreateTaskInput{ProjectID: p.ID, BoardID: b.ID, ColumnID: b.Columns[1].ID, Title: "t",
		Status: domain.Todo, OrderKey: "a0", CreatedBy: "alice", CreatedAt: time.Now().UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	ahead := time.Now().Add(time.Hour).UnixMilli()
	if _, err := get(ctx, s, func(st *state) (struct{}, error) {
		it := store.ContextItem{ProjectID: p.ID, TaskID: task.ID, ID: "CI-zzzz", Kind: store.ContextNote, Body: "CI-zzzz", Author: "alice", TS: ahead}
		st.contextItems[[3]string{p.ID, task.ID, it.ID}] = it
		return struct{}{}, nil
	}); err != nil {
		t.Fatal(err)
	}
	added, err := s.AddContextItem(ctx, store.AddContextItemInput{ProjectID: p.ID, TaskID: task.ID, Kind: store.ContextNote, Body: "added", Author: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("stamped: the item ahead %d, AddContextItem now %d", ahead, added.TS)
	if added.TS != ahead+1 {
		t.Fatalf("AddContextItem stamped %d, want %d (one past the task's newest item)", added.TS, ahead+1)
	}
	page, err := s.ListContextItems(ctx, p.ID, task.ID, store.ContextItemsQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].Body != "added" {
		t.Fatalf("newest first %+v, want the add first", page.Items)
	}
}
