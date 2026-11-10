package service

import (
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// add_comment's parentCommentId names a comment of the same task: a missing one, or one on another
// task, is NOT_FOUND and writes nothing, and the argument is bounded like every other id.
func TestAddCommentParentIsACommentOfTheTask(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		cm := e.c.Comments()
		task, other := e.task(t, domain.Todo), e.task(t, domain.Todo)
		add := func(taskID, parent string) (AddCommentResp, error) {
			return cm.AddComment(e.ctx, e.alice, AddCommentReq{ProjectID: e.project, TaskID: taskID, Body: "b", ParentCommentID: parent})
		}
		root, err := add(task.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		elsewhere, err := add(other.ID, "")
		if err != nil {
			t.Fatal(err)
		}
		reply, err := add(task.ID, root.Comment.ID)
		if err != nil || reply.Comment.ParentCommentID == nil || *reply.Comment.ParentCommentID != root.Comment.ID {
			t.Fatalf("a reply to a comment of the task: %+v, %v", reply, err)
		}
		_, err = add(task.ID, "CM-does-not-exist")
		wantCode(t, err, domain.CodeNotFound)
		_, err = add(task.ID, elsewhere.Comment.ID)
		wantCode(t, err, domain.CodeNotFound)
		_, err = add(task.ID, strings.Repeat("x", 100_000))
		wantCode(t, err, domain.CodeValidation)
		page, err := cm.ListComments(e.ctx, e.alice, ListCommentsReq{ProjectID: e.project, TaskID: task.ID})
		if err != nil || len(page.Comments) != 2 {
			t.Fatalf("the task's comments %+v, %v: want the root and its reply only", page, err)
		}
	})
}
