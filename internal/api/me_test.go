package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// The settings, CLI-token, agent-session and audit routes (R2.5) on each engine, through the real
// login: each answers the signed-in user's own data; the refusals are the service's and the route's.
func TestMeAgentsAuditRoutesOnTheStack(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := newStackHarness(t, s, Options{})
		admin := h.asAdmin("root")
		decode := func(code int, body string) map[string]any {
			t.Helper()
			if code != http.StatusOK {
				t.Fatalf("status %d %s, want 200", code, body)
			}
			var out map[string]any
			if err := json.Unmarshal([]byte(body), &out); err != nil {
				t.Fatal(err)
			}
			return out
		}

		got := decode(h.do("GET", "/api/me/settings", "", nil))
		if got["tokenPolicy"] != nil || got["estimated"] != true {
			t.Fatalf("GET settings: %v", got)
		}
		if got := decode(h.do("PATCH", "/api/me/settings", `{"tokenPolicy":"warn","maxTicketTokens":2000}`, nil)); got["tokenPolicy"] != "warn" || got["maxTicketTokens"] != float64(2000) {
			t.Fatalf("PATCH settings: %v", got)
		}
		if code, body := h.do("PATCH", "/api/me/settings", `{"maxTicketTokens":5}`, nil); code != http.StatusBadRequest || codeOf(t, body) != "VALIDATION" {
			t.Fatalf("PATCH a ceiling under 100: %d %s", code, body)
		}
		if got := decode(admin.do("GET", "/api/me/settings", "", nil)); got["tokenPolicy"] != nil {
			t.Fatalf("root reads alice's settings: %v", got)
		}

		for body, why := range map[string]string{`{"label":"x","scope":"all"}`: "an unknown key", `{"label":"x","ttlSeconds":-1}`: "a negative ttl", `{"label":""}`: "an empty label", `{"label":"x","ttlSeconds":10}`: "a ttl under a minute"} {
			if code, out := h.do("POST", "/api/me/cli-tokens", body, nil); code != http.StatusBadRequest || codeOf(t, out) != "VALIDATION" {
				t.Fatalf("mint with %s: %d %s, want 400 VALIDATION", why, code, out)
			}
		}
		minted := decode(h.do("POST", "/api/me/cli-tokens", `{"label":"ci","ttlSeconds":3600}`, nil))
		if minted["shownOnce"] != true || minted["lastUsedAt"] != nil || !strings.HasPrefix(str(minted["token"]), "bd_"+str(minted["id"])+"_") {
			t.Fatalf("mint: %v", minted)
		}
		if code, body := h.do("POST", "/api/me/cli-tokens/"+str(minted["id"])+"/revoke", `{}`, nil); code != http.StatusOK || !strings.Contains(body, `"status":"revoked"`) {
			t.Fatalf("revoke: %d %s", code, body)
		}
		if code, body := h.do("POST", "/api/me/cli-tokens/bd_0000000000000000/revoke", "", nil); code != http.StatusNotFound {
			t.Fatalf("revoke a missing token: %d %s", code, body)
		}

		// Sessions: a cookie session holds no MCP session, so the list is empty; all:true is the
		// admin's; a disconnect of nothing is NOT_FOUND, of another's agent FORBIDDEN.
		if got := decode(h.do("GET", "/api/agents/sessions?limit=10", "", nil)); len(got["sessions"].([]any)) != 0 {
			t.Fatalf("GET sessions: %v", got)
		}
		if code, body := h.do("GET", "/api/agents/sessions?all=true", "", nil); code != http.StatusForbidden {
			t.Fatalf("GET sessions all=true as a member: %d %s", code, body)
		}
		decode(admin.do("GET", "/api/agents/sessions?all=true", "", nil))
		ctx := context.Background()
		if _, err := s.Services.Sessions.TouchAgentSession(ctx, h.caller(), service.TouchAgentSessionReq{SessionKey: service.SessionKey(h.userID, "claude"), AgentName: "claude", Kind: "service_account"}); err != nil {
			t.Fatal(err)
		}
		if code, body := admin.do("POST", "/api/agents/sessions/disconnect", `{"identityId":"`+h.userID+`","agentId":"nope"}`, nil); code != http.StatusNotFound {
			t.Fatalf("disconnect nothing: %d %s", code, body)
		}
		if code, body := h.do("POST", "/api/agents/sessions/disconnect", `{"identityId":"`+admin.userID+`","agentId":"claude"}`, nil); code != http.StatusForbidden {
			t.Fatalf("disconnect another's agent: %d %s", code, body)
		}
		if got := decode(h.do("POST", "/api/agents/sessions/disconnect", `{"identityId":"`+h.userID+`","agentId":"claude"}`, nil)); got["disconnected"] != true {
			t.Fatalf("disconnect: %v", got)
		}

		// The audit log: a member is FORBIDDEN, the admin reads the disconnect and the token rows.
		if code, body := h.do("GET", "/api/audit", "", nil); code != http.StatusForbidden || codeOf(t, body) != "FORBIDDEN" {
			t.Fatalf("GET audit as a member: %d %s", code, body)
		}
		// No subject filter here: domain.DescribeAudit lowercases an identity subject (canonID), so
		// agent.disconnected names "u-…" where the token rows name the account id as it is.
		rows := decode(admin.do("GET", "/api/audit?days=1&limit=10&actor="+h.userID, "", nil))["entries"].([]any)
		var actions []string
		for _, r := range rows {
			actions = append(actions, str(r.(map[string]any)["action"]))
		}
		if strings.Join(actions, " ") != "agent.disconnected cli_token.revoked cli_token.minted" {
			t.Fatalf("GET audit as the admin: %v", actions)
		}
		if code, body := admin.do("GET", "/api/audit?days=400", "", nil); code != http.StatusBadRequest {
			t.Fatalf("GET audit days=400: %d %s", code, body)
		}
	})
}

// No session is 401 before any service or the TokenService.
func TestMeRoutesRefusals(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := newStackHarness(t, s, Options{})
		for _, p := range []string{"/api/me/settings", "/api/me/cli-tokens", "/api/agents/sessions", "/api/audit"} {
			req := h.request("GET", p, "", nil)
			req.Header.Del("Cookie")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("GET %s with no session: %d, want 401", p, resp.StatusCode)
			}
		}
	})
}

func str(v any) string { s, _ := v.(string); return s }
