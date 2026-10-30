package service

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// P-208 AC1: a run completed with a deliverable and then cancelled is DONE with the cancelled label
// but was not delivered, so pick_run refuses it with INVALID_TRANSITION and the race stays open.
func TestPickRunRefusesACancelledRun(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		parent := e.task(t, domain.Todo)
		started, err := e.c.Runs().Start(e.ctx, e.alice, StartRunsReq{ProjectID: e.project, TaskID: parent.ID, Version: parent.Version, Count: 2})
		if err != nil {
			t.Fatal(err)
		}
		run := started.Runs[0].ID
		c := e.claim(t, e.alice, run, 0)
		done, err := e.c.Lifecycle().Complete(e.ctx, e.alice, CompleteTaskReq{ProjectID: e.project, TaskID: run, LeaseID: c.LeaseID, Deliverable: "the fix"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.c.Lifecycle().Cancel(e.ctx, e.alice, CancelTaskReq{ProjectID: e.project, TaskID: run, Version: int64(done.Task.Version), Reason: "wrong approach"}); err != nil {
			t.Fatalf("cancel the %s run: %v", done.Task.Status, err)
		}
		cancelled := e.get(t, run)
		_, err = e.c.Runs().Pick(e.ctx, e.alice, PickRunReq{ProjectID: e.project, TaskID: parent.ID, RunTaskID: run, Version: e.get(t, parent.ID).Version})
		be := wantCode(t, err, domain.CodeInvalidTransition)
		got := e.get(t, parent.ID)
		t.Logf("run %s %s labels %v deliverable %q; pick_run: %s %q; parent racing %v", run, cancelled.Status, cancelled.Labels, *cancelled.Deliverable, be.Code, be.Message, got.Racing)
		if !got.Racing || len(e.runIDs(t, parent.ID)) != 2 {
			t.Fatalf("a refused pick closed the race: %+v", got)
		}
	})
}

// P-208 AC2: a bound proposal task that was cancelled is DONE but not delivered, so the sprint does
// not activate; the refusal names the cancellation and the sprint stays PROPOSED.
func TestActivateRefusesACancelledProposal(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		sp, err := e.c.Sprints().Create(e.ctx, e.alice, CreateSprintReq{ProjectID: e.project, BoardID: e.board.ID, Name: "s", Goal: "g", Acceptance: "a"})
		if err != nil {
			t.Fatal(err)
		}
		proposal := e.task(t, domain.Todo)
		raw := json.RawMessage(`{"sprintProposalFor":"` + sp.Sprint.ID + `"}`)
		bound, err := e.st.UpdateTask(e.ctx, e.project, proposal.ID, proposal.Version, store.TaskPatch{CustomFields: &raw, UpdatedBy: "alice", UpdatedAt: 1})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.c.Lifecycle().Cancel(e.ctx, e.alice, CancelTaskReq{ProjectID: e.project, TaskID: proposal.ID, Version: bound.Version, Reason: "dropped"}); err != nil {
			t.Fatal(err)
		}
		_, err = e.c.Sprints().Update(e.ctx, e.alice, UpdateSprintReq{ProjectID: e.project, SprintID: sp.Sprint.ID, Version: int64(sp.Sprint.Version),
			Patch: SprintPatchIn{ProposalTaskID: Value(proposal.ID), Status: Some(SprintStatus("ACTIVE"))}})
		be := wantCode(t, err, domain.CodeInvalidTransition)
		got, err := e.st.GetSprint(e.ctx, e.project, sp.Sprint.ID)
		if err != nil {
			t.Fatal(err)
		}
		labels := e.get(t, proposal.ID).Labels
		t.Logf("proposal %s labels %v; activate: %s %q; sprint %s", proposal.ID, labels, be.Code, be.Message, got.Status)
		if !strings.Contains(be.Message, "cancelled") || got.Status != domain.SprintProposed || !slices.Contains(labels, domain.LabelCancelled) {
			t.Fatalf("activation with a cancelled proposal: %+v", got)
		}
	})
}

func (e *lcEnv) runIDs(t *testing.T, parent string) []string {
	t.Helper()
	rows, err := e.st.ListRuns(e.ctx, e.project, parent)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.RunTaskID)
	}
	return ids
}
