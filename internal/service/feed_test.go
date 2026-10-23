package service

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// otherWriter is a mutation made by another process: its task lands in the store and its change row
// in the change log, and this process learns of it only from the bus (ObserveChange).
func otherWriter(t *testing.T, f *fixture, boardID string) store.Change {
	t.Helper()
	f.task(t, domain.Todo, nil)
	payload, _ := json.Marshal(map[string]string{"kind": ChangeKindChanged, "projectId": f.project.ID, "boardId": boardID})
	if _, err := f.st.AppendChange(f.ctx, store.ChangeInput{Scope: BroadcastScope(f.project.ID), ProjectID: f.project.ID, Kind: ChangeKindChanged, Payload: payload, Ts: 1}); err != nil {
		t.Fatal(err)
	}
	ch := f.changes(t)
	return ch[len(ch)-1]
}

// wire is the bus attaching: it delivers a row of another project, which makes the cache live.
func (f *fixture) wire() {
	f.c.ObserveChange(store.Change{ID: 0, Payload: json.RawMessage(`{"projectId":"P-elsewhere"}`)})
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
	f.wire()
	ready := f.column(t, domain.Todo)
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
	t.Logf("cached read: %v (TODO %v); after the store write, before the row is observed: %v (TODO %v); after ObserveChange(#%d): %v (TODO %v, cursor %d)",
		hitReads, hit.TaskCounts[ready], staleReads, stale.TaskCounts[ready], ch.ID, freshReads, fresh.TaskCounts[ready], fresh.Cursor)
	if hitReads["GetBoard"] != 0 || staleReads["GetBoard"] != 0 {
		t.Fatalf("a cached snapshot read the store: %v, %v", hitReads, staleReads)
	}
	if freshReads["GetBoard"] != 1 || fresh.TaskCounts[ready] != 1 || fresh.Cursor != ch.ID {
		t.Fatalf("after the change row: reads %v, TODO %v, cursor %d (row %d)", freshReads, fresh.TaskCounts[ready], fresh.Cursor, ch.ID)
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
	f.wire()
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
	f.wire()
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

// With no bus attached nothing would ever drop an entry, so every read composes: a task written by
// another lane's mutation shows at once.
func TestBoardSnapshotUncachedUntilTheBusDelivers(t *testing.T) {
	f, n := counted(t)
	ready := f.column(t, domain.Todo)
	f.snapshot(t)
	f.task(t, domain.Todo, nil)
	n.take()
	got := f.snapshot(t)
	reads := n.take()
	t.Logf("unwired: read after a store write: %v, TODO %v", reads, got.TaskCounts[ready])
	if reads["GetBoard"] != 1 || got.TaskCounts[ready] != 1 {
		t.Fatalf("an unwired cache served a snapshot: reads %v, TODO %v", reads, got.TaskCounts[ready])
	}
}

// A composition that began before a drop does not store what it read.
func TestSnapshotCacheDropBeatsAFillInFlight(t *testing.T) {
	s := newSnapshotCache()
	s.observe(0, "", "")
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
	if _, err := f.st.UpdateColumn(f.ctx, f.project.ID, f.board.ID, f.board.Columns[0].ID, store.ColumnPatch{SetWIPLimit: true, WIPLimit: ptr(int64(3))}); err != nil {
		t.Fatal(err)
	}
	f.wire()
	for i := range 2 { // the miss's answer, then a hit's
		got := f.snapshot(t)
		got.TaskCounts["x"] = 9
		got.Board.Columns[0].Name = "changed"
		*got.Board.Columns[0].WIPLimit = 999
		got.Board.Transitions["TODO"][0] = domain.Done
		got.Board.Transitions["DONE"] = nil
		again := f.snapshot(t)
		if _, ok := again.TaskCounts["x"]; ok || again.Board.Columns[0].Name == "changed" || *again.Board.Columns[0].WIPLimit != 3 || !reflect.DeepEqual(again.Board.Transitions, fixedTable) {
			t.Fatalf("read %d: the cached snapshot was changed through a returned copy: wipLimit %v, transitions %v", i, *again.Board.Columns[0].WIPLimit, again.Board.Transitions)
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
	// Newest first, the store's order (P-99): a later event leads, and the cursor pages older.
	// Equal timestamps order by ts DESC, id DESC over random ids, not by insertion, so this event is a minute later.
	later, err := f.st.AppendBoardEvent(f.ctx, store.EventInput{ProjectID: f.project.ID, BoardID: got.Board.ID, Actor: "alice", Type: domain.EventTaskDeleted, Ts: f.clock.now().UnixMilli() + 60_000})
	if err != nil {
		t.Fatal(err)
	}
	both, err := f.c.Feed().BoardFeed(f.ctx, f.alice, BoardFeedReq{ProjectID: f.project.ID, BoardID: got.Board.ID})
	if err != nil || len(both.Events) != 2 || both.Events[0].ID != later.ID || both.Events[1].ID != e.ID {
		t.Fatalf("one page %+v (err %v), want %s then %s", both.Events, err, later.ID, e.ID)
	}
	first, err := f.c.Feed().BoardFeed(f.ctx, f.alice, BoardFeedReq{ProjectID: f.project.ID, BoardID: got.Board.ID, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	next, err := f.c.Feed().BoardFeed(f.ctx, f.alice, BoardFeedReq{ProjectID: f.project.ID, BoardID: got.Board.ID, Limit: 1, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 1 || first.Events[0].ID != later.ID || first.NextCursor == "" || len(next.Events) != 1 || next.Events[0].ID != e.ID || next.NextCursor != "" {
		t.Fatalf("pages %+v then %+v, want %s then %s", first, next, later.ID, e.ID)
	}
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

// fillAll gives every pointer, slice, map and scalar reachable from v a non-zero value, so a walk
// over it is never vacuous: a field added to the cached type later is filled with no edit here.
func fillAll(t *testing.T, path string, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fillAll(t, path+".*", v.Elem())
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 2, 2))
		for i := range 2 {
			fillAll(t, fmt.Sprintf("%s[%d]", path, i), v.Index(i))
		}
	case reflect.Map:
		v.Set(reflect.MakeMap(v.Type()))
		k, e := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
		fillAll(t, path+"[key]", k)
		fillAll(t, path+"[]", e)
		v.SetMapIndex(k, e)
	case reflect.Struct:
		for i := range v.NumField() {
			if !v.Type().Field(i).IsExported() {
				t.Fatalf("%s.%s is unexported: fillAll cannot populate it", path, v.Type().Field(i).Name)
			}
			fillAll(t, path+"."+v.Type().Field(i).Name, v.Field(i))
		}
	case reflect.String:
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1)
	default:
		t.Fatalf("%s is a %s: fillAll cannot populate it, so the alias walk cannot judge it", path, v.Kind())
	}
}

// aliasWalk reports every pointer, slice or map backing store a and b share at any depth, and every
// reference a holds empty where b does not (a copy that dropped it).
func aliasWalk(path string, a, b reflect.Value, shared, dropped *[]string) {
	switch a.Kind() {
	case reflect.Pointer:
		if a.IsNil() != b.IsNil() {
			*dropped = append(*dropped, path)
			return
		}
		if a.IsNil() {
			return
		}
		if a.Pointer() == b.Pointer() {
			*shared = append(*shared, path)
		}
		aliasWalk(path+".*", a.Elem(), b.Elem(), shared, dropped)
	case reflect.Slice, reflect.Map:
		if a.Len() != b.Len() {
			*dropped = append(*dropped, path)
			return
		}
		if a.Len() > 0 && a.Pointer() == b.Pointer() {
			*shared = append(*shared, path)
		}
		if a.Kind() == reflect.Slice {
			for i := range a.Len() {
				aliasWalk(fmt.Sprintf("%s[%d]", path, i), a.Index(i), b.Index(i), shared, dropped)
			}
			return
		}
		for _, k := range a.MapKeys() {
			if bv := b.MapIndex(k); bv.IsValid() {
				aliasWalk(fmt.Sprintf("%s[%v]", path, k), a.MapIndex(k), bv, shared, dropped)
			} else {
				*dropped = append(*dropped, fmt.Sprintf("%s[%v]", path, k))
			}
		}
	case reflect.Struct:
		for i := range a.NumField() {
			aliasWalk(path+"."+a.Type().Field(i).Name, a.Field(i), b.Field(i), shared, dropped)
		}
	}
}

// cloneSnapshot shares no pointer, slice or map with what it copied, at any depth of the cached
// type: a reference field added to it later is filled by fillAll and judged with no edit here.
func TestCloneSnapshotSharesNothing(t *testing.T) {
	var orig BoardSnapshotResp
	fillAll(t, "snap", reflect.ValueOf(&orig).Elem())
	clone := cloneSnapshot(orig)
	if !reflect.DeepEqual(clone, orig) {
		t.Fatalf("the copy differs from what it copied:\n got %+v\nwant %+v", clone, orig)
	}
	var shared, dropped []string
	aliasWalk("snap", reflect.ValueOf(clone), reflect.ValueOf(orig), &shared, &dropped)
	if len(shared) > 0 || len(dropped) > 0 {
		t.Fatalf("cloneSnapshot shares %v and drops %v", shared, dropped)
	}
}
