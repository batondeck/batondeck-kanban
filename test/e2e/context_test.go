//go:build e2e

package e2e

import (
	"testing"
)

func init() {
	register(scenario{area: "context", name: "items-and-summary", tools: []string{"add_context_item", "get_task_context", "set_summary"}, run: ctxItemsAndSummary})
	register(scenario{area: "context", name: "memory", tools: []string{"write_memory", "read_memory", "recall_memory"}, run: ctxMemory})
	register(scenario{area: "context", name: "dependencies", tools: []string{"add_dependency", "remove_dependency"}, run: ctxDependencies})
	register(scenario{area: "context", name: "subtasks", tools: []string{"add_subtask", "list_subtasks"}, run: ctxSubtasks})
}

// The worker adds a decision and a note (a retry replays the item), the viewer reads them in the
// task's briefing, and the worker replaces the rolling summary, read back through get_task and the
// briefing. The viewer cannot write, an unknown kind is refused, a stale summary is STALE, and an
// outsider reads nothing.
func ctxItemsAndSummary(t *testing.T, e *env) {
	p := e.project(t, "Context")
	id := S(e.task(t, p, "briefed", nil), "id")
	key := uniq("item")
	dec := O(e.worker.Call(t, "add_context_item", M{"projectId": p.id, "taskId": id, "kind": "decision", "body": "Use a hand-written lexer.", "idempotencyKey": key}), "item")
	again := O(e.worker.Call(t, "add_context_item", M{"projectId": p.id, "taskId": id, "kind": "decision", "body": "Use a hand-written lexer.", "idempotencyKey": key}), "item")
	want(t, S(dec, "id") != "" && S(again, "id") == S(dec, "id"), "a retried add_context_item answered %s, want the original %s", js(again), js(dec))
	e.worker.Call(t, "add_context_item", M{"projectId": p.id, "taskId": id, "kind": "note", "body": "The feed is UTF-8."})
	e.viewer.Refuse(t, "add_context_item", M{"projectId": p.id, "taskId": id, "kind": "note", "body": "a viewer's note"}, "FORBIDDEN")
	e.worker.Refuse(t, "add_context_item", M{"projectId": p.id, "taskId": id, "kind": "rumour", "body": "x"}, "VALIDATION")

	ctx := e.viewer.Call(t, "get_task_context", M{"projectId": p.id, "taskId": id, "include": []any{"items", "summary"}})
	kinds := map[string]string{}
	for _, it := range A(ctx, "items") {
		kinds[S(it, "kind")] = S(it, "body")
	}
	want(t, kinds["decision"] == "Use a hand-written lexer." && kinds["note"] == "The feed is UTF-8.", "get_task_context items: %s", js(A(ctx, "items")))
	e.outsider.Refuse(t, "get_task_context", M{"projectId": p.id, "taskId": id}, "FORBIDDEN")
	e.viewer.Refuse(t, "get_task_context", M{"projectId": p.id, "taskId": "T-nosuchtask"}, "NOT_FOUND")

	v := e.version(t, p, id)
	sum := O(e.worker.Call(t, "set_summary", M{"projectId": p.id, "taskId": id, "version": v, "summary": "Lexer done; parser next."}), "task")
	want(t, N(sum, "version") > v && S(sum, "contextSummary") == "Lexer done; parser next.", "set_summary answered %s", js(sum))
	want(t, S(e.get(t, p, id), "contextSummary") == "Lexer done; parser next.", "get_task after set_summary: %s", S(e.get(t, p, id), "contextSummary"))
	brief := e.viewer.Call(t, "get_task_context", M{"projectId": p.id, "taskId": id, "include": []any{"summary"}})
	want(t, S(brief, "summary") == "Lexer done; parser next.", "get_task_context summary after set_summary: %q", S(brief, "summary"))
	e.worker.Refuse(t, "set_summary", M{"projectId": p.id, "taskId": id, "version": v, "summary": "stale"}, "STALE")
	e.viewer.Refuse(t, "set_summary", M{"projectId": p.id, "taskId": id, "version": N(sum, "version"), "summary": "mine"}, "FORBIDDEN")
}

