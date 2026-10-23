package mcp_test

import (
	"strings"
	"testing"
)

// proposalFor is a DONE task bound to sprint sid (customFields.sprintProposalFor): claimed,
// completed to REVIEW and moved to DONE by ada.
func (r *raceRig) proposalFor(sid string) string {
	r.t.Helper()
	id := r.task("proposal", map[string]any{"customFields": map[string]any{"sprintProposalFor": sid}})
	r.submit(id, "approved plan")
	r.ada.call("move_task", map[string]any{"projectId": r.p, "taskId": id, "version": r.get(id)["version"], "toStatus": "DONE"})
	return id
}

func (r *raceRig) sprint(name string) map[string]any {
	r.t.Helper()
	return obj(r.ada.call("create_sprint", map[string]any{"projectId": r.p, "boardId": r.b, "name": name, "goal": "ship it", "acceptance": "it ships"})["sprint"])
}

func (r *raceRig) join(sid, task string) {
	r.t.Helper()
	r.ada.call("set_sprint", map[string]any{"projectId": r.p, "taskId": task, "version": r.get(task)["version"], "sprintId": sid, "action": "add"})
}

func (r *raceRig) activate(sp map[string]any) map[string]any {
	r.t.Helper()
	sid := str(sp["id"])
	bound := obj(r.ada.call("update_sprint", map[string]any{"projectId": r.p, "sprintId": sid, "version": sp["version"], "patch": map[string]any{"proposalTaskId": r.proposalFor(sid)}})["sprint"])
	return obj(r.ada.call("update_sprint", map[string]any{"projectId": r.p, "sprintId": sid, "version": bound["version"], "patch": map[string]any{"status": "ACTIVE"}})["sprint"])
}

// poison claims task and fails it with retryable false: BLOCKED with reason poison.
func (r *raceRig) poison(task string) {
	r.t.Helper()
	lease := str(r.ada.call("claim_task", map[string]any{"projectId": r.p, "taskId": task})["leaseId"])
	r.ada.call("fail_task", map[string]any{"projectId": r.p, "taskId": task, "leaseId": lease, "reason": "broken", "retryable": false})
}

// AC3, as the contract can carry it: "open" (plan §Fold 2, sprints.ts:171) is update_sprint's
// close-time member filter, status != DONE, since list_sprints takes only the four sprint statuses.
// Closing a sprint whose member is BLOCKED-poison warns and names it; closing one whose members are
// all DONE, a cancelled one (DONE, label cancelled) among them, does not. get_sprint's workflow
// transitions are the fixed table and its counts carry poisoned, not deadLetter.
func TestSprintOpenMeansNotDone(t *testing.T) {
	raceStacks(t, func(t *testing.T, r *raceRig) {
		poisonedSprint, doneSprint := r.sprint("has poison"), r.sprint("all done")
		bad, done, cancelled := r.task("bad", nil), r.task("done", nil), r.task("cancelled", nil)
		for _, id := range []string{bad, done, cancelled} {
			r.join(str(poisonedSprint["id"]), id)
		}
		r.poison(bad)
		r.submit(done, "shipped")
		r.ada.call("move_task", map[string]any{"projectId": r.p, "taskId": done, "version": r.get(done)["version"], "toStatus": "DONE"})
		r.ada.call("cancel_task", map[string]any{"projectId": r.p, "taskId": cancelled, "version": r.get(cancelled)["version"], "reason": "not needed"})

		read := r.ada.call("get_sprint", map[string]any{"projectId": r.p, "sprintId": poisonedSprint["id"]})
		if got := obj(obj(read["workflow"])["transitions"]); !jsonEqual(got, asJSON(t, fixedTable())) {
			t.Fatalf("get_sprint workflow.transitions %v, want the fixed table", got)
		}
		counts := obj(read["counts"])
		if _, ok := counts["deadLetter"]; ok || counts["poisoned"] != float64(1) || counts["blocked"] != float64(0) || counts["done"] != float64(1) ||
			counts["total"] != float64(3) || counts["backlog"] != float64(0) {
			t.Fatalf("counts %v: want poisoned 1, blocked 0, done 1 (the cancelled one is not done), total 3", counts)
		}
		att := read["attention"].([]any)
		if len(att) != 1 || str(obj(att[0])["taskId"]) != bad || obj(att[0])["why"] != "poisoned" {
			t.Fatalf("attention %v, want %s poisoned", att, bad)
		}

		active := r.activate(poisonedSprint)
		if active["status"] != "ACTIVE" || active["startedAt"] == nil || str(active["approvedBy"]) == "" {
			t.Fatalf("activated %v", active)
		}
		if n := len(r.ada.call("list_sprints", map[string]any{"projectId": r.p, "status": "ACTIVE"})["sprints"].([]any)); n != 1 {
			t.Fatalf("list_sprints ACTIVE: %d, want 1", n)
		}
		closed := r.ada.call("update_sprint", map[string]any{"projectId": r.p, "sprintId": active["id"], "version": active["version"], "patch": map[string]any{"status": "CLOSED"}})
		warn, _ := closed["warnings"].([]any)
		if len(warn) != 1 || !strings.Contains(str(warn[0]), bad) || strings.Contains(str(warn[0]), done) || strings.Contains(str(warn[0]), cancelled) {
			t.Fatalf("closing with a poisoned member warned %v, want one warning naming %s alone", warn, bad)
		}

		finished := r.task("finished", nil)
		r.join(str(doneSprint["id"]), finished)
		r.submit(finished, "done")
		r.ada.call("move_task", map[string]any{"projectId": r.p, "taskId": finished, "version": r.get(finished)["version"], "toStatus": "DONE"})
		act := r.activate(doneSprint)
		if quiet := r.ada.call("update_sprint", map[string]any{"projectId": r.p, "sprintId": act["id"], "version": act["version"], "patch": map[string]any{"status": "CLOSED"}}); quiet["warnings"] != nil {
			t.Fatalf("closing an all-DONE sprint warned %v", quiet["warnings"])
		}
		t.Logf("%s: closing %s warned %q; closing %s did not", r.s.Engine, active["id"], warn[0], act["id"])
	})
}

