package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
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
		// P-257 B1: a label is one identity's agent, so alice cannot mint root's live label.
		decode(admin.do("POST", "/api/me/cli-tokens", `{"label":"agent-root","ttlSeconds":3600}`, nil))
		if code, out := h.do("POST", "/api/me/cli-tokens", `{"label":"agent-root"}`, nil); code != http.StatusBadRequest || codeOf(t, out) != "VALIDATION" || !strings.Contains(out, `"label":"agent-root"`) {
			t.Fatalf("alice mints root's live label: %d %s, want 400 VALIDATION", code, out)
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

// B2 of the #62 review: conductor's cap of 10 live CLI tokens per identity. The 11th mint is 400
// VALIDATION and mints nothing (refusal); one revoked, the next mint goes through (control).
func TestCLITokenMintIsCappedAtTenLive(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := newStackHarness(t, s, Options{})
		var first string
		for i := range 10 {
			code, body := h.do("POST", "/api/me/cli-tokens", `{"label":"t"}`, nil)
			if code != http.StatusOK {
				t.Fatalf("mint %d: %d %s", i+1, code, body)
			}
			if i == 0 {
				var m map[string]any
				_ = json.Unmarshal([]byte(body), &m)
				first = str(m["id"])
			}
		}
		code, body := h.do("POST", "/api/me/cli-tokens", `{"label":"eleventh"}`, nil)
		if code != http.StatusBadRequest || codeOf(t, body) != "VALIDATION" || !strings.Contains(body, "max 10") {
			t.Fatalf("the 11th mint: %d %s, want 400 VALIDATION naming the cap", code, body)
		}
		if _, list := h.do("GET", "/api/me/cli-tokens", "", nil); strings.Contains(list, "eleventh") || strings.Count(list, `"status":"live"`) != 10 {
			t.Fatalf("the refused mint left a token: %s", list)
		}
		if code, body := h.do("POST", "/api/me/cli-tokens/"+first+"/revoke", "", nil); code != http.StatusOK {
			t.Fatalf("revoke: %d %s", code, body)
		}
		if code, body := h.do("POST", "/api/me/cli-tokens", `{"label":"after a revoke"}`, nil); code != http.StatusOK {
			t.Fatalf("the mint after a revoke: %d %s, want 200", code, body)
		}
	})
}

// P-257 R2-B1 (review round 2, the adopted LapsedLabelTakeover probe, its mint half): a label stays
// its first minter's on each engine. Carol's agent-b token lapses and another is revoked; alice's
// REST mint of agent-b is refused each time, and carol mints her label back.
func TestALapsedLabelStaysItsMintersOnTheStack(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		var now atomic.Int64
		now.Store(time.Now().UnixMilli())
		clk := func() time.Time { return time.UnixMilli(now.Load()) }
		a := &auth.Service{Accounts: &auth.Accounts{Store: s.Store, Params: fastArgon}, Tokens: &auth.TokenService{Store: s.Store, Audit: s.Store, Now: clk},
			Sessions: s.Store, Now: clk}
		h := mountHarness(t, a, Options{Services: s.Services, Bus: s.Bus})
		carol := h.as("carol")
		mint := func(who *harness, label string) (int, string) {
			t.Helper()
			return who.do("POST", "/api/me/cli-tokens", `{"label":"`+label+`","ttlSeconds":60}`, nil)
		}
		refused := func(when string) {
			t.Helper()
			if code, out := mint(h, "agent-b"); code != http.StatusBadRequest || codeOf(t, out) != "VALIDATION" || !strings.Contains(out, `"label":"agent-b"`) {
				t.Fatalf("alice mints carol's agent-b %s: %d %s, want 400 VALIDATION", when, code, out)
			}
		}
		if code, out := mint(carol, "agent-b"); code != http.StatusOK {
			t.Fatalf("carol mints agent-b: %d %s", code, out)
		}
		refused("while carol's token is live")
		now.Add((2 * time.Minute).Milliseconds()) // carol's token lapses
		refused("once carol's token lapsed")
		code, out := mint(carol, "agent-b")
		if code != http.StatusOK {
			t.Fatalf("carol mints her lapsed label back: %d %s", code, out)
		}
		var again map[string]any
		if err := json.Unmarshal([]byte(out), &again); err != nil {
			t.Fatal(err)
		}
		if code, out := carol.do("POST", "/api/me/cli-tokens/"+str(again["id"])+"/revoke", "", nil); code != http.StatusOK {
			t.Fatalf("carol revokes: %d %s", code, out)
		}
		refused("once carol's tokens are revoked or expired")
	})
}

// P-352: on each engine, a REST mint of a label of the form "id:<identity>" is refused, another
// member's id and the caller's own alike, and writes no token; an ordinary label still mints.
func TestAnIDLabelIsRefusedAtTheRESTMint(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := newStackHarness(t, s, Options{})
		carol := h.as("carol")
		for _, label := range []string{"id:" + carol.userID, "id:" + h.userID} {
			code, out := h.do("POST", "/api/me/cli-tokens", `{"label":"`+label+`","ttlSeconds":3600}`, nil)
			if code != http.StatusBadRequest || codeOf(t, out) != "VALIDATION" || !strings.Contains(out, `"label":"`+label+`"`) {
				t.Fatalf("mint %q: %d %s, want 400 VALIDATION naming the label", label, code, out)
			}
		}
		code, out := h.do("GET", "/api/me/cli-tokens", "", nil)
		if code != http.StatusOK || strings.Contains(out, `"label":"id:`) {
			t.Fatalf("list after the refusals: %d %s, want no id: label", code, out)
		}
		if code, out := h.do("POST", "/api/me/cli-tokens", `{"label":"agent-b","ttlSeconds":3600}`, nil); code != http.StatusOK {
			t.Fatalf("mint agent-b: %d %s", code, out)
		}
	})
}
