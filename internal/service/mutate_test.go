package service

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// deleteTask is a delete_task through mutate as R1.3 will write it: the audited mutation.
func deleteTask(f *fixture, task store.Task, version int64) error {
	_, err := mutate(f.ctx, f.c, f.alice, mutation{
		Tool: "delete_task", Args: map[string]any{"projectId": f.project.ID, "taskId": task.ID},
		ProjectID: f.project.ID, TaskID: task.ID, Version: version,
	}, func(t *txn) (struct{}, error) {
		if err := t.Tx.DeleteTask(f.ctx, t.Task.ProjectID, t.Task.ID, t.Task.Version); err != nil {
			return struct{}{}, err
		}
		t.Deleted()
		t.BoardEvent(store.EventInput{Type: domain.EventTaskDeleted, Data: json.RawMessage(`{"taskId":"` + t.Task.ID + `"}`)})
		return struct{}{}, nil
	})
	return err
}

// retitle is an update_task through mutate: a versioned write, an unaudited tool.
func retitle(f *fixture, task store.Task, version int64, title string) (store.Task, error) {
	return mutate(f.ctx, f.c, f.alice, mutation{
		Tool: "update_task", Args: map[string]any{"projectId": f.project.ID, "taskId": task.ID},
		ProjectID: f.project.ID, TaskID: task.ID, Version: version,
	}, func(t *txn) (store.Task, error) {
		got, err := t.Tx.UpdateTask(f.ctx, t.Task.ProjectID, t.Task.ID, t.Task.Version, store.TaskPatch{Title: &title, UpdatedBy: t.Caller.Identity, UpdatedAt: t.Now.UnixMilli()})
		if err != nil {
			return store.Task{}, err
		}
		t.Wrote(got)
		t.Event(store.EventInput{Type: domain.EventTaskUpdated})
		return got, nil
	})
}

// AC1: one mutation writes exactly one task_events, one audit_log and one change_log row.
func TestMutateWritesOneEventOneAuditOneChange(t *testing.T) {
	f := newFixture(t, Options{})
	task := f.task(t, domain.Backlog, nil)
	before := f.count(t)
	if err := deleteTask(f, task, task.Version); err != nil {
		t.Fatal(err)
	}
	after := f.count(t)
	t.Logf("delete_task: events %d→%d, audit_log %d→%d, change_log %d→%d", before.events, after.events, before.audit, after.audit, before.changes, after.changes)
	if after.events-before.events != 1 || after.audit-before.audit != 1 || after.changes-before.changes != 1 {
		t.Fatalf("delete_task wrote %+v, want one of each", rows{after.events - before.events, after.audit - before.audit, after.changes - before.changes})
	}
	page, _ := f.st.ListAuditLog(f.ctx, store.AuditFilter{}, "", 0)
	if r := page.Rows[0]; r.Action != "task.deleted" || r.Subject != task.ID || r.Actor != "alice" || r.Agent != "claude" || r.ProjectID != f.project.ID || r.Result != store.AuditOK {
		t.Fatalf("audit row %+v", r)
	}
	if ch := f.changes(t); ch[0].Kind != ChangeKindChanged || ch[0].Scope != BroadcastScope(f.project.ID) {
		t.Fatalf("change row %+v, want a %q row in the project's scope", ch[0], ChangeKindChanged)
	}
}

// The positive control of AC1: an unaudited tool writes one event, NO audit row, one change row,
// and bumps the version exactly once.
func TestMutateUnauditedWritesNoAuditAndBumpsOnce(t *testing.T) {
	f := newFixture(t, Options{})
	task := f.task(t, domain.Backlog, nil)
	before := f.count(t)
	got, err := retitle(f, task, task.Version, "renamed")
	if err != nil {
		t.Fatal(err)
	}
	after := f.count(t)
	t.Logf("update_task: events %d→%d, audit_log %d→%d, change_log %d→%d, version %d→%d", before.events, after.events, before.audit, after.audit, before.changes, after.changes, task.Version, got.Version)
	if after.events-before.events != 1 || after.audit != before.audit || after.changes-before.changes != 1 || got.Version != task.Version+1 {
		t.Fatalf("update_task wrote %+v and version %d", after, got.Version)
	}
}

