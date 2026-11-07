//go:build e2e

package e2e

import (
	"strings"
	"testing"
	"time"
)

func init() {
	register(scenario{area: "lifecycle", name: "claim-heartbeat-release", tools: []string{"claim_task", "heartbeat_task", "release_task"}, run: lifeClaimHeartbeatRelease})
	register(scenario{area: "lifecycle", name: "next-and-claim-next", tools: []string{"next_task", "claim_next"}, run: lifeNextAndClaimNext})
	register(scenario{area: "lifecycle", name: "complete", tools: []string{"complete_task"}, run: lifeComplete})
	register(scenario{area: "lifecycle", name: "block-and-fail", tools: []string{"block_task", "fail_task"}, run: lifeBlockAndFail})
	register(scenario{area: "lifecycle", name: "reap", tools: []string{"reap_stale_leases"}, run: lifeReap})
	register(scenario{area: "lifecycle", name: "handoff", tools: []string{"handoff_task"}, run: lifeHandoff})
	register(scenario{area: "lifecycle", name: "cancel", tools: []string{"cancel_task"}, run: lifeCancel})
}

// Agent A of the worker claims a task; agent B of the same identity is refused (another holder),
// the viewer cannot claim, A extends the lease without moving the version, and releases it back to
// TODO; B's heartbeat or release of A's lease is LEASE_EXPIRED. A retried claim replays its lease.
func lifeClaimHeartbeatRelease(t *testing.T, e *env) {
	p := e.project(t, "Claims")
	id := S(e.task(t, p, "claimable", nil), "id")
	e.viewer.Refuse(t, "claim_task", M{"projectId": p.id, "taskId": id}, "FORBIDDEN")
	e.worker.Refuse(t, "claim_task", M{"projectId": p.id, "taskId": id, "leaseSeconds": 100000}, "VALIDATION")

	key := uniq("claim")
	got := e.worker.Call(t, "claim_task", M{"projectId": p.id, "taskId": id, "leaseSeconds": 120, "idempotencyKey": key})
	lease := S(got, "leaseId")
	want(t, lease != "" && S(got, "task.status") == "IN_PROGRESS", "claim_task answered %s", js(got))
	again := e.worker.Call(t, "claim_task", M{"projectId": p.id, "taskId": id, "leaseSeconds": 120, "idempotencyKey": key})
	want(t, S(again, "leaseId") == lease, "a retried claim minted lease %q, want the original %q", S(again, "leaseId"), lease)

	held := e.get(t, p, id)
	want(t, S(held, "status") == "IN_PROGRESS" && S(held, "claim.owner") == e.srv.ids["worker"] && S(held, "claim.agent") == "e2e-wa",
		"get_task after the claim: %s", js(held))
	e.workerB.Refuse(t, "claim_task", M{"projectId": p.id, "taskId": id}, "CONFLICT_LOCKED")
	e.worker2.Refuse(t, "claim_task", M{"projectId": p.id, "taskId": id}, "CONFLICT_LOCKED")

	v := N(held, "version")
	exp1, _ := time.Parse(time.RFC3339Nano, S(held, "claim.expiresAt"))
	hb := e.worker.Call(t, "heartbeat_task", M{"projectId": p.id, "taskId": id, "leaseId": lease, "leaseSeconds": 3600})
	exp2, err := time.Parse(time.RFC3339Nano, S(hb, "expiresAt"))
	want(t, err == nil && exp2.After(exp1), "heartbeat_task answered expiresAt %q, not after %s", S(hb, "expiresAt"), exp1)
	after := e.get(t, p, id)
	want(t, N(after, "version") == v && S(after, "claim.expiresAt") != S(held, "claim.expiresAt"), "after the heartbeat: version %v (was %v), claim %s", N(after, "version"), v, js(O(after, "claim")))
	e.workerB.Refuse(t, "heartbeat_task", M{"projectId": p.id, "taskId": id, "leaseId": lease}, "LEASE_EXPIRED")
	e.worker.Refuse(t, "heartbeat_task", M{"projectId": p.id, "taskId": id, "leaseId": "L-not-the-lease"}, "LEASE_EXPIRED")

	e.workerB.Refuse(t, "release_task", M{"projectId": p.id, "taskId": id, "leaseId": lease}, "LEASE_EXPIRED")
	rel := e.worker.Call(t, "release_task", M{"projectId": p.id, "taskId": id, "leaseId": lease})
	want(t, S(rel, "task.status") == "TODO" && O(rel, "task")["claim"] == nil, "release_task answered %s", js(rel))
	free := e.get(t, p, id)
	want(t, S(free, "status") == "TODO" && free["claim"] == nil && free["nextEligibleAt"] == nil, "get_task after release: %s", js(free))
	e.worker.Refuse(t, "heartbeat_task", M{"projectId": p.id, "taskId": id, "leaseId": lease}, "LEASE_EXPIRED")
	// Released with no backoff: the other agent claims it at once.
	claim(t, e.workerB, p, id)
}

