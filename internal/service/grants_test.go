package service

import (
	"context"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// A maxPriority and labels grant judges what a call writes as well as the task it loads: a capped
// member creates within its cap and labels, and cannot raise a task above the cap or relabel it out
// of the grant, from create_task, update_task, add_subtask or orchestrate_subtasks.
func TestAWriteStaysWithinItsGrant(t *testing.T) {
	grant := domain.CapabilityGrant{MaxPriority: domain.PriorityNormal, Labels: []string{"frontend"}}
	f := newFixture(t, Options{Grants: func(_ context.Context, _, identity string) (domain.CapabilityGrant, bool, error) {
		return grant, identity == "mona", nil
	}})
	mona := f.addMember(t, "mona", domain.RoleMaster)
	created, err := f.c.Tasks().Create(f.ctx, mona, CreateTaskReq{ProjectID: f.project.ID, BoardID: f.board.ID, Title: "t",
		Priority: domain.PriorityLow, Labels: []string{"frontend"}})
	t.Logf("create_task low/frontend under {maxPriority normal, labels frontend}: %v", err)
	if err != nil {
		t.Fatalf("a create within the cap and the labels is refused: %v", err)
	}
	task := created.Task
	_, err = f.c.Tasks().Create(f.ctx, mona, CreateTaskReq{ProjectID: f.project.ID, BoardID: f.board.ID, Title: "t",
		Priority: domain.PriorityUrgent, Labels: []string{"frontend"}})
	wantCode(t, err, domain.CodeForbidden)

	_, err = f.c.Tasks().Update(f.ctx, mona, UpdateTaskReq{ProjectID: f.project.ID, TaskID: task.ID, Version: int64(task.Version),
		Patch: TaskPatch{Priority: domain.PriorityUrgent}})
	t.Logf("update_task priority urgent: %v", err)
	wantCode(t, err, domain.CodeForbidden)
	_, err = f.c.Tasks().Update(f.ctx, mona, UpdateTaskReq{ProjectID: f.project.ID, TaskID: task.ID, Version: int64(task.Version),
		Patch: TaskPatch{Labels: []string{"backend"}}})
	t.Logf("update_task labels [backend]: %v", err)
	wantCode(t, err, domain.CodeForbidden)
	_, err = f.c.Context().AddSubtask(f.ctx, mona, AddSubtaskReq{ProjectID: f.project.ID, ParentTaskID: task.ID, Title: "c",
		Priority: domain.PriorityUrgent})
	t.Logf("add_subtask urgent: %v", err)
	wantCode(t, err, domain.CodeForbidden)
	claim, err := f.c.Lifecycle().Claim(f.ctx, mona, ClaimTaskReq{ProjectID: f.project.ID, TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.c.Ranking().Orchestrate(f.ctx, mona, OrchestrateSubtasksReq{ProjectID: f.project.ID, ParentTaskID: task.ID,
		LeaseID: claim.LeaseID, Children: []OrchestrateChild{{Title: "a"}, {Title: "b", Priority: domain.PriorityUrgent}}})
	t.Logf("orchestrate_subtasks with an urgent child: %v", err)
	wantCode(t, err, domain.CodeForbidden)

	// Positive controls: a write that stays within the grant passes.
	got, err := f.c.Tasks().Update(f.ctx, mona, UpdateTaskReq{ProjectID: f.project.ID, TaskID: task.ID, Version: int64(claim.Task.Version),
		Patch: TaskPatch{Priority: domain.PriorityNormal, Labels: []string{"frontend", "backend"}}})
	if err != nil || got.Task.Priority != domain.PriorityNormal {
		t.Fatalf("update_task within the grant: %+v (err %v)", got.Task, err)
	}
	if _, err := f.c.Context().AddSubtask(f.ctx, mona, AddSubtaskReq{ProjectID: f.project.ID, ParentTaskID: task.ID, Title: "c"}); err != nil {
		t.Fatalf("add_subtask at the default priority: %v", err)
	}
}

// detect_anomalies under a boards grant is judged on the board it scans: the boardId it names, or
// the board of the task it names. It was refused "board:*" on every board, the granted one too.
func TestDetectAnomaliesUnderABoardsGrant(t *testing.T) {
	f := newFixture(t, Options{})
	other, err := f.st.CreateBoard(f.ctx, store.CreateBoardInput{ProjectID: f.project.ID, Name: "B2", CreatedBy: "alice"}, store.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	grant := domain.CapabilityGrant{Boards: []string{f.board.ID}}
	old := f.c // newFixture's cleanup closes the Core f.c names then
	f.c = New(f.st, Options{Grants: func(_ context.Context, _, identity string) (domain.CapabilityGrant, bool, error) {
		return grant, identity == "mona", nil
	}, ToolCallFlush: -1})
	if err := old.Close(f.ctx); err != nil {
		t.Fatal(err)
	}
	mona := f.addMember(t, "mona", domain.RoleMaster)
	onGranted := f.task(t, domain.Todo, nil)
	onOther, err := f.st.CreateTask(f.ctx, store.CreateTaskInput{ProjectID: f.project.ID, BoardID: other.ID, ColumnID: other.Columns[0].ID,
		Title: "t", Status: other.Columns[0].Status, OrderKey: "a0", CreatedBy: "alice", CreatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.c.Anomalies().Detect(f.ctx, mona, DetectAnomaliesReq{ProjectID: f.project.ID, BoardID: f.board.ID})
	t.Logf("detect_anomalies on the granted board: %v", err)
	if err != nil {
		t.Fatalf("detect_anomalies on the granted board is refused: %v", err)
	}
	if _, err := f.c.Anomalies().Detect(f.ctx, mona, DetectAnomaliesReq{ProjectID: f.project.ID, TaskID: onGranted.ID}); err != nil {
		t.Fatalf("detect_anomalies on a task of the granted board is refused: %v", err)
	}
	_, err = f.c.Anomalies().Detect(f.ctx, mona, DetectAnomaliesReq{ProjectID: f.project.ID, BoardID: other.ID})
	if e := wantCode(t, err, domain.CodeForbidden); e.Details["requiredCapability"] != "board:"+other.ID {
		t.Fatalf("details %+v, want board:%s", e.Details, other.ID)
	}
	_, err = f.c.Anomalies().Detect(f.ctx, mona, DetectAnomaliesReq{ProjectID: f.project.ID, TaskID: onOther.ID})
	if e := wantCode(t, err, domain.CodeForbidden); e.Details["requiredCapability"] != "board:"+other.ID {
		t.Fatalf("details %+v, want board:%s", e.Details, other.ID)
	}
}
