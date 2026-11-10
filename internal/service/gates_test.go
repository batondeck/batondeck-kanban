package service

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// A dependency on a blocker that is already DONE is recorded (get_task_context's upstream reads the
// edge) but gates nothing: blockedBy holds open blockers only, and nothing would ever clear a DONE
// one. Before, the dependent kept the DONE blocker in blockedBy and was unclaimable for ever.
func TestAddDependencyOnADoneBlockerGatesNothing(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		a := e.task(t, domain.Done)
		b := e.task(t, domain.Todo)
		e.dependOn(t, a.ID, b.ID)
		got := e.get(t, b.ID)
		t.Logf("add_dependency %s (DONE) -> %s: %s blockedBy %v; %s blocks %v", a.ID, b.ID, b.ID, got.BlockedBy, a.ID, e.get(t, a.ID).Blocks)
		if got.Status != domain.Todo || len(got.BlockedBy) != 0 {
			t.Fatalf("%s: status %s blockedBy %v, want TODO with no open blocker", b.ID, got.Status, got.BlockedBy)
		}
		if blocks := e.get(t, a.ID).Blocks; !slices.Equal(blocks, []string{b.ID}) {
			t.Fatalf("%s blocks %v, want [%s]: the edge is recorded", a.ID, blocks, b.ID)
		}
		edges, err := e.st.ListBlockers(e.ctx, e.project, b.ID)
		if err != nil || len(edges) != 1 || edges[0].FromTaskID != a.ID {
			t.Fatalf("blockers of %s %+v (err %v), want the edge from %s", b.ID, edges, err, a.ID)
		}
		r, err := e.c.Lifecycle().ClaimNext(e.ctx, e.alice, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID})
		if err != nil || r.Task == nil || r.Task.ID != b.ID {
			t.Fatalf("claim_next = %+v (err %v), want %s", r.Task, err, b.ID)
		}
	})
}

// block_task naming a blocker that is already DONE is refused (VALIDATION naming it) and writes
// nothing: the caller keeps its lease and can carry on, rather than parking its task BLOCKED on a
// blocker that will never clear.
func TestBlockOnADoneBlockerIsRefused(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		a := e.task(t, domain.Done)
		c := e.task(t, domain.Todo)
		lease := e.claim(t, e.alice, c.ID, 0)
		before := e.get(t, c.ID)
		_, err := e.c.Lifecycle().Block(e.ctx, e.alice, BlockTaskReq{ProjectID: e.project, TaskID: c.ID, LeaseID: lease.LeaseID, Reason: "waiting", BlockedBy: []string{a.ID}})
		got := e.get(t, c.ID)
		t.Logf("block_task %s blockedBy [%s (DONE)]: err %v; %s status %s blockedBy %v", c.ID, a.ID, err, c.ID, got.Status, got.BlockedBy)
		be := wantCode(t, err, domain.CodeValidation)
		if be.Details["blockedBy"] != a.ID {
			t.Fatalf("details %v, want blockedBy %s", be.Details, a.ID)
		}
		if got.Status != domain.InProgress || got.Version != before.Version || got.Claim == nil || got.Claim.LeaseID != lease.LeaseID || len(got.BlockedBy) != 0 {
			t.Fatalf("%s after the refusal %+v, want it untouched", c.ID, got)
		}
		if edges, err := e.st.ListBlockers(e.ctx, e.project, c.ID); err != nil || len(edges) != 0 {
			t.Fatalf("blockers of %s %+v (err %v), want none", c.ID, edges, err)
		}
	})
}

// complete claims id and completes it (DONE: reviewEnabled is off).
func (e *lcEnv) complete(t *testing.T, id string) {
	t.Helper()
	e.settings(t, map[string]any{"reviewEnabled": false})
	c := e.claim(t, e.alice, id, 0)
	if _, err := e.c.Lifecycle().Complete(e.ctx, e.alice, CompleteTaskReq{ProjectID: e.project, TaskID: id, LeaseID: c.LeaseID}); err != nil {
		t.Fatalf("complete %s: %v", id, err)
	}
}

