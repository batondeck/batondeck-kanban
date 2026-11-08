//go:build e2e

package e2e

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func init() {
	register(scenario{area: "agents", name: "sessions-and-disconnect", tools: []string{"list_agent_sessions", "disconnect_agent"}, run: agentsSessions})
	register(scenario{area: "me", name: "settings", tools: []string{"get_my_settings", "set_my_settings"}, run: meSettings})
	register(scenario{area: "audit", name: "log", tools: []string{"list_audit_log"}, run: auditLog})
}

// The worker lists its own agent sessions (each label is one agent), with request and per-tool
// counts; all:true is the workspace admin's. The worker disconnects one of its own agents: that
// agent's live session is answered 404 session not found and leaves the listing, a new initialize
// works and brings it back, and its other agents keep their sessions. worker2 may not disconnect the worker's agent.
func agentsSessions(t *testing.T, e *env) {
	p := e.project(t, "Agents")
	disc := e.srv.connect(t, e.srv.tokens["worker/e2e-disc"], "e2e-worker-disc")
	disc.Call(t, "get_project", M{"projectId": p.id})

	sessions := A(e.worker.Call(t, "list_agent_sessions", M{}), "sessions")
	byName := map[string]M{}
	for _, s := range sessions {
		want(t, S(s, "identityId") == e.srv.ids["worker"], "list_agent_sessions as the worker lists another identity's session: %s", js(s))
		byName[S(s, "agentName")] = s.(M)
	}
	d, ok := byName["e2e-disc"]
	want(t, ok && byName["e2e-wa"] != nil && byName["e2e-wb"] != nil, "list_agent_sessions agents: %v", keys(byName))
	want(t, N(d, "requestCount") >= 1 && N(d, "toolCounts.get_project") >= 1 && S(d, "lastTool") == "get_project" && S(d, "clientName") == "e2e-worker-disc",
		"the disc agent's session row: %s", js(d))
	e.worker.Refuse(t, "list_agent_sessions", M{"all": true}, "FORBIDDEN")
	everyone := map[string]bool{}
	for _, s := range A(e.admin.Call(t, "list_agent_sessions", M{"all": true, "limit": 200}), "sessions") {
		everyone[S(s, "identityId")] = true
	}
	want(t, everyone[e.srv.ids["worker"]] && everyone[e.srv.ids["viewer"]], "list_agent_sessions all:true as the admin: %v", everyone)

	e.worker2.Refuse(t, "disconnect_agent", M{"identityId": e.srv.ids["worker"], "agentId": S(d, "agentId")}, "FORBIDDEN")
	got := e.worker.Call(t, "disconnect_agent", M{"identityId": e.srv.ids["worker"], "agentId": S(d, "agentId")})
	want(t, B(got, "disconnected"), "disconnect_agent answered %s", js(got))
	_, terr, err := disc.Try("get_project", M{"projectId": p.id})
	var he *HTTPError
	want(t, terr == nil && errors.As(err, &he) && he.Status == 404, "the disconnected agent's next request answered %v / %v, want HTTP 404", terr, err)
	agents := func() []string { return ids(A(e.worker.Call(t, "list_agent_sessions", M{}), "sessions"), "agentName") }
	want(t, !has(agents(), "e2e-disc") && has(agents(), "e2e-wb"), "list_agent_sessions after the disconnect: %v", agents())
	// Its other agents are untouched, and the disconnected one may initialise again.
	e.workerB.Call(t, "get_project", M{"projectId": p.id})
	again := e.srv.connect(t, e.srv.tokens["worker/e2e-disc"], "e2e-worker-disc")
	again.Call(t, "get_project", M{"projectId": p.id})
	want(t, has(agents(), "e2e-disc"), "list_agent_sessions after the agent initialised again: %v", agents())
}

