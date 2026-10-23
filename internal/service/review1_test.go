package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// Review round 1, B1: one key used by two tools replays neither into the other.
func TestMutateIdempotencyKeyIsPerTool(t *testing.T) {
	f := newFixture(t, Options{IdempotencyTTL: time.Hour})
	task, err := createTask(f, "k", "t")
	if err != nil {
		t.Fatal(err)
	}
	board, err := mutate(f.ctx, f.c, f.alice, mutation{Tool: "create_board", ProjectID: f.project.ID, IdempotencyKey: "k"}, func(t *txn) (store.Board, error) {
		return t.Tx.CreateBoard(f.ctx, store.CreateBoardInput{ProjectID: f.project.ID, Name: "second", CreatedBy: "alice"}, store.WriteOptions{})
	})
	t.Logf("create_task k -> %s; create_board k -> id %q name %q (err %v)", task.ID, board.ID, board.Name, err)
	if err != nil || board.Name != "second" || board.ID == task.ID {
		t.Fatalf("create_board under create_task's key came back as %+v (err %v)", board, err)
	}
}

// Review round 1, B2: a supplied version 0 is checked, and STALE (versions start at 1).
func TestMutateVersionZeroIsStale(t *testing.T) {
	f := newFixture(t, Options{})
	task := f.task(t, domain.Done, nil)
	task, err := retitle(f, task, task.Version, "v2")
	if err != nil {
		t.Fatal(err)
	}
	before := f.count(t)
	_, err = retitle(f, task, 0, "zero")
	e := wantCode(t, err, domain.CodeStale)
	t.Logf("update_task version 0 on a v%d task: %v; rows %+v→%+v", task.Version, e, before, f.count(t))
	if e.Details["expected"] != int64(0) || e.Details["actual"] != task.Version || f.count(t) != before {
		t.Fatalf("details %v; rows %+v→%+v", e.Details, before, f.count(t))
	}
}

func (f *fixture) assign(t *testing.T, task store.Task, to string) store.Change {
	t.Helper()
	_, err := mutate(f.ctx, f.c, f.alice, mutation{Tool: "handoff_task", ProjectID: f.project.ID, TaskID: task.ID}, func(t *txn) (store.Task, error) {
		got, err := t.Tx.UpdateTask(f.ctx, f.project.ID, task.ID, t.Task.Version, store.TaskPatch{Assignee: &store.Nullable[string]{Value: ptr(to)}, UpdatedBy: "alice", UpdatedAt: t.Now.UnixMilli()})
		if err != nil {
			return got, err
		}
		t.Wrote(got)
		t.RingFor(t.Task, got, false)
		return got, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ch := f.changes(t)
	return ch[len(ch)-1]
}

// Review round 1, B3: an assigned doorbell is scoped to the identity when the assignee is a
// member, and to AgentScope(label) when it is a free-text agent label.
func TestMutateAssignedScopeResolvesTheAssignee(t *testing.T) {
	f := newFixture(t, Options{})
	f.member(t, "bob", domain.RoleWorker)
	member := f.assign(t, f.task(t, domain.Todo, nil), "bob")
	label := f.assign(t, f.task(t, domain.Todo, nil), "builder-R0-21")
	t.Logf("member assignee -> scope %q; label assignee -> scope %q", member.Scope, label.Scope)
	if member.Kind != DoorbellAssigned || member.Scope != "bob" {
		t.Fatalf("member assignee row %+v, want scope bob", member)
	}
	if label.Kind != DoorbellAssigned || label.Scope != AgentScope("builder-R0-21") || label.Scope != "agent:builder-R0-21" {
		t.Fatalf("label assignee row %+v, want scope agent:builder-R0-21", label)
	}
}

// Review round 1, B4: a create that rings nobody still names its task and board.
func TestMutateChangedRowNamesTheCreatedTask(t *testing.T) {
	f := newFixture(t, Options{})
	done := f.column(t, domain.Done)
	task, err := mutate(f.ctx, f.c, f.alice, mutation{Tool: "create_task", ProjectID: f.project.ID}, func(t *txn) (store.Task, error) {
		task, err := t.Tx.CreateTask(f.ctx, store.CreateTaskInput{
			ProjectID: f.project.ID, BoardID: f.board.ID, ColumnID: done, Title: "b", Status: domain.Done,
			OrderKey: "a0", CreatedBy: "alice", CreatedAt: t.Now.UnixMilli(),
		})
		if err != nil {
			return task, err
		}
		t.Wrote(task)
		t.Event(store.EventInput{TaskID: task.ID, Type: domain.EventTaskCreated})
		t.RingFor(nil, task, false)
		return task, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ch := f.changes(t)
	var p changePayload
	if len(ch) != 1 || json.Unmarshal(ch[0].Payload, &p) != nil {
		t.Fatalf("changes %+v", ch)
	}
	t.Logf("DONE create change row: kind %s payload %s", ch[0].Kind, ch[0].Payload)
	if ch[0].Kind != ChangeKindChanged || p.TaskID != task.ID || p.BoardID != f.board.ID {
		t.Fatalf("changed row %s, want taskId %s and boardId %s", ch[0].Payload, task.ID, f.board.ID)
	}
}

// stop with a context already done still flushes what is buffered.
func TestToolCallsCloseWithDoneContextStillFlushes(t *testing.T) {
	f := newFixture(t, Options{ToolCallFlush: time.Hour})
	_, _ = f.c.ToolCalls().Record(f.ctx, f.alice, okCall)
	done, cancel := contextWithCancel(f)
	cancel()
	_ = f.c.Close(done)
	if n := f.toolCallRows(t); n != 1 {
		t.Fatalf("Close with a done context left the row unflushed (%d landed); log %q", n, f.logs.String())
	}
}
