package cli

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// boardCounting is the SQLite engine counting its GetBoard calls (two statements each: the board
// and one grouped count), the one read the board snapshot cache saves. It keeps *store.SQLite's
// methods, so compose finds its change watcher.
type boardCounting struct {
	*store.SQLite
	boards atomic.Int64
}

func (s *boardCounting) GetBoard(ctx context.Context, projectID, boardID string) (store.Board, store.TaskCounts, error) {
	s.boards.Add(1)
	return s.SQLite.GetBoard(ctx, projectID, boardID)
}

// AC1, AC2 and AC4 (P-100): served over SQLite, a second get_task_context on a board with no
// intervening change reads no board (the snapshot cache, fed by serve's bus tap), and a change row
// naming the board, delivered by the bus alone, makes the next call read it again.
func TestServeFeedsTheBoardSnapshotCacheFromTheBus(t *testing.T) {
	path := migratedDB(t)
	inner, err := store.OpenSQLite(path, "FULL")
	if err != nil {
		t.Fatal(err)
	}
	defer inner.Close()
	st := &boardCounting{SQLite: inner}
	ctx := context.Background()
	project, err := st.CreateProject(ctx, store.CreateProjectInput{Name: "P", CreatedBy: "alice"}, store.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	board, err := st.CreateBoard(ctx, store.CreateBoardInput{ProjectID: project.ID, Name: "B", CreatedBy: "alice"}, store.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var todo string
	for _, c := range board.Columns {
		if c.Status == domain.Todo {
			todo = c.ID
		}
	}
	task, err := st.CreateTask(ctx, store.CreateTaskInput{ProjectID: project.ID, BoardID: board.ID, ColumnID: todo, Title: "t",
		Status: domain.Todo, OrderKey: "a0", CreatedBy: "alice", CreatedAt: time.Now().UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}

	a := mustCompose(t, st, "", log.New(io.Discard, "", 0))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- serveUntil(serveCtx, []net.Listener{ln}, a, "x", log.New(io.Discard, "", 0)) }()
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("serveUntil: %v", err)
		}
	}()

	alice := domain.Caller{Identity: "alice", Agent: "claude", TokenScope: "alice"}
	reads := func() int64 {
		t.Helper()
		before := st.boards.Load()
		if _, err := a.core.Context().GetTaskContext(ctx, alice, service.GetTaskContextReq{ProjectID: project.ID, TaskID: task.ID}); err != nil {
			t.Fatal(err)
		}
		return st.boards.Load() - before
	}
	// eventually is pairs of calls until one's second reads no board, within 5 s. Before the tap is
	// subscribed a pair is [1 1]; it logs every pair, so the cold call is the one that shows 1.
	eventually := func(what string) {
		t.Helper()
		var pairs [][]int64
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			last := []int64{reads(), reads()}
			pairs = append(pairs, last)
			if last[1] == 0 {
				t.Logf("%s: GetBoard per call, pair by pair until warm: %v", what, pairs)
				return
			}
		}
		t.Fatalf("%s: the second get_task_context still read the board after 5 s: GetBoard per call %v; the snapshot cache is not fed by the bus", what, pairs[len(pairs)-1])
	}
	eventually("served")

	// Another writer's change row, which only the bus brings to this process.
	payload, _ := json.Marshal(map[string]string{"kind": service.ChangeKindChanged, "projectId": project.ID, "boardId": board.ID})
	id, err := st.AppendChange(ctx, store.ChangeInput{Scope: service.BroadcastScope(project.ID), ProjectID: project.ID, Kind: service.ChangeKindChanged, Payload: payload, Ts: time.Now().UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); a.bus.Frontier() < id; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the bus never delivered change #%d (frontier %d)", id, a.bus.Frontier())
		}
	}
	cold := int64(0)
	for deadline := time.Now().Add(5 * time.Second); cold == 0 && time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		cold = reads()
	}
	if cold != 1 {
		t.Fatalf("after change #%d naming the board, get_task_context read the board %d times, want 1 (cold)", id, cold)
	}
	if warm := reads(); warm != 0 {
		t.Fatalf("after change #%d the recomposed snapshot was not cached: GetBoard %d", id, warm)
	}
	t.Logf("change #%d: next call cold (GetBoard %d), the one after warm (0)", id, cold)
}

// tapFix is a composed app over a counted SQLite store holding project A with boards X and Y and one
// task on X; its tests drive their own tap and feedSnapshots, without serveUntil.
type tapFix struct {
	st       *boardCounting
	a        *app
	projectA string
	boardX   string
	boardY   string
	taskX    string
}

