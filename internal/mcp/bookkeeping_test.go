package mcp

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicefake"
)

// The call bookkeeping stores no caller text unchecked: a tools/call of a tool nobody registered is
// recorded and counted under unknownTool, never under the name it sent (free text, up to the 4 MiB
// body, a NUL included), and a projectId that is no id (a NUL, past idMaxBytes) records no project.
// On Postgres one NUL row refused the whole tool_calls batch it rode in.
func TestCallBookkeepingStoresNoCallerText(t *testing.T) {
	f := servicefake.New()
	h := bootHarness(t, f.Services(), resolver())
	plant(h)
	cs := h.connect(t, tokenAda)
	huge := strings.Repeat("x", 5000)
	for _, name := range []string{"a\x00b", huge} {
		if res, err := cs.CallTool(ctx(t), &mcp.CallToolParams{Name: name, Arguments: map[string]any{}}); err == nil {
			t.Fatalf("an unregistered tool answered %+v, want a JSON-RPC error", res)
		}
	}
	for _, p := range []string{"P\x00", huge} {
		if _, err := cs.CallTool(ctx(t), &mcp.CallToolParams{Name: "whoami", Arguments: map[string]any{"projectId": p}}); err != nil {
			t.Fatal(err)
		}
	}
	var rows []string
	for _, c := range calls(f, "ToolCalls.Record") {
		r := c.Req.(service.RecordToolCallReq)
		rows = append(rows, r.Tool+" "+r.Outcome+" "+r.Code+" ["+r.ProjectID+"]")
	}
	want := []string{unknownTool + " error INTERNAL []", unknownTool + " error INTERNAL []", "whoami ok OK []", "whoami ok OK []"}
	if strings.Join(rows, "|") != strings.Join(want, "|") {
		t.Errorf("tool_calls rows = %q, want %q", rows, want)
	}
	var tools []string
	for _, c := range calls(f, "Sessions.TouchAgentSession") {
		if r := c.Req.(service.TouchAgentSessionReq); r.Tool != "" {
			tools = append(tools, r.Tool+" ["+r.ProjectID+"]")
		}
	}
	if want := []string{unknownTool + " []", unknownTool + " []", "whoami []", "whoami []"}; strings.Join(tools, "|") != strings.Join(want, "|") {
		t.Errorf("agent session touches = %q, want %q", tools, want)
	}
}

// An initialize's clientInfo reaches the agent session cleaned and bounded as an agent name is
// (domain.AgentLabel): a NUL used to fail the row's write on Postgres (a live session the admin's
// list did not show), and a multi-megabyte name or version was stored whole.
func TestClientInfoIsCleanedAndBounded(t *testing.T) {
	f := servicefake.New()
	h := bootHarness(t, f.Services(), resolver())
	long := strings.Repeat("v", 5000)
	for _, info := range []string{`{"name":"\u0000","version":"1\u0000` + long + `"}`, `{"name":"` + long + `","version":"` + long + `"}`} {
		body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":` + info + `}}`
		if sid := rawPOST(t, h.url, tokenAda, "", body).Header.Get("Mcp-Session-Id"); sid == "" {
			t.Fatalf("initialize with clientInfo %.40s... was refused", info)
		}
	}
	touches := calls(f, "Sessions.TouchAgentSession")
	if len(touches) != 2 {
		t.Fatalf("%d agent session writes, want the two connections", len(touches))
	}
	for i, c := range touches {
		r := c.Req.(service.TouchAgentSessionReq)
		for name, v := range map[string]string{"agentName": r.AgentName, "clientName": r.ClientName, "clientVersion": r.ClientVersion, "sessionKey": r.SessionKey} {
			if strings.ContainsRune(v, 0) || len(v) > 80 {
				t.Errorf("connection %d: %s is %d bytes %.40q..., want no NUL and at most an agent name's length", i, name, len(v), v)
			}
		}
	}
	if r := touches[0].Req.(service.TouchAgentSessionReq); r.AgentName != "mcp" || r.ClientName != "" || !strings.HasPrefix(r.ClientVersion, "1v") {
		t.Errorf("a NUL clientInfo.name connected as agentName %q clientName %q clientVersion %.10q..., want mcp, none, the version without its NUL", r.AgentName, r.ClientName, r.ClientVersion)
	}
}
