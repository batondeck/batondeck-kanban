package mcp

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/domain"
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
	want := []string{unknownTool + " error VALIDATION []", unknownTool + " error VALIDATION []", "whoami ok OK []", "whoami ok OK []"}
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

// One identity's sessions are bounded on their own: at its bound an initialize closes that
// identity's longest-idle session instead of taking another slot, so one token holder (or a client
// that never ends its sessions) cannot fill the table and lock every other client out. The global
// bound still holds behind it.
func TestSessionsBoundedPerIdentity(t *testing.T) {
	f := servicefake.New()
	h := bootHarness(t, f.Services(), resolver(), func(o *Options) { o.MaxSessions = 3; o.MaxSessionsPerIdentity = 2 })
	plant(h)
	whoami := func(cs *mcp.ClientSession) error {
		_, err := cs.CallTool(ctx(t), &mcp.CallToolParams{Name: "whoami", Arguments: map[string]any{"projectId": "P-1"}})
		return err
	}
	idle := h.connect(t, tokenAda)
	time.Sleep(20 * time.Millisecond)
	busy := h.connect(t, tokenAda)
	time.Sleep(20 * time.Millisecond)
	if err := whoami(busy); err != nil {
		t.Fatal(err)
	}
	third := h.connect(t, tokenAda) // ada's bound is 2: her longest-idle session makes room
	waitFor(t, func() bool {
		resp := rawPOST(t, h.url, tokenAda, idle.ID(), `{"jsonrpc":"2.0","id":9,"method":"ping"}`)
		return resp.StatusCode == http.StatusNotFound
	})
	for name, cs := range map[string]*mcp.ClientSession{"the busy session": busy, "the new session": third} {
		if err := whoami(cs); err != nil {
			t.Errorf("%s of ada's after the eviction: %v", name, err)
		}
	}
	if cs, err := h.dial(tokenGrace); err != nil {
		t.Errorf("grace was locked out by ada's sessions: %v", err)
	} else {
		t.Cleanup(func() { _ = cs.Close() })
	}
	// ada's two and grace's one fill the table of 3: the global bound refuses a fourth identity.
	if cs, err := h.dial(tokenAdmin); err == nil {
		t.Cleanup(func() { _ = cs.Close() })
		t.Error("a session past MaxSessions 3 was admitted")
	}
	if logs := h.log.String(); !strings.Contains(logs, "u-ada holds 2 MCP sessions") {
		t.Errorf("the eviction was not logged:\n%s", logs)
	}
}

// The per-identity eviction never cuts a call in progress: a session with a call in hand is not
// the longest-idle one, whenever its last request arrived. When every one of the identity's
// sessions has a call in hand, its new initialize is refused (its own, never another identity's),
// and the calls finish.
func TestEvictionSparesASessionWithACallInHand(t *testing.T) {
	f := servicefake.New()
	h := bootHarness(t, f.Services(), resolver(), func(o *Options) { o.MaxSessionsPerIdentity = 2 })
	plant(h)
	started, release := make(chan struct{}, 4), make(chan struct{})
	var once sync.Once
	free := func() { once.Do(func() { close(release) }) }
	t.Cleanup(free) // before the harness's own cleanup, which waits for the handlers
	AddTool(h.srv, &mcp.Tool{Name: "slow"}, nil, func(context.Context, *mcp.CallToolRequest, plantIn) (*mcp.CallToolResult, plantOut, error) {
		started <- struct{}{}
		<-release
		return nil, plantOut{Identity: "done"}, nil
	})
	slow := func(cs *mcp.ClientSession) chan error {
		done := make(chan error, 1)
		go func() {
			_, err := cs.CallTool(ctx(t), &mcp.CallToolParams{Name: "slow", Arguments: map[string]any{"projectId": "P-1"}})
			done <- err
		}()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("the slow call never started")
		}
		return done
	}
	gone := func(cs *mcp.ClientSession) bool {
		return rawPOST(t, h.url, tokenAda, cs.ID(), `{"jsonrpc":"2.0","id":9,"method":"ping"}`).StatusCode == http.StatusNotFound
	}

	busy := h.connect(t, tokenAda)
	call := slow(busy) // busy's last request arrives first, and stays in hand
	time.Sleep(20 * time.Millisecond)
	idle := h.connect(t, tokenAda)
	if err := idle.Ping(ctx(t), nil); err != nil {
		t.Fatal(err)
	}
	h.connect(t, tokenAda) // ada's bound is 2: the session to close is idle, not busy
	waitFor(t, func() bool { return gone(idle) })
	if gone(busy) {
		t.Fatal("the session with a call in hand was closed to make room")
	}

	// Both of ada's sessions now have a call in hand: a third is refused, and both calls finish.
	third, err := h.dial(tokenAda)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = third.Close() })
	call3 := slow(third)
	if _, err := h.dial(tokenAda); err == nil || !strings.Contains(err.Error(), "too many MCP sessions for this identity") {
		t.Errorf("an initialize while every one of ada's sessions has a call in hand = %v, want refused", err)
	}
	if cs, err := h.dial(tokenGrace); err != nil {
		t.Errorf("grace was refused while ada's sessions were busy: %v", err)
	} else {
		_ = cs.Close()
	}
	free()
	for name, c := range map[string]chan error{"busy": call, "third": call3} {
		if err := <-c; err != nil {
			t.Errorf("%s session's call: %v, want it to finish", name, err)
		}
	}
}

// A tools/call's touch carries its project when the call succeeded (so the project is the caller's:
// every tool naming one authorizes on it), and no project for a refused or failed call, whose
// projectId may be any project's, a non-member's included.
func TestToolCallTouchCarriesItsProject(t *testing.T) {
	f := servicefake.New()
	h := bootHarness(t, f.Services(), resolver())
	plant(h)
	AddTool(h.srv, &mcp.Tool{Name: "denied"}, nil, func(context.Context, *mcp.CallToolRequest, plantIn) (*mcp.CallToolResult, plantOut, error) {
		return nil, plantOut{}, domain.NewError(domain.CodeForbidden, "not a member", nil)
	})
	cs := h.connect(t, tokenAda)
	for _, c := range []struct{ tool, project string }{{"whoami", "P-1"}, {"denied", "P-2"}, {"stale", "P-3"}, {"whoami", "P-4"}} {
		if _, err := cs.CallTool(ctx(t), &mcp.CallToolParams{Name: c.tool, Arguments: map[string]any{"projectId": c.project}}); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, c := range calls(f, "Sessions.TouchAgentSession") {
		if r := c.Req.(service.TouchAgentSessionReq); r.Tool != "" {
			got = append(got, r.Tool+" ["+r.ProjectID+"]")
		}
	}
	if want := []string{"whoami [P-1]", "denied []", "stale []", "whoami [P-4]"}; strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("touches = %q, want %q", got, want)
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
