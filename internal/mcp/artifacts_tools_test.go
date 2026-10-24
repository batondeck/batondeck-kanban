package mcp_test

import (
	"strings"
	"testing"
)

// TestArtifactsAddArtifact: add_artifact's happy path (two kinds, the task's version moved once,
// the entries on the task and on get_task), the de-duplicated retry (added 0, case-folded url),
// and each declared error: VALIDATION (no url or ref, a non-http url, the 50 cap), STALE on a
// supplied version, NOT_FOUND for a missing task, FORBIDDEN across projects and for a viewer-less
// grace.
func TestArtifactsAddArtifact(t *testing.T) {
	notesStacks(t, func(t *testing.T, r *notesRig) {
		task := r.task("evidence", "")
		id := str(task["id"])
		v := task["version"].(float64)
		got := r.ada.call("add_artifact", map[string]any{"projectId": r.p, "taskId": id, "version": v, "artifacts": []any{
			map[string]any{"kind": "pr", "url": " https://example.com/PR/1 ", "repo": "org/repo", "title": "the fix"},
			map[string]any{"kind": "branch", "ref": "feat/x"},
		}})
		out := obj(got["task"])
		arts, _ := out["artifacts"].([]any)
		if got["added"] != float64(2) || out["version"] != v+1 || len(arts) != 2 {
			t.Fatalf("add_artifact = %v", got)
		}
		// Entries of one call share a millisecond and list in (ts, id) order, not the call's.
		pr := obj(arts[0])
		if pr["kind"] != "pr" {
			pr = obj(arts[1])
		}
		if pr["kind"] != "pr" || pr["url"] != "https://example.com/PR/1" || pr["addedBy"] != "u-ada" || pr["addedByAgent"] != "agent-ada" || pr["ref"] != nil {
			t.Fatalf("the pr entry = %v", pr)
		}
		t.Logf("%s add_artifact: happy path, added 2, version %v -> %v", r.engine, v, out["version"])
		again := r.will.call("add_artifact", map[string]any{"projectId": r.p, "taskId": id, "artifacts": []any{map[string]any{"kind": "pr", "url": "https://example.com/pr/1"}}})
		if again["added"] != float64(0) || len(obj(again["task"])["artifacts"].([]any)) != 2 {
			t.Fatalf("the retry = %v, want added 0", again)
		}
		if n := len(r.get(id)["artifacts"].([]any)); n != 2 {
			t.Fatalf("get_task carries %d artifacts, want 2", n)
		}

		one := func(a map[string]any) map[string]any {
			return map[string]any{"projectId": r.p, "taskId": id, "artifacts": []any{a}}
		}
		r.wantCode(r.ada, "add_artifact", one(map[string]any{"kind": "pr"}), "VALIDATION", "neither url nor ref")
		r.wantCode(r.ada, "add_artifact", one(map[string]any{"kind": "doc", "url": "ftp://example.com/x"}), "VALIDATION", "not an http(s) url")
		var many []any
		for i := range 49 {
			many = append(many, map[string]any{"kind": "commit", "ref": "c" + strings.Repeat("0", i)})
		}
		r.wantCode(r.ada, "add_artifact", map[string]any{"projectId": r.p, "taskId": id, "artifacts": many}, "VALIDATION", "the 50 per task cap")
		r.wantCode(r.ada, "add_artifact", map[string]any{"projectId": r.p, "taskId": id, "version": 1, "artifacts": []any{map[string]any{"kind": "file", "ref": "a.go"}}}, "STALE", "a stale version")
		r.wantCode(r.ada, "add_artifact", map[string]any{"projectId": r.p, "taskId": "T-404", "artifacts": []any{map[string]any{"kind": "file", "ref": "a.go"}}}, "NOT_FOUND", "a missing task")
		r.wantCode(r.grace, "add_artifact", one(map[string]any{"kind": "file", "ref": "a.go"}), "FORBIDDEN", "another project's caller")
		qt := str(obj(r.grace.call("create_task", map[string]any{"projectId": r.q, "boardId": r.qb, "title": "q's"})["task"])["id"])
		r.wantCode(r.ada, "add_artifact", map[string]any{"projectId": r.q, "taskId": qt, "artifacts": []any{map[string]any{"kind": "file", "ref": "a.go"}}}, "FORBIDDEN", "a project ada is not in")
		if n := len(r.get(id)["artifacts"].([]any)); n != 2 {
			t.Fatalf("the refused calls left %d artifacts, want 2", n)
		}
	})
}

