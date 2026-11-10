package service

import (
	"slices"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// setStatus moves sprint sp to status in the store, unchecked: the test's way to an ACTIVE sprint
// without a proposal.
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
