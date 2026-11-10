package service

import (
	"context"
	"encoding/json"
	"log"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func (e *env) tkDelete(c domain.Caller, task store.Task) error {
	_, err := e.c.Tasks().Delete(e.ctx, c, DeleteTaskReq{ProjectID: e.project, TaskID: task.ID, Version: task.Version})
	return err
}

func (e *env) tkGone(t *testing.T, id string) {
	t.Helper()
	if _, err := e.st.GetTask(e.ctx, e.project, id); tkCode(err) != domain.CodeNotFound {
		t.Fatalf("%s is still there: %v", id, err)
	}
}

func (e *env) tkDepend(t *testing.T, from, to string) {
	t.Helper()
	if _, err := e.c.Context().AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: from, ToTaskID: to}); err != nil {
		t.Fatal(err)
	}
}

func (e *env) feed(t *testing.T) []store.Event {
	t.Helper()
	page, err := e.st.ListBoardFeed(e.ctx, e.project, e.board.ID, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	return page.Events
}

// AC5, the R0.10 handoff: deleting a blocker strips its id from every dependent's blockedBy BEFORE
// the store drops the edges. The non-poison dependent goes TODO with one task.dependency_cleared
// (autoReady); the poisoned one stays BLOCKED with its reason (the refusal); neither cache names the
// deleted task.
func TestDeleteBlockerUnblocksPoisonAware(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		blocker := e.tkSeed(t, domain.Todo, "", 0, nil)
		plain, poison := e.tkSeed(t, domain.Blocked, "", 0, nil), e.tkSeed(t, domain.Blocked, domain.BlockedReasonPoison, 3, []string{domain.LabelPoisoned})
		e.tkDepend(t, blocker.ID, plain.ID)
		e.tkDepend(t, blocker.ID, poison.ID)
		if err := e.tkDelete(e.alice, e.get(t, blocker.ID)); err != nil {
			t.Fatal(err)
		}
		got := e.get(t, plain.ID)
		cleared := ofType(e.events(t, plain.ID), domain.EventTaskDependencyCleared)
		if got.Status != domain.Todo || len(got.BlockedBy) != 0 || len(cleared) != 1 || !json.Valid(cleared[0].Data) || !slices.Contains([]string{`{"autoReady":true,"clearedBy":"` + blocker.ID + `"}`}, compact(t, cleared[0].Data)) {
			t.Fatalf("plain dependent %s blockedBy %v events %d, want TODO, [] and one dependency_cleared autoReady", got.Status, got.BlockedBy, len(cleared))
		}
		p := e.get(t, poison.ID)
		if p.Status != domain.Blocked || reasonOf(p) != domain.BlockedReasonPoison || slices.Contains(p.BlockedBy, blocker.ID) {
			t.Fatalf("poisoned dependent %s %q blockedBy %v, want BLOCKED poison not naming %s", p.Status, reasonOf(p), p.BlockedBy, blocker.ID)
		}
		t.Logf("plain %s → %s (%d task.dependency_cleared); poisoned %s stays %s %q, blockedBy %v", plain.ID, got.Status, len(cleared), poison.ID, p.Status, reasonOf(p), p.BlockedBy)
	})
}

func compact(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Deleting a dependent strips it from its blocker's blocks and bumps the blocker once; deleting a
// subtask its parent waits on (a blockedBy entry with no edge) unblocks the parent.
func TestDeleteStripsBlockersAndUnblocksTheParent(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		blocker, dep := e.tkSeed(t, domain.Todo, "", 0, nil), e.tkSeed(t, domain.Done, "", 0, nil)
		e.tkDepend(t, blocker.ID, dep.ID)
		b := e.get(t, blocker.ID)
		if err := e.tkDelete(e.alice, e.get(t, dep.ID)); err != nil {
			t.Fatal(err)
		}
		if after := e.get(t, blocker.ID); len(after.Blocks) != 0 || after.Version != b.Version+1 {
			t.Fatalf("blocker blocks %v v%d, want [] at v%d", after.Blocks, after.Version, b.Version+1)
		}
		parent := e.tkSeed(t, domain.Blocked, "", 0, nil)
		child, err := e.c.Context().AddSubtask(e.ctx, e.alice, AddSubtaskReq{ProjectID: e.project, ParentTaskID: parent.ID, Title: "c"})
		if err != nil {
			t.Fatal(err)
		}
		if err := e.st.SetCaches(e.ctx, e.project, parent.ID, []string{child.Task.ID}, nil); err != nil {
			t.Fatal(err)
		}
		if err := e.tkDelete(e.alice, e.get(t, child.Task.ID)); err != nil {
			t.Fatal(err)
		}
		if p := e.get(t, parent.ID); p.Status != domain.Todo || len(p.BlockedBy) != 0 {
			t.Fatalf("parent %s blockedBy %v after its last child went, want TODO []", p.Status, p.BlockedBy)
		}
	})
}

