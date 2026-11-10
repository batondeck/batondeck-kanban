package service

import (
	"slices"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// setSprintStatus moves sprint sp to status in the store, unchecked: the test's way to an ACTIVE
// sprint without a proposal.
func (e *lcEnv) setSprintStatus(t *testing.T, sp store.Sprint, status domain.SprintStatus) store.Sprint {
	t.Helper()
	got, err := e.st.UpdateSprint(e.ctx, e.project, sp.ID, sp.Version, store.SprintPatch{Status: &status, UpdatedBy: "alice", UpdatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (e *lcEnv) joinSprint(t *testing.T, sprintID, taskID string) (SetSprintResp, error) {
	t.Helper()
	return e.c.Sprints().Set(e.ctx, e.alice, SetSprintReq{ProjectID: e.project, SprintID: sprintID, TaskID: taskID, Action: "add", Version: e.get(t, taskID).Version})
}

// A member left unfinished by a CLOSED sprint, or by an ABANDONED one, is carried to a successor with
// set_sprint add, as the close warning says: an ended sprint stays in its sprintIds for history and
// holds it from no other sprint. The claim's sprint is the successor, the ended sprint keeps its
// member (a remove from it is still INVALID_TRANSITION), and a task in an open sprint is still
// CONFLICT_LOCKED out of a second. Before, the ended sprint held it out of every sprint for good.
func TestAnEndedSprintsMemberCarriesToASuccessor(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		s1 := e.setSprintStatus(t, e.sprint(t, e.board.ID), domain.SprintActive)
		task := e.task(t, domain.Todo)
		if _, err := e.joinSprint(t, s1.ID, task.ID); err != nil {
			t.Fatal(err)
		}
		s1 = e.getSprint(t, s1.ID)
		closed, err := e.c.Sprints().Update(e.ctx, e.alice, UpdateSprintReq{ProjectID: e.project, SprintID: s1.ID, Version: s1.Version,
			Patch: SprintPatchIn{Status: Some(SprintStatus("CLOSED"))}})
		if err != nil {
			t.Fatal(err)
		}
		s2 := e.sprint(t, e.board.ID)
		carried, err := e.joinSprint(t, s2.ID, task.ID)
		t.Logf("close warned %q; set_sprint add %s to successor %s: %v", closed.Warnings, task.ID, s2.ID, err)
		if err != nil {
			t.Fatalf("carrying %s from CLOSED %s to %s: %v", task.ID, s1.ID, s2.ID, err)
		}
		if len(closed.Warnings) != 1 || !strings.Contains(closed.Warnings[0], "set_sprint") || strings.Contains(closed.Warnings[0], "descope") {
			t.Fatalf("the close warning %q does not say how to carry a member", closed.Warnings)
		}
		if !slices.Equal(carried.Task.SprintIDs, []string{s1.ID, s2.ID}) || e.getSprint(t, s2.ID).MemberCount != 1 || e.getSprint(t, s1.ID).MemberCount != 1 {
			t.Fatalf("after the carry: sprintIds %v, %s members %d, %s members %d", carried.Task.SprintIDs, s1.ID, e.getSprint(t, s1.ID).MemberCount, s2.ID, e.getSprint(t, s2.ID).MemberCount)
		}
		_, err = e.c.Sprints().Set(e.ctx, e.alice, SetSprintReq{ProjectID: e.project, SprintID: s1.ID, TaskID: task.ID, Action: "remove", Version: e.get(t, task.ID).Version})
		wantCode(t, err, domain.CodeInvalidTransition)

		e.setSprintStatus(t, e.getSprint(t, s2.ID), domain.SprintActive)
		next, err := e.c.Lifecycle().ClaimNext(e.ctx, e.alice, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, SprintID: s2.ID})
		if err != nil || next.Task == nil || next.Task.ID != task.ID || next.Sprint == nil || next.Sprint.ID != s2.ID {
			t.Fatalf("claim_next {sprintId %s}: task %+v sprint %+v %v, want %s in %s", s2.ID, next.Task, next.Sprint, err, task.ID, s2.ID)
		}

		// Still exclusive among open sprints.
		s3 := e.sprint(t, e.board.ID)
		_, err = e.joinSprint(t, s3.ID, task.ID)
		wantCode(t, err, domain.CodeConflictLocked)

		// An ABANDONED sprint lets its member go the same way.
		s4 := e.sprint(t, e.board.ID)
		other := e.task(t, domain.Todo)
		if _, err := e.joinSprint(t, s4.ID, other.ID); err != nil {
			t.Fatal(err)
		}
		s4 = e.getSprint(t, s4.ID)
		if _, err := e.c.Sprints().Update(e.ctx, e.alice, UpdateSprintReq{ProjectID: e.project, SprintID: s4.ID, Version: s4.Version,
			Patch: SprintPatchIn{Status: Some(SprintStatus("ABANDONED"))}}); err != nil {
			t.Fatal(err)
		}
		if got, err := e.joinSprint(t, s3.ID, other.ID); err != nil || !slices.Equal(got.Task.SprintIDs, []string{s4.ID, s3.ID}) {
			t.Fatalf("carrying %s from ABANDONED %s to %s: %v %v", other.ID, s4.ID, s3.ID, got.Task.SprintIDs, err)
		}
	})
}

func (e *lcEnv) getSprint(t *testing.T, id string) store.Sprint {
	t.Helper()
	sp, err := e.st.GetSprint(e.ctx, e.project, id)
	if err != nil {
		t.Fatal(err)
	}
	return sp
}

// A CLOSED or ABANDONED sprint's name, goal and acceptance are its record: a patch that changes one
// is VALIDATION, as proposalTaskId outside PROPOSED is, and writes nothing; resending the value it
// holds, and checkpoint and eventCursor, still pass. Before, any master rewrote what the closed
// sprint had been approved against.
func TestAnEndedSprintsRecordIsNotRewritten(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		for _, end := range []domain.SprintStatus{domain.SprintClosed, domain.SprintAbandoned} {
			sp := e.setSprintStatus(t, e.sprint(t, e.board.ID), domain.SprintActive)
			r, err := e.c.Sprints().Update(e.ctx, e.alice, UpdateSprintReq{ProjectID: e.project, SprintID: sp.ID, Version: sp.Version,
				Patch: SprintPatchIn{Status: Some(SprintStatus(end))}})
			if err != nil {
				t.Fatal(err)
			}
			v := int64(r.Sprint.Version)
			for name, p := range map[string]SprintPatchIn{
				"name": {Name: Some("renamed")}, "goal": {Goal: Some("rewritten after close")}, "acceptance": {Acceptance: Some("lower bar")},
			} {
				_, err := e.c.Sprints().Update(e.ctx, e.alice, UpdateSprintReq{ProjectID: e.project, SprintID: sp.ID, Version: v, Patch: p})
				be := wantCode(t, err, domain.CodeValidation)
				t.Logf("%s sprint, patch %s: %s %q", end, name, be.Code, be.Message)
			}
			got := e.getSprint(t, sp.ID)
			if got.Name != "s" || got.Goal != "ship it" || got.Acceptance != "a" || got.Version != v {
				t.Fatalf("%s sprint rewritten: %+v", end, got)
			}
			same, err := e.c.Sprints().Update(e.ctx, e.alice, UpdateSprintReq{ProjectID: e.project, SprintID: sp.ID, Version: v,
				Patch: SprintPatchIn{Goal: Some("ship it"), Checkpoint: Value("where we stopped"), EventCursor: Value("c-1")}})
			if err != nil || same.Sprint.Checkpoint == nil || *same.Sprint.Checkpoint != "where we stopped" {
				t.Fatalf("%s sprint: the same goal with a checkpoint: %+v %v", end, same.Sprint, err)
			}
		}
	})
}

