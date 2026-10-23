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
	// eventually is a pair of calls whose second reads no board, within 5 s.
	eventually := func(what string) {
		t.Helper()
		var last []int64
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			last = []int64{reads(), reads()}
			if last[1] == 0 {
				t.Logf("%s: GetBoard per call %v (cold, warm)", what, last)
				return
			}
		}
		t.Fatalf("%s: the second get_task_context still read the board after 5 s: GetBoard per call %v; the snapshot cache is not fed by the bus", what, last)
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
