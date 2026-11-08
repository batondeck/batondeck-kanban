package mcp_test

import (
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	bk "github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// The agent-session, settings and audit tools (R2.5) through the real client on every engine:
// each happy path and each declared error.

func wantCode(t *testing.T, cs *sdk.ClientSession, tool string, args map[string]any, code string) {
	t.Helper()
	if e := bk.ToolError(t, cs, tool, args); e["code"] != code {
		t.Fatalf("%s %v = %v, want %s", tool, args, e, code)
	}
}

// postOnSession is one raw tools/list POST on session id: what any client sends after it.
func postOnSession(t *testing.T, url, bearer, id string) (int, string) {
	t.Helper()
	return rawCall(t, url, bearer, id, `{"jsonrpc":"2.0","id":9,"method":"tools/list"}`)
}

// rawCall is one raw JSON-RPC POST on session id.
func rawCall(t *testing.T, url, bearer, id, msg string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(testCtx(t), http.MethodPost, url, strings.NewReader(msg))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", id)
	req.Header.Set("Mcp-Protocol-Version", "2025-06-18")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func sessionKeys(v map[string]any) []string {
	var out []string
	for _, s := range v["sessions"].([]any) {
		out = append(out, str(obj(s)["sessionKey"]))
	}
	slices.Sort(out)
	return out
}

// TestDisconnectAgentEndsTheSession (AC1): two live sessions listed; disconnect_agent on grace's
// makes its next request 404 session not found, raw and through the client, while ada's goes on;
// the row is gone, the audit row is written, and grace may initialise again (not sticky). The
// refusals: another's agent for a non-admin, all:true for a non-admin, an unknown agent, an empty
// id. And a session disconnecting itself gets its answer before it ends.
func TestDisconnectAgentEndsTheSession(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := bootStack(t, s)
		ada := &caller{t: t, cs: h.Connect(t, stackOwner), called: map[string]int{}}
		grace := h.Connect(t, stackGrace)
		root := &caller{t: t, cs: h.Connect(t, stackRoot), called: map[string]int{}}

		if got := sessionKeys(ada.call("list_agent_sessions", map[string]any{})); !slices.Equal(got, []string{"u-ada::agent-ada"}) {
			t.Fatalf("ada's own sessions: %v", got)
		}
		if got := sessionKeys(root.call("list_agent_sessions", map[string]any{"all": true})); !slices.Equal(got, []string{"u-ada::agent-ada", "u-grace::harness"}) {
			t.Fatalf("every session, as the admin: %v", got)
		}
		wantCode(t, ada.cs, "list_agent_sessions", map[string]any{"all": true}, "FORBIDDEN")
		wantCode(t, ada.cs, "disconnect_agent", map[string]any{"identityId": "u-grace", "agentId": "harness"}, "FORBIDDEN")
		wantCode(t, ada.cs, "disconnect_agent", map[string]any{"identityId": "u-nobody", "agentId": "x"}, "FORBIDDEN")
		wantCode(t, ada.cs, "disconnect_agent", map[string]any{"identityId": "u-ada", "agentId": "never-connected"}, "NOT_FOUND")
		wantCode(t, ada.cs, "disconnect_agent", map[string]any{"identityId": "u-ada", "agentId": ""}, "VALIDATION")
		if _, err := grace.ListTools(testCtx(t), nil); err != nil {
			t.Fatalf("grace's session before the disconnect: %v", err)
		}

		if got := root.call("disconnect_agent", map[string]any{"identityId": "u-grace", "agentId": "harness"}); got["disconnected"] != true {
			t.Fatalf("disconnect_agent answered %v", got)
		}
		if code, body := postOnSession(t, h.URL(), stackGrace, grace.ID()); code != http.StatusNotFound || !strings.Contains(body, "session not found") {
			t.Fatalf("grace's session after the disconnect answered %d %q, want 404 session not found", code, body)
		}
		// go-sdk's client learns of the end on its standalone GET stream first and closes itself.
		if _, err := grace.CallTool(testCtx(t), &sdk.CallToolParams{Name: "list_agent_sessions", Arguments: map[string]any{}}); err == nil {
			t.Fatal("grace's next call through the client answered after the disconnect")
		}
		if got := sessionKeys(ada.call("list_agent_sessions", map[string]any{})); !slices.Equal(got, []string{"u-ada::agent-ada"}) {
			t.Fatalf("ada's session after grace's disconnect: %v", got)
		}
		if got := sessionKeys(root.call("list_agent_sessions", map[string]any{"all": true})); slices.Contains(got, "u-grace::harness") {
			t.Fatalf("grace's row survived the disconnect: %v", got)
		}

		// Not sticky: grace initialises again and is served.
		again := &caller{t: t, cs: h.Connect(t, stackGrace), called: map[string]int{}}
		if got := sessionKeys(again.call("list_agent_sessions", map[string]any{})); !slices.Equal(got, []string{"u-grace::harness"}) {
			t.Fatalf("grace after initialising again: %v", got)
		}
		// Its own session, from inside it, raw: the answer arrives whole, then the session ends.
		// (go-sdk's client is not asked: with retries off it closes itself the moment its standalone
		// GET stream ends, racing the answer it has already been sent.)
		code, body := rawCall(t, h.URL(), stackGrace, again.cs.ID(), `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"disconnect_agent","arguments":{"identityId":"u-grace","agentId":"harness"}}}`)
		if code != http.StatusOK || !strings.Contains(body, `"disconnected":true`) {
			t.Fatalf("a self-disconnect answered %d %q, want 200 with disconnected true", code, body)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			code, body := postOnSession(t, h.URL(), stackGrace, again.cs.ID())
			if code == http.StatusNotFound && strings.Contains(body, "session not found") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("the self-disconnected session still answers after 5 s: %d %q", code, body)
			}
			time.Sleep(20 * time.Millisecond)
		}

		var rows []string
		for _, r := range root.call("list_audit_log", map[string]any{"action": "agent.disconnected"})["entries"].([]any) {
			rows = append(rows, str(obj(r)["actor"])+">"+str(obj(r)["subject"])+" "+str(obj(r)["data"]))
		}
		if want := []string{`u-grace>u-grace {"agentId":"harness"}`, `u-ada>u-grace {"agentId":"harness"}`}; !slices.Equal(rows, want) {
			t.Fatalf("agent.disconnected rows %v, want %v (newest first; the refused calls wrote none)", rows, want)
		}
	})
}

