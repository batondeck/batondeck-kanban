package mcp_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// lastProjectId is the project of the agent's last successful call on it (the Projects grid groups
// agent avatars by it): a member's get_project sets it, a non-member's refused one does not, and a
// self-disconnect's own touch does not write the row it deleted back into being.
func TestLastProjectIDFollowsTheCallersOwnCalls(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := bootStack(t, s)
		ada := &caller{t: t, cs: h.Connect(t, stackOwner), called: map[string]int{}}
		root := &caller{t: t, cs: h.Connect(t, stackRoot), called: map[string]int{}}
		grace := h.Connect(t, stackGrace)
		p := str(ada.call("create_project", map[string]any{"name": "Grid", "idempotencyKey": "p-grid"})["projectId"])
		ada.call("get_project", map[string]any{"projectId": p})
		if _, err := grace.CallTool(testCtx(t), &sdk.CallToolParams{Name: "get_project", Arguments: map[string]any{"projectId": p}}); err != nil {
			t.Fatal(err)
		}
		last := map[string]string{}
		for _, r := range root.call("list_agent_sessions", map[string]any{"all": true})["sessions"].([]any) {
			last[str(obj(r)["sessionKey"])] = str(obj(r)["lastProjectId"])
		}
		if last["u-ada::agent-ada"] != p || last["u-grace::harness"] != "" {
			t.Fatalf("%s: lastProjectId by session %v, want %s for ada (a member's call) and none for grace (refused)", s.Engine, last, p)
		}

		code, body := rawCall(t, h.URL(), stackGrace, grace.ID(), `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"disconnect_agent","arguments":{"identityId":"u-grace","agentId":"harness"}}}`)
		if code != 200 || !strings.Contains(body, `"disconnected":true`) {
			t.Fatalf("a self-disconnect answered %d %q", code, body)
		}
		for _, r := range root.call("list_agent_sessions", map[string]any{"all": true})["sessions"].([]any) {
			if str(obj(r)["sessionKey"]) == "u-grace::harness" {
				t.Fatalf("%s: grace's row is back after her self-disconnect: %v", s.Engine, r)
			}
		}
	})
}

// A resource read whose URI carries a NUL in an id is VALIDATION on every engine, as get_task's is:
// on Postgres it reached the store and came back a retryable INTERNAL, on SQLite NOT_FOUND.
func TestResourceReadRefusesNULOnEveryEngine(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := bootStack(t, s)
		c := &caller{t: t, cs: h.Connect(t, stackOwner), called: map[string]int{}}
		p := str(c.call("create_project", map[string]any{"name": "Nul", "idempotencyKey": "p-nul"})["projectId"])
		_, err := c.cs.ReadResource(testCtx(t), &sdk.ReadResourceParams{URI: "batondeck://" + p + "/task/T%00"})
		var werr *jsonrpc.Error
		var data domain.BatonDeckError
		if !errors.As(err, &werr) || werr.Code != jsonrpc.CodeInvalidParams || json.Unmarshal(werr.Data, &data) != nil || data.Code != domain.CodeValidation {
			t.Errorf("%s: read of a task id with a NUL = %v, want JSON-RPC invalid params with VALIDATION", s.Engine, err)
		}
	})
}