// TestArtifactsRetractArtifact: retract_artifact's happy path (the tombstone, its by, agent and
// reason), the safe retry (retracted 0), a re-record of the tombstoned key, and each declared
// error: NOT_FOUND for a selector matching nothing and for a missing task, VALIDATION for an empty
// reason and a selector with neither url nor ref, STALE, and FORBIDDEN across projects.
func TestArtifactsRetractArtifact(t *testing.T) {
	notesStacks(t, func(t *testing.T, r *notesRig) {
		id := str(r.task("retract", "")["id"])
		r.ada.call("add_artifact", map[string]any{"projectId": r.p, "taskId": id, "artifacts": []any{map[string]any{"kind": "pr", "url": "https://example.com/pr/9", "repo": "wrong/repo"}}})
		sel := map[string]any{"kind": "pr", "url": "https://EXAMPLE.com/pr/9"}
		got := r.will.call("retract_artifact", map[string]any{"projectId": r.p, "taskId": id, "artifact": sel, "reason": "wrong repo"})
		tomb := obj(obj(obj(got["task"])["artifacts"].([]any)[0])["retracted"])
		if got["retracted"] != float64(1) || tomb["by"] != "u-will" || tomb["byAgent"] != "agent-will" || tomb["reason"] != "wrong repo" || str(tomb["at"]) == "" {
			t.Fatalf("retract_artifact = %v", got)
		}
		t.Logf("%s retract_artifact: happy path, retracted 1", r.engine)
		if again := r.will.call("retract_artifact", map[string]any{"projectId": r.p, "taskId": id, "artifact": sel, "reason": "again"}); again["retracted"] != float64(0) {
			t.Fatalf("the retry = %v, want retracted 0", again)
		}
		if re := r.ada.call("add_artifact", map[string]any{"projectId": r.p, "taskId": id, "artifacts": []any{map[string]any{"kind": "pr", "url": "https://example.com/pr/9", "repo": "right/repo"}}}); re["added"] != float64(1) {
			t.Fatalf("re-recording the tombstoned key = %v, want added 1", re)
		}

		base := func(k, v string) map[string]any {
			a := map[string]any{"projectId": r.p, "taskId": id, "artifact": sel, "reason": "x"}
			if k != "" {
				a[k] = v
			}
			return a
		}
		r.wantCode(r.ada, "retract_artifact", map[string]any{"projectId": r.p, "taskId": id, "artifact": map[string]any{"kind": "commit", "ref": "abc"}, "reason": "x"}, "NOT_FOUND", "a selector matching nothing")
		r.wantCode(r.ada, "retract_artifact", map[string]any{"projectId": r.p, "taskId": "T-404", "artifact": sel, "reason": "x"}, "NOT_FOUND", "a missing task")
		r.wantCode(r.ada, "retract_artifact", base("reason", "   "), "VALIDATION", "an empty reason")
		r.wantCode(r.ada, "retract_artifact", map[string]any{"projectId": r.p, "taskId": id, "artifact": map[string]any{"kind": "pr"}, "reason": "x"}, "VALIDATION", "neither url nor ref")
		stale := base("", "")
		stale["version"] = 1
		r.wantCode(r.ada, "retract_artifact", stale, "STALE", "a stale version")
		r.wantCode(r.grace, "retract_artifact", base("", ""), "FORBIDDEN", "another project's caller")
	})
}

// TestArtifactsMissingArtifacts (AC3): list_tasks {missingArtifacts:true} returns a DONE task
// without a pr artifact, and one whose only pr is a tombstone, and not the one with a live pr.
func TestArtifactsMissingArtifacts(t *testing.T) {
	notesStacks(t, func(t *testing.T, r *notesRig) {
		with, without, tomb := str(r.task("with a pr", "")["id"]), str(r.task("without", "")["id"]), str(r.task("tombstoned", "")["id"])
		for _, id := range []string{with, tomb} {
			r.ada.call("add_artifact", map[string]any{"projectId": r.p, "taskId": id, "artifacts": []any{map[string]any{"kind": "pr", "url": "https://example.com/pr/" + id}}})
		}
		r.ada.call("retract_artifact", map[string]any{"projectId": r.p, "taskId": tomb, "artifact": map[string]any{"kind": "pr", "url": "https://example.com/pr/" + tomb}, "reason": "not it"})
		for _, id := range []string{with, without, tomb} {
			r.moveTo(id, "DONE")
		}
		got := r.ada.call("list_tasks", map[string]any{"projectId": r.p, "boardId": r.b, "status": "DONE", "missingArtifacts": true})
		var ids []string
		for _, x := range got["tasks"].([]any) {
			ids = append(ids, str(obj(x)["id"]))
		}
		if len(ids) != 2 || !strings.Contains(strings.Join(ids, " "), without) || !strings.Contains(strings.Join(ids, " "), tomb) || strings.Contains(" "+strings.Join(ids, " ")+" ", " "+with+" ") {
			t.Fatalf("list_tasks missingArtifacts DONE = %v; want %s and %s, not %s", ids, without, tomb, with)
		}
		all := r.ada.call("list_tasks", map[string]any{"projectId": r.p, "boardId": r.b, "status": "DONE"})
		t.Logf("%s list_tasks DONE: %d tasks; missingArtifacts: %v (with a live pr: %s)", r.engine, len(all["tasks"].([]any)), ids, with)
	})
}
