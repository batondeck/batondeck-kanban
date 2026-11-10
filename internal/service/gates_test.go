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
