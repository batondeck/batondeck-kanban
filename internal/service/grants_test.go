package service

import (
	"context"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
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
