package mcp_test

import (
	"testing"
)

// TestFollowUpsAddFollowUp: add_follow_up's happy path (targeted at the assignee, then at the live
// lease holder), a self-directed one untargeted, the task's version unmoved without a reopen, a
// reopen of a DONE task to TODO (assignee kept, version moved once), a reopen from TODO recorded
// only, and each declared error: FORBIDDEN for a worker (the tier is master) and across projects,
// NOT_FOUND for a missing task, VALIDATION for an empty body.
func TestFollowUpsAddFollowUp(t *testing.T) {
	notesStacks(t, func(t *testing.T, r *notesRig) {
		task := r.task("directed", "u-will")
		id := str(task["id"])
		got := r.mona.call("add_follow_up", map[string]any{"projectId": r.p, "taskId": id, "body": "add a test"})
		fu := obj(got["followUp"])
		if fu["target"] != "u-will" || fu["status"] != "open" || fu["reopened"] != false || fu["author"] != "u-mona" || obj(got["task"])["version"] != task["version"] {
			t.Fatalf("add_follow_up to the assignee = %v", got)
		}
		lease := str(r.max.call("claim_task", map[string]any{"projectId": r.p, "taskId": str(r.task("held", "")["id"])})["leaseId"])
		_ = lease
		held := r.max.call("list_tasks", map[string]any{"projectId": r.p, "boardId": r.b, "status": "IN_PROGRESS"})["tasks"].([]any)
		hid := str(obj(held[0])["id"])
		if fu := obj(r.ada.call("add_follow_up", map[string]any{"projectId": r.p, "taskId": hid, "body": "status?"})["followUp"]); fu["target"] != "u-max" {
			t.Fatalf("add_follow_up to the lease holder = %v, want target u-max", fu)
		}
		if fu := obj(r.ada.call("add_follow_up", map[string]any{"projectId": r.p, "taskId": str(r.task("mine", "u-ada")["id"]), "body": "note to self"})["followUp"]); fu["target"] != nil {
			t.Fatalf("a self-directed follow-up = %v, want no target", fu)
		}

		done := r.task("shipped", "u-will")
		did := str(done["id"])
		before := r.moveTo(did, "DONE")
		back := r.mona.call("add_follow_up", map[string]any{"projectId": r.p, "taskId": did, "body": "request changes", "reopen": true})
		bt := obj(back["task"])
		if obj(back["followUp"])["reopened"] != true || bt["status"] != "TODO" || bt["assignee"] != "u-will" || bt["version"] != before["version"].(float64)+1 {
			t.Fatalf("the reopen = %v (before version %v)", back, before["version"])
		}
		noop := r.mona.call("add_follow_up", map[string]any{"projectId": r.p, "taskId": did, "body": "again", "reopen": true})
		if obj(noop["followUp"])["reopened"] != false || obj(noop["task"])["status"] != "TODO" {
			t.Fatalf("a reopen from TODO = %v, want recorded only", noop)
		}
		t.Logf("%s add_follow_up: happy path to the assignee, the holder, self; reopen DONE -> TODO; a TODO reopen recorded only", r.engine)
		r.wantCode(r.will, "add_follow_up", map[string]any{"projectId": r.p, "taskId": id, "body": "x"}, "FORBIDDEN", "a worker: the tier is master")
		r.wantCode(r.grace, "add_follow_up", map[string]any{"projectId": r.p, "taskId": id, "body": "x"}, "FORBIDDEN", "another project's caller")
		r.wantCode(r.ada, "add_follow_up", map[string]any{"projectId": r.p, "taskId": "T-404", "body": "x"}, "NOT_FOUND", "a missing task")
		r.wantCode(r.ada, "add_follow_up", map[string]any{"projectId": r.p, "taskId": id, "body": ""}, "VALIDATION", "an empty body")
	})
}