// A blocks edge outlives its gate: when the blocker reached DONE the edge stayed for upstream
// context and the dependent's blockedBy dropped it. Re-adding that edge once the blocker is reopened
// must gate the dependent again, not answer the old edge with nothing written; and the cleared edge
// no longer closes a "cycle" for the reverse edge.
func TestReAddingAClearedEdgeGatesAgain(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		a := e.task(t, domain.Todo)
		b := e.task(t, domain.Todo)
		e.dependOn(t, a.ID, b.ID)
		e.complete(t, a.ID)
		if got := e.get(t, b.ID); len(got.BlockedBy) != 0 {
			t.Fatalf("%s blockedBy %v after %s reached DONE, want []", b.ID, got.BlockedBy, a.ID)
		}
		done := e.get(t, a.ID)
		if _, err := e.c.Tasks().Move(e.ctx, e.alice, MoveTaskReq{ProjectID: e.project, TaskID: a.ID, Version: done.Version, ToStatus: domain.InProgress}); err != nil {
			t.Fatalf("reopen %s: %v", a.ID, err)
		}
		e.dependOn(t, a.ID, b.ID)
		got := e.get(t, b.ID)
		_, err := e.c.Lifecycle().Claim(e.ctx, e.alice, ClaimTaskReq{ProjectID: e.project, TaskID: b.ID})
		t.Logf("%s reopened, add_dependency %s -> %s again: %s blockedBy %v; claim_task %s: %v", a.ID, a.ID, b.ID, b.ID, got.BlockedBy, b.ID, err)
		if !slices.Equal(got.BlockedBy, []string{a.ID}) {
			t.Fatalf("%s blockedBy %v, want [%s]: the re-added edge gates again", b.ID, got.BlockedBy, a.ID)
		}
		wantCode(t, err, domain.CodeInvalidTransition)
		edges, err := e.st.ListBlockers(e.ctx, e.project, b.ID)
		if err != nil || len(edges) != 1 {
			t.Fatalf("blockers of %s %+v (err %v), want the one edge", b.ID, edges, err)
		}
		// Once more, with the gate open: nothing changes, so the blocker is not bumped and no
		// dependency.added is logged.
		va, added := e.get(t, a.ID).Version, len(ofType(e.events(t, b.ID), domain.EventDependencyAdded))
		e.dependOn(t, a.ID, b.ID)
		if v, n := e.get(t, a.ID).Version, len(ofType(e.events(t, b.ID), domain.EventDependencyAdded)); v != va || n != added {
			t.Fatalf("a repeated add_dependency moved %s v%d -> v%d and dependency.added %d -> %d, want neither", a.ID, va, v, added, n)
		}
	})
}

func TestAClearedEdgeClosesNoCycle(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		a := e.task(t, domain.Todo)
		b := e.task(t, domain.Todo)
		e.dependOn(t, a.ID, b.ID)
		e.complete(t, a.ID)
		_, err := e.c.Context().AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: b.ID, ToTaskID: a.ID})
		t.Logf("%s -> %s cleared (%s DONE); add_dependency %s -> %s: %v", a.ID, b.ID, a.ID, b.ID, a.ID, err)
		if err != nil {
			t.Fatalf("add_dependency %s -> %s: %v, want it added: the %s -> %s edge gates nothing any more", b.ID, a.ID, err, a.ID, b.ID)
		}
		// A live gate still closes one: c blocks d, so d -> c is a cycle.
		c := e.task(t, domain.Todo)
		d := e.task(t, domain.Todo)
		e.dependOn(t, c.ID, d.ID)
		_, err = e.c.Context().AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: d.ID, ToTaskID: c.ID})
		wantCode(t, err, domain.CodeCycleDetected)
	})
}

// claim_task on a task with an open blocker says so: the refusal (INVALID_TRANSITION, as the
// contract has it) names the blockers in its message and details, rather than calling the one legal
// move out of TODO illegal.
func TestClaimingAGatedTaskNamesItsBlockers(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		a := e.task(t, domain.Todo)
		b := e.task(t, domain.Todo)
		e.dependOn(t, a.ID, b.ID)
		_, err := e.c.Lifecycle().Claim(e.ctx, e.alice, ClaimTaskReq{ProjectID: e.project, TaskID: b.ID})
		be := wantCode(t, err, domain.CodeInvalidTransition)
		t.Logf("claim_task %s (blocked by %s): %s %v", b.ID, a.ID, be.Message, be.Details)
		if !strings.Contains(be.Message, a.ID) {
			t.Fatalf("message %q names no blocker, want %s", be.Message, a.ID)
		}
		if got, _ := be.Details["blockedBy"].([]string); !slices.Equal(got, []string{a.ID}) {
			t.Fatalf("details %v, want blockedBy [%s]", be.Details, a.ID)
		}
	})
}

