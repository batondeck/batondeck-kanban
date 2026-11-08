//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

func init() {
	register(scenario{area: "tasks", name: "create-get-update", tools: []string{"create_task", "get_task", "update_task"}, run: taskCreateGetUpdate})
	register(scenario{area: "tasks", name: "move", tools: []string{"move_task"}, run: taskMove})
	register(scenario{area: "tasks", name: "list", tools: []string{"list_tasks"}, run: taskList})
	register(scenario{area: "tasks", name: "delete", tools: []string{"delete_task"}, run: taskDelete})
}

// The master creates a task (TODO, in the first TODO column), the worker reads it, and the master
// updates it; each write is read back through get_task and REST. A worker cannot create or update,
// a stale version is STALE, and null clears a nullable field.
func taskCreateGetUpdate(t *testing.T, e *env) {
	p := e.project(t, "Tasks")
	created := O(e.master.Call(t, "create_task", M{"projectId": p.id, "boardId": p.board, "title": "Write the parser",
		"description": "Parse the feed.", "priority": "high", "labels": []any{"backend"}, "requiredCapabilities": []any{"go"},
		"modelHint": M{"model": "opus", "effort": "high", "rationale": "subtle"}, "customFields": M{"ticket": "BD-1"}, "idempotencyKey": uniq("t")}), "task")
	id := S(created, "id")
	want(t, S(created, "status") == "TODO" && S(created, "columnId") == p.cols["TODO"], "create_task answered %s", js(created))

	got := O(e.worker.Call(t, "get_task", M{"projectId": p.id, "taskId": id}), "task")
	want(t, S(got, "title") == "Write the parser" && S(got, "priority") == "high" && S(got, "labels.0") == "backend" &&
		S(got, "modelHint.model") == "opus" && S(got, "customFields.ticket") == "BD-1", "get_task answered %s", js(got))
	code, rest := e.restTask(t, p, id)
	want(t, code == 200 && S(rest, "task.id") == id, "GET the task over REST: %d %s", code, js(rest))

	e.worker.Refuse(t, "create_task", M{"projectId": p.id, "boardId": p.board, "title": "not mine to make"}, "FORBIDDEN")
	e.master.Refuse(t, "create_task", M{"projectId": p.id, "boardId": p.board, "title": ""}, "VALIDATION")
	e.master.Refuse(t, "create_task", M{"projectId": p.id, "boardId": p.board, "title": "bad priority", "priority": "critical"}, "VALIDATION")
	e.outsider.Refuse(t, "get_task", M{"projectId": p.id, "taskId": id}, "FORBIDDEN")
	e.worker.Refuse(t, "get_task", M{"projectId": p.id, "taskId": "T-nosuchtask"}, "NOT_FOUND")

	v := N(got, "version")
	upd := O(e.master.Call(t, "update_task", M{"projectId": p.id, "taskId": id, "version": v, "patch": M{
		"title": "Write the feed parser", "priority": "urgent", "assignee": e.srv.ids["worker"], "modelHint": nil}}), "task")
	want(t, N(upd, "version") > v && S(upd, "title") == "Write the feed parser", "update_task answered %s", js(upd))
	after := e.get(t, p, id)
	want(t, S(after, "priority") == "urgent" && S(after, "assignee") == e.srv.ids["worker"] && after["modelHint"] == nil,
		"get_task after update_task: %s", js(after))
	e.master.Refuse(t, "update_task", M{"projectId": p.id, "taskId": id, "version": v, "patch": M{"title": "stale"}}, "STALE")
	e.worker.Refuse(t, "update_task", M{"projectId": p.id, "taskId": id, "version": N(after, "version"), "patch": M{"title": "mine now"}}, "FORBIDDEN")
	want(t, S(e.get(t, p, id), "title") == "Write the feed parser", "a refused update changed the title")
}

