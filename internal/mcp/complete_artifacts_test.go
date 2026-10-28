package mcp_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/auth/authfake"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	bk "github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/mcp/tools"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// The P-235 and P-236 rig: complete_task's artifacts and get_task_context's joins on the real
// stack (servicetest.Open, so attach_file is served), every selected engine. ada owns project p
// (its admin); bob is p's worker.

const (
	artAda = "bk_art_ada"
	artBob = "bk_art_bob"
)

type artRig struct {
	t        *testing.T
	s        *servicetest.Stack
	ada, bob *caller
	p, b     string
	engine   string
}

func artStacks(t *testing.T, fn func(t *testing.T, r *artRig)) {
	res := authfake.Resolver{Callers: map[string]domain.Caller{
		artAda: {Identity: "u-ada", Agent: "agent-ada", TokenScope: "u-ada"},
		artBob: {Identity: "u-bob", Agent: "agent-bob", TokenScope: "u-bob"},
	}}
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := bk.BootHarness(t, s.Services, res)
		tools.Register(h.Server(), s.Services)
		conn := func(tok string) *caller { return &caller{t: t, cs: h.Connect(t, tok), called: map[string]int{}} }
		r := &artRig{t: t, s: s, engine: s.Engine, ada: conn(artAda), bob: conn(artBob)}
		r.p = str(r.ada.call("create_project", map[string]any{"name": "Ada's"})["projectId"])
		r.b = str(obj(r.ada.call("create_board", map[string]any{"projectId": r.p, "name": "Main"})["board"])["id"])
		r.ada.call("add_member", map[string]any{"projectId": r.p, "identityId": "u-bob", "role": "worker"})
		fn(t, r)
	})
}

// settings is ada's update_settings on p.
func (r *artRig) settings(kv map[string]any) {
	r.t.Helper()
	args := map[string]any{"projectId": r.p}
	for k, v := range kv {
		args[k] = v
	}
	r.ada.call("update_settings", args)
}

// claimed is a new task on p claimed by c: its id and lease.
func (r *artRig) claimed(c *caller, title string) (string, string) {
	r.t.Helper()
	id := str(obj(r.ada.call("create_task", map[string]any{"projectId": r.p, "boardId": r.b, "title": title})["task"])["id"])
	return id, str(c.call("claim_task", map[string]any{"projectId": r.p, "taskId": id})["leaseId"])
}

func (r *artRig) completeArgs(id, lease string, arts []any) map[string]any {
	a := map[string]any{"projectId": r.p, "taskId": id, "leaseId": lease, "deliverable": "done"}
	if arts != nil {
		a["artifacts"] = arts
	}
	return a
}

func (r *artRig) get(id string) map[string]any {
	r.t.Helper()
	return obj(r.ada.call("get_task", map[string]any{"projectId": r.p, "taskId": id})["task"])
}

func (r *artRig) arts(id string) []any {
	r.t.Helper()
	a, _ := r.get(id)["artifacts"].([]any)
	return a
}

// events is the data of each of the task's events of type typ, read from the store.
func (r *artRig) events(id string, typ domain.EventType) []map[string]any {
	r.t.Helper()
	page, err := r.s.Store.ListTaskEvents(context.Background(), r.p, id, "", 200)
	if err != nil {
		r.t.Fatal(err)
	}
	var out []map[string]any
	for _, e := range page.Events {
		if e.Type == typ {
			var d map[string]any
			if err := json.Unmarshal(e.Data, &d); err != nil {
				r.t.Fatal(err)
			}
			out = append(out, d)
		}
	}
	return out
}

func (r *artRig) wantCode(c *caller, tool string, args map[string]any, code, why string) {
	r.t.Helper()
	e := bk.ToolError(r.t, c.cs, tool, args)
	if e["code"] != code {
		r.t.Fatalf("%s (%s): %v, want %s", tool, why, e, code)
	}
	r.t.Logf("%s %s: %s (%s): %v", r.engine, tool, code, why, e["message"])
}

// liveArts is the entries of arts without a retracted tombstone, as kind:url-or-ref.
func liveArts(arts []any) []string {
	var out []string
	for _, a := range arts {
		m := obj(a)
		if m["retracted"] != nil {
			continue
		}
		out = append(out, str(m["kind"])+":"+str(m["url"])+str(m["ref"]))
	}
	return out
}

