package mcp_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/service"
)

// The notification, profile, anomaly and ranking tools on the real stack, every selected engine:
// each tool's happy path and each error it declares (R2.3's AC1-AC3).

// changesSince is the change log after since, by kind, of the rows scoped to scope ("" every scope)
// and naming task ("" any task).
func (r *raceRig) changesSince(since int64, scope, task string) map[string]int {
	r.t.Helper()
	rows, err := r.s.Store.ReadChanges(context.Background(), since, 1000)
	if err != nil {
		r.t.Fatal(err)
	}
	out := map[string]int{}
	for _, c := range rows {
		if (scope == "" || c.Scope == scope) && (task == "" || strings.Contains(string(c.Payload), `"taskId":"`+task+`"`)) {
			out[c.Kind]++
		}
	}
	return out
}

func (r *raceRig) lastChange() int64 {
	r.t.Helper()
	rows, err := r.s.Store.ReadChanges(context.Background(), 0, 100000)
	if err != nil {
		r.t.Fatal(err)
	}
	if len(rows) == 0 {
		return 0
	}
	return rows[len(rows)-1].ID
}

func (r *raceRig) feedOf(c *caller) map[string]any {
	r.t.Helper()
	return c.call("list_notifications", map[string]any{})
}

// AC1 (as the schema carries it): a follow-up directed at grace writes one follow_up row for her,
// list_notifications as grace returns it with the unread count, its change row is scoped to her
// identity with kind notification (the stream's signal), mark_notifications_read clears it counting
// only what it flipped, and ada sees nothing. A mention and a review request notify the same way; a
// self-mention, a handoff and a self-directed follow-up notify nobody.
func TestNotificationFeed(t *testing.T) {
	raceStacks(t, func(t *testing.T, r *raceRig) {
		id := r.task("for grace", nil)
		r.ada.call("update_task", map[string]any{"projectId": r.p, "taskId": id, "version": r.get(id)["version"], "patch": map[string]any{"assignee": "u-grace"}})
		since := r.lastChange()
		fu := str(obj(r.ada.call("add_follow_up", map[string]any{"projectId": r.p, "taskId": id, "body": "please look"})["followUp"])["id"])

		feed := r.feedOf(r.grace)
		rows := feed["notifications"].([]any)
		if len(rows) != 1 || feed["unread"] != float64(1) {
			t.Fatalf("grace's feed %v, want one unread row", feed)
		}
		n := obj(rows[0])
		if n["type"] != "follow_up" || n["actor"] != "u-ada" || n["identityId"] != "u-grace" || n["projectId"] != r.p || n["boardId"] != r.b ||
			n["taskId"] != id || n["read"] != false || obj(n["data"])["followUpId"] != fu || str(n["ts"]) == "" {
			t.Fatalf("row %v does not name the follow-up", n)
		}
		// The follow-up's own doorbell (R2.1: assigned to its target) and one notification row.
		if got := r.changesSince(since, "u-grace", ""); got[service.ChangeKindNotification] != 1 || got["assigned"] != 1 || len(got) != 2 {
			t.Fatalf("grace's scoped change rows %v, want one notification beside add_follow_up's assigned doorbell", got)
		}
		if own := r.feedOf(r.ada); len(own["notifications"].([]any)) != 0 || own["unread"] != float64(0) {
			t.Fatalf("ada's feed %v: the author is never notified", own)
		}

		marked := r.grace.call("mark_notifications_read", map[string]any{"ids": []any{str(n["id"]), "N-unknown"}})
		again := r.grace.call("mark_notifications_read", map[string]any{"ids": []any{str(n["id"])}})
		if marked["updated"] != float64(1) || again["updated"] != float64(0) {
			t.Fatalf("mark read %v then %v, want 1 then 0", marked, again)
		}
		if feed := r.feedOf(r.grace); feed["unread"] != float64(0) || obj(feed["notifications"].([]any)[0])["read"] != true {
			t.Fatalf("after mark read %v", feed)
		}

		// A mention notifies the mentionee; the author's own @ does not.
		c := str(obj(r.ada.call("add_comment", map[string]any{"projectId": r.p, "taskId": id, "body": "@u-grace and @u-ada, see this"})["comment"])["id"])
		// A review request notifies the reviewer.
		lease := str(r.ada.call("claim_task", map[string]any{"projectId": r.p, "taskId": id})["leaseId"])
		r.ada.call("complete_task", map[string]any{"projectId": r.p, "taskId": id, "leaseId": lease, "reviewer": "u-grace"})
		// A handoff writes no row (conductor's notify.ts: a handoff is not a notification type).
		h := r.task("handed", nil)
		lease = str(r.ada.call("claim_task", map[string]any{"projectId": r.p, "taskId": h})["leaseId"])
		r.ada.call("handoff_task", map[string]any{"projectId": r.p, "taskId": h, "leaseId": lease, "toAgent": "u-grace", "memoryNote": "yours"})
		// A follow-up to oneself notifies nobody.
		self := r.task("mine", nil)
		r.ada.call("update_task", map[string]any{"projectId": r.p, "taskId": self, "version": r.get(self)["version"], "patch": map[string]any{"assignee": "u-ada"}})
		r.ada.call("add_follow_up", map[string]any{"projectId": r.p, "taskId": self, "body": "note to self"})

		feed = r.feedOf(r.grace)
		var types []string
		for _, x := range feed["notifications"].([]any) {
			types = append(types, str(obj(x)["type"]))
			if obj(x)["type"] == "mention" && obj(obj(x)["data"])["commentId"] != c {
				t.Fatalf("mention %v does not name comment %s", x, c)
			}
		}
		if !slices.Equal(types, []string{"review_request", "mention", "follow_up"}) || feed["unread"] != float64(2) {
			t.Fatalf("grace's feed is %v (unread %v), want review_request, mention, follow_up newest first, 2 unread", types, feed["unread"])
		}
		if own := r.feedOf(r.ada); len(own["notifications"].([]any)) != 0 {
			t.Fatalf("ada's feed %v: a self-mention and a self-directed follow-up notify nobody", own)
		}
		if all := r.grace.call("mark_notifications_read", map[string]any{"all": true}); all["updated"] != float64(2) {
			t.Fatalf("mark all %v, want 2", all)
		}

		page := r.grace.call("list_notifications", map[string]any{"limit": 2})
		next := r.grace.call("list_notifications", map[string]any{"limit": 2, "cursor": page["nextCursor"]})
		if len(page["notifications"].([]any)) != 2 || len(next["notifications"].([]any)) != 1 || next["nextCursor"] != nil {
			t.Fatalf("pages %v / %v, want 2 then 1", page, next)
		}

		r.wantCode(r.grace, "list_notifications", map[string]any{"limit": 201}, "VALIDATION")
		r.wantCode(r.grace, "list_notifications", map[string]any{"cursor": "%%%"}, "VALIDATION")
		ids := make([]any, 501)
		for i := range ids {
			ids[i] = "N-x"
		}
		r.wantCode(r.grace, "mark_notifications_read", map[string]any{"ids": ids}, "VALIDATION")
		r.wantCode(r.grace, "mark_notifications_read", map[string]any{"ids": []any{""}}, "VALIDATION")
	})
}

