package service

import (
	"slices"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The P-239 tests: create_task {sprintId} makes the task a member, next_task and claim_next narrow
// to a sprint, and a claim of a sprint member answers its sprint. They run on memstore and on both
// engines (the Postgres leg skips without BATONDECK_TEST_POSTGRES_DSN).

func (e *lcEnv) sprint(t *testing.T, boardID string) store.Sprint {
	t.Helper()
	sp, err := e.c.Sprints().Create(e.ctx, e.alice, CreateSprintReq{ProjectID: e.project, BoardID: boardID, Name: "s", Goal: "ship it", Acceptance: "a"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.st.GetSprint(e.ctx, e.project, sp.Sprint.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (e *lcEnv) taskCount(t *testing.T) int {
	t.Helper()
	page, err := e.st.ListTasks(e.ctx, e.project, store.TaskFilter{BoardID: e.board.ID}, "", 500)
	if err != nil {
		t.Fatal(err)
	}
	return len(page.Tasks)
}

func (e *lcEnv) otherBoard(t *testing.T) store.Board {
	t.Helper()
	b, err := e.st.CreateBoard(e.ctx, store.CreateBoardInput{ProjectID: e.project, Name: "B2", CreatedBy: "alice"}, store.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// AC1: create_task {sprintId} lands the task in the sprint (sprintIds, memberCount 1, the event
// sprint.task_added); an unknown sprint is NOT_FOUND, a sprint on another board VALIDATION and a
// CLOSED one INVALID_TRANSITION, each creating no task.
func TestCreateTaskInSprint(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		sp := e.sprint(t, e.board.ID)
		r, err := e.c.Tasks().Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "member", SprintID: sp.ID})
		if err != nil {
			t.Fatal(err)
		}
		task := e.get(t, r.Task.ID)
		after, err := e.st.GetSprint(e.ctx, e.project, sp.ID)
		if err != nil {
			t.Fatal(err)
		}
		added := ofType(e.events(t, task.ID), domain.EventSprintTaskAdded)
		t.Logf("created %s sprintIds %v (resp %v); sprint memberCount %d; sprint.task_added %d", task.ID, task.SprintIDs, r.Task.SprintIDs, after.MemberCount, len(added))
		if !slices.Equal(task.SprintIDs, []string{sp.ID}) || !slices.Equal(r.Task.SprintIDs, []string{sp.ID}) || after.MemberCount != 1 || len(added) != 1 {
			t.Fatalf("create_task {sprintId} did not make a member")
		}
		if !strings.Contains(string(added[0].Data), `"memberCount":1`) || !strings.Contains(string(added[0].Data), sp.ID) {
			t.Fatalf("sprint.task_added data %s", added[0].Data)
		}
		members, err := e.st.ListSprintMembers(e.ctx, e.project, sp.ID, 10)
		if err != nil || len(members) != 1 || members[0].ID != task.ID {
			t.Fatalf("members %v %v", members, err)
		}

		other := e.otherBoard(t)
		elsewhere := e.sprint(t, other.ID)
		gone := e.sprint(t, e.board.ID)
		closed := domain.SprintClosed
		if _, err := e.st.UpdateSprint(e.ctx, e.project, gone.ID, int64(gone.Version), store.SprintPatch{Status: &closed, UpdatedBy: "alice", UpdatedAt: 1}); err != nil {
			t.Fatal(err)
		}
		before := e.taskCount(t)
		for _, c := range []struct {
			sprintID string
			code     domain.Code
		}{{"S-999", domain.CodeNotFound}, {elsewhere.ID, domain.CodeValidation}, {gone.ID, domain.CodeInvalidTransition}} {
			_, err := e.c.Tasks().Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "refused", SprintID: c.sprintID})
			be := wantCode(t, err, c.code)
			t.Logf("sprintId %s: %s %q", c.sprintID, be.Code, be.Message)
		}
		if n := e.taskCount(t); n != before {
			t.Fatalf("a refused create_task {sprintId} made a task: %d -> %d", before, n)
		}
		if g, _ := e.st.GetSprint(e.ctx, e.project, elsewhere.ID); g.MemberCount != 0 {
			t.Fatalf("other-board sprint memberCount %d", g.MemberCount)
		}
	})
}

// AC3: next_task and claim_next {sprintId} take the sprint's member past a better non-member, answer
// null when no member is claimable, refuse an unknown sprint NOT_FOUND and another board's sprint
// VALIDATION. Without sprintId the better non-member wins, as before.
func TestSelectionInSprint(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		sp := e.sprint(t, e.board.ID)
		urgent, err := e.c.Tasks().Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "urgent", Priority: domain.PriorityUrgent})
		if err != nil {
			t.Fatal(err)
		}
		member, err := e.c.Tasks().Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "member", Priority: domain.PriorityLow, SprintID: sp.ID})
		if err != nil {
			t.Fatal(err)
		}
		lc := e.c.Lifecycle()
		plain, err := lc.Next(e.ctx, e.alice, NextTaskReq{ProjectID: e.project, BoardID: e.board.ID})
		if err != nil || plain.Task == nil || plain.Task.ID != urgent.Task.ID {
			t.Fatalf("next_task without sprintId: %+v %v", plain.Task, err)
		}
		next, err := lc.Next(e.ctx, e.alice, NextTaskReq{ProjectID: e.project, BoardID: e.board.ID, SprintID: sp.ID})
		if err != nil || next.Task == nil || next.Task.ID != member.Task.ID {
			t.Fatalf("next_task {sprintId}: %+v %v", next.Task, err)
		}
		got, err := lc.ClaimNext(e.ctx, e.alice, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, SprintID: sp.ID})
		if err != nil || got.Task == nil || got.Task.ID != member.Task.ID {
			t.Fatalf("claim_next {sprintId}: %+v %v", got.Task, err)
		}
		t.Logf("next_task: %s; next_task {sprintId}: %s; claim_next {sprintId}: %s sprint %+v", plain.Task.ID, next.Task.ID, got.Task.ID, got.Sprint)
		if got.Sprint == nil || got.Sprint.ID != sp.ID || got.Sprint.Goal != "ship it" || got.Sprint.Status != "PROPOSED" {
			t.Fatalf("claim_next sprint brief %+v", got.Sprint)
		}
		// The member is leased: the sprint has no claimable member left, though the board has one.
		none, err := lc.ClaimNext(e.ctx, e.alice, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, SprintID: sp.ID})
		if err != nil || none.Task != nil || none.LeaseID != nil {
			t.Fatalf("claim_next {sprintId} with no claimable member: %+v %v", none, err)
		}
		empty, err := lc.Next(e.ctx, e.alice, NextTaskReq{ProjectID: e.project, BoardID: e.board.ID, SprintID: sp.ID})
		if err != nil || empty.Task != nil {
			t.Fatalf("next_task {sprintId} with no claimable member: %+v %v", empty.Task, err)
		}
		if e.get(t, urgent.Task.ID).Claim != nil {
			t.Fatalf("claim_next {sprintId} leased a non-member")
		}

		elsewhere := e.sprint(t, e.otherBoard(t).ID)
		for _, c := range []struct {
			sprintID string
			code     domain.Code
		}{{"S-999", domain.CodeNotFound}, {elsewhere.ID, domain.CodeValidation}} {
			_, err := lc.Next(e.ctx, e.alice, NextTaskReq{ProjectID: e.project, BoardID: e.board.ID, SprintID: c.sprintID})
			be := wantCode(t, err, c.code)
			_, err = lc.ClaimNext(e.ctx, e.alice, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, SprintID: c.sprintID})
			be2 := wantCode(t, err, c.code)
			t.Logf("sprintId %s: next_task %s %q; claim_next %s", c.sprintID, be.Code, be.Message, be2.Code)
		}
		if e.get(t, urgent.Task.ID).Claim != nil {
			t.Fatalf("a refused claim_next leased a task")
		}
	})
}

