package mcp_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	bk "github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// The runs and sprints tools on the real stack, every selected engine (BATONDECK_TEST_ENGINE): each
// tool's happy path and each error it declares, the losing runs deleted through the cascade (AC1,
// AC2) and the sprint semantics of plan §Fold 1 and 2 (AC3).

type raceRig struct {
	t          *testing.T
	s          *servicetest.Stack
	ada, grace *caller
	p, b       string // ada's project (grace a worker in it) and its board
	q          string // grace's own project
}

func raceStacks(t *testing.T, fn func(t *testing.T, r *raceRig)) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := bootStack(t, s)
		r := &raceRig{t: t, s: s,
			ada:   &caller{t: t, cs: h.Connect(t, stackOwner), called: map[string]int{}},
			grace: &caller{t: t, cs: h.Connect(t, stackGrace), called: map[string]int{}},
		}
		r.p = str(r.ada.call("create_project", map[string]any{"name": "Ada's"})["projectId"])
		r.b = str(obj(r.ada.call("create_board", map[string]any{"projectId": r.p, "name": "Main"})["board"])["id"])
		r.ada.call("add_member", map[string]any{"projectId": r.p, "identityId": "u-grace", "role": "worker"})
		r.q = str(r.grace.call("create_project", map[string]any{"name": "Grace's"})["projectId"])
		fn(t, r)
	})
}

func (r *raceRig) wantCode(c *caller, tool string, args map[string]any, code string) map[string]any {
	r.t.Helper()
	e := bk.ToolError(r.t, c.cs, tool, args)
	if e["code"] != code {
		r.t.Fatalf("%s %v: %v, want %s", tool, args, e, code)
	}
	return e
}

func (r *raceRig) task(title string, extra map[string]any) string {
	r.t.Helper()
	args := map[string]any{"projectId": r.p, "boardId": r.b, "title": title}
	for k, v := range extra {
		args[k] = v
	}
	return str(obj(r.ada.call("create_task", args)["task"])["id"])
}

func (r *raceRig) get(id string) map[string]any {
	r.t.Helper()
	return obj(r.ada.call("get_task", map[string]any{"projectId": r.p, "taskId": id})["task"])
}

func (r *raceRig) start(parent string, n int) []string {
	r.t.Helper()
	out := r.ada.call("start_runs", map[string]any{"projectId": r.p, "taskId": parent, "version": r.get(parent)["version"], "count": n})
	var ids []string
	for _, x := range out["runs"].([]any) {
		ids = append(ids, str(obj(x)["id"]))
	}
	return ids
}

// submit claims run as ada and completes it with a deliverable: REVIEW (a new project reviews).
func (r *raceRig) submit(run, deliverable string) {
	r.t.Helper()
	lease := str(r.ada.call("claim_task", map[string]any{"projectId": r.p, "taskId": run})["leaseId"])
	r.ada.call("complete_task", map[string]any{"projectId": r.p, "taskId": run, "leaseId": lease, "deliverable": deliverable})
}

// feed is the board's activity feed as a client reads it (the board-feed resource), every page.
func (r *raceRig) feed() []map[string]any {
	r.t.Helper()
	var out []map[string]any
	uri := bk.Scheme + "://" + r.p + "/board/" + r.b + "/feed"
	for range 50 {
		res, err := r.ada.cs.ReadResource(testCtx(r.t), &sdk.ReadResourceParams{URI: uri})
		if err != nil || len(res.Contents) != 1 {
			r.t.Fatalf("read %s: %+v, %v", uri, res, err)
		}
		var page struct {
			Events     []map[string]any `json:"events"`
			NextCursor string           `json:"nextCursor"`
		}
		if err := json.Unmarshal([]byte(res.Contents[0].Text), &page); err != nil {
			r.t.Fatal(err)
		}
		out = append(out, page.Events...)
		if page.NextCursor == "" {
			return out
		}
		uri = bk.Scheme + "://" + r.p + "/board/" + r.b + "/feed?cursor=" + page.NextCursor
	}
	r.t.Fatal("the feed did not end in 50 pages")
	return nil
}

