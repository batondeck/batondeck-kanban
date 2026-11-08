//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

func init() {
	register(scenario{area: "notifications", name: "list-and-mark", tools: []string{"list_notifications", "mark_notifications_read"}, run: notificationsListAndMark})
	register(scenario{area: "agents_profile", name: "profile-and-stats", tools: []string{"register_agent_profile", "get_skill_stats"}, run: agentProfileAndStats})
	register(scenario{area: "anomalies", name: "thrash-and-deadlock", tools: []string{"detect_anomalies"}, run: anomaliesDetect})
	register(scenario{area: "rank", name: "rank", tools: []string{"rank_tasks"}, run: rankTasks})
	register(scenario{area: "rank", name: "orchestrate", tools: []string{"orchestrate_subtasks"}, run: rankOrchestrate})
	register(scenario{area: "grants", name: "narrow-and-clear", tools: []string{"grant_capabilities"}, run: grantsNarrow})
}

// notificationsAbout is c's notifications about task in p (task ids are per project), of type typ
// when it is not "", newest first, and c's unread count.
func notificationsAbout(t *testing.T, c *Client, p *project, task, typ string) ([]M, float64) {
	t.Helper()
	out := c.Call(t, "list_notifications", M{"limit": 200})
	var mine []M
	for _, n := range A(out, "notifications") {
		if S(n, "projectId") == p.id && S(n, "taskId") == task && (typ == "" || S(n, "type") == typ) {
			mine = append(mine, n.(M))
		}
	}
	return mine, N(out, "unread")
}

// A mention and a follow-up notify worker2, newest first, unread; marking one by id lowers the
// unread count by one, another identity's mark and a second mark of it count nothing, a mark naming
// neither ids nor all:true is refused and marks nothing, and all:true clears the rest. A limit past
// 200 and an empty id are refused.
func notificationsListAndMark(t *testing.T, e *env) {
	p := e.project(t, "Notifications")
	id := S(e.task(t, p, "noisy", nil), "id")
	e.worker.Call(t, "add_comment", M{"projectId": p.id, "taskId": id, "body": "@second please look"})
	e.master.Call(t, "update_task", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id), "patch": M{"assignee": e.srv.ids["worker2"]}})
	e.master.Call(t, "add_follow_up", M{"projectId": p.id, "taskId": id, "body": "and this too"})

	notes, unread := notificationsAbout(t, e.worker2, p, id, "")
	want(t, len(notes) == 2 && S(notes[0], "type") == "follow_up" && S(notes[1], "type") == "mention" && !B(notes[0], "read") && !B(notes[1], "read") && unread >= 2, "worker2's notifications about %s: %s (unread %v)", id, js(notes), unread)
	want(t, S(notes[0], "ts") >= S(notes[1], "ts") && S(notes[0], "projectId") == p.id, "notifications are not newest first: %s", js(notes))
	none, _ := notificationsAbout(t, e.worker, p, id, "")
	want(t, len(none) == 0, "the worker is notified about its own mention: %s", js(none))

	// Another identity cannot mark worker2's notification.
	foreign := e.worker.Call(t, "mark_notifications_read", M{"ids": []any{S(notes[0], "id")}})
	want(t, N(foreign, "updated") == 0, "the worker marked worker2's notification: %s", js(foreign))
	got := e.worker2.Call(t, "mark_notifications_read", M{"ids": []any{S(notes[0], "id")}})
	want(t, N(got, "updated") == 1, "mark_notifications_read by id answered %s", js(got))
	after, unread2 := notificationsAbout(t, e.worker2, p, id, "")
	want(t, unread2 == unread-1 && B(after[0], "read") && !B(after[1], "read"), "after marking one: %s (unread %v, was %v)", js(after), unread2, unread)
	again := e.worker2.Call(t, "mark_notifications_read", M{"ids": []any{S(notes[0], "id")}})
	want(t, N(again, "updated") == 0, "a second mark of the same notification answered %s", js(again))
	for _, args := range []M{{}, {"all": false}, {"ids": []any{}}} {
		refused := e.worker2.Refuse(t, "mark_notifications_read", args, "VALIDATION")
		want(t, refused.Details["field"] == "ids", "mark_notifications_read %v refused %s %v", args, refused.Message, refused.Details)
	}
	still, unreadStill := notificationsAbout(t, e.worker2, p, id, "")
	want(t, unreadStill == unread2 && !B(still[1], "read"), "a refused mark changed the unread count to %v (was %v): %s", unreadStill, unread2, js(still))
	all := e.worker2.Call(t, "mark_notifications_read", M{"all": true})
	want(t, N(all, "updated") >= 1, "mark_notifications_read all:true answered %s", js(all))
	_, unread3 := notificationsAbout(t, e.worker2, p, id, "")
	want(t, unread3 == 0, "unread after all:true is %v", unread3)

	e.worker2.Refuse(t, "list_notifications", M{"limit": 201}, "VALIDATION")
	e.worker2.Refuse(t, "mark_notifications_read", M{"ids": []any{""}}, "VALIDATION")
}

