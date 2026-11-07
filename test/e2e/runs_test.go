//go:build e2e

package e2e

import (
	"testing"
)

func init() {
	register(scenario{area: "runs", name: "race-and-pick", tools: []string{"start_runs", "open_run", "list_runs", "pick_run"}, run: runsRaceAndPick})
	register(scenario{area: "runs", name: "cancel", tools: []string{"cancel_runs"}, run: runsCancel})
	register(scenario{area: "sprints", name: "propose-activate-members", tools: []string{"create_sprint", "update_sprint", "set_sprint", "get_sprint", "list_sprints"}, run: sprintsLifecycle})
}

// The master races a task with two runs and opens a third routed to worker2, up to the project's
// maxRunsPerTask; the racing parent is out of the pool and cannot be moved. The worker submits one
// run; a run with no deliverable cannot be picked; picking the submitted run gives the parent its
// deliverable (REVIEW) and deletes the other runs. A worker cannot race and a race past the cap is
// refused.
func runsRaceAndPick(t *testing.T, e *env) {
	p := e.project(t, "Races")
	e.admin.Call(t, "update_settings", M{"projectId": p.id, "maxRunsPerTask": 3})
	id := S(e.task(t, p, "two approaches", nil), "id")
	e.worker.Refuse(t, "start_runs", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id), "count": 2}, "FORBIDDEN")
	e.master.Refuse(t, "start_runs", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id), "count": 4}, "VALIDATION")

	started := e.master.Call(t, "start_runs", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id), "count": 2, "brief": "try it two ways"})
	runs := A(started, "runs")
	want(t, len(runs) == 2 && B(started, "task.racing") && S(runs[0], "runOf") == id && S(runs[0], "status") == "TODO", "start_runs answered %s", js(started))
	e.master.Refuse(t, "move_task", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id), "toStatus": "IN_PROGRESS"}, "CONFLICT_LOCKED")

	opened := O(e.master.Call(t, "open_run", M{"projectId": p.id, "taskId": id, "agent": e.srv.ids["worker2"]}), "run")
	want(t, S(opened, "assignee") == e.srv.ids["worker2"] && S(opened, "runOf") == id, "open_run answered %s", js(opened))
	e.master.Refuse(t, "open_run", M{"projectId": p.id, "taskId": id}, "VALIDATION")
	e.worker.Refuse(t, "open_run", M{"projectId": p.id, "taskId": id}, "FORBIDDEN")
	listed := A(e.viewer.Call(t, "list_runs", M{"projectId": p.id, "taskId": id}), "runs")
	want(t, len(listed) == 3, "list_runs: %s", js(listed))
	e.outsider.Refuse(t, "list_runs", M{"projectId": p.id, "taskId": id}, "FORBIDDEN")

	win, lose := S(runs[0], "id"), S(runs[1], "id")
	lease := claim(t, e.worker, p, win)
	e.worker.Call(t, "complete_task", M{"projectId": p.id, "taskId": win, "leaseId": lease, "deliverable": "approach A: streaming"})
	submitted := false
	for _, r := range A(e.viewer.Call(t, "list_runs", M{"projectId": p.id, "taskId": id}), "runs") {
		if S(r, "taskId") == win {
			submitted = B(r, "hasDeliverable") && S(r, "deliverable") == "approach A: streaming"
		}
	}
	want(t, submitted, "list_runs does not show the submitted run's deliverable")
	e.master.Refuse(t, "pick_run", M{"projectId": p.id, "taskId": id, "runTaskId": lose, "version": e.version(t, p, id)}, "INVALID_TRANSITION")
	e.worker.Refuse(t, "pick_run", M{"projectId": p.id, "taskId": id, "runTaskId": win, "version": e.version(t, p, id)}, "FORBIDDEN")
	e.master.Refuse(t, "pick_run", M{"projectId": p.id, "taskId": id, "runTaskId": win, "version": e.version(t, p, id) - 1}, "STALE")

	picked := O(e.master.Call(t, "pick_run", M{"projectId": p.id, "taskId": id, "runTaskId": win, "version": e.version(t, p, id)}), "task")
	want(t, S(picked, "status") == "REVIEW" && S(picked, "deliverable") == "approach A: streaming" && !B(picked, "racing"), "pick_run answered %s", js(picked))
	got := e.get(t, p, id)
	want(t, S(got, "status") == "REVIEW" && S(got, "deliverable") == "approach A: streaming", "get_task of the parent after pick_run: %s", js(got))
	e.admin.Refuse(t, "get_task", M{"projectId": p.id, "taskId": lose}, "NOT_FOUND")
	e.admin.Refuse(t, "get_task", M{"projectId": p.id, "taskId": S(opened, "id")}, "NOT_FOUND")
}