// TestMySettingsTools: the defaults with nothing set, each field replaced alone and the others
// kept, null clearing, one identity's settings not another's, and the VALIDATION refusals.
func TestMySettingsTools(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := bootStack(t, s)
		ada := &caller{t: t, cs: h.Connect(t, stackOwner), called: map[string]int{}}
		grace := &caller{t: t, cs: h.Connect(t, stackGrace), called: map[string]int{}}

		got := ada.call("get_my_settings", map[string]any{})
		t.Log(h.Log())
		d := obj(got["defaults"])
		if got["maxTicketTokens"] != nil || got["tokenPolicy"] != nil || len(got["starredAssets"].([]any)) != 0 || got["estimated"] != true ||
			d["maxTicketTokens"] != float64(50000) || d["tokenPolicy"] != "warn" || d["charsPerToken"] != float64(4) {
			t.Fatalf("get_my_settings with nothing set: %v", got)
		}
		if got := ada.call("set_my_settings", map[string]any{"maxTicketTokens": 5000}); got["maxTicketTokens"] != float64(5000) || got["tokenPolicy"] != nil {
			t.Fatalf("set maxTicketTokens: %v", got)
		}
		if got := ada.call("set_my_settings", map[string]any{"tokenPolicy": "off", "starredAssets": []any{"batondeck/skill/review"}}); got["maxTicketTokens"] != float64(5000) || got["tokenPolicy"] != "off" {
			t.Fatalf("set tokenPolicy kept maxTicketTokens? %v", got)
		}
		if got := ada.call("set_my_settings", map[string]any{"maxTicketTokens": nil}); got["maxTicketTokens"] != nil || got["tokenPolicy"] != "off" || len(got["starredAssets"].([]any)) != 1 {
			t.Fatalf("null cleared maxTicketTokens alone? %v", got)
		}
		if got := ada.call("set_my_settings", map[string]any{"starredAssets": []any{}, "tokenPolicy": nil}); len(got["starredAssets"].([]any)) != 0 || got["tokenPolicy"] != nil {
			t.Fatalf("[] and null cleared? %v", got)
		}
		if got := ada.call("get_my_settings", map[string]any{}); got["maxTicketTokens"] != nil || got["tokenPolicy"] != nil || len(got["starredAssets"].([]any)) != 0 {
			t.Fatalf("read back after clearing: %v", got)
		}
		ada.call("set_my_settings", map[string]any{"tokenPolicy": "enforce"})
		if got := grace.call("get_my_settings", map[string]any{}); got["tokenPolicy"] != nil {
			t.Fatalf("grace reads ada's setting: %v", got)
		}

		many := make([]any, 201)
		for i := range many {
			many[i] = "batondeck/skill/s" + strings.Repeat("x", i%5)
		}
		for name, args := range map[string]map[string]any{
			"ceiling below 100":     {"maxTicketTokens": 99},
			"ceiling above 1000000": {"maxTicketTokens": 1000001},
			"unknown rung":          {"tokenPolicy": "strict"},
			"not a registry key":    {"starredAssets": []any{"skill:review"}},
			"201 stars":             {"starredAssets": many},
			"an unknown field":      {"identityId": "u-grace"},
		} {
			t.Run(name, func(t *testing.T) { wantCode(t, ada.cs, "set_my_settings", args, "VALIDATION") })
		}
		if got := ada.call("get_my_settings", map[string]any{}); got["tokenPolicy"] != "enforce" {
			t.Fatalf("a refused set changed the row: %v", got)
		}
	})
}