// The master moves a task along the fixed table and reorders it, with toColumnId and with an order
// alone; an illegal jump is INVALID_TRANSITION, a stale version STALE, a live lease another holds
// CONFLICT_LOCKED (a reorder alone too), and the worker may not move tasks. Each move is read back
// through get_task and the board's counts.
func taskMove(t *testing.T, e *env) {
	p := e.project(t, "Moves")
	id := S(e.task(t, p, "movable", nil), "id")
	e.master.Refuse(t, "move_task", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id), "toStatus": "DONE"}, "INVALID_TRANSITION")
	e.worker.Refuse(t, "move_task", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id), "toStatus": "IN_PROGRESS"}, "FORBIDDEN")

	v := e.version(t, p, id)
	moved := O(e.master.Call(t, "move_task", M{"projectId": p.id, "taskId": id, "version": v, "toStatus": "IN_PROGRESS"}), "task")
	want(t, S(moved, "status") == "IN_PROGRESS" && S(moved, "columnId") == p.cols["IN_PROGRESS"], "move_task answered %s", js(moved))
	e.master.Refuse(t, "move_task", M{"projectId": p.id, "taskId": id, "version": v, "toStatus": "TODO"}, "STALE")
	board := e.viewer.Call(t, "get_board", M{"projectId": p.id, "boardId": p.board})
	want(t, N(board, "taskCounts."+p.cols["IN_PROGRESS"]) == 1, "get_board counts after the move: %s", js(O(board, "taskCounts")))
	// By column: back to TODO through its column id.
	back := O(e.master.Call(t, "move_task", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id), "toColumnId": p.cols["TODO"]}), "task")
	want(t, S(back, "status") == "TODO", "move_task by toColumnId answered %s", js(back))

	// Reorder: the second task moved above the first in its own column, by a fractional order key
	// that sorts before it.
	other := S(e.task(t, p, "second", nil), "id")
	first := e.get(t, p, id)
	reordered := O(e.master.Call(t, "move_task", M{"projectId": p.id, "taskId": other, "version": e.version(t, p, other), "toColumnId": p.cols["TODO"], "order": "Zz"}), "task")
	want(t, S(reordered, "order") < S(first, "order"), "the reorder left order %q not before %q", S(reordered, "order"), S(first, "order"))
	e.master.Refuse(t, "move_task", M{"projectId": p.id, "taskId": other, "version": e.version(t, p, other), "toColumnId": p.cols["TODO"], "order": "0"}, "VALIDATION")
	// A reorder by its order key alone, as the description says: the first task back above the
	// second, in the same column and status, read back through get_task. Naming none of toColumnId,
	// toStatus and order is refused.
	alone := O(e.master.Call(t, "move_task", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id), "order": "Zy"}), "task")
	gotFirst, gotOther := e.get(t, p, id), e.get(t, p, other)
	want(t, S(alone, "order") == "Zy" && S(gotFirst, "order") == "Zy" && S(gotFirst, "order") < S(gotOther, "order") &&
		S(gotFirst, "status") == "TODO" && S(gotFirst, "columnId") == p.cols["TODO"], "move_task with order alone answered %s, reads back %s (the other at %q)", js(alone), js(gotFirst), S(gotOther, "order"))
	e.master.Refuse(t, "move_task", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id)}, "VALIDATION")
	desc := description(t, e.master, "move_task")
	want(t, strings.Contains(desc, "`order` alone: in its column"), "move_task's description does not say an order alone reorders in its column: %q", desc)

	// Another holder's live lease refuses a move, and a reorder alone.
	claim(t, e.worker, p, id)
	e.master.Refuse(t, "move_task", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id), "toStatus": "TODO"}, "CONFLICT_LOCKED")
	e.master.Refuse(t, "move_task", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id), "order": "Zx"}, "CONFLICT_LOCKED")
	want(t, S(e.get(t, p, id), "status") == "IN_PROGRESS", "a refused move changed the status")
}