// The worker registers its profile, reads it back as its own, and the master reads it by agent key;
// a completed task needing go is counted in the go stats. A re-register resets omitted fields. The
// viewer cannot register, and an unknown cost class is refused.
func agentProfileAndStats(t *testing.T, e *env) {
	p := e.project(t, "Profiles")
	reg := O(e.worker.Call(t, "register_agent_profile", M{"projectId": p.id, "capabilities": []any{"go", "sql"}, "tags": []any{"backend"}, "costClass": "standard", "maxConcurrency": 2, "maxEffort": "high", "models": []any{"opus"}}), "profile")
	key := S(reg, "agentKey")
	want(t, key != "" && js(A(reg, "capabilities")) == `["go","sql"]`, "register_agent_profile answered %s", js(reg))
	own := e.worker.Call(t, "get_skill_stats", M{"projectId": p.id})
	want(t, S(own, "profile.agentKey") == key && N(own, "profile.maxConcurrency") == 2, "get_skill_stats for myself: %s", js(own))

	id := S(e.task(t, p, "go work", M{"requiredCapabilities": []any{"go"}}), "id")
	lease := claim(t, e.worker, p, id)
	e.worker.Call(t, "complete_task", M{"projectId": p.id, "taskId": id, "leaseId": lease, "deliverable": "done"})
	byKey := e.master.Call(t, "get_skill_stats", M{"projectId": p.id, "agentKey": key})
	completed := -1.0
	for _, s := range A(byKey, "stats") {
		if S(s, "capability") == "go" {
			completed = N(s, "completed")
		}
	}
	want(t, S(byKey, "profile.agentKey") == key && completed == 1, "get_skill_stats by agentKey after a go completion: %s", js(byKey))

	re := O(e.worker.Call(t, "register_agent_profile", M{"projectId": p.id, "capabilities": []any{"go"}}), "profile")
	want(t, len(A(re, "tags")) == 0 && js(A(re, "capabilities")) == `["go"]`, "a re-register kept omitted fields: %s", js(re))
	want(t, len(A(e.worker.Call(t, "get_skill_stats", M{"projectId": p.id}), "profile.tags")) == 0, "get_skill_stats after the re-register still has tags")

	e.viewer.Refuse(t, "register_agent_profile", M{"projectId": p.id, "capabilities": []any{"go"}}, "FORBIDDEN")
	e.worker.Refuse(t, "register_agent_profile", M{"projectId": p.id, "capabilities": []any{"go"}, "costClass": "free"}, "VALIDATION")
	e.outsider.Refuse(t, "get_skill_stats", M{"projectId": p.id}, "FORBIDDEN")
}

// Three claims of one task are claim thrash, and a task blocked behind a poisoned one is a
// dependency deadlock; kinds filters the report. The viewer may not scan, and an unknown kind is
// refused.
func anomaliesDetect(t *testing.T, e *env) {
	p := e.project(t, "Anomalies")
	thrash := S(e.task(t, p, "thrashing", nil), "id")
	for i := 0; i < 3; i++ {
		lease := claim(t, e.worker, p, thrash)
		e.worker.Call(t, "release_task", M{"projectId": p.id, "taskId": thrash, "leaseId": lease})
	}
	poison := S(e.task(t, p, "poisoned", nil), "id")
	stuck := S(e.task(t, p, "behind it", nil), "id")
	e.master.Call(t, "add_dependency", M{"projectId": p.id, "fromTaskId": poison, "toTaskId": stuck})
	lease := claim(t, e.worker, p, poison)
	e.worker.Call(t, "fail_task", M{"projectId": p.id, "taskId": poison, "leaseId": lease, "reason": "corrupt input", "retryable": false})

	kinds := func(args M) map[string]string {
		args["projectId"] = p.id
		out := e.worker.Call(t, "detect_anomalies", args)
		want(t, S(out, "boardId") == p.board && N(out, "scanned.tasksScanned") >= 3, "detect_anomalies answered %s", js(out))
		got := map[string]string{}
		for _, a := range A(out, "anomalies") {
			got[S(a, "kind")] = S(a, "taskId")
		}
		return got
	}
	all := kinds(M{"boardId": p.board})
	want(t, all["claim_thrash"] == thrash && all["dep_deadlock"] == stuck, "detect_anomalies found %v, want claim_thrash on %s and dep_deadlock on %s", all, thrash, stuck)
	only := kinds(M{"boardId": p.board, "kinds": []any{"dep_deadlock"}})
	want(t, len(only) == 1 && only["dep_deadlock"] == stuck, "detect_anomalies kinds [dep_deadlock] found %v", only)

	e.viewer.Refuse(t, "detect_anomalies", M{"projectId": p.id, "boardId": p.board}, "FORBIDDEN")
	e.worker.Refuse(t, "detect_anomalies", M{"projectId": p.id, "boardId": p.board, "kinds": []any{"gremlins"}}, "VALIDATION")
}