// mutate bumps the version itself when fn wrote nothing versioned (a context item, a heartbeat).
func TestMutateBumpsWhenFnDoesNot(t *testing.T) {
	f := newFixture(t, Options{})
	task := f.task(t, domain.Backlog, nil)
	_, err := mutate(f.ctx, f.c, f.alice, mutation{Tool: "add_context_item", ProjectID: f.project.ID, TaskID: task.ID}, func(t *txn) (int, error) {
		t.Event(store.EventInput{Type: domain.EventContextAdded})
		return 0, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := f.st.GetTask(f.ctx, f.project.ID, task.ID)
	if got.Version != task.Version+1 || got.UpdatedBy != "alice" {
		t.Fatalf("version %d (updated by %s), want %d by alice", got.Version, got.UpdatedBy, task.Version+1)
	}
}

// A second versioned write in one mutation is a bug mutate refuses, rolled back.
func TestMutateRefusesTwoBumps(t *testing.T) {
	f := newFixture(t, Options{})
	task := f.task(t, domain.Backlog, nil)
	before := f.count(t)
	_, err := mutate(f.ctx, f.c, f.alice, mutation{Tool: "update_task", ProjectID: f.project.ID, TaskID: task.ID}, func(t *txn) (int, error) {
		if _, err := t.Bump(); err != nil {
			return 0, err
		}
		_, err := t.Bump()
		return 0, err
	})
	wantCode(t, err, domain.CodeInternal)
	got, _ := f.st.GetTask(f.ctx, f.project.ID, task.ID)
	if got.Version != task.Version || f.count(t) != before {
		t.Fatalf("a refused mutation left version %d and %+v", got.Version, f.count(t))
	}
}

// AC2: a stale expectedVersion is STALE with expected and actual, and writes nothing.
func TestMutateStaleWritesNothing(t *testing.T) {
	f := newFixture(t, Options{})
	task := f.task(t, domain.Backlog, nil)
	before := f.count(t)
	ran := false
	_, err := mutate(f.ctx, f.c, f.alice, mutation{Tool: "update_task", ProjectID: f.project.ID, TaskID: task.ID, Version: task.Version + 7}, func(t *txn) (int, error) {
		ran = true
		return 0, nil
	})
	e := wantCode(t, err, domain.CodeStale)
	after := f.count(t)
	got, _ := f.st.GetTask(f.ctx, f.project.ID, task.ID)
	t.Logf("STALE details %v; rows before %+v after %+v; version %d", e.Details, before, after, got.Version)
	if e.Details["expected"] != task.Version+7 || e.Details["actual"] != task.Version || !e.Retryable {
		t.Fatalf("STALE details %v, want expected %d actual %d", e.Details, task.Version+7, task.Version)
	}
	if ran || after != before || got.Version != task.Version {
		t.Fatalf("a stale call ran fn (%v), wrote %+v→%+v, version %d", ran, before, after, got.Version)
	}
}

// createTask is a create_task through mutate under an idempotency key.
func createTask(f *fixture, key, title string) (store.Task, error) {
	return mutate(f.ctx, f.c, f.alice, mutation{Tool: "create_task", ProjectID: f.project.ID, IdempotencyKey: key}, func(t *txn) (store.Task, error) {
		col := ""
		for _, c := range f.board.Columns {
			if c.Status == domain.Ready {
				col = c.ID
			}
		}
		task, err := t.Tx.CreateTask(f.ctx, store.CreateTaskInput{
			ProjectID: f.project.ID, BoardID: f.board.ID, ColumnID: col, Title: title, Status: domain.Ready,
			OrderKey: "a0", CreatedBy: t.Caller.Identity, CreatedAt: t.Now.UnixMilli(),
		})
		if err != nil {
			return store.Task{}, err
		}
		t.Event(store.EventInput{TaskID: task.ID, Type: domain.EventTaskCreated})
		t.RingFor(nil, task, false)
		return task, nil
	})
}

func (f *fixture) taskCount(t *testing.T) int {
	t.Helper()
	page, err := f.st.ListTasks(f.ctx, f.project.ID, store.TaskFilter{BoardID: f.board.ID}, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	return len(page.Tasks)
}

// AC3: an idempotent create replayed within the TTL returns the first result and leaves one row.
func TestMutateIdempotentCreateReplays(t *testing.T) {
	f := newFixture(t, Options{IdempotencyTTL: time.Hour})
	first, err := createTask(f, "k-1", "first")
	if err != nil {
		t.Fatal(err)
	}
	before := f.count(t)
	again, err := createTask(f, "k-1", "second")
	if err != nil {
		t.Fatal(err)
	}
	after := f.count(t)
	t.Logf("first %s %q, replay %s %q; tasks %d; rows %+v→%+v", first.ID, first.Title, again.ID, again.Title, f.taskCount(t), before, after)
	if again.ID != first.ID || again.Title != "first" || f.taskCount(t) != 1 || after != before {
		t.Fatalf("the replay created %s %q, %d tasks, rows %+v→%+v", again.ID, again.Title, f.taskCount(t), before, after)
	}
	// Positive control: another key creates.
	other, err := createTask(f, "k-2", "other")
	if err != nil || other.ID == first.ID || f.taskCount(t) != 2 {
		t.Fatalf("another key: %v, %s, %d tasks", err, other.ID, f.taskCount(t))
	}
}

// Outside the TTL the key counts as never seen: the call runs again.
func TestMutateIdempotencyExpires(t *testing.T) {
	f := newFixture(t, Options{IdempotencyTTL: 30 * time.Millisecond})
	first, err := createTask(f, "k", "first")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	again, err := createTask(f, "k", "again")
	if err != nil || again.ID == first.ID || f.taskCount(t) != 2 {
		t.Fatalf("past the TTL: %v, %s after %s, %d tasks", err, again.ID, first.ID, f.taskCount(t))
	}
}

// A create that lands READY with no assignee rings broadcast, in the project's scope.
func TestMutateCreateRingsBroadcast(t *testing.T) {
	f := newFixture(t, Options{})
	task, err := createTask(f, "", "t")
	if err != nil {
		t.Fatal(err)
	}
	ch := f.changes(t)
	var p changePayload
	if len(ch) != 1 || json.Unmarshal(ch[0].Payload, &p) != nil {
		t.Fatalf("changes %+v", ch)
	}
	if ch[0].Kind != DoorbellBroadcast || ch[0].Scope != BroadcastScope(f.project.ID) || p.TaskID != task.ID || p.BoardID != f.board.ID || p.ProjectID != f.project.ID {
		t.Fatalf("change %+v payload %+v, want broadcast for %s", ch[0], p, task.ID)
	}
}

// Assigning a task rings assigned in the ASSIGNEE's scope, not the caller's.
func TestMutateAssignRingsTheAssignee(t *testing.T) {
	f := newFixture(t, Options{})
	task := f.task(t, domain.Ready, nil)
	_, err := mutate(f.ctx, f.c, f.alice, mutation{Tool: "update_task", ProjectID: f.project.ID, TaskID: task.ID, Version: task.Version}, func(t *txn) (store.Task, error) {
		got, err := t.Tx.UpdateTask(f.ctx, f.project.ID, task.ID, task.Version, store.TaskPatch{Assignee: &store.Nullable[string]{Value: ptr("bob")}, UpdatedBy: "alice", UpdatedAt: t.Now.UnixMilli()})
		if err != nil {
			return got, err
		}
		t.Wrote(got)
		t.Event(store.EventInput{Type: domain.EventTaskUpdated})
		t.RingFor(t.Task, got, false)
		return got, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ch := f.changes(t)
	if len(ch) != 1 || ch[0].Kind != DoorbellAssigned || ch[0].Scope != "bob" {
		t.Fatalf("changes %+v, want one assigned row scoped to bob", ch)
	}
}

// The doorbell rule, case by case (F4 CONTEXT, amended 2026-10-23).
func TestDoorbellsFor(t *testing.T) {
	task := func(s domain.Status, assignee *string) store.Task {
		return store.Task{ID: "T-1", BoardID: "B-1", Status: s, Assignee: assignee}
	}
	kinds := func(bs []Doorbell) []string {
		out := []string{}
		for _, b := range bs {
			out = append(out, b.Kind+":"+b.Agent)
		}
		return out
	}
	readyBob, readyNone := task(domain.Ready, ptr("bob")), task(domain.Ready, nil)
	blockedBob, blockedNone := task(domain.Blocked, ptr("bob")), task(domain.Blocked, nil)
	for _, c := range []struct {
		name      string
		before    *store.Task
		after     store.Task
		unblocked bool
		want      []string
	}{
		{"create READY unassigned", nil, readyNone, false, []string{"broadcast:"}},
		{"create BACKLOG", nil, task(domain.Backlog, nil), false, []string{}},
		{"gains an assignee", &readyNone, readyBob, false, []string{"assigned:bob"}},
		{"assignee changes", &readyBob, task(domain.Ready, ptr("carol")), false, []string{"assigned:carol"}},
		{"claim (IN_PROGRESS, gains caller)", &readyNone, task(domain.InProgress, ptr("bob")), false, []string{"assigned:bob"}},
		{"release to READY unassigned", ptr(task(domain.InProgress, ptr("bob"))), readyNone, false, []string{"broadcast:"}},
		{"READY loses its assignee", &readyBob, readyNone, false, []string{"broadcast:"}},
		{"auto-unblock, assigned", &blockedBob, readyBob, true, []string{"assigned:bob"}},
		{"auto-unblock, unassigned", &blockedNone, readyNone, true, []string{"broadcast:"}},
		{"READY by a move, assigned: no ring", &blockedBob, readyBob, false, []string{}},
		{"no change", &readyBob, readyBob, true, []string{}},
		{"to BLOCKED", &readyNone, blockedNone, false, []string{}},
	} {
		if got := kinds(doorbellsFor(c.before, c.after, c.unblocked)); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// Every domain code passes through unchanged, and the failed mutation writes nothing.
func TestMutateEveryCodePassesThrough(t *testing.T) {
	f := newFixture(t, Options{})
	task := f.task(t, domain.Backlog, nil)
	before := f.count(t)
	for _, code := range domain.AllCodes() {
		_, err := mutate(f.ctx, f.c, f.alice, mutation{Tool: "update_task", ProjectID: f.project.ID, TaskID: task.ID}, func(t *txn) (int, error) {
			if _, err := t.Bump(); err != nil {
				return 0, err
			}
			t.Event(store.EventInput{Type: domain.EventTaskUpdated})
			return 0, domain.NewError(code, "from fn", map[string]any{"k": "v"})
		})
		e := wantCode(t, err, code)
		if e.Message != "from fn" || e.Details["k"] != "v" {
			t.Fatalf("%s came back as %+v", code, e)
		}
	}
	got, _ := f.st.GetTask(f.ctx, f.project.ID, task.ID)
	if after := f.count(t); after != before || got.Version != task.Version {
		t.Fatalf("failed mutations wrote %+v→%+v, version %d", before, after, got.Version)
	}
}

// A non-domain error is INTERNAL, logged whole; its text stays out of the client's message.
func TestMutateForeignErrorIsInternal(t *testing.T) {
	f := newFixture(t, Options{})
	task := f.task(t, domain.Backlog, nil)
	_, err := mutate(f.ctx, f.c, f.alice, mutation{Tool: "update_task", ProjectID: f.project.ID, TaskID: task.ID}, func(t *txn) (int, error) {
		return 0, errors.New("disk on fire")
	})
	e := wantCode(t, err, domain.CodeInternal)
	if strings.Contains(e.Message, "disk on fire") || !strings.Contains(f.logs.String(), "disk on fire") || !e.Retryable {
		t.Fatalf("INTERNAL %+v; log %q", e, f.logs.String())
	}
}

// A panic in fn is INTERNAL with the stack logged, and what fn wrote before it is rolled back.
func TestMutatePanicIsInternalAndRollsBack(t *testing.T) {
	f := newFixture(t, Options{})
	task := f.task(t, domain.Backlog, nil)
	before := f.count(t)
	_, err := mutate(f.ctx, f.c, f.alice, mutation{Tool: "update_task", ProjectID: f.project.ID, TaskID: task.ID}, func(t *txn) (int, error) {
		if _, err := t.Bump(); err != nil {
			return 0, err
		}
		panic("boom")
	})
	wantCode(t, err, domain.CodeInternal)
	logged := f.logs.String()
	t.Logf("logged: %.200s…", logged)
	if !strings.Contains(logged, "panic: boom") || !strings.Contains(logged, "goroutine") || !strings.Contains(logged, "mutate_test.go") {
		t.Fatalf("the panic's stack was not logged: %q", logged)
	}
	got, _ := f.st.GetTask(f.ctx, f.project.ID, task.ID)
	if after := f.count(t); after != before || got.Version != task.Version {
		t.Fatalf("a panicked mutation wrote %+v→%+v, version %d", before, after, got.Version)
	}
}

// A missing task is NOT_FOUND before fn runs.
func TestMutateMissingTaskIsNotFound(t *testing.T) {
	f := newFixture(t, Options{})
	_, err := mutate(f.ctx, f.c, f.alice, mutation{Tool: "update_task", ProjectID: f.project.ID, TaskID: "T-404", Version: 1}, func(t *txn) (int, error) {
		t.Event(store.EventInput{Type: domain.EventTaskUpdated})
		return 0, nil
	})
	wantCode(t, err, domain.CodeNotFound)
}