// next_task previews without claiming (the viewer may preview), claim_next claims the best
// claimable task and skips one assigned to someone else and one that is blocked; once the frontier
// is empty it answers null. Its documented refusals are taken.
func lifeNextAndClaimNext(t *testing.T, e *env) {
	p := e.project(t, "Frontier")
	low := S(e.task(t, p, "low", M{"priority": "low"}), "id")
	urgent := S(e.task(t, p, "urgent", M{"priority": "urgent"}), "id")
	theirs := S(e.task(t, p, "theirs", M{"priority": "urgent"}), "id")
	e.master.Call(t, "update_task", M{"projectId": p.id, "taskId": theirs, "version": e.version(t, p, theirs), "patch": M{"assignee": e.srv.ids["worker2"]}})
	blocked := S(e.task(t, p, "blocked", M{"priority": "urgent"}), "id")
	e.master.Call(t, "add_dependency", M{"projectId": p.id, "fromTaskId": low, "toTaskId": blocked})

	peek := e.worker.Call(t, "next_task", M{"projectId": p.id, "boardId": p.board, "strategy": "score", "explain": true, "includeInbox": true})
	want(t, S(peek, "task.id") == urgent, "next_task as the worker answered %s, want the urgent unassigned task %s", S(peek, "task.id"), urgent)
	want(t, O(peek, "explanation") != nil && O(peek, "inbox") != nil, "next_task explain/includeInbox answered %s", js(peek))
	want(t, S(e.get(t, p, urgent), "status") == "TODO", "next_task claimed the task it previewed")
	e.viewer.Call(t, "next_task", M{"projectId": p.id, "boardId": p.board})
	e.outsider.Refuse(t, "next_task", M{"projectId": p.id, "boardId": p.board}, "FORBIDDEN")
	e.worker.Refuse(t, "next_task", M{"projectId": p.id, "boardId": p.board, "strategy": "random"}, "VALIDATION")

	first := e.worker.Call(t, "claim_next", M{"projectId": p.id, "boardId": p.board})
	want(t, S(first, "task.id") == urgent && S(first, "leaseId") != "", "claim_next answered %s, want %s", js(first), urgent)
	want(t, S(e.get(t, p, urgent), "claim.agent") == "e2e-wa", "claim_next left no lease on %s", urgent)
	second := e.worker.Call(t, "claim_next", M{"projectId": p.id, "boardId": p.board})
	want(t, S(second, "task.id") == low, "the second claim_next answered %s, want %s (the assigned and the blocked are skipped)", S(second, "task.id"), low)
	none := e.worker.Call(t, "claim_next", M{"projectId": p.id, "boardId": p.board})
	want(t, none["task"] == nil && none["leaseId"] == nil, "claim_next on an empty frontier answered %s", js(none))
	// worker2 is the assignee of theirs, so their claim_next takes it.
	mine := e.worker2.Call(t, "claim_next", M{"projectId": p.id, "boardId": p.board})
	want(t, S(mine, "task.id") == theirs, "claim_next as the assignee answered %s, want %s", S(mine, "task.id"), theirs)

	e.viewer.Refuse(t, "claim_next", M{"projectId": p.id, "boardId": p.board}, "FORBIDDEN")
	e.worker.Refuse(t, "claim_next", M{"projectId": p.id, "boardId": p.board, "maxConcurrency": 2}, "VALIDATION")
	e.worker.Refuse(t, "claim_next", M{"projectId": p.id, "boardId": p.board, "shard": M{"count": 2, "index": 0}}, "VALIDATION")
}