// events counts the feed's events of type typ on task id.
func (r *raceRig) events(id, typ string) int {
	r.t.Helper()
	n := 0
	for _, e := range r.feed() {
		if str(e["taskId"]) == id && str(e["type"]) == typ {
			n++
		}
	}
	return n
}

// gone fails unless every id is NOT_FOUND to get_task, get_task_context and read_memory, and no
// event of the feed names it (the per-table row counts on the engines are internal/cli's).
func (r *raceRig) gone(ids ...string) {
	r.t.Helper()
	for _, id := range ids {
		for _, tool := range []string{"get_task", "get_task_context", "read_memory", "list_subtasks"} {
			args := map[string]any{"projectId": r.p, "taskId": id}
			if tool == "list_subtasks" {
				args = map[string]any{"projectId": r.p, "parentTaskId": id}
			}
			r.wantCode(r.ada, tool, args, "NOT_FOUND")
		}
	}
	for _, e := range r.feed() {
		if slices.Contains(ids, str(e["taskId"])) {
			r.t.Fatalf("the feed holds %s of deleted %s", e["type"], e["taskId"])
		}
	}
}

// AC1: pick_run over three runs keeps the winner, deletes the two losers through the cascade (one
// with a context item, task memory, a summary, an attachment and a dependency edge, the other under
// grace's live lease), and lands the parent REVIEW with racing false in one version, carrying two
// run.discarded events and the winner's deliverable.
func TestPickRunDeletesTheLosers(t *testing.T) {
	raceStacks(t, func(t *testing.T, r *raceRig) {
		parent := r.task("parent", nil)
		runs := r.start(parent, 3)
		win, lose, held := runs[0], runs[1], runs[2]
		r.submit(win, "the winning draft")
		other := r.task("downstream", nil)
		r.ada.call("add_context_item", map[string]any{"projectId": r.p, "taskId": lose, "kind": "note", "body": "a note"})
		r.ada.call("write_memory", map[string]any{"projectId": r.p, "taskId": lose, "scope": "task", "key": "k", "value": "v"})
		r.ada.call("set_summary", map[string]any{"projectId": r.p, "taskId": lose, "version": r.get(lose)["version"], "summary": "so far"})
		r.ada.call("attach_file", map[string]any{"projectId": r.p, "taskId": lose, "fileName": "a.txt", "mimeType": "text/plain", "kind": "file", "bytes": 5})
		r.ada.call("add_dependency", map[string]any{"projectId": r.p, "fromTaskId": lose, "toTaskId": other})
		r.grace.call("claim_task", map[string]any{"projectId": r.p, "taskId": held})
		v := r.get(parent)["version"].(float64)

		picked := obj(r.ada.call("pick_run", map[string]any{"projectId": r.p, "taskId": parent, "runTaskId": win, "version": v})["task"])
		if picked["status"] != "REVIEW" || picked["racing"] == true || picked["raceOpenedAt"] != nil || str(picked["deliverable"]) != "the winning draft" || picked["version"] != v+1 {
			t.Fatalf("pick_run: %v, want REVIEW, not racing, the deliverable, version %v", picked, v+1)
		}
		r.gone(lose, held)
		if w := r.get(win); str(w["runOf"]) != parent {
			t.Fatalf("the winner %v lost its runOf", w)
		}
		if n, m := r.events(parent, "run.discarded"), r.events(parent, "run.picked"); n != 2 || m != 1 {
			t.Fatalf("parent events: %d run.discarded, %d run.picked; want 2 and 1", n, m)
		}
		left := r.ada.call("list_runs", map[string]any{"projectId": r.p, "taskId": parent})["runs"].([]any)
		if len(left) != 1 || str(obj(left[0])["taskId"]) != win {
			t.Fatalf("list_runs after the pick: %v, want only %s", left, win)
		}
		if bb := r.get(other)["blockedBy"].([]any); len(bb) != 0 {
			t.Fatalf("the dependent's blockedBy %v still names the deleted loser", bb)
		}
		t.Logf("%s: picked %s; losers %s %s gone; parent %s REVIEW v%v, %d run.discarded", r.s.Engine, win, lose, held, parent, picked["version"], r.events(parent, "run.discarded"))
	})
}

