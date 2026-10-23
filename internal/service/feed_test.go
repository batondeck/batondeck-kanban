package service

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// otherWriter is a mutation made by another process: its task lands in the store and its change row
// in the change log, and this process learns of it only from the bus (ObserveChange).
func otherWriter(t *testing.T, f *fixture, boardID string) store.Change {
	t.Helper()
	f.task(t, domain.Ready, nil)
	payload, _ := json.Marshal(map[string]string{"kind": ChangeKindChanged, "projectId": f.project.ID, "boardId": boardID})
	if _, err := f.st.AppendChange(f.ctx, store.ChangeInput{Scope: BroadcastScope(f.project.ID), ProjectID: f.project.ID, Kind: ChangeKindChanged, Payload: payload, Ts: 1}); err != nil {
		t.Fatal(err)
	}
	ch := f.changes(t)
	return ch[len(ch)-1]
}

func (f *fixture) snapshot(t *testing.T) BoardSnapshotResp {
	t.Helper()
	s, err := f.c.Feed().BoardSnapshot(f.ctx, f.alice, BoardSnapshotReq{ProjectID: f.project.ID, BoardID: f.board.ID})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// AC3: BoardSnapshot is served from the cache until a change-log row for its board arrives: a miss
// costs one GetBoard, a hit none, and the row (through the bus hook) makes the next read one again.
func TestBoardSnapshotCachedUntilAChangeRow(t *testing.T) {
	f, n := counted(t)
	ready := f.column(t, domain.Ready)
	f.snapshot(t) // the miss; also warms alice's access entry
	n.take()

	hit := f.snapshot(t)
	hitReads := n.take()
	ch := otherWriter(t, f, f.board.ID)
	stale := f.snapshot(t)
	staleReads := n.take()
	f.c.ObserveChange(ch)
	fresh := f.snapshot(t)
	freshReads := n.take()
	t.Logf("cached read: %v (READY %v); after the store write, before the row is observed: %v (READY %v); after ObserveChange(#%d): %v (READY %v, cursor %d)",
		hitReads, hit.TaskCounts[ready], staleReads, stale.TaskCounts[ready], ch.ID, freshReads, fresh.TaskCounts[ready], fresh.Cursor)
	if hitReads["GetBoard"] != 0 || staleReads["GetBoard"] != 0 {
		t.Fatalf("a cached snapshot read the store: %v, %v", hitReads, staleReads)
	}
	if freshReads["GetBoard"] != 1 || fresh.TaskCounts[ready] != 1 || fresh.Cursor != ch.ID {
		t.Fatalf("after the change row: reads %v, READY %v, cursor %d (row %d)", freshReads, fresh.TaskCounts[ready], fresh.Cursor, ch.ID)
	}
	f.snapshot(t)
	if again := n.take(); again["GetBoard"] != 0 {
		t.Fatalf("the recomposed snapshot was not cached: %v", again)
	}
}

// Negative control of AC3: a row naming another board leaves this board's snapshot cached; a row
// naming only the project drops every board of it.
func TestBoardSnapshotOtherBoardRowKeepsTheEntry(t *testing.T) {
	f, n := counted(t)
	f.snapshot(t)
	n.take()
	f.c.ObserveChange(otherWriter(t, f, "B-other"))
	f.snapshot(t)
	if reads := n.take(); reads["GetBoard"] != 0 {
		t.Fatalf("another board's row dropped this snapshot: %v", reads)
	}
	pid := "P-other"
	f.c.ObserveChange(store.Change{ID: 99, ProjectID: &pid, Payload: json.RawMessage(`{}`)})
	f.snapshot(t)
	if reads := n.take(); reads["GetBoard"] != 0 {
		t.Fatalf("another project's row dropped this snapshot: %v", reads)
	}
	f.c.ObserveChange(store.Change{ID: 100, ProjectID: &f.project.ID, Payload: json.RawMessage(`{"projectId":"` + f.project.ID + `"}`)})
	f.snapshot(t)
	if reads := n.take(); reads["GetBoard"] != 1 {
		t.Fatalf("a project-wide row kept the snapshot: %v", reads)
	}
}

// A writer reads its own column change before any bus delivers the row.
func TestBoardSnapshotReadsItsOwnWrites(t *testing.T) {
	f, n := counted(t)
	before := f.snapshot(t)
	col, err := f.c.Boards().AddColumn(f.ctx, f.alice, AddColumnReq{ProjectID: f.project.ID, BoardID: f.board.ID, Name: "QA", Status: domain.Review})
	if err != nil {
		t.Fatal(err)
	}
	n.take()
	got, err := f.c.Boards().Get(f.ctx, f.alice, GetBoardReq{ProjectID: f.project.ID, BoardID: f.board.ID})
	if err != nil {
		t.Fatal(err)
	}
	reads := n.take()
	t.Logf("columns %d -> %d after add_column, get_board reads %v", len(before.Board.Columns), len(got.Board.Columns), reads)
	if len(got.Board.Columns) != len(before.Board.Columns)+1 || reads["GetBoard"] != 1 {
		t.Fatalf("get_board after add_column %s: %d columns, reads %v", col.Column.ID, len(got.Board.Columns), reads)
	}
	if _, ok := got.TaskCounts[col.Column.ID]; !ok {
		t.Fatalf("no count for the new column: %v", got.TaskCounts)
	}
	// update_column and remove_column drop it the same way.
	if _, err := f.c.Boards().UpdateColumn(f.ctx, f.alice, UpdateColumnReq{ProjectID: f.project.ID, BoardID: f.board.ID, ColumnID: col.Column.ID, Name: "QA2"}); err != nil {
		t.Fatal(err)
	}
	if got := f.snapshot(t); got.Board.Columns[len(got.Board.Columns)-1].Name != "QA2" {
		t.Fatalf("get_board after update_column: %+v", got.Board.Columns)
	}
	if _, err := f.c.Boards().RemoveColumn(f.ctx, f.alice, RemoveColumnReq{ProjectID: f.project.ID, BoardID: f.board.ID, ColumnID: col.Column.ID}); err != nil {
		t.Fatal(err)
	}
	if got := f.snapshot(t); len(got.Board.Columns) != len(before.Board.Columns) {
		t.Fatalf("get_board after remove_column: %d columns", len(got.Board.Columns))
	}
}

// A composition that began before a drop does not store what it read.
func TestSnapshotCacheDropBeatsAFillInFlight(t *testing.T) {
	s := newSnapshotCache()
	key := [2]string{"P", "B"}
	_, gen, _, ok := s.get(key)
	if ok {
		t.Fatal("an empty cache hit")
	}
	s.drop("P", "B")
	s.put(key, BoardSnapshotResp{}, gen)
	if _, _, _, ok := s.get(key); ok {
		t.Fatal("a fill that began before the drop was stored")
	}
	_, gen, _, _ = s.get(key)
	s.put(key, BoardSnapshotResp{}, gen)
	if _, _, _, ok := s.get(key); !ok {
		t.Fatal("positive control: an undisturbed fill was not stored")
	}
}

// What a caller does to a snapshot it got does not reach the cache.
func TestBoardSnapshotIsACopy(t *testing.T) {
	f := newFixture(t, Options{})
	for i := range 2 { // the miss's answer, then a hit's
		got := f.snapshot(t)
		got.TaskCounts["x"] = 9
		got.Board.Columns[0].Name = "changed"
		got.Board.Transitions["BACKLOG"] = nil
		again := f.snapshot(t)
		if _, ok := again.TaskCounts["x"]; ok || again.Board.Columns[0].Name == "changed" || !reflect.DeepEqual(again.Board.Transitions, fixedTable) {
			t.Fatalf("read %d: the cached snapshot was changed through a returned copy: %+v", i, again)
		}
	}
}

func TestBoardFeed(t *testing.T) {
	f := newFixture(t, Options{})
	got, err := f.c.Boards().Create(f.ctx, f.alice, CreateBoardReq{ProjectID: f.project.ID, Name: "Feed"})
	if err != nil {
		t.Fatal(err)
	}
	page, err := f.c.Feed().BoardFeed(f.ctx, f.alice, BoardFeedReq{ProjectID: f.project.ID, BoardID: got.Board.ID})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("feed %+v", page)
	if len(page.Events) != 1 {
		t.Fatalf("events %+v", page.Events)
	}
	e := page.Events[0]
	if e.Type != domain.EventBoardCreated || e.BoardID != got.Board.ID || e.Actor != "alice" || e.Agent != "claude" || e.Data["name"] != "Feed" || e.Ts == "" || e.TaskID != "" || page.NextCursor != "" {
		t.Fatalf("event %+v", e)
	}
	_, err = f.c.Feed().BoardFeed(f.ctx, domain.Caller{Identity: "mallory"}, BoardFeedReq{ProjectID: f.project.ID, BoardID: got.Board.ID})
	wantCode(t, err, domain.CodeForbidden)
	_, err = f.c.Feed().BoardFeed(f.ctx, f.alice, BoardFeedReq{ProjectID: f.project.ID, BoardID: "B-missing"})
	wantCode(t, err, domain.CodeNotFound)
	empty, err := f.c.Feed().BoardFeed(f.ctx, f.alice, BoardFeedReq{ProjectID: f.project.ID, BoardID: f.board.ID})
	if err != nil || empty.Events == nil || len(empty.Events) != 0 {
		t.Fatalf("a board with no events: %+v (err %v)", empty, err)
	}
}