// complete_task: with review on the task lands REVIEW and the reviewer becomes its assignee; with it
// off it lands DONE and unblocks its dependent, which claim_next then takes. Only the lease holder
// completes. Artifacts on the call are merged into the task's own (the service's documented rule;
// the tool description's "artifacts are refused" disagrees and is reported).
func lifeComplete(t *testing.T, e *env) {
	p := e.project(t, "Completing")
	id := S(e.task(t, p, "reviewed", nil), "id")
	lease := claim(t, e.worker, p, id)
	e.workerB.Refuse(t, "complete_task", M{"projectId": p.id, "taskId": id, "leaseId": lease}, "LEASE_EXPIRED")
	e.worker.Refuse(t, "complete_task", M{"projectId": p.id, "taskId": id, "leaseId": lease, "reviewer": strings.Repeat("r", 321)}, "VALIDATION")
	done := e.worker.Call(t, "complete_task", M{"projectId": p.id, "taskId": id, "leaseId": lease, "deliverable": "parser merged", "reviewer": e.srv.ids["master"],
		"artifacts": []any{M{"kind": "pr", "url": "https://example.com/pr/1"}}})
	want(t, S(done, "task.status") == "REVIEW", "complete_task with review on answered %s", S(done, "task.status"))
	got := e.get(t, p, id)
	want(t, S(got, "status") == "REVIEW" && S(got, "assignee") == e.srv.ids["master"] && S(got, "deliverable") == "parser merged" &&
		S(got, "review.completedBy") == e.srv.ids["worker"] && S(got, "artifacts.0.url") == "https://example.com/pr/1", "get_task after a reviewed completion: %s", js(got))
	ctx := e.viewer.Call(t, "get_task_context", M{"projectId": p.id, "taskId": id})
	want(t, S(ctx, "deliverable") == "parser merged" && S(ctx, "review.reviewer") == e.srv.ids["master"], "get_task_context after completion: %s", js(O(ctx, "review")))

	// Review off: DONE, and the dependent is unblocked.
	e.admin.Call(t, "update_settings", M{"projectId": p.id, "reviewEnabled": false})
	up := S(e.task(t, p, "upstream", nil), "id")
	down := S(e.task(t, p, "downstream", nil), "id")
	e.master.Call(t, "add_dependency", M{"projectId": p.id, "fromTaskId": up, "toTaskId": down})
	want(t, len(A(e.get(t, p, down), "blockedBy")) == 1, "the dependent is not blocked by its upstream")
	l2 := claim(t, e.worker, p, up)
	fin := e.worker.Call(t, "complete_task", M{"projectId": p.id, "taskId": up, "leaseId": l2, "deliverable": "schema"})
	want(t, S(fin, "task.status") == "DONE", "complete_task with review off answered %s", S(fin, "task.status"))
	next := e.workerB.Call(t, "claim_next", M{"projectId": p.id, "boardId": p.board})
	want(t, S(next, "task.id") == down, "after its upstream is DONE claim_next answered %s, want the dependent %s", S(next, "task.id"), down)
	e.worker.Refuse(t, "complete_task", M{"projectId": p.id, "taskId": up, "leaseId": l2}, "LEASE_EXPIRED")
}