// AC4: claim_task and claim_next of a sprint member answer {id, name, goal, status} of its sprint
// (claim_task on the first claim and on the held path, a second claim of the caller's live lease);
// a non-member's claim answers sprint null.
func TestClaimAnswersTheSprint(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		sp := e.sprint(t, e.board.ID)
		member, err := e.c.Tasks().Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "member", SprintID: sp.ID})
		if err != nil {
			t.Fatal(err)
		}
		loose := e.task(t, domain.Todo)
		first := e.claim(t, e.alice, member.Task.ID, 0)
		held := e.claim(t, e.alice, member.Task.ID, 0)
		other := e.claim(t, e.alice, loose.ID, 0)
		t.Logf("claim %s sprint %+v; held claim lease same %v sprint %+v; non-member %s sprint %v",
			member.Task.ID, first.Sprint, held.LeaseID == first.LeaseID, held.Sprint, loose.ID, other.Sprint)
		want := Sprint{ID: sp.ID, Name: "s", Goal: "ship it", Status: "PROPOSED"}
		if first.Sprint == nil || *first.Sprint != want || held.Sprint == nil || *held.Sprint != want || held.LeaseID != first.LeaseID {
			t.Fatalf("claim_task of a member: first %+v held %+v", first.Sprint, held.Sprint)
		}
		if other.Sprint != nil {
			t.Fatalf("claim_task of a non-member answered sprint %+v", other.Sprint)
		}
		// claim_next without sprintId: the board's one claimable task each time, member then non-member.
		lc := e.c.Lifecycle()
		member2, err := e.c.Tasks().Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "member 2", SprintID: sp.ID})
		if err != nil {
			t.Fatal(err)
		}
		inSprint, err := lc.ClaimNext(e.ctx, e.alice, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID})
		if err != nil || inSprint.Task == nil || inSprint.Task.ID != member2.Task.ID {
			t.Fatalf("claim_next: %+v %v", inSprint.Task, err)
		}
		loose2 := e.task(t, domain.Todo)
		outside, err := lc.ClaimNext(e.ctx, e.alice, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID})
		if err != nil || outside.Task == nil || outside.Task.ID != loose2.ID {
			t.Fatalf("claim_next: %+v %v", outside.Task, err)
		}
		t.Logf("claim_next %s sprint %+v; claim_next %s sprint %v", member2.Task.ID, inSprint.Sprint, loose2.ID, outside.Sprint)
		if inSprint.Sprint == nil || *inSprint.Sprint != want || outside.Sprint != nil {
			t.Fatalf("claim_next sprint: member %+v non-member %+v", inSprint.Sprint, outside.Sprint)
		}
	})
}
