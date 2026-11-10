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

// A sprint member raced with start_runs carries the sprint's work in its runs: get_sprint's
// frontier, next_task (both strategies), rank_tasks and claim_next {sprintId} offer them as they
// would the member, and the runs of a task outside the sprint stay out. Before, the runs were in no
// sprint: the frontier was empty and a sprint-scoped worker found nothing while the work waited.
func TestARacedSprintMembersRunsAreInItsFrontier(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		sp := e.sprint(t, e.board.ID)
		member, err := e.c.Tasks().Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "member", SprintID: sp.ID})
		if err != nil {
			t.Fatal(err)
		}
		started, err := e.c.Runs().Start(e.ctx, e.alice, StartRunsReq{ProjectID: e.project, TaskID: member.Task.ID, Version: int64(member.Task.Version), Count: 2})
		if err != nil {
			t.Fatal(err)
		}
		outsider := e.task(t, domain.Todo)
		if _, err := e.c.Runs().Start(e.ctx, e.alice, StartRunsReq{ProjectID: e.project, TaskID: outsider.ID, Version: outsider.Version, Count: 1}); err != nil {
			t.Fatal(err)
		}
		runs := []string{started.Runs[0].ID, started.Runs[1].ID}
		slices.Sort(runs)

		got, err := e.c.Sprints().Get(e.ctx, e.alice, GetSprintReq{ProjectID: e.project, SprintID: sp.ID})
		if err != nil {
			t.Fatal(err)
		}
		frontier := slices.Sorted(slices.Values(got.Frontier))
		rk, err := e.c.Ranking().Rank(e.ctx, e.alice, RankTasksReq{ProjectID: e.project, BoardID: e.board.ID, SprintID: sp.ID})
		if err != nil {
			t.Fatal(err)
		}
		var ranked []string
		for _, r := range rk.Ranked {
			ranked = append(ranked, r.Task.ID)
		}
		slices.Sort(ranked)
		var next []string
		for _, strategy := range []NextStrategy{"fifo", "score"} {
			n, err := e.c.Lifecycle().Next(e.ctx, e.alice, NextTaskReq{ProjectID: e.project, BoardID: e.board.ID, SprintID: sp.ID, Strategy: strategy})
			if err != nil {
				t.Fatal(err)
			}
			id := "<none>"
			if n.Task != nil {
				id = n.Task.ID
			}
			next = append(next, id)
		}
		t.Logf("member %s runs %v; get_sprint frontier %v (members %d); rank_tasks %v; next_task fifo/score %v", member.Task.ID, runs, frontier, len(got.Tasks), ranked, next)
		if !slices.Equal(frontier, runs) || !slices.Equal(ranked, runs) || !slices.Contains(runs, next[0]) || !slices.Contains(runs, next[1]) {
			t.Fatalf("the sprint does not offer its member's runs %v: frontier %v, rank_tasks %v, next_task %v", runs, frontier, ranked, next)
		}
		if len(got.Tasks) != 1 || got.Counts.Total != 1 {
			t.Fatalf("the runs became members: tasks %d, total %v", len(got.Tasks), got.Counts.Total)
		}
		for range runs {
			cn, err := e.c.Lifecycle().ClaimNext(e.ctx, e.alice, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, SprintID: sp.ID})
			if err != nil || cn.Task == nil || !slices.Contains(runs, cn.Task.ID) {
				t.Fatalf("claim_next {sprintId}: %+v %v, want one of %v", cn.Task, err, runs)
			}
		}
		if cn, err := e.c.Lifecycle().ClaimNext(e.ctx, e.alice, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, SprintID: sp.ID}); err != nil || cn.Task != nil {
			t.Fatalf("claim_next {sprintId} after both runs: %+v %v, want nothing (the outsider's run is not the sprint's)", cn.Task, err)
		}
	})
}