// approvedBy is who approved the proposal task, the actor whose move landed it DONE, when it names no
// reviewer: a master completes it (REVIEW), the admin moves it REVIEW → DONE, and a worker then adds a
// note to the DONE task; the activation stamps the admin, in the sprint and its sprint.activated.
// Before, it stamped the proposal's updatedBy: the worker who only wrote the note.
func TestSprintApprovedByIsTheProposalsApprover(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		e.settings(t, map[string]any{"reviewEnabled": true})
		master := e.member(t, e.project, "mia", domain.RoleMaster)
		worker := e.member(t, e.project, "wes", domain.RoleWorker)
		sp := e.sprint(t, e.board.ID)
		proposal, err := e.c.Tasks().Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "proposal",
			CustomFields: map[string]FieldValue{"sprintProposalFor": sp.ID}})
		if err != nil {
			t.Fatal(err)
		}
		pid := proposal.Task.ID
		bound, err := e.c.Sprints().Update(e.ctx, e.alice, UpdateSprintReq{ProjectID: e.project, SprintID: sp.ID, Version: sp.Version,
			Patch: SprintPatchIn{ProposalTaskID: Value(pid)}})
		if err != nil {
			t.Fatal(err)
		}
		c := e.claim(t, master, pid, 0)
		if _, err := e.c.Lifecycle().Complete(e.ctx, master, CompleteTaskReq{ProjectID: e.project, TaskID: pid, LeaseID: c.LeaseID, Deliverable: "the plan"}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.c.Tasks().Move(e.ctx, e.alice, MoveTaskReq{ProjectID: e.project, TaskID: pid, Version: e.get(t, pid).Version, ToStatus: domain.Done}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.c.Context().AddItem(e.ctx, worker, AddContextItemReq{ProjectID: e.project, TaskID: pid, Kind: "note", Body: "read it, looks good"}); err != nil {
			t.Fatal(err)
		}
		done := e.get(t, pid)
		active, err := e.c.Sprints().Update(e.ctx, e.alice, UpdateSprintReq{ProjectID: e.project, SprintID: sp.ID, Version: int64(bound.Sprint.Version),
			Patch: SprintPatchIn{Status: Some(SprintStatus("ACTIVE"))}})
		if err != nil {
			t.Fatal(err)
		}
		feed, err := e.st.ListBoardFeed(e.ctx, e.project, e.board.ID, "", 50)
		if err != nil {
			t.Fatal(err)
		}
		var activated string
		for _, ev := range feed.Events {
			if ev.Type == domain.EventSprintActivated {
				activated = string(ev.Data)
			}
		}
		got := "<nil>"
		if active.Sprint.ApprovedBy != nil {
			got = *active.Sprint.ApprovedBy
		}
		t.Logf("proposal %s %s updatedBy %s; approvedBy %s; sprint.activated %s", pid, done.Status, done.UpdatedBy, got, activated)
		if done.UpdatedBy != worker.Identity || done.Status != domain.Done {
			t.Fatalf("the setup did not leave the worker as updatedBy: %s %s", done.Status, done.UpdatedBy)
		}
		if got != e.alice.Identity || !strings.Contains(activated, `"approvedBy":"alice"`) {
			t.Fatalf("approvedBy %s (event %s), want %s, who moved the proposal to DONE", got, activated, e.alice.Identity)
		}
	})
}