// next_task is a read (read tier, grant-exempt, readOnlyHint): on an empty frontier it reaps nothing.
// Before, it ran the lazy reap under the caller, so a viewer's preview moved an expired lease's task
// back to TODO (or poisoned it) in its own name. claim_next and reap_stale_leases keep reaping.
func TestNextTaskReapsNothing(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		e.settings(t, map[string]any{"retryPolicy": domain.RetryPolicy{BaseDelayMs: 1000, MaxDelayMs: 60_000, PoisonAfterAttempts: 1}})
		vera := e.member(t, e.project, "vera", domain.RoleViewer)
		task := e.task(t, domain.Todo)
		e.claim(t, e.alice, task.ID, 5)
		e.clock.advance(10 * time.Minute)
		before := e.get(t, task.ID)
		r, err := e.c.Lifecycle().Next(e.ctx, vera, NextTaskReq{ProjectID: e.project, BoardID: e.board.ID})
		after := e.get(t, task.ID)
		t.Logf("viewer next_task over an expired lease: task %v err %v; %s %s v%d -> %s v%d attempts %d updatedBy %s",
			r.Task, err, task.ID, before.Status, before.Version, after.Status, after.Version, after.ClaimAttempts, after.UpdatedBy)
		if err != nil || r.Task != nil {
			t.Fatalf("next_task = %+v, %v; want null", r.Task, err)
		}
		if after.Status != domain.InProgress || after.Version != before.Version || after.ClaimAttempts != before.ClaimAttempts || len(ofType(e.events(t, task.ID), domain.EventTaskPoisoned)) != 0 {
			t.Fatalf("next_task wrote %s: %+v", task.ID, after)
		}
	})
}

// A WIP limit on In progress full of tasks whose leases have expired stops no claim: claim_next and
// claim_task reap the board when the limit refuses them and try once more, so the crashed agents'
// slots come back. Before, WIP_EXCEEDED was returned before the lazy reap could run, for ever.
func TestAnExpiredLeaseDoesNotHoldAWIPSlot(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		ip := e.column(t, domain.InProgress)
		if _, err := e.c.Boards().UpdateColumn(e.ctx, e.alice, UpdateColumnReq{ProjectID: e.project, BoardID: e.board.ID, ColumnID: ip, WIPLimit: Value(int64(1))}); err != nil {
			t.Fatal(err)
		}
		e.board, _, _ = e.st.GetBoard(e.ctx, e.project, e.board.ID)
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		lc := e.c.Lifecycle()
		a, b, c := e.task(t, domain.Todo), e.task(t, domain.Todo), e.task(t, domain.Todo)
		e.claim(t, e.alice, a.ID, 5)
		if _, err := lc.ClaimNext(e.ctx, bob, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID}); err == nil {
			t.Fatal("control: claim_next under a live lease in the one WIP slot succeeded")
		}
		e.clock.advance(10 * time.Minute)
		r, err := lc.ClaimNext(e.ctx, bob, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, LeaseSeconds: 5})
		t.Logf("WIP 1, %s's lease expired: claim_next -> %v (err %v); %s now %s", a.ID, r.Task, err, a.ID, e.get(t, a.ID).Status)
		if err != nil || r.Task == nil {
			t.Fatalf("claim_next = %+v, %v; want a claim once the expired lease is reaped", r.Task, err)
		}
		if got := e.get(t, a.ID); got.Status != domain.Todo || got.ClaimAttempts != 1 {
			t.Fatalf("%s %s attempts %d, want reaped to TODO with one attempt", a.ID, got.Status, got.ClaimAttempts)
		}
		// claim_task: bob's claim lapses in turn and alice names the remaining task.
		e.clock.advance(10 * time.Minute)
		other := b.ID
		if r.Task.ID == b.ID {
			other = c.ID
		}
		if _, err := lc.Claim(e.ctx, e.alice, ClaimTaskReq{ProjectID: e.project, TaskID: other}); err != nil {
			t.Fatalf("claim_task %s with the one WIP slot held by an expired lease: %v", other, err)
		}
	})
}