// The master races a task and ends the race without a winner: every run is deleted and the parent
// returns to TODO, back in the claim pool. A worker may not, and a task not racing is refused.
func runsCancel(t *testing.T, e *env) {
	p := e.project(t, "Cancelled races")
	id := S(e.task(t, p, "abandoned race", nil), "id")
	started := e.master.Call(t, "start_runs", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id), "agents": []any{e.srv.ids["worker"], e.srv.ids["worker2"]}})
	runs := A(started, "runs")
	want(t, len(runs) == 2 && S(runs[0], "assignee") == e.srv.ids["worker"], "start_runs by agents answered %s", js(runs))
	e.worker.Refuse(t, "cancel_runs", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id)}, "FORBIDDEN")

	got := O(e.master.Call(t, "cancel_runs", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id)}), "task")
	want(t, S(got, "status") == "TODO" && !B(got, "racing"), "cancel_runs answered %s", js(got))
	want(t, len(A(e.viewer.Call(t, "list_runs", M{"projectId": p.id, "taskId": id}), "runs")) == 0, "list_runs after cancel_runs still lists runs")
	for _, r := range runs {
		e.admin.Refuse(t, "get_task", M{"projectId": p.id, "taskId": S(r, "id")}, "NOT_FOUND")
	}
	want(t, S(e.worker.Call(t, "claim_next", M{"projectId": p.id, "boardId": p.board}), "task.id") == id, "the parent is not back in the claim pool")
	e.master.Refuse(t, "cancel_runs", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id)}, "INVALID_TRANSITION")
}

