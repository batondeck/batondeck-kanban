package mcp_test

import (
	"testing"
)

// TestResume (P-117, P-161 A40-1), on each engine over MCP: ada works a task the skill's way (a
// branch artifact, a commit artifact, a checkpoint note) and releases it; bob's claim_task, its
// idempotent replay, his re-claim, get_task_context and, on a second such task, claim_next all
// answer that branch, head commit and checkpoint. A task nobody worked answers resume null on all
// three tools.
func TestResume(t *testing.T) {
	artStacks(t, func(t *testing.T, r *artRig) {
		worked := func(title, branch string) string {
			t.Helper()
			id, lease := r.claimed(r.ada, title)
			r.ada.call("add_artifact", map[string]any{"projectId": r.p, "taskId": id, "artifacts": []any{
				map[string]any{"kind": "branch", "ref": branch},
				map[string]any{"kind": "commit", "ref": "0123abcd"},
			}})
			// The newest branch, withdrawn: never the one resume names.
			r.ada.call("add_artifact", map[string]any{"projectId": r.p, "taskId": id, "artifacts": []any{
				map[string]any{"kind": "branch", "ref": "feat/withdrawn"},
			}})
			r.ada.call("retract_artifact", map[string]any{"projectId": r.p, "taskId": id,
				"artifact": map[string]any{"kind": "branch", "ref": "feat/withdrawn"}, "reason": "wrong branch"})
			r.ada.call("add_context_item", map[string]any{"projectId": r.p, "taskId": id, "kind": "note", "body": "DONE: an older checkpoint"})
			r.ada.call("add_context_item", map[string]any{"projectId": r.p, "taskId": id, "kind": "note",
				"body": "## DONE\nthe parser (go test ./x ok)\n\n## NEXT\nwire the handler\n\n## REJECTED\na regexp per line: too slow\n"})
			r.ada.call("add_context_item", map[string]any{"projectId": r.p, "taskId": id, "kind": "decision", "body": "NEXT: a decision is no checkpoint"})
			r.ada.call("add_context_item", map[string]any{"projectId": r.p, "taskId": id, "kind": "note", "body": "a plain note, no heading"})
			r.ada.call("release_task", map[string]any{"projectId": r.p, "taskId": id, "leaseId": lease})
			return id
		}
		want := func(tool string, out map[string]any, branch string) {
			t.Helper()
			res, ok := out["resume"].(map[string]any)
			if !ok {
				t.Fatalf("%s: resume %v, want the earlier worker's", tool, out["resume"])
			}
			cp := obj(res["lastCheckpoint"])
			if res["branch"] != branch || res["headCommit"] != "0123abcd" || cp["DONE"] != "the parser (go test ./x ok)" ||
				cp["NEXT"] != "wire the handler" || cp["REJECTED"] != "a regexp per line: too slow" {
				t.Fatalf("%s: resume %v, want branch %s, commit 0123abcd and the newer checkpoint", tool, res, branch)
			}
			if v, present := cp["UNCERTAIN"]; !present || v != nil {
				t.Fatalf("%s: lastCheckpoint.UNCERTAIN %v (present %v), want null", tool, v, present)
			}
		}
		null := func(tool string, out map[string]any) {
			t.Helper()
			if v, present := out["resume"]; !present || v != nil {
				t.Fatalf("%s: resume %v (present %v), want null", tool, v, present)
			}
		}

		id := worked("Half done", "feat/half")
		claim := map[string]any{"projectId": r.p, "taskId": id, "idempotencyKey": "bob-claim-1"}
		want("claim_task", r.bob.call("claim_task", claim), "feat/half")
		want("claim_task replay", r.bob.call("claim_task", claim), "feat/half")
		want("claim_task re-claim", r.bob.call("claim_task", map[string]any{"projectId": r.p, "taskId": id}), "feat/half")
		want("get_task_context", r.bob.call("get_task_context", map[string]any{"projectId": r.p, "taskId": id}), "feat/half")
		// A one-item page holds no checkpoint: resume reads further than the page asked for.
		want("get_task_context limit 1", r.bob.call("get_task_context", map[string]any{"projectId": r.p, "taskId": id, "limit": 1}), "feat/half")
		want("get_task_context without items", r.bob.call("get_task_context", map[string]any{"projectId": r.p, "taskId": id, "include": []any{"summary"}}), "feat/half")

		next := worked("Also half done", "feat/next")
		got := r.bob.call("claim_next", map[string]any{"projectId": r.p, "boardId": r.b})
		if str(obj(got["task"])["id"]) != next {
			t.Fatalf("claim_next won %v, want %s", obj(got["task"])["id"], next)
		}
		want("claim_next", got, "feat/next")

		fresh := str(obj(r.ada.call("create_task", map[string]any{"projectId": r.p, "boardId": r.b, "title": "Untouched"})["task"])["id"])
		null("get_task_context fresh", r.bob.call("get_task_context", map[string]any{"projectId": r.p, "taskId": fresh}))
		null("claim_next fresh", r.bob.call("claim_next", map[string]any{"projectId": r.p, "boardId": r.b}))
		other := str(obj(r.ada.call("create_task", map[string]any{"projectId": r.p, "boardId": r.b, "title": "Untouched too"})["task"])["id"])
		null("claim_task fresh", r.bob.call("claim_task", map[string]any{"projectId": r.p, "taskId": other}))
		t.Logf("%s: claim_task, its replay and re-claim, get_task_context and claim_next carry resume; fresh tasks null", r.engine)
	})
}