// AC2: cancel_runs deletes every run, a submitted one included, and the parent is TODO with racing
// false in one version; list_runs is empty. An IN_PROGRESS parent entering a race drops to TODO
// and loses its lease.
func TestCancelRunsDeletesEveryRun(t *testing.T) {
	raceStacks(t, func(t *testing.T, r *raceRig) {
		parent := r.task("parent", nil)
		r.ada.call("claim_task", map[string]any{"projectId": r.p, "taskId": parent})
		runs := r.start(parent, 2)
		if p := r.get(parent); p["status"] != "TODO" || p["claim"] != nil || p["racing"] != true {
			t.Fatalf("an IN_PROGRESS parent raced: %v, want TODO, no lease, racing", p)
		}
		r.submit(runs[0], "done anyway")
		v := r.get(parent)["version"].(float64)
		back := obj(r.ada.call("cancel_runs", map[string]any{"projectId": r.p, "taskId": parent, "version": v})["task"])
		if back["status"] != "TODO" || back["racing"] == true || back["version"] != v+1 {
			t.Fatalf("cancel_runs: %v, want TODO, not racing, version %v", back, v+1)
		}
		if left := r.ada.call("list_runs", map[string]any{"projectId": r.p, "taskId": parent})["runs"].([]any); len(left) != 0 {
			t.Fatalf("list_runs after cancel: %v, want none", left)
		}
		r.gone(runs...)
		if n, m := r.events(parent, "run.discarded"), r.events(parent, "runs.cancelled"); n != 2 || m != 1 {
			t.Fatalf("parent events: %d run.discarded, %d runs.cancelled; want 2 and 1", n, m)
		}
		t.Logf("%s: cancelled %v; parent %s TODO v%v", r.s.Engine, runs, parent, back["version"])
	})
}

// start_runs and open_run: the runs copy the brief, are TODO, ordered and numbered, routed to their
// agents; a retry with the key replays the race; the cap counts the runs that exist.
func TestStartAndOpenRuns(t *testing.T) {
	raceStacks(t, func(t *testing.T, r *raceRig) {
		r.ada.call("update_settings", map[string]any{"projectId": r.p, "maxRunsPerTask": 3})
		parent := r.task("parent", map[string]any{"description": "the brief", "labels": []any{"ui"}})
		args := map[string]any{"projectId": r.p, "taskId": parent, "version": r.get(parent)["version"], "agents": []any{"u-grace", "bot-1"}, "idempotencyKey": "race-1"}
		first := r.ada.call("start_runs", args)
		again := r.ada.call("start_runs", args)
		if !jsonEqual(first, again) {
			t.Fatalf("the replay answered %v, want %v", again, first)
		}
		runs := first["runs"].([]any)
		if len(runs) != 2 {
			t.Fatalf("start_runs: %d runs, want 2", len(runs))
		}
		for i, x := range runs {
			run := obj(x)
			if run["status"] != "TODO" || str(run["runOf"]) != parent || run["description"] != "the brief" || run["assignee"] != []string{"u-grace", "bot-1"}[i] ||
				run["title"] != "parent · run "+string(rune('1'+i)) || len(run["labels"].([]any)) != 1 {
				t.Fatalf("run %d: %v", i, run)
			}
		}
		if a, b := str(obj(runs[0])["order"]), str(obj(runs[1])["order"]); !(a < b) {
			t.Fatalf("run orders %q, %q are not increasing", a, b)
		}
		third := obj(r.ada.call("open_run", map[string]any{"projectId": r.p, "taskId": parent, "agent": "bot-2", "brief": "late"})["run"])
		if third["title"] != "parent · run 3" || third["description"] != "late" || third["assignee"] != "bot-2" || !(str(third["order"]) > str(obj(runs[1])["order"])) {
			t.Fatalf("open_run: %v", third)
		}
		e := r.wantCode(r.ada, "open_run", map[string]any{"projectId": r.p, "taskId": parent}, "VALIDATION")
		if d := obj(e["details"]); d["live"] != float64(3) || d["max"] != float64(3) {
			t.Fatalf("the cap's details %v", d)
		}
		views := r.ada.call("list_runs", map[string]any{"projectId": r.p, "taskId": parent})["runs"].([]any)
		if len(views) != 3 || obj(views[0])["assignee"] != "u-grace" || obj(views[0])["hasDeliverable"] != false {
			t.Fatalf("list_runs: %v", views)
		}
		if out := r.ada.call("list_runs", map[string]any{"projectId": r.p, "taskId": "T-404"})["runs"].([]any); len(out) != 0 {
			t.Fatalf("list_runs of an unknown task: %v, want none", out)
		}
	})
}

