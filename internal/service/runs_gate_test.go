package service

import (
	"slices"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// A race carries the parent's work, so it waits on the parent's blockers: start_runs on a task with
// an open blocker, and open_run on a racing parent that gained one, are INVALID_TRANSITION naming
// the blockers and create no run. Before, each run was a TODO task with no blockedBy, claimable at
// once, and pick_run landed the parent DONE ahead of its blocker.
func TestARaceWaitsOnTheParentsBlockers(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		blocker, gated := e.task(t, domain.Todo), e.task(t, domain.Todo)
		e.dependOn(t, blocker.ID, gated.ID)
		cur := e.get(t, gated.ID)
		_, err := e.c.Runs().Start(e.ctx, e.alice, StartRunsReq{ProjectID: e.project, TaskID: gated.ID, Version: cur.Version, Count: 2})
		be := wantCode(t, err, domain.CodeInvalidTransition)
		after := e.get(t, gated.ID)
		t.Logf("start_runs on %s blocked by %v: %s %q %v; racing %v; runs %v", gated.ID, cur.BlockedBy, be.Code, be.Message, be.Details, after.Racing, e.runIDs(t, gated.ID))
		if after.Racing || len(e.runIDs(t, gated.ID)) != 0 || after.Version != cur.Version || !strings.Contains(be.Message, blocker.ID) {
			t.Fatalf("start_runs raced a blocked task: racing %v, runs %v, version %d -> %d", after.Racing, e.runIDs(t, gated.ID), cur.Version, after.Version)
		}

		// A racing parent that gains a blocker takes no new run.
		parent := e.task(t, domain.Todo)
		started, err := e.c.Runs().Start(e.ctx, e.alice, StartRunsReq{ProjectID: e.project, TaskID: parent.ID, Version: parent.Version, Count: 1})
		if err != nil {
			t.Fatal(err)
		}
		e.dependOn(t, blocker.ID, parent.ID)
		_, err = e.c.Runs().Open(e.ctx, e.alice, OpenRunReq{ProjectID: e.project, TaskID: parent.ID})
		be = wantCode(t, err, domain.CodeInvalidTransition)
		runs := e.runIDs(t, parent.ID)
		t.Logf("open_run on racing %s blocked by %s: %s %q; runs %v", parent.ID, blocker.ID, be.Code, be.Message, runs)
		if !slices.Equal(runs, []string{started.Runs[0].ID}) {
			t.Fatalf("open_run opened a run of a blocked parent: runs %v", runs)
		}
	})
}