// register_agent_profile and get_skill_stats: the profile keyed by the caller's agent name, an
// overwrite resetting omitted fields, the stats a completion and a block write, and the errors.
func TestAgentProfileTools(t *testing.T) {
	raceStacks(t, func(t *testing.T, r *raceRig) {
		reg := obj(r.ada.call("register_agent_profile", map[string]any{"projectId": r.p, "capabilities": []any{"go", "sql"}, "tags": []any{"backend"},
			"maxConcurrency": 3, "costClass": "premium", "models": []any{"opus"}, "maxEffort": "high"})["profile"])
		if reg["agentKey"] != "agent-ada" || reg["createdBy"] != "u-ada" || reg["maxConcurrency"] != float64(3) || reg["costClass"] != "premium" || reg["maxEffort"] != "high" {
			t.Fatalf("register_agent_profile %v", reg)
		}
		re := obj(r.ada.call("register_agent_profile", map[string]any{"projectId": r.p, "capabilities": []any{"go"}})["profile"])
		if re["maxConcurrency"] != nil || re["costClass"] != nil || re["maxEffort"] != nil || len(re["tags"].([]any)) != 0 || re["createdAt"] != reg["createdAt"] {
			t.Fatalf("re-register %v: omitted fields reset, creation kept", re)
		}

		done := r.task("go work", map[string]any{"requiredCapabilities": []any{"go"}})
		lease := str(r.ada.call("claim_task", map[string]any{"projectId": r.p, "taskId": done})["leaseId"])
		r.ada.call("complete_task", map[string]any{"projectId": r.p, "taskId": done, "leaseId": lease})
		stuck := r.task("go stuck", map[string]any{"requiredCapabilities": []any{"go"}})
		lease = str(r.ada.call("claim_task", map[string]any{"projectId": r.p, "taskId": stuck})["leaseId"])
		r.ada.call("block_task", map[string]any{"projectId": r.p, "taskId": stuck, "leaseId": lease, "reason": "waiting"})
		plain := r.task("no caps", nil)
		lease = str(r.ada.call("claim_task", map[string]any{"projectId": r.p, "taskId": plain})["leaseId"])
		r.ada.call("complete_task", map[string]any{"projectId": r.p, "taskId": plain, "leaseId": lease})

		got := r.ada.call("get_skill_stats", map[string]any{"projectId": r.p})
		stats := got["stats"].([]any)
		if obj(got["profile"])["agentKey"] != "agent-ada" || len(stats) != 1 {
			t.Fatalf("get_skill_stats %v, want the profile and one stat (go)", got)
		}
		s := obj(stats[0])
		if s["capability"] != "go" || s["completed"] != float64(1) || s["blocked"] != float64(1) || s["samples"] != float64(2) ||
			s["reliability"] != float64(4+1)/float64(5+2) || s["meanMs"] == nil {
			t.Fatalf("stat %v, want go 1 completed 1 blocked, reliability 5/7", s)
		}
		other := r.grace.call("get_skill_stats", map[string]any{"projectId": r.p, "agentKey": "nobody"})
		if other["profile"] != nil || len(other["stats"].([]any)) != 0 {
			t.Fatalf("an unknown agent %v, want a null profile and no stats", other)
		}

		r.wantCode(r.ada, "register_agent_profile", map[string]any{"projectId": r.q, "capabilities": []any{}}, "FORBIDDEN")
		r.wantCode(r.ada, "get_skill_stats", map[string]any{"projectId": r.q}, "FORBIDDEN")
		r.wantCode(r.ada, "register_agent_profile", map[string]any{"projectId": r.p, "capabilities": []any{}, "maxConcurrency": 1001}, "VALIDATION")
		r.wantCode(r.ada, "register_agent_profile", map[string]any{"projectId": r.p, "capabilities": []any{}, "models": []any{""}}, "VALIDATION")
		r.wantCode(r.ada, "get_skill_stats", map[string]any{"projectId": "P-missing"}, "FORBIDDEN")
	})
}