// Every declared error of the five race tools, each with a passing control beside it where the
// refusal is a guard: FORBIDDEN (a non-member, and a worker below the master tier), NOT_FOUND,
// STALE, INVALID_TRANSITION, VALIDATION.
func TestRunToolErrors(t *testing.T) {
	raceStacks(t, func(t *testing.T, r *raceRig) {
		parent := r.task("parent", nil)
		v := r.get(parent)["version"]
		at := func(project string, extra map[string]any) map[string]any {
			out := map[string]any{"projectId": project, "taskId": parent, "version": v}
			for k, x := range extra {
				out[k] = x
			}
			return out
		}
		for _, tool := range []string{"start_runs", "open_run", "pick_run", "cancel_runs", "list_runs"} {
			args := at(r.p, map[string]any{"runTaskId": parent})
			if tool == "open_run" || tool == "list_runs" {
				args = map[string]any{"projectId": r.p, "taskId": parent}
			}
			if tool != "pick_run" {
				delete(args, "runTaskId")
			}
			if tool != "list_runs" {
				r.wantCode(r.grace, tool, args, "FORBIDDEN") // a worker, below the master tier
			}
			cross := map[string]any{}
			for k, x := range args {
				cross[k] = x
			}
			cross["projectId"] = r.q
			r.wantCode(r.ada, tool, cross, "FORBIDDEN") // a project ada is no member of
		}
		r.grace.call("list_runs", map[string]any{"projectId": r.p, "taskId": parent}) // the read's control

		r.wantCode(r.ada, "start_runs", at(r.p, map[string]any{"taskId": "T-404"}), "NOT_FOUND")
		r.wantCode(r.ada, "start_runs", at(r.p, map[string]any{"version": 99}), "STALE")
		r.wantCode(r.ada, "start_runs", at(r.p, map[string]any{"count": 6}), "VALIDATION")
		r.wantCode(r.ada, "start_runs", at(r.p, map[string]any{"agents": []any{}}), "VALIDATION")
		r.wantCode(r.ada, "start_runs", at(r.p, map[string]any{"count": 51}), "VALIDATION")
		r.wantCode(r.ada, "open_run", map[string]any{"projectId": r.p, "taskId": parent}, "INVALID_TRANSITION")
		r.wantCode(r.ada, "open_run", map[string]any{"projectId": r.p, "taskId": "T-404"}, "NOT_FOUND")
		r.wantCode(r.ada, "pick_run", at(r.p, map[string]any{"runTaskId": parent}), "INVALID_TRANSITION")
		r.wantCode(r.ada, "cancel_runs", at(r.p, nil), "INVALID_TRANSITION")
		r.wantCode(r.ada, "cancel_runs", at(r.p, map[string]any{"taskId": "T-404"}), "NOT_FOUND")

		poisoned := r.task("poisoned", nil)
		lease := str(r.ada.call("claim_task", map[string]any{"projectId": r.p, "taskId": poisoned})["leaseId"])
		r.ada.call("fail_task", map[string]any{"projectId": r.p, "taskId": poisoned, "leaseId": lease, "reason": "broken", "retryable": false})
		r.wantCode(r.ada, "start_runs", map[string]any{"projectId": r.p, "taskId": poisoned, "version": r.get(poisoned)["version"]}, "INVALID_TRANSITION")

		runs := r.start(parent, 2)
		v = r.get(parent)["version"]
		r.wantCode(r.ada, "start_runs", at(r.p, nil), "INVALID_TRANSITION")                                // already racing
		r.wantCode(r.ada, "pick_run", at(r.p, map[string]any{"runTaskId": runs[0]}), "INVALID_TRANSITION") // not submitted
		r.wantCode(r.ada, "pick_run", at(r.p, map[string]any{"runTaskId": poisoned}), "VALIDATION")        // not its run
		r.wantCode(r.ada, "pick_run", at(r.p, map[string]any{"runTaskId": "T-404"}), "NOT_FOUND")
		r.wantCode(r.ada, "pick_run", at(r.p, map[string]any{"runTaskId": runs[0], "version": 99}), "STALE")
		r.wantCode(r.ada, "cancel_runs", at(r.p, map[string]any{"version": 99}), "STALE")
		if n := len(r.ada.call("list_runs", map[string]any{"projectId": r.p, "taskId": parent})["runs"].([]any)); n != 2 {
			t.Fatalf("the refusals changed the race: %d runs, want 2", n)
		}
		r.submit(runs[0], "draft")
		r.ada.call("pick_run", at(r.p, map[string]any{"runTaskId": runs[0], "version": r.get(parent)["version"]})) // the controls
	})
}