// rank_tasks orders the claimable TODO tasks by score with factors and a reason: urgent first, the
// blocked and the in-progress left out, limit caps it, and capabilities keeps only the tasks whose
// required capabilities it covers. An outsider may not rank.
func rankTasks(t *testing.T, e *env) {
	p := e.project(t, "Ranking")
	low := S(e.task(t, p, "low", M{"priority": "low"}), "id")
	urgent := S(e.task(t, p, "urgent", M{"priority": "urgent"}), "id")
	rust := S(e.task(t, p, "rust", M{"priority": "low", "requiredCapabilities": []any{"rust"}}), "id")
	held := S(e.task(t, p, "held", M{"priority": "urgent"}), "id")
	claim(t, e.workerB, p, held)
	blocked := S(e.task(t, p, "blocked", M{"priority": "urgent"}), "id")
	e.master.Call(t, "add_dependency", M{"projectId": p.id, "fromTaskId": low, "toTaskId": blocked})

	rank := func(args M) []any {
		args["projectId"], args["boardId"] = p.id, p.board
		return A(e.viewer.Call(t, "rank_tasks", args), "ranked")
	}
	all := rank(M{})
	order := []string{}
	for _, r := range all {
		order = append(order, S(r, "task.id"))
	}
	want(t, len(order) > 0 && order[0] == urgent && !has(order, held) && !has(order, blocked), "rank_tasks order %v: want %s first, without %s and %s", order, urgent, held, blocked)
	want(t, O(all[0], "factors") != nil && S(all[0], "reason") != "", "rank_tasks gives no factors or reason: %s", js(all[0]))
	want(t, len(rank(M{"limit": 1})) == 1, "rank_tasks limit 1 answered more")
	want(t, has(order, rust), "rank_tasks without capabilities leaves out the rust task: %v", order)
	goOnly := []string{}
	for _, r := range rank(M{"capabilities": []any{"go"}}) {
		goOnly = append(goOnly, S(r, "task.id"))
	}
	want(t, !has(goOnly, rust) && has(goOnly, low) && has(goOnly, urgent), "rank_tasks capabilities [go]: %v, want the rust task left out", goOnly)
	e.outsider.Refuse(t, "rank_tasks", M{"projectId": p.id, "boardId": p.board}, "FORBIDDEN")
}

// The master fans out the task it holds: two TODO children that block it, and the parent parked
// BLOCKED, lease dropped, assigned to the master. Once both children are DONE the parent is back in
// TODO. A worker holding a lease may not fan out, and a lease the master does not hold is refused.
func rankOrchestrate(t *testing.T, e *env) {
	p := e.project(t, "Fan out")
	e.admin.Call(t, "update_settings", M{"projectId": p.id, "reviewEnabled": false})
	parent := S(e.task(t, p, "big job", nil), "id")
	wl := claim(t, e.worker, p, parent)
	e.worker.Refuse(t, "orchestrate_subtasks", M{"projectId": p.id, "parentTaskId": parent, "leaseId": wl, "children": []any{M{"title": "a"}}}, "FORBIDDEN")
	e.worker.Call(t, "release_task", M{"projectId": p.id, "taskId": parent, "leaseId": wl})
	lease := claim(t, e.master, p, parent)
	e.master.Refuse(t, "orchestrate_subtasks", M{"projectId": p.id, "parentTaskId": parent, "leaseId": "L-not-mine", "children": []any{M{"title": "a"}}}, "LEASE_EXPIRED")

	out := e.master.Call(t, "orchestrate_subtasks", M{"projectId": p.id, "parentTaskId": parent, "leaseId": lease, "children": []any{
		M{"title": "part one", "priority": "high"}, M{"title": "part two", "requiredCapabilities": []any{"go"}}}})
	kids := A(out, "children")
	want(t, len(kids) == 2 && S(kids[0], "status") == "TODO" && S(kids[0], "parentTaskId") == parent, "orchestrate_subtasks children: %s", js(kids))
	got := e.get(t, p, parent)
	want(t, S(got, "status") == "BLOCKED" && got["claim"] == nil && S(got, "assignee") == e.srv.ids["master"] && len(A(got, "blockedBy")) == 2,
		"get_task of the parent after orchestrate_subtasks: %s", js(got))
	listed := ids(A(e.viewer.Call(t, "list_subtasks", M{"projectId": p.id, "parentTaskId": parent}), "tasks"), "id")
	want(t, len(listed) == 2, "list_subtasks after orchestrate_subtasks: %v", listed)

	for _, k := range kids {
		l := claim(t, e.worker, p, S(k, "id"))
		e.worker.Call(t, "complete_task", M{"projectId": p.id, "taskId": S(k, "id"), "leaseId": l, "deliverable": "part"})
	}
	want(t, S(e.get(t, p, parent), "status") == "TODO", "with both children DONE the parent is %s", S(e.get(t, p, parent), "status"))
}

