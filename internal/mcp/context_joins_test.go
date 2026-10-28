package mcp_test

import (
	"strings"
	"testing"
)

// TestTaskContextJoins (P-236 AC1): get_task_context returns the task's artifacts (a retracted one
// with its tombstone), its attachments (a pending one with gcsUri blob:… and a null sha256) and its
// open follow-ups with their count; with include ["items"] the attachments and follow-ups are
// empty and the count 0, and the artifacts remain. bob, p's worker, reads what ada recorded.
func TestTaskContextJoins(t *testing.T) {
	artStacks(t, func(t *testing.T, r *artRig) {
		id, _ := r.claimed(r.ada, "joins")
		r.ada.call("add_artifact", map[string]any{"projectId": r.p, "taskId": id, "artifacts": []any{
			map[string]any{"kind": "branch", "ref": "feat/x"},
			map[string]any{"kind": "pr", "url": "https://example.com/pr/1"},
		}})
		r.ada.call("retract_artifact", map[string]any{"projectId": r.p, "taskId": id, "artifact": map[string]any{"kind": "pr", "url": "https://example.com/pr/1"}, "reason": "wrong pr"})
		att := str(r.ada.call("attach_file", map[string]any{"projectId": r.p, "taskId": id, "fileName": "shot.png", "mimeType": "image/png", "kind": "image", "bytes": 3})["attachmentId"])
		open := obj(r.ada.call("add_follow_up", map[string]any{"projectId": r.p, "taskId": id, "body": "still open"})["followUp"])
		tick()
		acked := obj(r.ada.call("add_follow_up", map[string]any{"projectId": r.p, "taskId": id, "body": "answered"})["followUp"])
		r.ada.call("ack_follow_up", map[string]any{"projectId": r.p, "taskId": id, "followUpId": acked["id"]})

		got := r.bob.call("get_task_context", map[string]any{"projectId": r.p, "taskId": id})
		arts, _ := got["artifacts"].([]any)
		var tomb, branch map[string]any
		for _, a := range arts {
			switch m := obj(a); m["kind"] {
			case "pr":
				tomb = m
			case "branch":
				branch = m
			}
		}
		if len(arts) != 2 || branch["ref"] != "feat/x" || branch["retracted"] != nil || tomb == nil || obj(tomb["retracted"])["reason"] != "wrong pr" {
			t.Fatalf("artifacts = %v, want the branch and the retracted pr", arts)
		}
		atts, _ := got["attachments"].([]any)
		var a map[string]any
		if len(atts) == 1 {
			a = obj(atts[0])
		}
		if len(atts) != 1 || a["id"] != att || a["status"] != "pending" || !strings.HasPrefix(str(a["gcsUri"]), "blob:") || !strings.HasSuffix(str(a["gcsUri"]), att) ||
			a["sha256"] != nil || a["kind"] != "image" || a["mimeType"] != "image/png" || a["bytes"] != float64(3) || a["createdBy"] != "u-ada" {
			t.Fatalf("attachments = %v, want one pending attachment %s", atts, att)
		}
		if _, ok := a["downloadUrl"]; ok {
			t.Fatalf("the attachment brief carries a downloadUrl: %v", a)
		}
		fus, _ := got["openFollowUps"].([]any)
		if len(fus) != 1 || obj(fus[0])["id"] != open["id"] || obj(fus[0])["body"] != "still open" || got["openFollowUpCount"] != float64(1) {
			t.Fatalf("openFollowUps = %v, count %v; want the open one and 1", fus, got["openFollowUpCount"])
		}
		t.Logf("%s get_task_context: artifacts %d (one retracted), attachment %s %s sha256 %v, open follow-ups %d of 2",
			r.engine, len(arts), a["status"], a["gcsUri"], a["sha256"], len(fus))
		tick()
		r.ada.call("add_follow_up", map[string]any{"projectId": r.p, "taskId": id, "body": "one more"})
		if n := r.bob.call("get_task_context", map[string]any{"projectId": r.p, "taskId": id})["openFollowUpCount"]; n != float64(2) {
			t.Fatalf("openFollowUpCount after a second open follow-up = %v, want 2", n)
		}

		items := r.bob.call("get_task_context", map[string]any{"projectId": r.p, "taskId": id, "include": []any{"items"}})
		if n := len(items["artifacts"].([]any)); n != 2 {
			t.Fatalf("include items: %d artifacts, want 2", n)
		}
		if len(items["attachments"].([]any)) != 0 || len(items["openFollowUps"].([]any)) != 0 || items["openFollowUpCount"] != float64(0) {
			t.Fatalf("include items: attachments %v, openFollowUps %v, count %v; want empty and 0", items["attachments"], items["openFollowUps"], items["openFollowUpCount"])
		}
		t.Logf("%s get_task_context include [items]: artifacts 2, attachments and follow-ups empty, count 0", r.engine)
	})
}