// AC2: on a board with a poisoned task P, a dependent D blocked by it and a DONE task X,
// detect_anomalies returns exactly one finding, dep_deadlock warn on D naming P's blockedReason, and
// nothing for X; the first sighting is recorded once; taskId scopes; an empty anomalyPolicy reads as
// the defaults (R0.22); and the errors.
func TestDetectAnomaliesPoisonIsWarn(t *testing.T) {
	raceStacks(t, func(t *testing.T, r *raceRig) {
		p, d, x := r.task("poisoned", nil), r.task("dependent", nil), r.task("finished", nil)
		r.ada.call("add_dependency", map[string]any{"projectId": r.p, "fromTaskId": p, "toTaskId": d})
		r.poison(p)
		r.submit(x, "shipped")
		r.ada.call("move_task", map[string]any{"projectId": r.p, "taskId": x, "version": r.get(x)["version"], "toStatus": "DONE"})

		got := r.ada.call("detect_anomalies", map[string]any{"projectId": r.p, "boardId": r.b})
		found := got["anomalies"].([]any)
		if len(found) != 1 {
			t.Fatalf("detect_anomalies %v, want one finding", got)
		}
		a := obj(found[0])
		dead := obj(obj(a["evidence"])["deadBlockers"].([]any)[0])
		if a["kind"] != "dep_deadlock" || a["severity"] != "warn" || a["taskId"] != d || dead["taskId"] != p || dead["blockedReason"] != "poison" || dead["status"] != "BLOCKED" {
			t.Fatalf("finding %v, want dep_deadlock warn on %s naming %s's blockedReason poison", a, d, p)
		}
		if strings.Contains(asText(t, got), `"`+x+`"`) {
			t.Fatalf("the DONE task %s appears in %v", x, got)
		}
		scan := obj(got["scanned"])
		if scan["tasksScanned"] != float64(3) || got["boardId"] != r.b || scan["truncated"] != false {
			t.Fatalf("scanned %v", got)
		}
		r.ada.call("detect_anomalies", map[string]any{"projectId": r.p, "boardId": r.b})
		if n := r.events(d, "task.anomaly_detected"); n != 1 {
			t.Fatalf("%d task.anomaly_detected on %s after two scans, want 1 (the first sighting)", n, d)
		}

		scoped := r.grace.call("detect_anomalies", map[string]any{"projectId": r.p, "taskId": d})
		none := r.ada.call("detect_anomalies", map[string]any{"projectId": r.p, "taskId": x})
		kinds := r.ada.call("detect_anomalies", map[string]any{"projectId": r.p, "boardId": r.b, "kinds": []any{"wip_jam"}})
		if len(scoped["anomalies"].([]any)) != 1 || scoped["boardId"] != r.b || len(none["anomalies"].([]any)) != 0 || len(kinds["anomalies"].([]any)) != 0 {
			t.Fatalf("scoped %v / task %s %v / kinds %v", scoped, x, none, kinds)
		}

		// An urgent card created now is not starving under the defaults, and anomalyPolicy {} is
		// stored as all-zero thresholds, which must read as the defaults, never as "fire on all".
		r.task("urgent", map[string]any{"priority": "urgent"})
		r.ada.call("update_settings", map[string]any{"projectId": r.p, "anomalyPolicy": map[string]any{}})
		if s := r.ada.call("detect_anomalies", map[string]any{"projectId": r.p, "boardId": r.b, "kinds": []any{"starved_urgent", "claim_thrash", "rework_loop"}}); len(s["anomalies"].([]any)) != 0 {
			t.Fatalf("an empty anomalyPolicy fired %v", s)
		}

		r.wantCode(r.ada, "detect_anomalies", map[string]any{"projectId": r.p}, "VALIDATION")
		r.wantCode(r.ada, "detect_anomalies", map[string]any{"projectId": r.p, "boardId": r.b, "kinds": []any{}}, "VALIDATION")
		r.wantCode(r.ada, "detect_anomalies", map[string]any{"projectId": r.p, "boardId": r.b, "sinceSec": 30*24*3600 + 1}, "VALIDATION")
		r.wantCode(r.ada, "detect_anomalies", map[string]any{"projectId": r.p, "taskId": "T-999"}, "NOT_FOUND")
		r.wantCode(r.ada, "detect_anomalies", map[string]any{"projectId": r.q, "boardId": r.b}, "FORBIDDEN")
	})
}