// TestListAuditLogTool: listed to the workspace admin alone, and a non-admin calling it by name is
// FORBIDDEN by service.authorize (refusal); the admin reads delete_task's row with its fields, the
// filters narrow it, and days and limit out of range are VALIDATION (control).
func TestListAuditLogTool(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := bootStack(t, s)
		ada := &caller{t: t, cs: h.Connect(t, stackOwner), called: map[string]int{}}
		root := &caller{t: t, cs: h.Connect(t, stackRoot), called: map[string]int{}}

		listed := func(cs *sdk.ClientSession) bool {
			res, err := cs.ListTools(testCtx(t), nil)
			if err != nil {
				t.Fatal(err)
			}
			return slices.ContainsFunc(res.Tools, func(x *sdk.Tool) bool { return x.Name == "list_audit_log" })
		}
		if listed(ada.cs) || !listed(root.cs) {
			t.Fatalf("list_audit_log listed to ada %v, to the admin %v; want false, true", listed(ada.cs), listed(root.cs))
		}
		wantCode(t, ada.cs, "list_audit_log", map[string]any{}, "FORBIDDEN")

		p := str(ada.call("create_project", map[string]any{"name": "Audited"})["projectId"])
		b := str(obj(ada.call("create_board", map[string]any{"projectId": p, "name": "Main"})["board"])["id"])
		task := obj(ada.call("create_task", map[string]any{"projectId": p, "boardId": b, "title": "doomed"})["task"])
		ada.call("delete_task", map[string]any{"projectId": p, "taskId": task["id"], "version": task["version"]})

		got := root.call("list_audit_log", map[string]any{"action": "task.deleted"})
		rows := got["entries"].([]any)
		if got["days"] != float64(90) || len(rows) != 1 {
			t.Fatalf("list_audit_log task.deleted: %v", got)
		}
		r := obj(rows[0])
		if r["actor"] != "u-ada" || r["agent"] != "agent-ada" || r["subject"] != task["id"] || r["projectId"] != p || r["result"] != "ok" ||
			r["errorCode"] != "" || r["data"] != "{}" || !strings.HasSuffix(str(r["ts"]), "Z") {
			t.Fatalf("the task.deleted row: %v", r)
		}
		if n := len(root.call("list_audit_log", map[string]any{"actor": "u-grace"})["entries"].([]any)); n != 0 {
			t.Fatalf("actor u-grace: %d rows, want 0", n)
		}
		if n := len(root.call("list_audit_log", map[string]any{"subject": str(task["id"]), "days": 1, "limit": 1})["entries"].([]any)); n != 1 {
			t.Fatalf("subject, days 1, limit 1: %d rows, want 1", n)
		}
		wantCode(t, root.cs, "list_audit_log", map[string]any{"days": 366}, "VALIDATION")
		wantCode(t, root.cs, "list_audit_log", map[string]any{"limit": 501}, "VALIDATION")
		wantCode(t, root.cs, "list_audit_log", map[string]any{"projectId": p}, "VALIDATION")

		// P-435: an identity subject is stored lower-cased, and the filter finds it by the id as the
		// API hands it out ("U-…") and as stored; a task id stays exact, so its lower-cased form finds
		// nothing.
		ada.call("add_member", map[string]any{"projectId": p, "identityId": "U-5eed0435", "role": "worker"})
		for _, subject := range []string{"U-5eed0435", "u-5eed0435", " U-5EED0435 "} {
			rows := root.call("list_audit_log", map[string]any{"subject": subject})["entries"].([]any)
			if len(rows) != 1 || obj(rows[0])["action"] != "member.added" || obj(rows[0])["subject"] != "u-5eed0435" {
				t.Fatalf("subject %q: %v, want the one member.added row with subject u-5eed0435", subject, rows)
			}
		}
		if n := len(root.call("list_audit_log", map[string]any{"subject": strings.ToLower(str(task["id"]))})["entries"].([]any)); n != 0 {
			t.Fatalf("subject %q (a task id lower-cased): %d rows, want 0", strings.ToLower(str(task["id"])), n)
		}
	})
}
