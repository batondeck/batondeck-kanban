package mcp_test

import (
	"context"
	"slices"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/auth/authfake"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	bk "github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/mcp/tools"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// P-257 (audit B1): the agents of one identity are told apart. The bearers stand for what
// auth.ResolveCaller answers: a token labelled agent-a, a token with the default label (no agent of
// its own), and an OAuth access token of a client registered as "Claude Desktop".
const (
	agentLabelled = "bk_agent_labelled"
	agentPlain    = "bk_agent_plain"
	agentOAuth    = "bk_agent_oauth"
)

func agentResolver() authfake.Resolver {
	return authfake.Resolver{Callers: map[string]domain.Caller{
		agentLabelled: {Identity: "u-ada", TokenScope: "u-ada", Agent: "agent-a", Labelled: true, Via: domain.ViaAPIToken},
		agentPlain:    {Identity: "u-ada", TokenScope: "u-ada", Via: domain.ViaAPIToken},
		agentOAuth:    {Identity: "u-ada", Role: "admin", Agent: "Claude Desktop", Via: domain.ViaOAuth, OAuthClientID: "c-1"},
	}}
}

// The agent reaches the claim, the task's creator and every tool_calls row; two clients on one
// unlabelled token are two agent_sessions rows by their clientInfo.name, a labelled token's client
// is its label's row whatever it calls itself, and each row says how it authenticated (D6).
func TestTheAgentIsStampedAndKeysTheSessionOnTheRealStack(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := bk.BootHarness(t, s.Services, agentResolver(), func(o *bk.Options) { o.HiddenTools = s.Core.HiddenTools })
		tools.Register(h.Server(), s.Services)
		s.Core.SetAgentSessionEnder(h.Server())
		called := map[string]int{}
		a := &caller{t: t, cs: h.ConnectAs(t, agentLabelled, "some-client"), called: called}

		p := str(a.call("create_project", map[string]any{"name": "Agents", "idempotencyKey": "p-agents"})["projectId"])
		b := str(obj(a.call("create_board", map[string]any{"projectId": p, "name": "Main"})["board"])["id"])
		created := obj(a.call("create_task", map[string]any{"projectId": p, "boardId": b, "title": "one", "description": "the agent's task"})["task"])
		if got := str(created["createdByAgent"]); got != "agent-a" {
			t.Fatalf("create_task createdByAgent %q, want agent-a", got)
		}
		id := str(created["id"])
		a.call("update_task", map[string]any{"projectId": p, "taskId": id, "version": created["version"], "patch": map[string]any{"status": "READY"}})
		a.call("claim_next", map[string]any{"projectId": p, "boardId": b})
		claim := obj(obj(a.call("get_task", map[string]any{"projectId": p, "taskId": id})["task"])["claim"])
		if str(claim["agent"]) != "agent-a" || str(claim["agentId"]) != "agent-a" || str(claim["owner"]) != "u-ada" {
			t.Fatalf("get_task claim %v, want agent-a's, owned by u-ada", claim)
		}

		x := &caller{t: t, cs: h.ConnectAs(t, agentPlain, "client-x"), called: called}
		y := &caller{t: t, cs: h.ConnectAs(t, agentPlain, "client-y"), called: called}
		o := &caller{t: t, cs: h.ConnectAs(t, agentOAuth, "ignored-for-oauth"), called: called}
		x.call("list_projects", map[string]any{})
		y.call("list_projects", map[string]any{})
		o.call("list_projects", map[string]any{})

		kinds := map[string]string{}
		for _, r := range a.call("list_agent_sessions", map[string]any{})["sessions"].([]any) {
			kinds[str(obj(r)["agentId"])] = str(obj(r)["kind"])
		}
		want := map[string]string{"agent-a": "api_token", "client-x": "api_token", "client-y": "api_token", "Claude Desktop": "oauth_user"}
		if len(kinds) != len(want) {
			t.Fatalf("list_agent_sessions agents %v, want %v", kinds, want)
		}
		for agent, kind := range want {
			if kinds[agent] != kind {
				t.Fatalf("list_agent_sessions agents %v, want %v", kinds, want)
			}
		}

		if err := s.Core.FlushToolCalls(context.Background()); err != nil {
			t.Fatal(err)
		}
		groups, err := s.Store.SummarizeToolCalls(context.Background(), p, 0)
		if err != nil {
			t.Fatal(err)
		}
		var stamped []string
		for _, g := range groups {
			if g.Agent != "agent-a" {
				t.Fatalf("tool_calls group %+v in the project carries agent %q, want agent-a", g, g.Agent)
			}
			stamped = append(stamped, g.Tool)
		}
		for _, tool := range []string{"create_board", "create_task", "claim_next", "get_task"} {
			if !slices.Contains(stamped, tool) {
				t.Fatalf("tool_calls groups %v, want %s stamped agent-a", stamped, tool)
			}
		}
	})
}