// AC6: a run X of a racing parent P, X itself racing with two runs and holding two subtasks, one
// with a context item and task memory. Deleting X leaves no row for X, its subtasks or its runs in
// tasks, events, context items, memory, dependencies or runs; P (its last run gone) is TODO with
// racing false.
func TestDeleteCascadesSubtasksAndRuns(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		on := true
		now := time.Now().UnixMilli()
		run := func(t *testing.T, of string) store.Task {
			r, err := e.st.CreateTask(e.ctx, store.CreateTaskInput{ProjectID: e.project, BoardID: e.board.ID, ColumnID: e.column(t, domain.Todo), Title: "run", Status: domain.Todo, OrderKey: "a0", RunOf: &of, CreatedBy: "alice", CreatedAt: now})
			if err != nil {
				t.Fatal(err)
			}
			return r
		}
		racing := func(t *testing.T, x store.Task) store.Task {
			x, err := e.st.UpdateTask(e.ctx, e.project, x.ID, x.Version, store.TaskPatch{Racing: &on, RaceOpenedAt: &store.Nullable[int64]{Value: &now}, UpdatedBy: "alice", UpdatedAt: now})
			if err != nil {
				t.Fatal(err)
			}
			return x
		}
		p := racing(t, e.tkSeed(t, domain.Todo, "", 0, nil))
		x := racing(t, run(t, p.ID))
		r1, r2 := run(t, x.ID), run(t, x.ID)
		var subs []string
		for range 2 {
			s, err := e.c.Context().AddSubtask(e.ctx, e.alice, AddSubtaskReq{ProjectID: e.project, ParentTaskID: x.ID, Title: "s"})
			if err != nil {
				t.Fatal(err)
			}
			subs = append(subs, s.Task.ID)
		}
		other := e.tkSeed(t, domain.Todo, "", 0, nil)
		e.tkDepend(t, other.ID, subs[0])
		if _, err := e.c.Context().AddItem(e.ctx, e.alice, AddContextItemReq{ProjectID: e.project, TaskID: subs[0], Kind: "note", Body: "n"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.c.Context().WriteMemory(e.ctx, e.alice, WriteMemoryReq{ProjectID: e.project, TaskID: subs[1], Scope: domain.MemoryScopeTask, Key: "k", Value: "v"}); err != nil {
			t.Fatal(err)
		}
		gone := append([]string{x.ID, r1.ID, r2.ID}, subs...)
		if err := e.tkDelete(e.alice, e.get(t, x.ID)); err != nil {
			t.Fatal(err)
		}
		for _, id := range gone {
			e.tkGone(t, id)
			if _, err := e.st.ListContextItems(e.ctx, e.project, id, store.ContextItemsQuery{}); tkCode(err) != domain.CodeNotFound {
				t.Fatalf("context items of %s: %v, want NOT_FOUND", id, err)
			}
			if n, err := e.st.DeleteMemoryByTask(e.ctx, e.project, id); err != nil || n != 0 {
				t.Fatalf("memory of %s: %d rows (%v), want 0", id, n, err)
			}
		}
		for _, ev := range e.feed(t) {
			if ev.TaskID != nil && slices.Contains(gone, *ev.TaskID) {
				t.Fatalf("the feed holds event %s of deleted %s", ev.Type, *ev.TaskID)
			}
		}
		for _, ed := range e.edges(t) {
			if slices.Contains(gone, ed.FromTaskID) || slices.Contains(gone, ed.ToTaskID) {
				t.Fatalf("edge %s %s→%s survived", ed.ID, ed.FromTaskID, ed.ToTaskID)
			}
		}
		if o := e.get(t, other.ID); slices.Contains(o.Blocks, subs[0]) {
			t.Fatalf("the blocker's blocks %v still names %s", o.Blocks, subs[0])
		}
		for _, of := range []string{p.ID, x.ID} {
			page, err := e.st.ListTasks(e.ctx, e.project, store.TaskFilter{BoardID: e.board.ID, RunOf: &of}, "", 50)
			if err != nil || len(page.Tasks) != 0 {
				t.Fatalf("runs of %s: %d (%v), want none", of, len(page.Tasks), err)
			}
		}
		pa := e.get(t, p.ID)
		if pa.Status != domain.Todo || pa.Racing || pa.RaceOpenedAt != nil || len(ofType(e.events(t, p.ID), domain.EventRunDiscarded)) != 1 {
			t.Fatalf("parent %s racing %v raceOpenedAt %v, want TODO, false, nil and one run.discarded", pa.Status, pa.Racing, pa.RaceOpenedAt)
		}
		t.Logf("deleted %v; parent %s %s racing=%v", gone, pa.ID, pa.Status, pa.Racing)
	})
}