// TestFollowUpsAckFollowUp: ack_follow_up's happy path (the target acks; the version unmoved),
// an admin acking another's, any writer an untargeted one, and each declared error: FORBIDDEN for
// a writer who is not the target and across projects, NOT_FOUND for a missing follow-up.
func TestFollowUpsAckFollowUp(t *testing.T) {
	notesStacks(t, func(t *testing.T, r *notesRig) {
		task := r.task("ack", "u-will")
		id := str(task["id"])
		add := func(c *caller, tid string) string {
			return str(obj(c.call("add_follow_up", map[string]any{"projectId": r.p, "taskId": tid, "body": "do it"})["followUp"])["id"])
		}
		f1, f2 := add(r.mona, id), add(r.mona, id)
		r.wantCode(r.max, "ack_follow_up", map[string]any{"projectId": r.p, "taskId": id, "followUpId": f1}, "FORBIDDEN", "a writer who is not the target")
		if got := obj(r.will.call("ack_follow_up", map[string]any{"projectId": r.p, "taskId": id, "followUpId": f1})["followUp"]); got["status"] != "acked" {
			t.Fatalf("the target's ack = %v", got)
		}
		if v := r.get(id)["version"]; v != task["version"] {
			t.Fatalf("the task moved from version %v to %v on an ack", task["version"], v)
		}
		if got := obj(r.ada.call("ack_follow_up", map[string]any{"projectId": r.p, "taskId": id, "followUpId": f2})["followUp"]); got["status"] != "acked" {
			t.Fatalf("the admin's ack = %v", got)
		}
		open := r.task("open", "")
		f3 := add(r.mona, str(open["id"]))
		if got := obj(r.max.call("ack_follow_up", map[string]any{"projectId": r.p, "taskId": str(open["id"]), "followUpId": f3})["followUp"]); got["status"] != "acked" || got["target"] != nil {
			t.Fatalf("any writer's ack of an untargeted one = %v", got)
		}
		t.Logf("%s ack_follow_up: happy path by the target, an admin, and any writer for an untargeted one", r.engine)
		r.wantCode(r.grace, "ack_follow_up", map[string]any{"projectId": r.p, "taskId": id, "followUpId": f1}, "FORBIDDEN", "another project's caller")
		r.wantCode(r.will, "ack_follow_up", map[string]any{"projectId": r.p, "taskId": id, "followUpId": "FU-404"}, "NOT_FOUND", "a missing follow-up")
		r.wantCode(r.will, "ack_follow_up", map[string]any{"projectId": r.p, "taskId": "T-404", "followUpId": f1}, "NOT_FOUND", "a missing task")
	})
}

// TestFollowUpsListFollowUps: list_follow_ups' happy path (oldest first, openOnly, paged) and each
// declared error: VALIDATION (a malformed cursor), NOT_FOUND for a missing task, FORBIDDEN across
// projects.
func TestFollowUpsListFollowUps(t *testing.T) {
	notesStacks(t, func(t *testing.T, r *notesRig) {
		id := str(r.task("list", "")["id"])
		var ids []string
		for _, b := range []string{"one", "two", "three"} {
			ids = append(ids, str(obj(r.ada.call("add_follow_up", map[string]any{"projectId": r.p, "taskId": id, "body": b})["followUp"])["id"]))
			tick()
		}
		r.ada.call("ack_follow_up", map[string]any{"projectId": r.p, "taskId": id, "followUpId": ids[0]})
		all := r.will.call("list_follow_ups", map[string]any{"projectId": r.p, "taskId": id})["followUps"].([]any)
		open := r.will.call("list_follow_ups", map[string]any{"projectId": r.p, "taskId": id, "openOnly": true})["followUps"].([]any)
		if len(all) != 3 || obj(all[0])["body"] != "one" || obj(all[0])["status"] != "acked" || len(open) != 2 || obj(open[0])["body"] != "two" {
			t.Fatalf("list_follow_ups all %v, open %v", all, open)
		}
		page := r.will.call("list_follow_ups", map[string]any{"projectId": r.p, "taskId": id, "limit": 2})
		if len(page["followUps"].([]any)) != 2 || str(page["nextCursor"]) == "" {
			t.Fatalf("list_follow_ups limit 2 = %v", page)
		}
		t.Logf("%s list_follow_ups: happy path, oldest first, openOnly, paged", r.engine)
		r.wantCode(r.ada, "list_follow_ups", map[string]any{"projectId": r.p, "taskId": id, "cursor": "nope!"}, "VALIDATION", "a malformed cursor")
		r.wantCode(r.ada, "list_follow_ups", map[string]any{"projectId": r.p, "taskId": "T-404"}, "NOT_FOUND", "a missing task")
		r.wantCode(r.grace, "list_follow_ups", map[string]any{"projectId": r.p, "taskId": id}, "FORBIDDEN", "another project's caller")
	})
}