// rank_tasks: the claimable TODO pool highest score first, with factors and a reason; the
// capabilities filter; the profile path's tiers; and the errors.
func TestRankTasks(t *testing.T) {
	raceStacks(t, func(t *testing.T, r *raceRig) {
		low := r.task("low", map[string]any{"priority": "low"})
		urgent := r.task("urgent", map[string]any{"priority": "urgent", "requiredCapabilities": []any{"go"}})
		normal := r.task("normal", nil)
		tagged := r.task("tagged", map[string]any{"requiredCapabilities": []any{"backend-api"}})
		rust := r.task("rust", map[string]any{"priority": "high", "requiredCapabilities": []any{"rust"}})
		held := r.task("held", map[string]any{"priority": "urgent"})
		r.ada.call("claim_task", map[string]any{"projectId": r.p, "taskId": held})

		ids := func(out map[string]any) []string {
			var s []string
			for _, x := range out["ranked"].([]any) {
				s = append(s, str(obj(obj(x)["task"])["id"]))
			}
			return s
		}
		all := r.grace.call("rank_tasks", map[string]any{"projectId": r.p, "boardId": r.b})
		if got := ids(all); !slices.Equal(got, []string{urgent, rust, tagged, normal, low}) {
			t.Fatalf("rank_tasks %v, want urgent, rust (high), tagged (its one capability outweighs nothing), normal, low; held is claimed", got)
		}
		top := obj(all["ranked"].([]any)[0])
		if f := obj(obj(top["factors"])["priority"]); f["value"] != float64(1) || f["weight"] != 0.4 || top["reason"] != "urgent · needs go" || top["agedOut"] != false {
			t.Fatalf("top %v", top)
		}
		if _, ok := top["tier"]; ok {
			t.Fatalf("the plain path carries a tier: %v", top)
		}
		if got := ids(r.ada.call("rank_tasks", map[string]any{"projectId": r.p, "boardId": r.b, "capabilities": []any{"go"}, "limit": 2})); !slices.Equal(got, []string{urgent, normal}) {
			t.Fatalf("capabilities [go] limit 2: %v", got)
		}
		// No profile yet: useProfile falls back to the plain path.
		if got := ids(r.ada.call("rank_tasks", map[string]any{"projectId": r.p, "boardId": r.b, "useProfile": true})); len(got) != 5 {
			t.Fatalf("useProfile with no profile: %v, want the plain pool", got)
		}
		r.ada.call("register_agent_profile", map[string]any{"projectId": r.p, "capabilities": []any{"go"}, "tags": []any{"backend"}})
		prof := r.ada.call("rank_tasks", map[string]any{"projectId": r.p, "boardId": r.b, "useProfile": true})
		if got := ids(prof); slices.Contains(got, rust) || !slices.Contains(got, tagged) || got[0] != urgent {
			t.Fatalf("useProfile: %v, want rust excluded, the tag match kept, urgent first", got)
		}
		for _, x := range prof["ranked"].([]any) {
			want := map[string]string{urgent: "exact", tagged: "tag", normal: "exact", low: "exact"}[str(obj(obj(x)["task"])["id"])]
			if obj(x)["tier"] != want || obj(x)["reliability"] != 0.8 {
				t.Fatalf("profile row %v, want tier %s and the prior reliability 0.8", x, want)
			}
		}

		r.wantCode(r.ada, "rank_tasks", map[string]any{"projectId": r.q, "boardId": r.b}, "FORBIDDEN")
		r.wantCode(r.ada, "rank_tasks", map[string]any{"projectId": r.p, "boardId": r.b, "limit": 201}, "VALIDATION")
		r.wantCode(r.ada, "rank_tasks", map[string]any{"projectId": r.p, "boardId": "B-missing"}, "NOT_FOUND")
	})
}