// Memory in each scope: task memory is read by any member through read_memory, agent memory is
// private to its writer, project memory outlives the task and is recalled by another member, and
// agent_global memory is recalled without a project. The viewer cannot write, task scope needs a
// taskId, and recalling project memory needs a projectId.
func ctxMemory(t *testing.T, e *env) {
	p := e.project(t, "Memory")
	id := S(e.task(t, p, "remembering", nil), "id")
	w := e.worker.Call(t, "write_memory", M{"projectId": p.id, "taskId": id, "scope": "task", "key": "plan", "value": M{"steps": []any{"lex", "parse"}}})
	want(t, S(w, "entryId") != "", "write_memory answered %s", js(w))
	got := A(e.viewer.Call(t, "read_memory", M{"projectId": p.id, "taskId": id, "key": "plan"}), "entries")
	want(t, len(got) == 1 && S(got[0], "value.steps.1") == "parse" && S(got[0], "scope") == "task", "read_memory by key as the viewer: %s", js(got))

	e.worker.Call(t, "write_memory", M{"projectId": p.id, "taskId": id, "scope": "agent", "key": "scratch", "value": "only mine"})
	mine := A(e.worker.Call(t, "read_memory", M{"projectId": p.id, "taskId": id, "scope": "agent"}), "entries")
	want(t, len(mine) == 1 && S(mine[0], "value") == "only mine", "read_memory agent scope as its writer: %s", js(mine))
	theirs := A(e.worker2.Call(t, "read_memory", M{"projectId": p.id, "taskId": id, "scope": "agent"}), "entries")
	want(t, len(theirs) == 0, "another member reads the worker's agent memory: %s", js(theirs))

	pkey := uniq("convention")
	e.worker.Call(t, "write_memory", M{"projectId": p.id, "scope": "project", "key": pkey, "value": "tabs, not spaces"})
	rec := A(e.master.Call(t, "recall_memory", M{"projectId": p.id, "scope": "project", "key": pkey}), "entries")
	want(t, len(rec) == 1 && S(rec[0], "value") == "tabs, not spaces", "recall_memory project as another member: %s", js(rec))
	gkey := uniq("habit")
	e.worker.Call(t, "write_memory", M{"projectId": p.id, "scope": "agent_global", "key": gkey, "value": "run the tests first"})
	glob := A(e.worker.Call(t, "recall_memory", M{"scope": "agent_global", "key": gkey}), "entries")
	want(t, len(glob) == 1 && S(glob[0], "value") == "run the tests first", "recall_memory agent_global without a project: %s", js(glob))

	// Project memory outlives its task: the task is deleted and the convention is still recalled.
	e.master.Call(t, "delete_task", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id)})
	want(t, len(A(e.viewer.Call(t, "recall_memory", M{"projectId": p.id, "scope": "project", "key": pkey}), "entries")) == 1, "project memory died with a task")

	e.viewer.Refuse(t, "write_memory", M{"projectId": p.id, "scope": "project", "key": "nope", "value": 1}, "FORBIDDEN")
	e.worker.Refuse(t, "write_memory", M{"projectId": p.id, "scope": "task", "key": "orphan", "value": 1}, "VALIDATION")
	e.worker.Refuse(t, "recall_memory", M{"scope": "project", "key": pkey}, "VALIDATION")
	e.outsider.Refuse(t, "read_memory", M{"projectId": p.id, "taskId": S(e.task(t, p, "other", nil), "id")}, "FORBIDDEN")
}

