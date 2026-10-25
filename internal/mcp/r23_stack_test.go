package mcp_test

import (
	"testing"

	bk "github.com/batondeck/batondeck-kanban/internal/mcp"
)

// r23OnTheStack drives the seven R2.3 tools once each on the real stack: a follow-up lands in
// grace's feed and she marks it read, ada registers a profile and reads her stats, ranks the board,
// scans it for anomalies and fans a held task out.
func r23OnTheStack(t *testing.T, h *bk.Harness, c *caller, p, b string, task func(string) map[string]any, get func(string) map[string]any) {
	t.Helper()
	g := &caller{t: t, cs: h.Connect(t, stackGrace), called: c.called}
	id := str(task("for grace")["id"])
	c.call("update_task", map[string]any{"projectId": p, "taskId": id, "version": get(id)["version"], "patch": map[string]any{"assignee": "u-grace"}})
	c.call("add_follow_up", map[string]any{"projectId": p, "taskId": id, "body": "over to you"})
	// Two unread: this follow-up and the stack's earlier handoff of a task to u-grace (P-275).
	feed := g.call("list_notifications", map[string]any{})
	if feed["unread"] != float64(2) {
		t.Fatalf("list_notifications: %v", feed)
	}
	if got := g.call("mark_notifications_read", map[string]any{"all": true}); got["updated"] != float64(2) {
		t.Fatalf("mark_notifications_read: %v", got)
	}
	c.call("register_agent_profile", map[string]any{"projectId": p, "capabilities": []any{"go"}})
	if got := c.call("get_skill_stats", map[string]any{"projectId": p}); got["profile"] == nil {
		t.Fatalf("get_skill_stats: %v", got)
	}
	if got := c.call("rank_tasks", map[string]any{"projectId": p, "boardId": b, "useProfile": true}); len(got["ranked"].([]any)) == 0 {
		t.Fatalf("rank_tasks: %v", got)
	}
	c.call("detect_anomalies", map[string]any{"projectId": p, "boardId": b})
	epic := str(task("epic")["id"])
	lease := str(c.call("claim_task", map[string]any{"projectId": p, "taskId": epic})["leaseId"])
	out := c.call("orchestrate_subtasks", map[string]any{"projectId": p, "parentTaskId": epic, "leaseId": lease, "children": []any{map[string]any{"title": "part"}}})
	if obj(out["parent"])["status"] != "BLOCKED" || len(out["children"].([]any)) != 1 {
		t.Fatalf("orchestrate_subtasks: %v", out)
	}
}