// A dependent that was already TODO when its last blocker cleared becomes claimable: it rings as one
// that became TODO does, broadcast when unassigned and assigned to its assignee, rather than only a
// changed row that rings masters alone. (complete_task to DONE; every unblock goes through
// unblockDependent.)
func TestATodoDependentThatBecomesClaimableRingsItsWorker(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		a, b := e.task(t, domain.Todo), e.task(t, domain.Todo)
		c, err := e.st.CreateTask(e.ctx, store.CreateTaskInput{ProjectID: e.project, BoardID: e.board.ID, ColumnID: e.column(t, domain.Todo), Title: "c",
			Status: domain.Todo, Assignee: &bob.Identity, OrderKey: "a1", CreatedBy: "alice", CreatedAt: time.Now().UnixMilli()})
		if err != nil {
			t.Fatal(err)
		}
		e.dependOn(t, a.ID, b.ID)
		e.dependOn(t, a.ID, c.ID)
		from := e.changeCount(t)
		e.complete(t, a.ID)
		ch, err := e.st.ReadChanges(e.ctx, 0, 1000)
		if err != nil {
			t.Fatal(err)
		}
		rung := map[string]string{}
		for _, row := range ch[from:] {
			var p changePayload
			_ = json.Unmarshal(row.Payload, &p)
			if p.TaskID == b.ID || p.TaskID == c.ID {
				rung[p.TaskID] = p.Kind + "@" + row.Scope
			}
		}
		t.Logf("complete_task %s: dependents rung %v", a.ID, rung)
		if rung[b.ID] != DoorbellBroadcast+"@"+BroadcastScope(e.project) || rung[c.ID] != DoorbellAssigned+"@bob" {
			t.Fatalf("rows %v, want %s broadcast and %s assigned to bob", rung, b.ID, c.ID)
		}
	})
}

// An idempotency key is bound to the call's arguments: the same key on another task is another
// call, not a replay of the first one's answer. Before, complete_task(T-2, key "done") within the
// window answered T-1's DONE body and left T-2 IN_PROGRESS with its deliverable dropped. The same
// key with the same arguments still replays.
func TestAnIdempotencyKeyReusedOnAnotherTaskIsAnotherCall(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		e.settings(t, map[string]any{"reviewEnabled": false})
		lc := e.c.Lifecycle()
		a, b := e.task(t, domain.Todo), e.task(t, domain.Todo)
		la, lb := e.claim(t, e.alice, a.ID, 0), e.claim(t, e.alice, b.ID, 0)
		if _, err := lc.Complete(e.ctx, e.alice, CompleteTaskReq{ProjectID: e.project, TaskID: a.ID, LeaseID: la.LeaseID, IdempotencyKey: "done"}); err != nil {
			t.Fatal(err)
		}
		r, err := lc.Complete(e.ctx, e.alice, CompleteTaskReq{ProjectID: e.project, TaskID: b.ID, LeaseID: lb.LeaseID, Deliverable: "b's", IdempotencyKey: "done"})
		got := e.get(t, b.ID)
		t.Logf("complete_task %s then %s under one key: answered %s %s (err %v); %s in the store %s", a.ID, b.ID, r.Task.ID, r.Task.Status, err, b.ID, got.Status)
		if err != nil || r.Task.ID != b.ID || got.Status != domain.Done || got.Deliverable == nil || *got.Deliverable != "b's" {
			t.Fatalf("the second complete_task answered %s (err %v) and left %s %s, want %s completed", r.Task.ID, err, b.ID, got.Status, b.ID)
		}
		c, d := e.task(t, domain.Todo), e.task(t, domain.Todo)
		first, err := lc.Claim(e.ctx, e.alice, ClaimTaskReq{ProjectID: e.project, TaskID: c.ID, IdempotencyKey: "claim"})
		if err != nil {
			t.Fatal(err)
		}
		other, err := lc.Claim(e.ctx, e.alice, ClaimTaskReq{ProjectID: e.project, TaskID: d.ID, IdempotencyKey: "claim"})
		if err != nil || other.Task.ID != d.ID || e.get(t, d.ID).Status != domain.InProgress {
			t.Fatalf("claim_task %s under %s's key answered %s (err %v), want %s claimed", d.ID, c.ID, other.Task.ID, err, d.ID)
		}
		again, err := lc.Claim(e.ctx, e.alice, ClaimTaskReq{ProjectID: e.project, TaskID: c.ID, IdempotencyKey: "claim"})
		if err != nil || again.LeaseID != first.LeaseID {
			t.Fatalf("the same claim_task again answered lease %s (err %v), want the replay of %s", again.LeaseID, err, first.LeaseID)
		}
	})
}