// Review B2 (rev5758): a race resolves when the parent shares a dependency edge with a losing run
// or a losing run's subtask, in either direction, for pick_run and cancel_runs: the call succeeds,
// the parent moves one version, and its blockedBy and blocks no longer name what was deleted. The
// control is delete_task of a run with the same edge.
func TestRaceResolvesWithParentEdges(t *testing.T) {
	raceStacks(t, func(t *testing.T, r *raceRig) {
		for _, tool := range []string{"pick_run", "cancel_runs"} {
			for _, shape := range []string{"parent blocked by the loser", "parent blocks the loser", "parent blocked by the loser's subtask", "parent blocks the loser's subtask"} {
				parent := r.task(tool+" "+shape, nil)
				runs := r.start(parent, 2)
				win, lose := runs[0], runs[1]
				other := lose
				if strings.Contains(shape, "subtask") {
					other = str(obj(r.ada.call("add_subtask", map[string]any{"projectId": r.p, "parentTaskId": lose, "title": "sub"})["task"])["id"])
				}
				from, to := other, parent
				if strings.Contains(shape, "parent blocks") {
					from, to = parent, other
				}
				r.ada.call("add_dependency", map[string]any{"projectId": r.p, "fromTaskId": from, "toTaskId": to})
				args := map[string]any{"projectId": r.p, "taskId": parent}
				if tool == "pick_run" {
					r.submit(win, "draft")
					args["runTaskId"] = win
				}
				v := r.get(parent)["version"].(float64)
				args["version"] = v
				out := obj(r.ada.call(tool, args)["task"])
				gone := []string{lose, other}
				for _, k := range []string{"blockedBy", "blocks"} {
					for _, id := range out[k].([]any) {
						if slices.Contains(gone, str(id)) {
							t.Fatalf("%s, %s: the parent's %s still names %s: %v", tool, shape, k, id, out[k])
						}
					}
				}
				if out["version"] != v+1 || out["racing"] == true {
					t.Fatalf("%s, %s: parent v%v racing %v, want v%v and not racing", tool, shape, out["version"], out["racing"], v+1)
				}
				r.gone(gone...)
			}
		}
		parent := r.task("control", nil)
		runs := r.start(parent, 2)
		r.ada.call("add_dependency", map[string]any{"projectId": r.p, "fromTaskId": runs[1], "toTaskId": parent})
		r.ada.call("delete_task", map[string]any{"projectId": r.p, "taskId": runs[1], "version": r.get(runs[1])["version"]})
		t.Logf("%s: pick_run and cancel_runs resolved under 4 edge shapes each; delete_task control ok", r.s.Engine)
	})
}

