//go:build e2e

package e2e

import (
	"testing"
)

func init() {
	register(scenario{area: "comments", name: "thread-and-mention", tools: []string{"add_comment", "list_comments"}, run: commentsThread})
	register(scenario{area: "followups", name: "reopen-and-ack", tools: []string{"add_follow_up", "ack_follow_up", "list_follow_ups"}, run: followUps})
}

// The worker comments with an @mention of worker2's login (second@e2e.test, by its local part),
// which notifies worker2 (a label aliases only a live worker session, and the suite runs none);
// worker2 replies in the thread. The comments list oldest first and page by one. The viewer cannot comment and an
// empty body is refused.
func commentsThread(t *testing.T, e *env) {
	p := e.project(t, "Comments")
	id := S(e.task(t, p, "discussed", nil), "id")
	key := uniq("c")
	first := O(e.worker.Call(t, "add_comment", M{"projectId": p.id, "taskId": id, "body": "@second can you **review** the lexer?", "idempotencyKey": key}), "comment")
	want(t, S(first, "id") != "" && S(first, "author") == e.srv.ids["worker"] && len(A(first, "mentions")) == 1, "add_comment answered %s", js(first))
	replay := O(e.worker.Call(t, "add_comment", M{"projectId": p.id, "taskId": id, "body": "@second can you **review** the lexer?", "idempotencyKey": key}), "comment")
	want(t, S(replay, "id") == S(first, "id"), "a retried add_comment made %s, want %s", S(replay, "id"), S(first, "id"))

	mentions, _ := notificationsAbout(t, e.worker2, p, id, "mention")
	want(t, len(mentions) == 1 && S(mentions[0], "data.commentId") == S(first, "id"), "the @mention notified worker2 with %s", js(mentions))
	reply := O(e.worker2.Call(t, "add_comment", M{"projectId": p.id, "taskId": id, "body": "Looks right to me.", "parentCommentId": S(first, "id")}), "comment")
	want(t, S(reply, "parentCommentId") == S(first, "id"), "the reply answered %s", js(reply))

	all := A(e.viewer.Call(t, "list_comments", M{"projectId": p.id, "taskId": id}), "comments")
	want(t, js(ids(all, "id")) == js([]string{S(first, "id"), S(reply, "id")}), "list_comments oldest first: %v", ids(all, "id"))
	page := e.viewer.Call(t, "list_comments", M{"projectId": p.id, "taskId": id, "limit": 1})
	want(t, len(A(page, "comments")) == 1 && S(page, "nextCursor") != "", "list_comments limit 1: %s", js(page))
	rest := e.viewer.Call(t, "list_comments", M{"projectId": p.id, "taskId": id, "limit": 1, "cursor": S(page, "nextCursor")})
	want(t, js(ids(A(rest, "comments"), "id")) == js([]string{S(reply, "id")}), "the second page: %s", js(rest))

	e.viewer.Refuse(t, "add_comment", M{"projectId": p.id, "taskId": id, "body": "a viewer's comment"}, "FORBIDDEN")
	e.worker.Refuse(t, "add_comment", M{"projectId": p.id, "taskId": id, "body": ""}, "VALIDATION")
	e.outsider.Refuse(t, "list_comments", M{"projectId": p.id, "taskId": id}, "FORBIDDEN")
}

// A task the worker is assigned and completed (review off) is reopened by the master's follow-up:
// back to TODO, directed at the worker. On a TODO task a follow-up is recorded only. Only the target
// or a project admin acks, after which it leaves the open list. A worker cannot direct one.
func followUps(t *testing.T, e *env) {
	p := e.project(t, "Followups")
	e.admin.Call(t, "update_settings", M{"projectId": p.id, "reviewEnabled": false})
	id := S(e.task(t, p, "finished too soon", nil), "id")
	e.master.Call(t, "update_task", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id), "patch": M{"assignee": e.srv.ids["worker"]}})
	lease := claim(t, e.worker, p, id)
	e.worker.Call(t, "complete_task", M{"projectId": p.id, "taskId": id, "leaseId": lease, "deliverable": "v1"})
	want(t, S(e.get(t, p, id), "status") == "DONE", "the task did not complete")

	e.worker.Refuse(t, "add_follow_up", M{"projectId": p.id, "taskId": id, "body": "redo", "reopen": true}, "FORBIDDEN")
	fu := e.master.Call(t, "add_follow_up", M{"projectId": p.id, "taskId": id, "body": "The parser drops the last token.", "reopen": true})
	f := O(fu, "followUp")
	want(t, S(f, "target") == e.srv.ids["worker"] && B(f, "reopened") && S(fu, "task.status") == "TODO", "add_follow_up answered %s", js(fu))
	want(t, S(e.get(t, p, id), "status") == "TODO", "the reopened task is %s", S(e.get(t, p, id), "status"))

	todo := S(e.task(t, p, "not started", nil), "id")
	rec := e.master.Call(t, "add_follow_up", M{"projectId": p.id, "taskId": todo, "body": "Mind the edge case.", "reopen": true})
	want(t, !B(rec, "followUp.reopened") && S(e.get(t, p, todo), "status") == "TODO", "a follow-up on a TODO task: %s", js(rec))

	open := A(e.viewer.Call(t, "list_follow_ups", M{"projectId": p.id, "taskId": id, "openOnly": true}), "followUps")
	want(t, len(open) == 1 && S(open[0], "id") == S(f, "id"), "list_follow_ups openOnly: %s", js(open))
	e.outsider.Refuse(t, "list_follow_ups", M{"projectId": p.id, "taskId": id}, "FORBIDDEN")
	ctx := e.worker.Call(t, "get_task_context", M{"projectId": p.id, "taskId": id})
	want(t, N(ctx, "openFollowUpCount") == 1, "get_task_context openFollowUpCount: %v", N(ctx, "openFollowUpCount"))

	e.worker2.Refuse(t, "ack_follow_up", M{"projectId": p.id, "taskId": id, "followUpId": S(f, "id")}, "FORBIDDEN")
	ack := O(e.worker.Call(t, "ack_follow_up", M{"projectId": p.id, "taskId": id, "followUpId": S(f, "id")}), "followUp")
	want(t, S(ack, "status") != "open", "ack_follow_up answered %s", js(ack))
	want(t, len(A(e.viewer.Call(t, "list_follow_ups", M{"projectId": p.id, "taskId": id, "openOnly": true}), "followUps")) == 0, "an acked follow-up is still open")
	want(t, len(A(e.viewer.Call(t, "list_follow_ups", M{"projectId": p.id, "taskId": id}), "followUps")) == 1, "the acked follow-up left the full list")
	e.worker.Refuse(t, "ack_follow_up", M{"projectId": p.id, "taskId": id, "followUpId": "F-nosuch"}, "NOT_FOUND")
}
