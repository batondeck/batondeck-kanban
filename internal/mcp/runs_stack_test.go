package mcp_test

import "testing"

// runsAndSprintsOnTheStack drives the ten runs and sprints tools once each on the real stack: a race
// opened, grown and picked (REVIEW: a new project reviews), a second race cancelled, and a sprint created, filled, read, listed and
// checkpointed.
func runsAndSprintsOnTheStack(t *testing.T, c *caller, p, b string, task func(string) map[string]any, get func(string) map[string]any) {
	t.Helper()
	parent := str(task("raced")["id"])
	started := c.call("start_runs", map[string]any{"projectId": p, "taskId": parent, "version": get(parent)["version"], "count": 2})
	runs := started["runs"].([]any)
	if len(runs) != 2 || obj(started["task"])["racing"] != true {
		t.Fatalf("start_runs: %v", started)
	}
	opened := obj(c.call("open_run", map[string]any{"projectId": p, "taskId": parent})["run"])
	if str(opened["runOf"]) != parent {
		t.Fatalf("open_run: %v", opened)
	}
	win := str(obj(runs[0])["id"])
	lease := str(c.call("claim_task", map[string]any{"projectId": p, "taskId": win})["leaseId"])
	c.call("complete_task", map[string]any{"projectId": p, "taskId": win, "leaseId": lease, "deliverable": "the winning draft"})
	if n := len(c.call("list_runs", map[string]any{"projectId": p, "taskId": parent})["runs"].([]any)); n != 3 {
		t.Fatalf("list_runs: %d runs, want 3", n)
	}
	picked := obj(c.call("pick_run", map[string]any{"projectId": p, "taskId": parent, "runTaskId": win, "version": get(parent)["version"]})["task"])
	if picked["status"] != "REVIEW" || obj(picked["review"])["completedBy"] != "u-ada" || picked["racing"] == true || str(picked["deliverable"]) != "the winning draft" {
		t.Fatalf("pick_run: %v", picked)
	}
	again := str(task("raced again")["id"])
	c.call("start_runs", map[string]any{"projectId": p, "taskId": again, "version": get(again)["version"], "agents": []any{"u-grace"}})
	if back := obj(c.call("cancel_runs", map[string]any{"projectId": p, "taskId": again, "version": get(again)["version"]})["task"]); back["status"] != "TODO" || back["racing"] == true {
		t.Fatalf("cancel_runs: %v", back)
	}

	sp := obj(c.call("create_sprint", map[string]any{"projectId": p, "boardId": b, "name": "S1", "goal": "ship", "acceptance": "it ships"})["sprint"])
	sid := str(sp["id"])
	c.call("set_sprint", map[string]any{"projectId": p, "taskId": again, "version": get(again)["version"], "sprintId": sid, "action": "add"})
	read := c.call("get_sprint", map[string]any{"projectId": p, "sprintId": sid, "include": []any{"deliverables", "memory"}})
	if n := len(read["tasks"].([]any)); n != 1 || obj(read["counts"])["ready"] != float64(1) {
		t.Fatalf("get_sprint: %v", read)
	}
	if n := len(c.call("list_sprints", map[string]any{"projectId": p, "status": "PROPOSED"})["sprints"].([]any)); n != 1 {
		t.Fatalf("list_sprints: %d, want 1", n)
	}
	c.call("update_sprint", map[string]any{"projectId": p, "sprintId": sid, "version": sp["version"], "patch": map[string]any{"checkpoint": "half way", "eventCursor": nil}})
}