// Review B3 (rev5758): pick_run promotes the winner's live artifacts onto the parent in the same
// one-version write, leaving a retracted one behind. A parent with no evidence is listed by
// list_tasks {missingArtifacts:true} before the pick and not after. A second race checks the
// de-duplication: the winner's pr, already on the parent (its url in another case), is not copied.
func TestPickRunPromotesTheWinnersArtifacts(t *testing.T) {
	raceStacks(t, func(t *testing.T, r *raceRig) {
		missing := func(id string) bool {
			for _, x := range r.ada.call("list_tasks", map[string]any{"projectId": r.p, "boardId": r.b, "missingArtifacts": true, "limit": 200})["tasks"].([]any) {
				if str(obj(x)["id"]) == id {
					return true
				}
			}
			return false
		}
		live := func(task map[string]any) []string {
			var out []string
			for _, a := range task["artifacts"].([]any) {
				if obj(a)["retracted"] == nil {
					out = append(out, str(obj(a)["kind"]))
				}
			}
			slices.Sort(out)
			return out
		}
		pr := map[string]any{"kind": "pr", "url": "https://example.com/pr/1"}

		parent := r.task("parent", nil)
		win := r.start(parent, 2)[0]
		r.ada.call("add_artifact", map[string]any{"projectId": r.p, "taskId": win, "artifacts": []any{pr,
			map[string]any{"kind": "branch", "ref": "feat/win"}, map[string]any{"kind": "commit", "ref": "abc123"}}})
		r.ada.call("retract_artifact", map[string]any{"projectId": r.p, "taskId": win, "artifact": map[string]any{"kind": "commit", "ref": "abc123"}, "reason": "wrong sha"})
		r.submit(win, "the winning draft")
		if !missing(parent) {
			t.Fatalf("before the pick, missingArtifacts:true does not list %s: the check would pass vacuously", parent)
		}
		v := r.get(parent)["version"].(float64)
		picked := obj(r.ada.call("pick_run", map[string]any{"projectId": r.p, "taskId": parent, "runTaskId": win, "version": v})["task"])
		if got := live(picked); strings.Join(got, " ") != "branch pr" || picked["version"] != v+1 {
			t.Fatalf("the picked parent's live artifacts %v at v%v, want branch and pr (the retracted commit left) at v%v", got, picked["version"], v+1)
		}
		if got := live(r.get(parent)); strings.Join(got, " ") != "branch pr" {
			t.Fatalf("get_task: the parent's artifacts %v, want branch and pr", got)
		}
		if missing(parent) {
			t.Fatalf("list_tasks missingArtifacts:true still lists the picked parent %s", parent)
		}

		dup := r.task("parent with its own pr", nil)
		win2 := r.start(dup, 2)[0]
		r.ada.call("add_artifact", map[string]any{"projectId": r.p, "taskId": dup, "artifacts": []any{map[string]any{"kind": "pr", "url": "https://EXAMPLE.com/pr/1"}}})
		r.ada.call("add_artifact", map[string]any{"projectId": r.p, "taskId": win2, "artifacts": []any{pr}})
		r.submit(win2, "again")
		got := obj(r.ada.call("pick_run", map[string]any{"projectId": r.p, "taskId": dup, "runTaskId": win2, "version": r.get(dup)["version"]})["task"])
		if n := len(got["artifacts"].([]any)); n != 1 {
			t.Fatalf("the winner's pr, already on the parent, was copied: %v", got["artifacts"])
		}
		t.Logf("%s: parent %s carries %v after the pick (v%v), no longer missing artifacts; the duplicate pr not copied", r.s.Engine, parent, live(picked), picked["version"])
	})
}