// block_task parks the task BLOCKED with its reason, records each blockedBy as a dependency and drops
// the lease; a blockedBy closing a cycle writes nothing. fail_task with retryable sends the task
// back to TODO after a backoff, and without it poisons it (BLOCKED, label poisoned).
func lifeBlockAndFail(t *testing.T, e *env) {
	p := e.project(t, "Blocking")
	id := S(e.task(t, p, "stuck", nil), "id")
	dep := S(e.task(t, p, "prerequisite", nil), "id")
	e.master.Call(t, "add_dependency", M{"projectId": p.id, "fromTaskId": id, "toTaskId": dep})
	lease := claim(t, e.worker, p, id)
	// dep already depends on id, so id blocked by dep would close a cycle.
	e.worker.Refuse(t, "block_task", M{"projectId": p.id, "taskId": id, "leaseId": lease, "reason": "waiting", "blockedBy": []any{dep}}, "CYCLE_DETECTED")
	want(t, S(e.get(t, p, id), "status") == "IN_PROGRESS", "a refused block changed the task")
	e.workerB.Refuse(t, "block_task", M{"projectId": p.id, "taskId": id, "leaseId": lease, "reason": "not mine"}, "LEASE_EXPIRED")

	other := S(e.task(t, p, "other blocker", nil), "id")
	blk := e.worker.Call(t, "block_task", M{"projectId": p.id, "taskId": id, "leaseId": lease, "reason": "waiting on the schema", "blockedBy": []any{other}})
	want(t, S(blk, "task.status") == "BLOCKED", "block_task answered %s", js(blk))
	got := e.get(t, p, id)
	want(t, S(got, "status") == "BLOCKED" && got["claim"] == nil && has(strs(A(got, "blockedBy")), other), "get_task after block_task: %s", js(got))
	deps := e.viewer.Call(t, "get_task_context", M{"projectId": p.id, "taskId": id, "include": []any{"deps"}})
	want(t, has(strs(A(deps, "deps.blockedBy")), other), "get_task_context deps after block_task: %s", js(O(deps, "deps")))
	e.worker.Refuse(t, "release_task", M{"projectId": p.id, "taskId": id, "leaseId": lease}, "LEASE_EXPIRED")

	retry := S(e.task(t, p, "flaky", nil), "id")
	l2 := claim(t, e.worker, p, retry)
	e.worker.Refuse(t, "fail_task", M{"projectId": p.id, "taskId": retry, "leaseId": l2, "reason": ""}, "VALIDATION")
	failed := O(e.worker.Call(t, "fail_task", M{"projectId": p.id, "taskId": retry, "leaseId": l2, "reason": "network flake", "retryable": true}), "task")
	want(t, S(failed, "status") == "TODO" && S(failed, "nextEligibleAt") != "" && N(failed, "claimAttempts") >= 1, "fail_task retryable answered %s", js(failed))
	// Inside its backoff it is not claimable.
	nt := e.workerB.Call(t, "next_task", M{"projectId": p.id, "boardId": p.board})
	want(t, S(nt, "task.id") != retry, "next_task offered a task inside its backoff")

	poison := S(e.task(t, p, "broken", nil), "id")
	l3 := claim(t, e.worker, p, poison)
	e.worker.Call(t, "fail_task", M{"projectId": p.id, "taskId": poison, "leaseId": l3, "reason": "bad input", "retryable": false})
	pz := e.get(t, p, poison)
	want(t, S(pz, "status") == "BLOCKED" && has(strs(A(pz, "labels")), "poisoned"), "fail_task not retryable left %s", js(pz))
	e.worker.Refuse(t, "fail_task", M{"projectId": p.id, "taskId": poison, "leaseId": l3, "reason": "again"}, "LEASE_EXPIRED")
}

// A lease left to expire is reaped back to TODO with a backoff; a live lease is not; a viewer may
// not reap.
func lifeReap(t *testing.T, e *env) {
	p := e.project(t, "Reaping")
	stale := S(e.task(t, p, "abandoned", nil), "id")
	live := S(e.task(t, p, "attended", nil), "id")
	e.worker.Call(t, "claim_task", M{"projectId": p.id, "taskId": stale, "leaseSeconds": 1})
	claim(t, e.workerB, p, live)
	time.Sleep(1500 * time.Millisecond)
	e.viewer.Refuse(t, "reap_stale_leases", M{"projectId": p.id, "boardId": p.board}, "FORBIDDEN")
	got := e.worker2.Call(t, "reap_stale_leases", M{"projectId": p.id, "boardId": p.board})
	want(t, N(got, "reaped") == 1, "reap_stale_leases answered %s, want 1", js(got))
	reaped := e.get(t, p, stale)
	want(t, S(reaped, "status") == "TODO" && reaped["claim"] == nil, "the reaped task: %s", js(reaped))
	want(t, S(e.get(t, p, live), "status") == "IN_PROGRESS", "the live lease was reaped")
	again := e.worker2.Call(t, "reap_stale_leases", M{"projectId": p.id, "boardId": p.board})
	want(t, N(again, "reaped") == 0, "a second reap answered %s", js(again))
}