// The sprint tools' happy paths: create, membership both ways and its retry-safe no-ops, get with
// deliverables and memory, list paging and filters, checkpoint, abandon.
func TestSprintTools(t *testing.T) {
	raceStacks(t, func(t *testing.T, r *raceRig) {
		sp := r.sprint("one")
		sid := str(sp["id"])
		if sp["status"] != "PROPOSED" || sp["memberCount"] != float64(0) || sp["version"] != float64(1) || !strings.HasPrefix(sid, "S-") {
			t.Fatalf("create_sprint: %v", sp)
		}
		a, b := r.task("a", nil), r.task("b", nil)
		r.join(sid, a)
		v := r.get(a)["version"]
		again := obj(r.ada.call("set_sprint", map[string]any{"projectId": r.p, "taskId": a, "version": 1, "sprintId": sid, "action": "add"})["task"])
		if again["version"] != v {
			t.Fatalf("re-adding a member moved it to v%v (from v%v), want a no-op", again["version"], v)
		}
		r.join(sid, b)
		r.submit(b, "b's deliverable")
		r.ada.call("write_memory", map[string]any{"projectId": r.p, "scope": "project", "key": "k", "value": "remembered"})
		read := r.ada.call("get_sprint", map[string]any{"projectId": r.p, "sprintId": sid, "include": []any{"deliverables", "memory"}})
		if obj(read["sprint"])["memberCount"] != float64(2) || len(read["tasks"].([]any)) != 2 || len(read["deliverables"].([]any)) != 1 ||
			len(read["memory"].([]any)) != 1 || read["truncated"] != false || len(obj(read["workflow"])["lanes"].([]any)) == 0 {
			t.Fatalf("get_sprint: %v", read)
		}
		if f := read["frontier"].([]any); len(f) != 1 || str(f[0]) != a {
			t.Fatalf("frontier %v, want %s", f, a)
		}
		if p := obj(read["policies"]); p["artifactPolicy"] != "warn" || p["selfApprovalPolicy"] != "warn" {
			t.Fatalf("policies %v", p)
		}
		gone := obj(r.ada.call("set_sprint", map[string]any{"projectId": r.p, "taskId": a, "version": r.get(a)["version"], "sprintId": sid, "action": "remove"})["task"])
		if ids, _ := gone["sprintIds"].([]any); len(ids) != 0 {
			t.Fatalf("removed: sprintIds %v", ids)
		}
		r.ada.call("set_sprint", map[string]any{"projectId": r.p, "taskId": a, "version": 1, "sprintId": sid, "action": "remove"}) // a no-op, unversioned

		r.sprint("two")
		r.sprint("three")
		var names []string
		cursor := ""
		for range 4 {
			args := map[string]any{"projectId": r.p, "limit": 2}
			if cursor != "" {
				args["cursor"] = cursor
			}
			page := r.ada.call("list_sprints", args)
			for _, x := range page["sprints"].([]any) {
				names = append(names, str(obj(x)["name"]))
			}
			if cursor = str(page["nextCursor"]); cursor == "" {
				break
			}
		}
		if strings.Join(names, " ") != "one two three" {
			t.Fatalf("list_sprints paged %v, want one two three", names)
		}
		cp := obj(r.ada.call("update_sprint", map[string]any{"projectId": r.p, "sprintId": sid, "version": 1, "patch": map[string]any{"checkpoint": "half way", "eventCursor": "42", "name": "one renamed"}})["sprint"])
		if cp["checkpoint"] != "half way" || cp["eventCursor"] != "42" || cp["name"] != "one renamed" || cp["version"] != float64(2) {
			t.Fatalf("checkpoint: %v", cp)
		}
		ab := obj(r.ada.call("update_sprint", map[string]any{"projectId": r.p, "sprintId": sid, "version": 2, "patch": map[string]any{"status": "ABANDONED", "checkpoint": nil}})["sprint"])
		if ab["status"] != "ABANDONED" || ab["closedAt"] == nil || ab["checkpoint"] != nil {
			t.Fatalf("abandon: %v", ab)
		}
	})
}