// The master proposes a sprint and binds a proposal task; the project admin's activation is refused
// until that task is DONE, and the master may not activate. Members are added (a re-add is a no-op),
// read back through get_sprint and the task's sprintIds, and removed; a task on another board is
// refused. list_sprints filters by status and pages.
func sprintsLifecycle(t *testing.T, e *env) {
	p := e.project(t, "Sprints")
	e.admin.Call(t, "update_settings", M{"projectId": p.id, "reviewEnabled": false})
	e.worker.Refuse(t, "create_sprint", M{"projectId": p.id, "boardId": p.board, "name": "S1", "goal": "g", "acceptance": "a"}, "FORBIDDEN")
	e.master.Refuse(t, "create_sprint", M{"projectId": p.id, "boardId": p.board, "name": "", "goal": "g", "acceptance": "a"}, "VALIDATION")
	sp := O(e.master.Call(t, "create_sprint", M{"projectId": p.id, "boardId": p.board, "name": "Parser sprint", "goal": "Ship the parser", "acceptance": "All feeds parse", "idempotencyKey": uniq("sp")}), "sprint")
	sid := S(sp, "id")
	want(t, S(sp, "status") == "PROPOSED" && S(sp, "boardId") == p.board, "create_sprint answered %s", js(sp))

	proposal := S(e.task(t, p, "Sprint proposal", M{"customFields": M{"sprintProposalFor": sid}}), "id")
	bound := O(e.master.Call(t, "update_sprint", M{"projectId": p.id, "sprintId": sid, "version": N(sp, "version"), "patch": M{"proposalTaskId": proposal}}), "sprint")
	want(t, S(bound, "proposalTaskId") == proposal, "update_sprint proposalTaskId answered %s", js(bound))
	e.master.Refuse(t, "update_sprint", M{"projectId": p.id, "sprintId": sid, "version": N(sp, "version"), "patch": M{"goal": "stale"}}, "STALE")
	e.master.Refuse(t, "update_sprint", M{"projectId": p.id, "sprintId": sid, "version": N(bound, "version"), "patch": M{"status": "ACTIVE"}}, "FORBIDDEN")
	e.admin.Refuse(t, "update_sprint", M{"projectId": p.id, "sprintId": sid, "version": N(bound, "version"), "patch": M{"status": "ACTIVE"}}, "INVALID_TRANSITION")

	lease := claim(t, e.worker, p, proposal)
	e.worker.Call(t, "complete_task", M{"projectId": p.id, "taskId": proposal, "leaseId": lease, "deliverable": "the plan"})
	active := O(e.admin.Call(t, "update_sprint", M{"projectId": p.id, "sprintId": sid, "version": N(bound, "version"), "patch": M{"status": "ACTIVE"}}), "sprint")
	want(t, S(active, "status") == "ACTIVE" && S(active, "startedAt") != "" && S(active, "approvedBy") != "", "the activation answered %s", js(active))

	a := S(e.task(t, p, "sprint work", nil), "id")
	added := O(e.master.Call(t, "set_sprint", M{"projectId": p.id, "sprintId": sid, "taskId": a, "action": "add", "version": e.version(t, p, a)}), "task")
	want(t, has(strs(A(added, "sprintIds")), sid), "set_sprint add answered %s", js(added))
	e.master.Call(t, "set_sprint", M{"projectId": p.id, "sprintId": sid, "taskId": a, "action": "add", "version": e.version(t, p, a)})
	view := e.viewer.Call(t, "get_sprint", M{"projectId": p.id, "sprintId": sid, "include": []any{"deliverables"}})
	want(t, N(view, "sprint.memberCount") == 1 && N(view, "counts.total") == 1 && js(ids(A(view, "tasks"), "id")) == js([]string{a}) && has(strs(A(view, "frontier")), a),
		"get_sprint after set_sprint: %s", js(view))
	e.outsider.Refuse(t, "get_sprint", M{"projectId": p.id, "sprintId": sid}, "FORBIDDEN")

	other := O(e.admin.Call(t, "create_board", M{"projectId": p.id, "name": "Elsewhere"}), "board")
	far := S(O(e.master.Call(t, "create_task", M{"projectId": p.id, "boardId": S(other, "id"), "title": "far away"}), "task"), "id")
	e.master.Refuse(t, "set_sprint", M{"projectId": p.id, "sprintId": sid, "taskId": far, "action": "add", "version": e.version(t, p, far)}, "VALIDATION")
	e.worker.Refuse(t, "set_sprint", M{"projectId": p.id, "sprintId": sid, "taskId": a, "action": "remove", "version": e.version(t, p, a)}, "FORBIDDEN")

	activeIDs := ids(A(e.viewer.Call(t, "list_sprints", M{"projectId": p.id, "status": "ACTIVE"}), "sprints"), "id")
	want(t, js(activeIDs) == js([]string{sid}), "list_sprints ACTIVE: %v", activeIDs)
	e.master.Call(t, "create_sprint", M{"projectId": p.id, "boardId": p.board, "name": "Next sprint", "goal": "g", "acceptance": "a"})
	want(t, len(A(e.viewer.Call(t, "list_sprints", M{"projectId": p.id, "status": "PROPOSED"}), "sprints")) == 1, "list_sprints PROPOSED")
	page := e.viewer.Call(t, "list_sprints", M{"projectId": p.id, "limit": 1})
	want(t, len(A(page, "sprints")) == 1 && S(page, "sprints.0.id") == sid && S(page, "nextCursor") != "", "list_sprints limit 1 (oldest first): %s", js(page))
	e.viewer.Refuse(t, "list_sprints", M{"projectId": p.id, "limit": 201}, "VALIDATION")

	removed := O(e.master.Call(t, "set_sprint", M{"projectId": p.id, "sprintId": sid, "taskId": a, "action": "remove", "version": e.version(t, p, a)}), "task")
	want(t, len(A(removed, "sprintIds")) == 0, "set_sprint remove answered %s", js(removed))
	want(t, N(e.viewer.Call(t, "get_sprint", M{"projectId": p.id, "sprintId": sid}), "sprint.memberCount") == 0, "get_sprint still counts the removed member")
}