// The worker hands its task to worker2 by identity: the lease is dropped, the task returns to TODO
// assigned to them with the memo kept as handoff memory, and worker2 is notified and claims it.
// Below master, a label another user's token carries is refused as toAgent.
func lifeHandoff(t *testing.T, e *env) {
	p := e.project(t, "Handoff")
	id := S(e.task(t, p, "relay", nil), "id")
	lease := claim(t, e.worker, p, id)
	e.worker.Refuse(t, "handoff_task", M{"projectId": p.id, "taskId": id, "leaseId": lease, "toAgent": "e2e-w2", "memoryNote": "yours"}, "FORBIDDEN")
	e.workerB.Refuse(t, "handoff_task", M{"projectId": p.id, "taskId": id, "leaseId": lease, "toAgent": e.srv.ids["worker2"], "memoryNote": "yours"}, "LEASE_EXPIRED")

	got := O(e.worker.Call(t, "handoff_task", M{"projectId": p.id, "taskId": id, "leaseId": lease, "toAgent": e.srv.ids["worker2"],
		"memoryNote": "the tokenizer is done; the parser remains"}), "task")
	want(t, S(got, "status") == "TODO" && S(got, "assignee") == e.srv.ids["worker2"] && got["claim"] == nil, "handoff_task answered %s", js(got))
	ctx := e.worker2.Call(t, "get_task_context", M{"projectId": p.id, "taskId": id})
	found := false
	for _, m := range A(ctx, "memory") {
		if S(m, "key") == "handoff" && S(m, "value.note") == "the tokenizer is done; the parser remains" && S(m, "value.toAgent") == e.srv.ids["worker2"] {
			found = true
		}
	}
	want(t, found, "the handoff memo is not in the task's memory: %s", js(A(ctx, "memory")))
	notes, _ := notificationsAbout(t, e.worker2, p, id, "handoff")
	want(t, len(notes) == 1, "worker2's handoff notifications for %s: %s", id, js(notes))
	// Assigned to worker2: the worker's agents may no longer claim it, worker2 may.
	e.workerB.Refuse(t, "claim_task", M{"projectId": p.id, "taskId": id}, "FORBIDDEN")
	claim(t, e.worker2, p, id)
}

// The master cancels a task: DONE with the label cancelled and the reason kept as a note, and its
// dependent is unblocked. A worker without the lease may not, a stale version is STALE, and a DONE
// task cannot be cancelled again.
func lifeCancel(t *testing.T, e *env) {
	p := e.project(t, "Cancelling")
	id := S(e.task(t, p, "unneeded", nil), "id")
	dep := S(e.task(t, p, "waiting on it", nil), "id")
	e.master.Call(t, "add_dependency", M{"projectId": p.id, "fromTaskId": id, "toTaskId": dep})
	v := e.version(t, p, id)
	e.worker.Refuse(t, "cancel_task", M{"projectId": p.id, "taskId": id, "version": v, "reason": "no"}, "FORBIDDEN")
	e.master.Refuse(t, "cancel_task", M{"projectId": p.id, "taskId": id, "version": v - 1, "reason": "stale"}, "STALE")

	got := O(e.master.Call(t, "cancel_task", M{"projectId": p.id, "taskId": id, "version": v, "reason": "superseded by the new design"}), "task")
	want(t, S(got, "status") == "DONE" && has(strs(A(got, "labels")), "cancelled"), "cancel_task answered %s", js(got))
	ctx := e.viewer.Call(t, "get_task_context", M{"projectId": p.id, "taskId": id, "include": []any{"items"}})
	noted := false
	for _, it := range A(ctx, "items") {
		if S(it, "kind") == "note" && containsStr(S(it, "body"), "superseded by the new design") {
			noted = true
		}
	}
	want(t, noted, "the cancel reason is not a note on the task: %s", js(A(ctx, "items")))
	want(t, len(A(e.get(t, p, dep), "blockedBy")) == 0 || S(e.workerB.Call(t, "next_task", M{"projectId": p.id, "boardId": p.board}), "task.id") == dep,
		"the cancelled task's dependent is still blocked")
	e.master.Refuse(t, "cancel_task", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id), "reason": "again"}, "INVALID_TRANSITION")

	// The lease holder may cancel its own task.
	own := S(e.task(t, p, "abandon mine", nil), "id")
	claim(t, e.worker, p, own)
	e.worker.Call(t, "cancel_task", M{"projectId": p.id, "taskId": own, "version": e.version(t, p, own), "reason": "duplicate"})
	want(t, S(e.get(t, p, own), "status") == "DONE", "the holder's cancel did not land")
}