// AC7: one board-level task.deleted {id, title, status}, one audit row, a change-log row; a second
// delete is NOT_FOUND. AC8: the next create takes the next T-n, never the deleted one.
func TestDeleteWritesItsTraceOnceAndNeverReusesTheID(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		var tasks []string
		for range 7 {
			r, err := e.c.Tasks().Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "t"})
			if err != nil {
				t.Fatal(err)
			}
			tasks = append(tasks, r.Task.ID)
		}
		last := e.get(t, tasks[6])
		auditBefore, changesBefore := e.auditCount(t), e.changeCount(t)
		if err := e.tkDelete(e.alice, last); err != nil {
			t.Fatal(err)
		}
		deleted := ofType(e.feed(t), domain.EventTaskDeleted)
		if len(deleted) != 1 || deleted[0].TaskID != nil || compact(t, deleted[0].Data) != `{"id":"`+last.ID+`","status":"TODO","title":"t"}` {
			t.Fatalf("task.deleted events %+v, want one board event {id,title,status}", deleted)
		}
		rows := e.auditRows(t)[auditBefore:]
		if len(rows) != 1 || rows[0].Action != "task.deleted" || rows[0].Subject != last.ID {
			t.Fatalf("audit rows %+v, want one task.deleted on %s", rows, last.ID)
		}
		if kinds := e.changeKinds(t, changesBefore); !slices.Equal(kinds, []string{ChangeKindChanged + ":" + last.ID}) {
			t.Fatalf("change rows %v, want one", kinds)
		}
		wantCode(t, e.tkDelete(e.alice, last), domain.CodeNotFound)
		next, err := e.c.Tasks().Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "after"})
		if err != nil {
			t.Fatal(err)
		}
		if tkNum(t, next.Task.ID) != tkNum(t, last.ID)+1 {
			t.Fatalf("after deleting %s the next create is %s, want T-%d", last.ID, next.Task.ID, tkNum(t, last.ID)+1)
		}
		t.Logf("deleted %s; second delete NOT_FOUND; next create %s", last.ID, next.Task.ID)
	})
}

func (e *env) auditRows(t *testing.T) []store.AuditRow {
	t.Helper()
	page, err := e.st.ListAuditLog(e.ctx, store.AuditFilter{}, "", 500)
	if err != nil {
		t.Fatal(err)
	}
	rows := slices.Clone(page.Rows)
	slices.SortFunc(rows, func(a, b store.AuditRow) int { return int(a.ID - b.ID) })
	return rows
}

func (e *env) auditCount(t *testing.T) int { return len(e.auditRows(t)) }