// AC3 (as the schema carries it): orchestrate_subtasks over three children lands the parent BLOCKED,
// lease dropped, assigned to the leader; creates three TODO children blocking it; rings one broadcast
// per child and no assigned doorbell (the children name nobody, and the leader's own assignment rings
// nobody); and when the last child is DONE the parent returns to TODO and rings the leader, once.
func TestOrchestrateSubtasks(t *testing.T) {
	raceStacks(t, func(t *testing.T, r *raceRig) {
		parent := r.task("epic", nil)
		lease := str(r.ada.call("claim_task", map[string]any{"projectId": r.p, "taskId": parent})["leaseId"])
		since := r.lastChange()
		args := map[string]any{"projectId": r.p, "parentTaskId": parent, "leaseId": lease, "idempotencyKey": "fan-1", "children": []any{
			map[string]any{"title": "a"}, map[string]any{"title": "b", "priority": "high", "requiredCapabilities": []any{"go"}}, map[string]any{"title": "c", "description": "the third"},
		}}
		out := r.ada.call("orchestrate_subtasks", args)
		p := obj(out["parent"])
		kids := out["children"].([]any)
		if p["status"] != "BLOCKED" || p["claim"] != nil || p["assignee"] != "u-ada" || len(p["blockedBy"].([]any)) != 3 || len(kids) != 3 {
			t.Fatalf("orchestrate_subtasks parent %v, %d children", p, len(kids))
		}
		var kidIDs []string
		for i, k := range kids {
			k := obj(k)
			kidIDs = append(kidIDs, str(k["id"]))
			if k["status"] != "TODO" || k["parentTaskId"] != parent || k["assignee"] != nil || !slices.Equal(asStrings(k["blocks"]), []string{parent}) || k["title"] != []string{"a", "b", "c"}[i] {
				t.Fatalf("child %v", k)
			}
		}
		if got := r.changesSince(since, "", ""); got["broadcast"] != 3 || got["assigned"] != 0 {
			t.Fatalf("change rows by kind %v, want 3 broadcast (one per child) and 0 assigned", got)
		}
		if n := r.events(parent, "task.orchestrated"); n != 1 {
			t.Fatalf("%d task.orchestrated, want 1", n)
		}
		if n := r.events(parent, "dependency.added"); n != 3 {
			t.Fatalf("%d dependency.added on the parent, want 3", n)
		}
		if replay := r.ada.call("orchestrate_subtasks", args); !slices.Equal(asStrings(obj(replay["parent"])["blockedBy"]), asStrings(p["blockedBy"])) {
			t.Fatalf("the replay %v differs", replay)
		}

		for _, k := range kidIDs {
			r.submit(k, "done")
			r.ada.call("move_task", map[string]any{"projectId": r.p, "taskId": k, "version": r.get(k)["version"], "toStatus": "DONE"})
		}
		after := r.get(parent)
		if after["status"] != "TODO" || len(after["blockedBy"].([]any)) != 0 {
			t.Fatalf("after the last child the parent is %v, want TODO, unblocked", after)
		}
		if got := r.changesSince(since, "u-ada", parent); got["assigned"] != 1 {
			t.Fatalf("assigned rows to u-ada naming the parent %v, want exactly 1 (the join)", got)
		}

		other := r.task("other", nil)
		ol := str(r.ada.call("claim_task", map[string]any{"projectId": r.p, "taskId": other})["leaseId"])
		one := []any{map[string]any{"title": "x"}}
		r.wantCode(r.grace, "orchestrate_subtasks", map[string]any{"projectId": r.p, "parentTaskId": other, "leaseId": ol, "children": one}, "FORBIDDEN")
		r.wantCode(r.ada, "orchestrate_subtasks", map[string]any{"projectId": r.q, "parentTaskId": other, "leaseId": ol, "children": one}, "FORBIDDEN")
		r.wantCode(r.ada, "orchestrate_subtasks", map[string]any{"projectId": r.p, "parentTaskId": other, "leaseId": "L-wrong", "children": one}, "LEASE_EXPIRED")
		r.wantCode(r.ada, "orchestrate_subtasks", map[string]any{"projectId": r.p, "parentTaskId": other, "leaseId": ol, "children": []any{}}, "VALIDATION")
		r.wantCode(r.ada, "orchestrate_subtasks", map[string]any{"projectId": r.p, "parentTaskId": "T-999", "leaseId": ol, "children": one}, "NOT_FOUND")
		if got := r.get(other); got["status"] != "IN_PROGRESS" || len(got["blockedBy"].([]any)) != 0 {
			t.Fatalf("a refused fan-out wrote %v", got)
		}
	})
}

func asText(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func asStrings(v any) []string {
	var out []string
	for _, x := range v.([]any) {
		out = append(out, str(x))
	}
	return out
}