func newTapFix(t *testing.T) *tapFix {
	t.Helper()
	inner, err := store.OpenSQLite(migratedDB(t), "FULL")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { inner.Close() })
	st := &boardCounting{SQLite: inner}
	ctx := context.Background()
	p, err := st.CreateProject(ctx, store.CreateProjectInput{Name: "P", CreatedBy: "alice"}, store.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	bx, err := st.CreateBoard(ctx, store.CreateBoardInput{ProjectID: p.ID, Name: "X", CreatedBy: "alice"}, store.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	by, err := st.CreateBoard(ctx, store.CreateBoardInput{ProjectID: p.ID, Name: "Y", CreatedBy: "alice"}, store.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var todo string
	for _, c := range bx.Columns {
		if c.Status == domain.Todo {
			todo = c.ID
		}
	}
	task, err := st.CreateTask(ctx, store.CreateTaskInput{ProjectID: p.ID, BoardID: bx.ID, ColumnID: todo, Title: "t",
		Status: domain.Todo, OrderKey: "a0", CreatedBy: "alice", CreatedAt: time.Now().UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	a := mustCompose(t, st, "", log.New(io.Discard, "", 0))
	return &tapFix{st: st, a: a, projectA: p.ID, boardX: bx.ID, boardY: by.ID, taskX: task.ID}
}

func (f *tapFix) reads(t *testing.T) int64 {
	t.Helper()
	before := f.st.boards.Load()
	alice := domain.Caller{Identity: "alice", Agent: "claude", TokenScope: "alice"}
	if _, err := f.a.core.Context().GetTaskContext(context.Background(), alice, service.GetTaskContextReq{ProjectID: f.projectA, TaskID: f.taskX}); err != nil {
		t.Fatal(err)
	}
	return f.st.boards.Load() - before
}

func (f *tapFix) appendChange(t *testing.T, boardID string) int64 {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"kind": service.ChangeKindChanged, "projectId": f.projectA, "boardId": boardID})
	id, err := f.st.AppendChange(context.Background(), store.ChangeInput{Scope: service.BroadcastScope(f.projectA), ProjectID: f.projectA,
		Kind: service.ChangeKindChanged, Payload: payload, Ts: time.Now().UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// A change the tap dropped (buffer 1: Y fits, X's two rows do not) must clear X's snapshot.
func TestTapDropClearsEverySnapshot(t *testing.T) {
	f := newTapFix(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tap, err := f.a.bus.Tap(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer f.a.bus.Close()
	f.a.core.ResetSnapshots(true)
	f.reads(t)
	if warm := f.reads(t); warm != 0 {
		t.Fatalf("not warm before the drop: %d", warm)
	}
	f.appendChange(t, f.boardY)
	f.appendChange(t, f.boardX)
	last := f.appendChange(t, f.boardX)
	for deadline := time.Now().Add(5 * time.Second); tap.Dropped() < 2 || f.a.bus.Frontier() < last; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("tap dropped %d, frontier %d, want 2 and %d", tap.Dropped(), f.a.bus.Frontier(), last)
		}
	}
	if warm := f.reads(t); warm != 0 {
		t.Fatalf("X went cold before the feed ran: %d", warm)
	}
	go feedSnapshots(tap, f.a.core, log.New(io.Discard, "", 0))
	var cold int64
	for deadline := time.Now().Add(3 * time.Second); cold == 0 && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		cold = f.reads(t)
	}
	if cold != 1 {
		t.Fatalf("after the tap dropped X's rows, get_task_context on X read the board %d times, want 1: a stale snapshot is served", cold)
	}
}

// A closed tap turns the cache off: two reads in a row both read the board.
func TestTapCloseTurnsTheCacheOff(t *testing.T) {
	f := newTapFix(t)
	tap, err := f.a.bus.Tap(context.Background(), 16)
	if err != nil {
		t.Fatal(err)
	}
	f.a.core.ResetSnapshots(true)
	done := make(chan struct{})
	go func() { feedSnapshots(tap, f.a.core, log.New(io.Discard, "", 0)); close(done) }()
	f.reads(t)
	if warm := f.reads(t); warm != 0 {
		t.Fatalf("not warm while the tap is open: %d", warm)
	}
	f.a.bus.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("feedSnapshots did not return after the bus closed")
	}
	if got := []int64{f.reads(t), f.reads(t)}; got[0] != 1 || got[1] != 1 {
		t.Fatalf("after the tap closed: GetBoard per call %v, want [1 1] (cache off)", got)
	}
}