// Every declared error of the five sprint tools.
func TestSprintToolErrors(t *testing.T) {
	raceStacks(t, func(t *testing.T, r *raceRig) {
		sp := r.sprint("s")
		sid := str(sp["id"])
		task := r.task("t", nil)
		create := map[string]any{"projectId": r.p, "boardId": r.b, "name": "n", "goal": "g", "acceptance": "a"}
		r.wantCode(r.grace, "create_sprint", create, "FORBIDDEN")
		r.wantCode(r.ada, "create_sprint", with(create, "projectId", r.q), "FORBIDDEN")
		r.wantCode(r.ada, "create_sprint", with(create, "boardId", "B-404"), "NOT_FOUND")
		r.wantCode(r.ada, "create_sprint", with(create, "goal", ""), "VALIDATION")

		r.wantCode(r.ada, "get_sprint", map[string]any{"projectId": r.p, "sprintId": "S-404"}, "NOT_FOUND")
		r.wantCode(r.ada, "get_sprint", map[string]any{"projectId": r.q, "sprintId": sid}, "FORBIDDEN")
		r.grace.call("get_sprint", map[string]any{"projectId": r.p, "sprintId": sid}) // a worker reads: the control
		r.wantCode(r.ada, "list_sprints", map[string]any{"projectId": r.q}, "FORBIDDEN")
		r.wantCode(r.ada, "list_sprints", map[string]any{"projectId": r.p, "cursor": "not-a-cursor"}, "VALIDATION")

		set := map[string]any{"projectId": r.p, "taskId": task, "version": r.get(task)["version"], "sprintId": sid, "action": "add"}
		r.wantCode(r.grace, "set_sprint", set, "FORBIDDEN")
		r.wantCode(r.ada, "set_sprint", with(set, "sprintId", "S-404"), "NOT_FOUND")
		r.wantCode(r.ada, "set_sprint", with(set, "taskId", "T-404"), "NOT_FOUND")
		r.wantCode(r.ada, "set_sprint", with(set, "version", 99), "STALE")
		other := str(obj(r.ada.call("create_board", map[string]any{"projectId": r.p, "name": "Other"})["board"])["id"])
		elsewhere := str(obj(r.ada.call("create_task", map[string]any{"projectId": r.p, "boardId": other, "title": "elsewhere"})["task"])["id"])
		r.wantCode(r.ada, "set_sprint", with(with(set, "taskId", elsewhere), "version", 1), "VALIDATION")
		r.join(sid, task)
		second := r.sprint("second")
		r.wantCode(r.ada, "set_sprint", with(with(set, "sprintId", second["id"]), "version", r.get(task)["version"]), "CONFLICT_LOCKED")

		upd := map[string]any{"projectId": r.p, "sprintId": sid, "version": 1, "patch": map[string]any{"status": "ACTIVE"}}
		r.wantCode(r.grace, "update_sprint", upd, "FORBIDDEN")
		r.wantCode(r.ada, "update_sprint", with(upd, "sprintId", "S-404"), "NOT_FOUND")
		r.wantCode(r.ada, "update_sprint", with(upd, "version", 99), "STALE")
		r.wantCode(r.ada, "update_sprint", upd, "INVALID_TRANSITION") // no proposal bound
		r.wantCode(r.ada, "update_sprint", with(upd, "patch", map[string]any{"status": "CLOSED"}), "INVALID_TRANSITION")
		r.wantCode(r.ada, "update_sprint", with(upd, "patch", map[string]any{"name": ""}), "VALIDATION")
		r.ada.call("add_member", map[string]any{"projectId": r.p, "identityId": "u-grace", "role": "master"})
		bound := obj(r.ada.call("update_sprint", map[string]any{"projectId": r.p, "sprintId": sid, "version": 1, "patch": map[string]any{"proposalTaskId": r.proposalFor(sid)}})["sprint"])
		r.wantCode(r.grace, "update_sprint", with(upd, "version", bound["version"]), "FORBIDDEN") // a master, not an admin, activating
		r.ada.call("update_settings", map[string]any{"projectId": r.p, "selfApprovalPolicy": "enforce"})
		r.wantCode(r.ada, "update_sprint", with(upd, "version", bound["version"]), "FORBIDDEN") // its own proposal, under enforce
		r.ada.call("update_settings", map[string]any{"projectId": r.p, "selfApprovalPolicy": "warn"})
		active := obj(r.ada.call("update_sprint", with(upd, "version", bound["version"]))["sprint"]) // the control
		r.wantCode(r.ada, "update_sprint", map[string]any{"projectId": r.p, "sprintId": sid, "version": active["version"], "patch": map[string]any{"proposalTaskId": nil}}, "VALIDATION")
		closed := obj(r.ada.call("update_sprint", map[string]any{"projectId": r.p, "sprintId": sid, "version": active["version"], "patch": map[string]any{"status": "CLOSED"}})["sprint"])
		r.wantCode(r.ada, "set_sprint", map[string]any{"projectId": r.p, "taskId": task, "version": r.get(task)["version"], "sprintId": sid, "action": "remove"}, "INVALID_TRANSITION")
		_ = closed
	})
}