// The project admin denies worker2 add_comment: the next comment is refused, the grant reads back,
// and clearing it with null restores the role's tools. The master may not grant, and a grant naming
// a tool no grant can restrict (one that takes no projectId, or an exempt read) or a name that is no
// tool is refused, naming it, and leaves the grant in force as it was.
func grantsNarrow(t *testing.T, e *env) {
	p := e.project(t, "Grants")
	id := S(e.task(t, p, "guarded", nil), "id")
	e.worker2.Call(t, "add_comment", M{"projectId": p.id, "taskId": id, "body": "before the grant"})

	g := e.admin.Call(t, "grant_capabilities", M{"projectId": p.id, "identityId": e.srv.ids["worker2"], "scope": M{"deny": []any{"add_comment"}}})
	want(t, S(g, "identityId") == e.srv.ids["worker2"] && js(A(g, "grant.deny")) == `["add_comment"]`, "grant_capabilities answered %s", js(g))
	e.worker2.Refuse(t, "add_comment", M{"projectId": p.id, "taskId": id, "body": "after the grant"}, "FORBIDDEN")
	// Narrowed, not removed: the other worker tools still answer.
	e.worker2.Call(t, "add_context_item", M{"projectId": p.id, "taskId": id, "kind": "note", "body": "still allowed"})
	e.worker.Call(t, "add_comment", M{"projectId": p.id, "taskId": id, "body": "the worker is not narrowed"})

	e.master.Refuse(t, "grant_capabilities", M{"projectId": p.id, "identityId": e.srv.ids["worker2"], "scope": nil}, "FORBIDDEN")
	e.admin.Refuse(t, "grant_capabilities", M{"projectId": p.id, "identityId": e.srv.ids["worker2"], "scope": M{"deny": []any{"list_audit_log"}}}, "VALIDATION")
	// A typo in tools, an allowlist, would have denied worker2 every governed tool; it is refused.
	typo := e.admin.Refuse(t, "grant_capabilities", M{"projectId": p.id, "identityId": e.srv.ids["worker2"], "scope": M{"tools": []any{"no_such_tool"}}}, "VALIDATION")
	want(t, typo.Details["field"] == "tools" && js(typo.Details["unknown"]) == `["no_such_tool"]` && strings.Contains(typo.Message, "no_such_tool"),
		"grant_capabilities tools [no_such_tool] refused %s %v", typo.Message, typo.Details)
	exempt := e.admin.Refuse(t, "grant_capabilities", M{"projectId": p.id, "identityId": e.srv.ids["worker2"], "scope": M{"deny": []any{"add_coment", "get_task"}}}, "VALIDATION")
	want(t, exempt.Details["field"] == "deny" && js(exempt.Details["unknown"]) == `["add_coment"]` && js(exempt.Details["exempt"]) == `["get_task"]`,
		"grant_capabilities deny [add_coment get_task] refused %s %v", exempt.Message, exempt.Details)
	e.worker2.Refuse(t, "add_comment", M{"projectId": p.id, "taskId": id, "body": "the refused grants left the deny"}, "FORBIDDEN")
	e.worker2.Call(t, "add_context_item", M{"projectId": p.id, "taskId": id, "kind": "note", "body": "and the rest allowed"})

	cleared := e.admin.Call(t, "grant_capabilities", M{"projectId": p.id, "identityId": e.srv.ids["worker2"], "scope": nil})
	want(t, cleared["grant"] == nil, "grant_capabilities null answered %s", js(cleared))
	e.worker2.Call(t, "add_comment", M{"projectId": p.id, "taskId": id, "body": "after the grant is cleared"})
	want(t, len(A(e.viewer.Call(t, "list_comments", M{"projectId": p.id, "taskId": id}), "comments")) == 3, "the comments do not read back three")
}