// A created task lands at the END of its column, so column order is creation order and claim_next
// and next_task's "fifo" serve the oldest first. Before, every create landed at the column's head
// (no last-key read), and the frontier's order key put the newest first: old tasks starved.
func TestFifoServesTheOldestTaskFirst(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		var ids []string
		for _, title := range []string{"one", "two", "three", "four"} {
			r, err := e.c.Tasks().Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: title})
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, r.Task.ID)
			e.clock.advance(time.Hour)
		}
		lc := e.c.Lifecycle()
		peek, err := lc.Next(e.ctx, e.alice, NextTaskReq{ProjectID: e.project, BoardID: e.board.ID, Strategy: "fifo"})
		if err != nil || peek.Task == nil || peek.Task.ID != ids[0] {
			t.Fatalf("next_task fifo = %+v (err %v), want %s", peek.Task, err, ids[0])
		}
		var claimed []string
		for range ids {
			r, err := lc.ClaimNext(e.ctx, e.alice, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID})
			if err != nil || r.Task == nil {
				t.Fatalf("claim_next: %+v %v", r.Task, err)
			}
			claimed = append(claimed, r.Task.ID)
		}
		t.Logf("created %v; claim_next took %v", ids, claimed)
		if !slices.Equal(claimed, ids) {
			t.Fatalf("claim_next took %v, want creation order %v", claimed, ids)
		}
		// A subtask lands at its column's end too.
		sub, err := e.c.Context().AddSubtask(e.ctx, e.alice, AddSubtaskReq{ProjectID: e.project, ParentTaskID: ids[0], Title: "child"})
		if err != nil {
			t.Fatal(err)
		}
		page, err := e.st.ListTasks(e.ctx, e.project, store.TaskFilter{BoardID: e.board.ID, ColumnID: sub.Task.ColumnID}, "", 200)
		if err != nil || len(page.Tasks) == 0 || page.Tasks[len(page.Tasks)-1].ID != sub.Task.ID {
			t.Fatalf("the subtask %s is not last in its column: %v (err %v)", sub.Task.ID, page.Tasks, err)
		}
	})
}

// claim_task honours the retry backoff fail_task set (domain.IsClaimable: past nextEligibleAt), as
// claim_next does: a claim inside it is refused, naming when it ends, and writes nothing. Before, a
// fail_task then claim_task loop retried at once and burnt through poisonAfterAttempts in seconds.
func TestClaimTaskHonoursTheRetryBackoff(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		e.settings(t, map[string]any{"retryPolicy": policy3})
		lc := e.c.Lifecycle()
		task := e.task(t, domain.Todo)
		c := e.claim(t, e.alice, task.ID, 0)
		if _, err := lc.Fail(e.ctx, e.alice, FailTaskReq{ProjectID: e.project, TaskID: task.ID, LeaseID: c.LeaseID, Reason: "boom"}); err != nil {
			t.Fatal(err)
		}
		failed := e.get(t, task.ID)
		_, err := lc.Claim(e.ctx, e.alice, ClaimTaskReq{ProjectID: e.project, TaskID: task.ID})
		t.Logf("claim_task inside the backoff (nextEligibleAt %v): %v", failed.NextEligibleAt, err)
		be := wantCode(t, err, domain.CodeInvalidTransition)
		if be.Details["nextEligibleAt"] != isoMs(*failed.NextEligibleAt) {
			t.Fatalf("details %v, want nextEligibleAt %s", be.Details, isoMs(*failed.NextEligibleAt))
		}
		if got := e.get(t, task.ID); got.Status != domain.Todo || got.Version != failed.Version {
			t.Fatalf("the refused claim wrote %+v", got)
		}
		e.clock.advance(2 * time.Second)
		if _, err := lc.Claim(e.ctx, e.alice, ClaimTaskReq{ProjectID: e.project, TaskID: task.ID}); err != nil {
			t.Fatalf("claim_task past the backoff: %v", err)
		}
	})
}