// The refusals: STALE, another identity's live lease CONFLICT_LOCKED (the holder's own delete is
// the control), a worker FORBIDDEN (a master is the control), another project FORBIDDEN.
func TestDeleteRefusals(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		task := e.tkSeed(t, domain.Todo, "", 0, nil)
		wantCode(t, e.tkDelete(e.alice, store.Task{ID: task.ID, Version: task.Version + 1}), domain.CodeStale)
		worker := e.member(t, e.project, "will", domain.RoleWorker)
		wantCode(t, e.tkDelete(worker, task), domain.CodeForbidden)
		_, err := e.c.Tasks().Delete(e.ctx, e.alice, DeleteTaskReq{ProjectID: "P-other", TaskID: task.ID, Version: task.Version})
		wantCode(t, err, domain.CodeForbidden)
		now := time.Now()
		held, err := e.st.ClaimTask(e.ctx, e.project, task.ID, task.Version, store.ClaimInput{
			Owner: "bob", LeaseID: "L-1", ExpiresAt: now.Add(time.Minute).UnixMilli(), Now: now.UnixMilli(), ColumnID: e.column(t, domain.InProgress),
		})
		if err != nil {
			t.Fatal(err)
		}
		wantCode(t, e.tkDelete(e.alice, held), domain.CodeConflictLocked)
		bob := e.member(t, e.project, "bob", domain.RoleMaster)
		if err := e.tkDelete(bob, held); err != nil {
			t.Fatalf("the holder's own delete: %v", err)
		}
		master := e.member(t, e.project, "mia", domain.RoleMaster)
		if err := e.tkDelete(master, e.tkSeed(t, domain.Todo, "", 0, nil)); err != nil {
			t.Fatalf("a master's delete: %v", err)
		}
	})
}

// The blob hook (requirement 7): called once per committed delete, with no hash for a pending
// attachment, which holds no blob; never on a refused delete. The deleted task's pending attachment
// goes with it.
func TestDeleteCallsTheBlobHook(t *testing.T) {
	var mu sync.Mutex
	var calls [][]string
	f := newFixture(t, Options{})
	old := f.c
	f.c = New(f.st, Options{Logger: log.New(f.logs, "", 0), Now: f.clock.now, ToolCallFlush: -1, BlobGC: func(_ context.Context, hashes []string) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, hashes)
	}})
	if err := old.Close(f.ctx); err != nil {
		t.Fatal(err)
	}
	task := f.task(t, domain.Todo, nil)
	if _, err := f.st.CreateAttachment(f.ctx, store.CreateAttachmentInput{ProjectID: f.project.ID, TaskID: task.ID, MimeType: "text/plain", Bytes: 1, Kind: store.AttachmentFile, CreatedBy: "alice"}); err != nil {
		t.Fatal(err)
	}
	_, err := f.c.Tasks().Delete(f.ctx, f.alice, DeleteTaskReq{ProjectID: f.project.ID, TaskID: task.ID, Version: task.Version + 1})
	wantCode(t, err, domain.CodeStale)
	if len(calls) != 0 {
		t.Fatalf("a refused delete called the hook %d times", len(calls))
	}
	if _, err := f.c.Tasks().Delete(f.ctx, f.alice, DeleteTaskReq{ProjectID: f.project.ID, TaskID: task.ID, Version: task.Version}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0] == nil || len(calls[0]) != 0 {
		t.Fatalf("hook calls %v, want one call with no files", calls)
	}
	if n, err := f.st.DeleteAttachmentsByTask(f.ctx, f.project.ID, task.ID); err != nil || n != 0 {
		t.Fatalf("%d attachment rows left (%v), want 0", n, err)
	}
}