// list_tasks filters by status, label and assignee (null is the unassigned) and pages with limit
// and nextCursor; a viewer may list, an outsider may not, and a limit past 200 is refused.
func taskList(t *testing.T, e *env) {
	p := e.project(t, "Listing")
	a := S(e.task(t, p, "alpha", M{"labels": []any{"ui"}}), "id")
	b := S(e.task(t, p, "beta", nil), "id")
	c := S(e.task(t, p, "gamma", nil), "id")
	e.master.Call(t, "update_task", M{"projectId": p.id, "taskId": b, "version": e.version(t, p, b), "patch": M{"assignee": e.srv.ids["worker2"]}})
	claim(t, e.worker, p, c)

	list := func(args M) []string {
		args["projectId"], args["boardId"] = p.id, p.board
		return ids(A(e.viewer.Call(t, "list_tasks", args), "tasks"), "id")
	}
	all := list(M{})
	want(t, len(all) == 3, "list_tasks: %v, want 3", all)
	want(t, js(list(M{"status": "IN_PROGRESS"})) == js([]string{c}), "list_tasks status IN_PROGRESS: %v", list(M{"status": "IN_PROGRESS"}))
	want(t, js(list(M{"label": "ui"})) == js([]string{a}), "list_tasks label ui: %v", list(M{"label": "ui"}))
	want(t, js(list(M{"assignee": e.srv.ids["worker2"]})) == js([]string{b}), "list_tasks assignee: %v", list(M{"assignee": e.srv.ids["worker2"]}))
	unassigned := list(M{"assignee": nil})
	want(t, has(unassigned, a) && has(unassigned, c) && !has(unassigned, b), "list_tasks assignee null: %v", unassigned)

	var paged []string
	cursor := ""
	for i := 0; i < 5; i++ {
		args := M{"projectId": p.id, "boardId": p.board, "limit": 1}
		if cursor != "" {
			args["cursor"] = cursor
		}
		page := e.viewer.Call(t, "list_tasks", args)
		want(t, len(A(page, "tasks")) <= 1, "a limit 1 page held %d tasks", len(A(page, "tasks")))
		paged = append(paged, ids(A(page, "tasks"), "id")...)
		if cursor = S(page, "nextCursor"); cursor == "" {
			break
		}
	}
	want(t, len(paged) == 3 && has(paged, a) && has(paged, b) && has(paged, c), "paging by 1 answered %v", paged)

	e.viewer.Refuse(t, "list_tasks", M{"projectId": p.id, "boardId": p.board, "limit": 201}, "VALIDATION")
	e.outsider.Refuse(t, "list_tasks", M{"projectId": p.id, "boardId": p.board}, "FORBIDDEN")
}

// The master deletes a task with its subtask: neither reads afterwards, over MCP or REST. A worker
// cannot delete and a stale version is STALE.
func taskDelete(t *testing.T, e *env) {
	p := e.project(t, "Deleting")
	id := S(e.task(t, p, "to delete", nil), "id")
	sub := S(O(e.master.Call(t, "add_subtask", M{"projectId": p.id, "parentTaskId": id, "title": "child"}), "task"), "id")
	v := e.version(t, p, id)
	e.worker.Refuse(t, "delete_task", M{"projectId": p.id, "taskId": id, "version": v}, "FORBIDDEN")
	e.master.Refuse(t, "delete_task", M{"projectId": p.id, "taskId": id, "version": v - 1}, "STALE")

	got := e.master.Call(t, "delete_task", M{"projectId": p.id, "taskId": id, "version": v})
	want(t, B(got, "ok"), "delete_task answered %s", js(got))
	e.admin.Refuse(t, "get_task", M{"projectId": p.id, "taskId": id}, "NOT_FOUND")
	e.admin.Refuse(t, "get_task", M{"projectId": p.id, "taskId": sub}, "NOT_FOUND")
	code, _ := e.restTask(t, p, id)
	want(t, code == 404, "GET the deleted task over REST answered %d", code)
	want(t, len(A(e.viewer.Call(t, "list_tasks", M{"projectId": p.id, "boardId": p.board}), "tasks")) == 0, "list_tasks still lists a deleted task")
}
