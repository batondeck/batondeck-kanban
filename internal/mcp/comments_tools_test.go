package mcp_test

import (
	"testing"
)

// TestCommentsAddComment: add_comment's happy path (a reply, the @mention of a member resolved and
// of a non-member dropped), the task's version unmoved (conductor's addComment writes beside the
// task), the idempotent replay, and each declared error: VALIDATION (an empty body, an unknown
// key), NOT_FOUND for a missing task, FORBIDDEN across projects.
func TestCommentsAddComment(t *testing.T) {
	notesStacks(t, func(t *testing.T, r *notesRig) {
		task := r.task("discuss", "")
		id := str(task["id"])
		first := obj(r.ada.call("add_comment", map[string]any{"projectId": r.p, "taskId": id, "body": "first"})["comment"])
		reply := obj(r.will.call("add_comment", map[string]any{"projectId": r.p, "taskId": id, "body": "@u-ada agreed, @nobody",
			"parentCommentId": str(first["id"]), "idempotencyKey": "c-1"})["comment"])
		ms, _ := reply["mentions"].([]any)
		if reply["author"] != "u-will" || reply["parentCommentId"] != first["id"] || len(ms) != 1 || ms[0] != "u-ada" || first["parentCommentId"] != nil {
			t.Fatalf("add_comment = %v (first %v)", reply, first)
		}
		if v := r.get(id)["version"]; v != task["version"] {
			t.Fatalf("the task moved from version %v to %v on a comment; conductor's comments leave it", task["version"], v)
		}
		replay := obj(r.will.call("add_comment", map[string]any{"projectId": r.p, "taskId": id, "body": "@u-ada agreed, @nobody",
			"parentCommentId": str(first["id"]), "idempotencyKey": "c-1"})["comment"])
		if replay["id"] != reply["id"] {
			t.Fatalf("the replay minted %v, want %v", replay["id"], reply["id"])
		}
		t.Logf("%s add_comment: happy path, a reply mentioning u-ada, version unmoved, replay", r.engine)
		r.wantCode(r.ada, "add_comment", map[string]any{"projectId": r.p, "taskId": id, "body": ""}, "VALIDATION", "an empty body")
		r.wantCode(r.ada, "add_comment", map[string]any{"projectId": r.p, "taskId": id, "body": "x", "unexpected": true}, "VALIDATION", "an unknown key")
		r.wantCode(r.ada, "add_comment", map[string]any{"projectId": r.p, "taskId": "T-404", "body": "x"}, "NOT_FOUND", "a missing task")
		r.wantCode(r.grace, "add_comment", map[string]any{"projectId": r.p, "taskId": id, "body": "x"}, "FORBIDDEN", "another project's caller")
	})
}

// TestCommentsListComments: list_comments' happy path (oldest first, paged by cursor) and each
// declared error: VALIDATION (a malformed cursor, a limit over 200), NOT_FOUND for a missing task,
// FORBIDDEN across projects.
func TestCommentsListComments(t *testing.T) {
	notesStacks(t, func(t *testing.T, r *notesRig) {
		id := str(r.task("thread", "")["id"])
		for _, b := range []string{"one", "two", "three"} {
			r.ada.call("add_comment", map[string]any{"projectId": r.p, "taskId": id, "body": b})
			tick()
		}
		page := r.max.call("list_comments", map[string]any{"projectId": r.p, "taskId": id, "limit": 2})
		cs := page["comments"].([]any)
		if len(cs) != 2 || obj(cs[0])["body"] != "one" || obj(cs[1])["body"] != "two" || str(page["nextCursor"]) == "" {
			t.Fatalf("list_comments page 1 = %v", page)
		}
		page = r.max.call("list_comments", map[string]any{"projectId": r.p, "taskId": id, "limit": 2, "cursor": page["nextCursor"]})
		if cs := page["comments"].([]any); len(cs) != 1 || obj(cs[0])["body"] != "three" || page["nextCursor"] != nil {
			t.Fatalf("list_comments page 2 = %v", page)
		}
		t.Logf("%s list_comments: happy path, oldest first over two pages", r.engine)
		r.wantCode(r.ada, "list_comments", map[string]any{"projectId": r.p, "taskId": id, "cursor": "nope!"}, "VALIDATION", "a malformed cursor")
		r.wantCode(r.ada, "list_comments", map[string]any{"projectId": r.p, "taskId": id, "limit": 201}, "VALIDATION", "a limit over 200")
		r.wantCode(r.ada, "list_comments", map[string]any{"projectId": r.p, "taskId": "T-404"}, "NOT_FOUND", "a missing task")
		r.wantCode(r.grace, "list_comments", map[string]any{"projectId": r.p, "taskId": id}, "FORBIDDEN", "another project's caller")
	})
}