// Review round 1 (the reviewer's TestProbeCascadeIntraSetEdge): edges inside the deletion set — a
// subtask blocking its sibling and the root — are not unblocked or stripped (they go with the set),
// while a blocker outside the set is stripped. Kills O1 (skip ignored in unblockDependentsOf) and O2
// (skip ignored in stripFromBlockers).
func TestDeleteCascadeIntraSetEdges(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		root := e.tkSeed(t, domain.Todo, "", 0, nil)
		var subs []string
		for range 2 {
			s, err := e.c.Context().AddSubtask(e.ctx, e.alice, AddSubtaskReq{ProjectID: e.project, ParentTaskID: root.ID, Title: "s"})
			if err != nil {
				t.Fatal(err)
			}
			subs = append(subs, s.Task.ID)
		}
		e.tkDepend(t, subs[0], subs[1])
		e.tkDepend(t, subs[1], root.ID)
		outside := e.tkSeed(t, domain.Todo, "", 0, nil)
		e.tkDepend(t, outside.ID, subs[0])
		if err := e.tkDelete(e.alice, e.get(t, root.ID)); err != nil {
			t.Fatalf("delete: %v", err)
		}
		for _, id := range append([]string{root.ID}, subs...) {
			e.tkGone(t, id)
		}
		if o := e.get(t, outside.ID); len(o.Blocks) != 0 {
			t.Fatalf("outside blocker blocks %v, want []", o.Blocks)
		}
	})
}

// Review round 1 (the reviewer's TestProbeCascadeOver200Runs): runsOf pages past one ListTasks page
// (200). Kills O5 (one page only).
func TestDeleteCascadeOver200Runs(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		root := e.tkSeed(t, domain.Todo, "", 0, nil)
		now := time.Now().UnixMilli()
		var ids []string
		for range 250 {
			of := root.ID
			r, err := e.st.CreateTask(e.ctx, store.CreateTaskInput{ProjectID: e.project, BoardID: e.board.ID, ColumnID: e.column(t, domain.Todo), Title: "run", Status: domain.Todo, OrderKey: "a0", RunOf: &of, CreatedBy: "alice", CreatedAt: now})
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, r.ID)
		}
		if err := e.tkDelete(e.alice, e.get(t, root.ID)); err != nil {
			t.Fatalf("delete: %v", err)
		}
		left := 0
		for _, id := range ids {
			if _, err := e.st.GetTask(e.ctx, e.project, id); tkCode(err) != domain.CodeNotFound {
				left++
			}
		}
		if left != 0 {
			t.Fatalf("%d of 250 runs survived the cascade", left)
		}
	})
}

// A deleted sprint member leaves the sprint's memberCount: deleting a member, and deleting a parent
// whose subtask is a member, each lower it by one, so a sprint that once held its cap takes members
// again once it holds fewer.
func TestDeleteLowersTheSprintMemberCount(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		sp, err := e.c.Sprints().Create(e.ctx, e.alice, CreateSprintReq{ProjectID: e.project, BoardID: e.board.ID, Name: "s", Goal: "g", Acceptance: "a"})
		if err != nil {
			t.Fatal(err)
		}
		count := func(t *testing.T) int64 {
			t.Helper()
			got, err := e.st.GetSprint(e.ctx, e.project, sp.Sprint.ID)
			if err != nil {
				t.Fatal(err)
			}
			return got.MemberCount
		}
		var members []string
		for range 3 {
			r, err := e.c.Tasks().Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "m", SprintID: sp.Sprint.ID})
			if err != nil {
				t.Fatal(err)
			}
			members = append(members, r.Task.ID)
		}
		parent := e.tkSeed(t, domain.Todo, "", 0, nil)
		sub, err := e.c.Tasks().Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "sub", SprintID: sp.Sprint.ID, ParentTaskID: &parent.ID})
		if err != nil {
			t.Fatal(err)
		}
		if n := count(t); n != 4 {
			t.Fatalf("after four creates memberCount=%d, want 4", n)
		}
		if err := e.tkDelete(e.alice, e.get(t, members[0])); err != nil {
			t.Fatal(err)
		}
		if n := count(t); n != 3 {
			t.Fatalf("after deleting member %s: memberCount=%d, want 3", members[0], n)
		}
		if err := e.tkDelete(e.alice, e.get(t, parent.ID)); err != nil {
			t.Fatal(err)
		}
		e.tkGone(t, sub.Task.ID)
		if n := count(t); n != 2 {
			t.Fatalf("after deleting %s's parent: memberCount=%d, want 2", sub.Task.ID, n)
		}
	})
}