func hasArt(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestCompleteArtifactsMerge (P-235 AC1): a completion with a branch and a pr entry lands DONE
// with both on get_task and in the answer's task; a duplicate of a live entry adds nothing and a
// retracted twin is added again; the REVIEW landing merges too.
func TestCompleteArtifactsMerge(t *testing.T) {
	artStacks(t, func(t *testing.T, r *artRig) {
		r.settings(map[string]any{"reviewEnabled": false})
		id, lease := r.claimed(r.ada, "both")
		got := r.ada.call("complete_task", r.completeArgs(id, lease, []any{
			map[string]any{"kind": "branch", "ref": "feat/x"},
			map[string]any{"kind": "pr", "url": "https://example.com/pr/1"},
		}))
		task := obj(got["task"])
		if task["status"] != "DONE" {
			t.Fatalf("complete_task = %v, want DONE", got)
		}
		for name, arts := range map[string][]any{"the answer's task": task["artifacts"].([]any), "get_task": r.arts(id)} {
			l := liveArts(arts)
			if len(arts) != 2 || !hasArt(l, "branch:feat/x") || !hasArt(l, "pr:https://example.com/pr/1") {
				t.Fatalf("%s artifacts = %v, want the branch and the pr", name, arts)
			}
		}
		if ev := r.events(id, domain.EventTaskCompleted); len(ev) != 1 || ev[0]["artifacts"] != float64(2) {
			t.Fatalf("task.completed = %v, want artifacts 2", ev)
		}
		if w, _ := got["warnings"].([]any); len(w) != 0 {
			t.Fatalf("warnings %v with two artifacts", w)
		}
		t.Logf("%s complete_task: DONE with the branch and the pr on the answer and on get_task", r.engine)

		id, lease = r.claimed(r.ada, "dedupe")
		r.ada.call("add_artifact", map[string]any{"projectId": r.p, "taskId": id, "artifacts": []any{
			map[string]any{"kind": "branch", "ref": "feat/y"},
			map[string]any{"kind": "pr", "url": "https://example.com/pr/2"},
			map[string]any{"kind": "pr", "url": "https://example.com/pr/3"},
		}})
		r.ada.call("retract_artifact", map[string]any{"projectId": r.p, "taskId": id, "artifact": map[string]any{"kind": "pr", "url": "https://example.com/pr/2"}, "reason": "wrong pr"})
		got = r.ada.call("complete_task", r.completeArgs(id, lease, []any{
			map[string]any{"kind": "branch", "ref": "feat/y"},
			map[string]any{"kind": "pr", "url": "https://EXAMPLE.com/pr/2"},
			map[string]any{"kind": "pr", "url": "https://EXAMPLE.com/pr/3"},
		}))
		arts := r.arts(id)
		l := liveArts(arts)
		// The key case-folds the url; the entry keeps it as written (add_artifact's rule).
		if len(arts) != 4 || len(l) != 3 || !hasArt(l, "branch:feat/y") || !hasArt(l, "pr:https://EXAMPLE.com/pr/2") || !hasArt(l, "pr:https://example.com/pr/3") {
			t.Fatalf("after the duplicates and the twin: %v, want the branch and pr/3 once, the tombstone and a live pr/2", arts)
		}
		if ev := r.events(id, domain.EventTaskCompleted); len(ev) != 1 || ev[0]["artifacts"] != float64(1) {
			t.Fatalf("task.completed = %v, want artifacts 1 (the twin only)", ev)
		}
		t.Logf("%s complete_task: the live duplicate added nothing, the retracted twin was added again", r.engine)

		r.settings(map[string]any{"reviewEnabled": true, "defaultReviewer": "u-bob"})
		id, lease = r.claimed(r.ada, "review")
		got = r.ada.call("complete_task", r.completeArgs(id, lease, []any{map[string]any{"kind": "branch", "ref": "feat/z"}}))
		task = obj(got["task"])
		if task["status"] != "REVIEW" || obj(got["handover"])["reviewer"] != "u-bob" || len(task["artifacts"].([]any)) != 1 {
			t.Fatalf("the REVIEW landing = %v", got)
		}
		if l := liveArts(r.arts(id)); len(l) != 1 || l[0] != "branch:feat/z" {
			t.Fatalf("get_task after the REVIEW landing: %v", l)
		}
		t.Logf("%s complete_task: REVIEW, reviewer u-bob, the branch merged", r.engine)
	})
}

// TestCompleteArtifactsRefusals (P-235 AC1): each VALIDATION case leaves the task IN_PROGRESS with
// no new artifact.
func TestCompleteArtifactsRefusals(t *testing.T) {
	artStacks(t, func(t *testing.T, r *artRig) {
		r.settings(map[string]any{"reviewEnabled": false})
		id, lease := r.claimed(r.ada, "refused")
		var many []any
		for i := range 50 {
			many = append(many, map[string]any{"kind": "commit", "ref": "c" + strings.Repeat("0", i)})
		}
		// 51 entries, one a duplicate of another: the request bound refuses them though the merge
		// would write 50.
		over := append(append([]any{}, many...), many[0])
		for _, c := range []struct {
			why  string
			arts []any
		}{
			{"an unknown kind", []any{map[string]any{"kind": "zip", "ref": "a"}}},
			{"neither url nor ref", []any{map[string]any{"kind": "pr"}}},
			{"a non-http url", []any{map[string]any{"kind": "doc", "url": "ftp://example.com/x"}}},
			{"51 entries", over},
		} {
			r.wantCode(r.ada, "complete_task", r.completeArgs(id, lease, c.arts), "VALIDATION", c.why)
			if task := r.get(id); task["status"] != "IN_PROGRESS" || len(task["artifacts"].([]any)) != 0 {
				t.Fatalf("after %s: %s with %v", c.why, task["status"], task["artifacts"])
			}
		}

		// The cap counts tombstones: 50 entries, one retracted, and one more is refused.
		r.ada.call("add_artifact", map[string]any{"projectId": r.p, "taskId": id, "artifacts": many})
		r.ada.call("retract_artifact", map[string]any{"projectId": r.p, "taskId": id, "artifact": map[string]any{"kind": "commit", "ref": "c"}, "reason": "x"})
		r.wantCode(r.ada, "complete_task", r.completeArgs(id, lease, []any{map[string]any{"kind": "branch", "ref": "one-more"}}), "VALIDATION", "the cap through tombstones")
		if task := r.get(id); task["status"] != "IN_PROGRESS" || len(task["artifacts"].([]any)) != 50 {
			t.Fatalf("after the cap: %s with %d artifacts", task["status"], len(task["artifacts"].([]any)))
		}
		if ev := r.events(id, domain.EventTaskCompleted); len(ev) != 0 {
			t.Fatalf("a refused completion wrote %v", ev)
		}
	})
}

// TestCompleteArtifactsPolicy (P-235 AC1): enforce refuses no artifact (and a tombstone only) and
// passes one live artifact; warn and unset carry the warning and one task.artifact_missing event;
// off has neither.
func TestCompleteArtifactsPolicy(t *testing.T) {
	artStacks(t, func(t *testing.T, r *artRig) {
		r.settings(map[string]any{"reviewEnabled": false})
		for _, policy := range []string{"", "warn", "off"} {
			if policy != "" {
				r.settings(map[string]any{"artifactPolicy": policy})
			}
			id, lease := r.claimed(r.ada, "policy "+policy)
			got := r.ada.call("complete_task", r.completeArgs(id, lease, nil))
			w, _ := got["warnings"].([]any)
			ev := r.events(id, domain.EventTaskArtifactMissing)
			if obj(got["task"])["status"] != "DONE" {
				t.Fatalf("%q: %v", policy, got)
			}
			if policy == "off" {
				if len(w) != 0 || len(ev) != 0 {
					t.Fatalf("off: warnings %v, events %v; want neither", w, ev)
				}
				continue
			}
			if len(w) != 1 || !strings.Contains(str(w[0]), "no artifact") || len(ev) != 1 || ev[0]["policy"] != "warn" {
				t.Fatalf("%q: warnings %v, events %v; want the warning and one task.artifact_missing", policy, w, ev)
			}
			t.Logf("%s artifactPolicy %q: %q", r.engine, policy, w[0])
		}

		r.settings(map[string]any{"artifactPolicy": "enforce"})
		id, lease := r.claimed(r.ada, "enforce")
		r.wantCode(r.ada, "complete_task", r.completeArgs(id, lease, nil), "VALIDATION", "enforce with no artifact")
		r.ada.call("add_artifact", map[string]any{"projectId": r.p, "taskId": id, "artifacts": []any{map[string]any{"kind": "branch", "ref": "gone"}}})
		r.ada.call("retract_artifact", map[string]any{"projectId": r.p, "taskId": id, "artifact": map[string]any{"kind": "branch", "ref": "gone"}, "reason": "x"})
		r.wantCode(r.ada, "complete_task", r.completeArgs(id, lease, nil), "VALIDATION", "enforce with a tombstone only")
		if task := r.get(id); task["status"] != "IN_PROGRESS" {
			t.Fatalf("enforce refused, yet %s", task["status"])
		}
		got := r.ada.call("complete_task", r.completeArgs(id, lease, []any{map[string]any{"kind": "branch", "ref": "kept"}}))
		if w, _ := got["warnings"].([]any); obj(got["task"])["status"] != "DONE" || len(w) != 0 {
			t.Fatalf("enforce with one live artifact = %v", got)
		}
		id, lease = r.claimed(r.ada, "enforce, bound before")
		r.ada.call("add_artifact", map[string]any{"projectId": r.p, "taskId": id, "artifacts": []any{map[string]any{"kind": "pr", "url": "https://example.com/pr/7"}}})
		if got := r.ada.call("complete_task", r.completeArgs(id, lease, nil)); obj(got["task"])["status"] != "DONE" {
			t.Fatalf("enforce with an artifact bound before = %v", got)
		}
		t.Logf("%s artifactPolicy enforce: refused bare and tombstone-only, passed one live artifact", r.engine)
	})
}