// The master adds an edge, which blocks the dependent without moving it; the reverse edge is
// CYCLE_DETECTED. A task blocked through block_task returns to TODO when its last edge is removed.
// A worker manages no edges.
func ctxDependencies(t *testing.T, e *env) {
	p := e.project(t, "Deps")
	a := S(e.task(t, p, "schema", nil), "id")
	b := S(e.task(t, p, "migration", nil), "id")
	edge := O(e.master.Call(t, "add_dependency", M{"projectId": p.id, "fromTaskId": a, "toTaskId": b}), "edge")
	want(t, S(edge, "id") != "" && S(edge, "type") == "blocks", "add_dependency answered %s", js(edge))
	got := e.get(t, p, b)
	want(t, has(strs(A(got, "blockedBy")), a) && S(got, "status") == "TODO", "get_task of the dependent: %s", js(got))
	want(t, has(strs(A(e.get(t, p, a), "blocks")), b), "get_task of the blocker does not list %s under blocks", b)
	e.master.Refuse(t, "add_dependency", M{"projectId": p.id, "fromTaskId": b, "toTaskId": a}, "CYCLE_DETECTED")
	want(t, len(A(e.get(t, p, a), "blockedBy")) == 0, "a refused cyclic edge was written")
	e.worker.Refuse(t, "add_dependency", M{"projectId": p.id, "fromTaskId": a, "toTaskId": S(e.task(t, p, "x", nil), "id")}, "FORBIDDEN")
	e.worker.Refuse(t, "remove_dependency", M{"projectId": p.id, "edgeId": S(edge, "id")}, "FORBIDDEN")

	ok := e.master.Call(t, "remove_dependency", M{"projectId": p.id, "edgeId": S(edge, "id")})
	want(t, B(ok, "ok"), "remove_dependency answered %s", js(ok))
	want(t, len(A(e.get(t, p, b), "blockedBy")) == 0, "get_task after remove_dependency: %s", js(e.get(t, p, b)))

	// Blocked through block_task, then unblocked by removing the edge.
	c := S(e.task(t, p, "rollout", nil), "id")
	lease := claim(t, e.worker, p, c)
	e.worker.Call(t, "block_task", M{"projectId": p.id, "taskId": c, "leaseId": lease, "reason": "needs the schema", "blockedBy": []any{a}})
	want(t, S(e.get(t, p, c), "status") == "BLOCKED", "block_task did not block %s", c)
	deps := e.viewer.Call(t, "get_task_context", M{"projectId": p.id, "taskId": c, "include": []any{"deps"}})
	edgeID := ""
	for _, ed := range A(deps, "deps.edges") {
		if S(ed, "fromTaskId") == a && S(ed, "toTaskId") == c {
			edgeID = S(ed, "id")
		}
	}
	want(t, edgeID != "", "get_task_context lists no edge %s→%s: %s", a, c, js(O(deps, "deps")))
	e.master.Call(t, "remove_dependency", M{"projectId": p.id, "edgeId": edgeID})
	after := e.get(t, p, c)
	want(t, S(after, "status") == "TODO" && len(A(after, "blockedBy")) == 0, "the dependent left with no blocker: %s", js(after))
}

// The master adds a subtask (a retry replays it): it lands in the parent board's first column with
// no edge, and the parent is left as it was; the viewer lists it. A worker cannot add one and an
// unknown parent is NOT_FOUND.
func ctxSubtasks(t *testing.T, e *env) {
	p := e.project(t, "Subtasks")
	parent := e.task(t, p, "epic", nil)
	pid := S(parent, "id")
	key := uniq("sub")
	sub := O(e.master.Call(t, "add_subtask", M{"projectId": p.id, "parentTaskId": pid, "title": "story one", "priority": "high", "idempotencyKey": key}), "task")
	again := O(e.master.Call(t, "add_subtask", M{"projectId": p.id, "parentTaskId": pid, "title": "story one", "priority": "high", "idempotencyKey": key}), "task")
	want(t, S(sub, "parentTaskId") == pid && S(sub, "status") == "TODO" && S(sub, "columnId") == p.cols["TODO"] && S(sub, "boardId") == p.board,
		"add_subtask answered %s", js(sub))
	want(t, S(again, "id") == S(sub, "id"), "a retried add_subtask made %s, want %s", S(again, "id"), S(sub, "id"))
	e.master.Call(t, "add_subtask", M{"projectId": p.id, "parentTaskId": pid, "title": "story two"})

	list := A(e.viewer.Call(t, "list_subtasks", M{"projectId": p.id, "parentTaskId": pid}), "tasks")
	want(t, len(list) == 2 && has(ids(list, "id"), S(sub, "id")), "list_subtasks: %v", ids(list, "id"))
	after := e.get(t, p, pid)
	want(t, N(after, "version") == N(parent, "version") && S(after, "status") == "TODO" && len(A(after, "blockedBy")) == 0,
		"add_subtask changed the parent: %s", js(after))
	want(t, len(A(e.get(t, p, S(sub, "id")), "blockedBy")) == 0, "add_subtask added a dependency edge")

	e.worker.Refuse(t, "add_subtask", M{"projectId": p.id, "parentTaskId": pid, "title": "not mine"}, "FORBIDDEN")
	e.master.Refuse(t, "add_subtask", M{"projectId": p.id, "parentTaskId": "T-nosuchtask", "title": "orphan"}, "NOT_FOUND")
	e.outsider.Refuse(t, "list_subtasks", M{"projectId": p.id, "parentTaskId": pid}, "FORBIDDEN")
}