func keys(m map[string]M) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// The worker sets its own preferences and reads them back; another identity's are untouched; null
// clears one and a field left out is kept. A ceiling below 100 is refused.
func meSettings(t *testing.T, e *env) {
	before := e.worker2.Call(t, "get_my_settings", M{})
	want(t, O(before, "defaults") != nil && B(before, "estimated"), "get_my_settings answered %s", js(before))

	set := e.worker.Call(t, "set_my_settings", M{"maxTicketTokens": 5000, "tokenPolicy": "warn", "starredAssets": []any{"batondeck/skill/triage", "org/prompt/handoff.v2"}})
	want(t, N(set, "maxTicketTokens") == 5000 && S(set, "tokenPolicy") == "warn", "set_my_settings answered %s", js(set))
	got := e.worker.Call(t, "get_my_settings", M{})
	want(t, N(got, "maxTicketTokens") == 5000 && S(got, "tokenPolicy") == "warn" && js(A(got, "starredAssets")) == `["batondeck/skill/triage","org/prompt/handoff.v2"]`, "get_my_settings after set: %s", js(got))
	// The worker's other agent is the same identity: it reads the same preferences.
	want(t, N(e.workerB.Call(t, "get_my_settings", M{}), "maxTicketTokens") == 5000, "the worker's other agent reads other preferences")
	want(t, js(e.worker2.Call(t, "get_my_settings", M{})) == js(before), "worker2's preferences changed with the worker's")

	cleared := e.worker.Call(t, "set_my_settings", M{"tokenPolicy": nil})
	want(t, cleared["tokenPolicy"] == nil && N(cleared, "maxTicketTokens") == 5000, "set_my_settings tokenPolicy null answered %s", js(cleared))
	e.worker.Refuse(t, "set_my_settings", M{"maxTicketTokens": 50}, "VALIDATION")
	e.worker.Refuse(t, "set_my_settings", M{"starredAssets": []any{"board:main"}}, "VALIDATION")
	want(t, N(e.worker.Call(t, "get_my_settings", M{}), "maxTicketTokens") == 5000, "a refused set changed the ceiling")
	e.worker.Call(t, "set_my_settings", M{"maxTicketTokens": nil, "starredAssets": []any{}})
}

// The audit log records the sensitive calls: the project admin's member additions and a grant, and
// the master's task deletion, each found by action, actor and subject. The subject filter takes an
// identity id as the API hands it out ("U-…"), though the row stores it lower-cased. Only the
// workspace admin reads it, and days past 365 is refused.
func auditLog(t *testing.T, e *env) {
	p := e.project(t, "Audited")
	e.admin.Call(t, "grant_capabilities", M{"projectId": p.id, "identityId": e.srv.ids["worker2"], "scope": M{"deny": []any{"add_comment"}}})
	id := S(e.task(t, p, "short-lived", nil), "id")
	e.master.Call(t, "delete_task", M{"projectId": p.id, "taskId": id, "version": e.version(t, p, id)})

	find := func(args M) []any {
		args["limit"] = 500
		var out []any
		for _, en := range A(e.admin.Call(t, "list_audit_log", args), "entries") {
			if S(en, "projectId") == p.id {
				out = append(out, en)
			}
		}
		return out
	}
	// data is the curated payload as a JSON string (the contract's type). An identity subject reads
	// back lower-cased, the form the server stores it in.
	dataOf := func(en any) M {
		var m M
		_ = json.Unmarshal([]byte(S(en, "data")), &m)
		return m
	}
	viewer, worker2 := e.srv.ids["viewer"], e.srv.ids["worker2"]
	want(t, strings.HasPrefix(viewer, "U-") && strings.HasPrefix(worker2, "U-"), "the ids handed out are not U-…: %q, %q", viewer, worker2)
	added := find(M{"action": "member.added", "actor": e.srv.ids["admin"], "subject": viewer})
	want(t, len(added) == 1 && S(added[0], "subject") == strings.ToLower(viewer) && S(dataOf(added[0]), "role") == "viewer" &&
		S(added[0], "result") == "ok" && S(added[0], "agent") == "e2e-admin", "list_audit_log member.added by subject %s: %s", viewer, js(added))
	granted := find(M{"action": "capabilities.granted", "subject": worker2})
	want(t, len(granted) == 1 && S(dataOf(granted[0]), "scope.deny.0") == "add_comment", "list_audit_log capabilities.granted by subject %s: %s", worker2, js(granted))
	// The stored form finds the same row; a task id stays exact.
	stored := find(M{"action": "capabilities.granted", "subject": strings.ToLower(worker2)})
	want(t, len(stored) == 1, "list_audit_log capabilities.granted by subject %s: %s", strings.ToLower(worker2), js(stored))
	deleted := find(M{"action": "task.deleted", "actor": e.srv.ids["master"], "subject": id})
	want(t, len(deleted) == 1 && S(deleted[0], "subject") == id && S(deleted[0], "agent") == "e2e-master", "list_audit_log task.deleted: %s", js(deleted))
	all := find(M{"days": 1})
	want(t, len(all) >= 6, "list_audit_log days 1 holds %d rows for the project, want the 4 additions, the grant and the deletion", len(all))

	e.master.Refuse(t, "list_audit_log", M{}, "FORBIDDEN")
	e.admin.Refuse(t, "list_audit_log", M{"days": 400}, "VALIDATION")
}
