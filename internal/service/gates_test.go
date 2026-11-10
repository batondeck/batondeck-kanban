package service

import (
	"slices"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
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
