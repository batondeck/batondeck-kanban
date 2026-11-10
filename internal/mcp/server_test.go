package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/auth/authfake"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicefake"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

const (
	tokenAda   = "bk_test_ada"
	tokenGrace = "bk_test_grace"
	tokenAdmin = "bk_test_admin"
)

func resolver() authfake.Resolver {
	return authfake.Resolver{Callers: map[string]domain.Caller{
		tokenAda:   {Identity: "u-ada", TokenScope: "u-ada"},
		tokenGrace: {Identity: "u-grace", TokenScope: "u-grace"},
		tokenAdmin: {Identity: "u-admin", TokenScope: "u-admin", WorkspaceAdmin: true},
	}}
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

// calls is the fake's recorded calls of method.
func calls(f *servicefake.Fake, method string) []servicefake.Call {
	var out []servicefake.Call
	for _, c := range f.Calls() {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// rawPOST sends one JSON-RPC message as a client would, with the bearer (when non-empty) and the
// session id (when non-empty).
func rawPOST(t *testing.T, url, bearer, session, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
		req.Header.Set("Mcp-Protocol-Version", "2025-11-25")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

const initBody = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}`

// TestInitializeAnswersTheSurface: a stateful session negotiates 2025-11-25 and lists no tools until
// an area registers them, the four prompts and the resource templates (surface_test.go checks
// their content), and advertises resources with listChanged and without subscribe.
func TestInitializeAnswersTheSurface(t *testing.T) {
	f := servicefake.New()
	h := bootHarness(t, f.Services(), resolver())
	cs := h.connect(t, tokenAda)

	init := cs.InitializeResult()
	if init.ProtocolVersion != "2025-11-25" {
		t.Errorf("negotiated %q, want 2025-11-25 (a stateful server refuses 2026-07-28)", init.ProtocolVersion)
	}
	caps := init.Capabilities
	if caps.Tools == nil || !caps.Tools.ListChanged || caps.Prompts == nil || !caps.Prompts.ListChanged ||
		caps.Resources == nil || !caps.Resources.ListChanged || caps.Resources.Subscribe {
		t.Errorf("capabilities = %+v, want tools, prompts and resources with listChanged, and no subscribe", caps)
	}
	if cs.ID() == "" {
		t.Error("no Mcp-Session-Id: the server is not stateful")
	}
	tools, err := cs.ListTools(ctx(t), nil)
	if err != nil || tools.Tools == nil || len(tools.Tools) != 0 {
		t.Errorf("tools/list = %+v, %v; want [] (not null)", tools, err)
	}
	prompts, err := cs.ListPrompts(ctx(t), nil)
	if err != nil || len(prompts.Prompts) != 4 {
		t.Errorf("prompts/list = %+v, %v; want the four prompts", prompts, err)
	}
	res, err := cs.ListResources(ctx(t), nil)
	if err != nil || len(res.Resources) != 0 {
		t.Errorf("resources/list = %+v, %v; want [] (every resource is a template)", res, err)
	}
	tpl, err := cs.ListResourceTemplates(ctx(t), nil)
	if err != nil || len(tpl.ResourceTemplates) != 11 {
		t.Errorf("resources/templates/list = %+v, %v; want the eleven templates (five board and task, six chronicle)", tpl, err)
	}
	// Every list is the caller's own (tools/list is filtered per audience): no cache shared across
	// callers may serve it, so none is labelled public, the protocol default.
	for name, scope := range map[string]string{"tools/list": tools.CacheScope, "prompts/list": prompts.CacheScope,
		"resources/list": res.CacheScope, "resources/templates/list": tpl.CacheScope} {
		if scope != "private" {
			t.Errorf("%s cacheScope %q, want private", name, scope)
		}
	}

	touches := calls(f, "Sessions.TouchAgentSession")
	if len(touches) == 0 {
		t.Fatal("initialize created no agent session")
	}
	first := touches[0]
	req := first.Req.(service.TouchAgentSessionReq)
	if len(touches) != 1 || first.Caller.Identity != "u-ada" || req.SessionKey != "u-ada::harness" || req.ClientName != "harness" ||
		req.ClientVersion != "0.0.1" || req.AgentName != "harness" || req.Kind != "api_token" || req.Tool != "" {
		t.Errorf("touches = %+v; want one, the connection of client harness 0.0.1 as u-ada (no list or ping counts as one)", touches)
	}
}

// It runs over servicefake and over the real service on every selected engine, where a valid
// bearer's initialize writes its agent session through the real store.
func TestBearerResolvesOrIs401(t *testing.T) {
	t.Run("servicefake", func(t *testing.T) { bearerResolvesOrIs401(t, servicefake.New().Services()) })
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) { bearerResolvesOrIs401(t, s.Services) })
}

func bearerResolvesOrIs401(t *testing.T, svc service.Services) {
	h := bootHarness(t, svc, resolver())

	none := rawPOST(t, h.url, "", "", initBody)
	revoked := rawPOST(t, h.url, "bk_revoked_or_unknown", "", initBody)
	valid := rawPOST(t, h.url, tokenAda, "", initBody)
	for name, resp := range map[string]*http.Response{"no bearer": none, "revoked bearer": revoked} {
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s: status %d, want 401", name, resp.StatusCode)
		}
		if got := resp.Header.Get("WWW-Authenticate"); got != "Bearer" {
			t.Errorf("%s: WWW-Authenticate %q, want Bearer", name, got)
		}
	}
	if valid.StatusCode != http.StatusOK || valid.Header.Get("Mcp-Session-Id") == "" {
		t.Errorf("valid bearer: status %d, Mcp-Session-Id %q; want 200 and an id", valid.StatusCode, valid.Header.Get("Mcp-Session-Id"))
	}

	// Another identity's bearer on a session is refused: the session is bound to its caller.
	sid := valid.Header.Get("Mcp-Session-Id")
	hijack := rawPOST(t, h.url, tokenGrace, sid, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if hijack.StatusCode != http.StatusForbidden {
		t.Errorf("grace on ada's session: status %d, want 403", hijack.StatusCode)
	}
}

// P-323: a session is bound to its bearer's identity, how it authenticated and the agent it
// carries. Another bearer of the same identity that would act as another agent is refused 403;
// one that keeps all three (a rotated token of the same label, another default-label token) works.
func TestASessionRefusesABearerOfTheSameIdentityWithAnotherAgent(t *testing.T) {
	const (
		labelledA  = "bk_swap_label_a"
		labelledA2 = "bk_swap_label_a_rotated"
		labelledB  = "bk_swap_label_b"
		plain      = "bk_swap_plain"
		plain2     = "bk_swap_plain_other"
		oauth      = "bk_swap_oauth"
	)
	res := authfake.Resolver{Callers: map[string]domain.Caller{
		labelledA:  {Identity: "u-ada", TokenScope: "u-ada", Agent: "agent-a", Labelled: true, Via: domain.ViaAPIToken},
		labelledA2: {Identity: "u-ada", TokenScope: "u-ada", Agent: "agent-a", Labelled: true, Via: domain.ViaAPIToken},
		labelledB:  {Identity: "u-ada", TokenScope: "u-ada", Agent: "agent-b", Labelled: true, Via: domain.ViaAPIToken},
		plain:      {Identity: "u-ada", TokenScope: "u-ada", Via: domain.ViaAPIToken},
		plain2:     {Identity: "u-ada", TokenScope: "u-ada", Via: domain.ViaAPIToken},
		oauth:      {Identity: "u-ada", Role: "admin", Agent: "agent-a", Via: domain.ViaOAuth, OAuthClientID: "c-1"},
	}}
	h := bootHarness(t, servicefake.New().Services(), res)
	open := func(bearer string) string {
		t.Helper()
		resp := rawPOST(t, h.url, bearer, "", initBody)
		sid := resp.Header.Get("Mcp-Session-Id")
		if resp.StatusCode != http.StatusOK || sid == "" {
			t.Fatalf("initialize on %s: status %d, session %q", bearer, resp.StatusCode, sid)
		}
		if r := rawPOST(t, h.url, bearer, sid, `{"jsonrpc":"2.0","method":"notifications/initialized"}`); r.StatusCode/100 != 2 {
			t.Fatalf("initialized on %s: status %d", bearer, r.StatusCode)
		}
		return sid
	}
	list := func(bearer, sid string) (int, string) {
		resp := rawPOST(t, h.url, bearer, sid, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	onA := open(labelledA)
	for _, c := range []struct {
		name, bearer string
		want         int
	}{
		{"another token labelled agent-b", labelledB, http.StatusForbidden},
		{"a default-label token", plain, http.StatusForbidden},
		{"an OAuth token whose client is named agent-a", oauth, http.StatusForbidden},
		{"a rotated token labelled agent-a", labelledA2, http.StatusOK},
		{"the session's own token", labelledA, http.StatusOK},
	} {
		code, body := list(c.bearer, onA)
		if code != c.want {
			t.Errorf("agent-a's session, tools/list on %s: status %d (%s), want %d", c.name, code, body, c.want)
		}
		if code == http.StatusOK && !strings.Contains(body, `"tools"`) {
			t.Errorf("agent-a's session, tools/list on %s: body %s, want the tool list", c.name, body)
		}
	}

	onPlain := open(plain)
	if code, body := list(plain2, onPlain); code != http.StatusOK || !strings.Contains(body, `"tools"`) {
		t.Errorf("a default-label session, tools/list on another default-label token: status %d (%s), want 200", code, body)
	}
	if code, _ := list(labelledA, onPlain); code != http.StatusForbidden {
		t.Errorf("a default-label session, tools/list on a token labelled agent-a: status %d, want 403", code)
	}
}

func TestResolverFailureIs500(t *testing.T) {
	h := bootHarness(t, servicefake.New().Services(), failingResolver{})
	resp := rawPOST(t, h.url, tokenAda, "", initBody)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status %d, want 500 when the resolver fails for a reason other than the bearer", resp.StatusCode)
	}
	if !strings.Contains(h.log.String(), "resolve bearer failed") {
		t.Errorf("the failure was not logged: %q", h.log.String())
	}
}

type failingResolver struct{}

func (failingResolver) ResolveCaller(context.Context, string) (domain.Caller, error) {
	return domain.Caller{}, errors.New("store closed")
}

type plantIn struct {
	ProjectID string `json:"projectId"`
}

type plantOut struct {
	Identity string `json:"identity"`
}

// plant registers the test tools: whoami returns its caller, stale fails with STALE, boom panics.
func plant(h *harness) {
	AddTool(h.srv, &mcp.Tool{Name: "whoami"}, nil, func(ctx context.Context, _ *mcp.CallToolRequest, _ plantIn) (*mcp.CallToolResult, plantOut, error) {
		c, ok := CallerFrom(ctx)
		if !ok {
			return nil, plantOut{}, domain.NewError(domain.CodeUnauthenticated, "no caller", nil)
		}
		return nil, plantOut{Identity: c.Identity}, nil
	})
	AddTool(h.srv, &mcp.Tool{Name: "stale"}, nil, func(context.Context, *mcp.CallToolRequest, plantIn) (*mcp.CallToolResult, plantOut, error) {
		return nil, plantOut{}, domain.Stale(3, 4)
	})
	AddTool(h.srv, &mcp.Tool{Name: "boom"}, nil, func(context.Context, *mcp.CallToolRequest, plantIn) (*mcp.CallToolResult, plantOut, error) {
		panic("planted panic")
	})
	AddTool(h.srv, &mcp.Tool{Name: "rpcerr"}, nil, func(context.Context, *mcp.CallToolRequest, plantIn) (*mcp.CallToolResult, plantOut, error) {
		return nil, plantOut{}, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: "planted protocol error"}
	})
	AddTool(h.srv, &mcp.Tool{Name: "plain"}, nil, func(context.Context, *mcp.CallToolRequest, plantIn) (*mcp.CallToolResult, plantOut, error) {
		return nil, plantOut{}, errors.New("planted plain error")
	})
}

func TestCallerReachesEveryCall(t *testing.T) {
	h := bootHarness(t, servicefake.New().Services(), resolver())
	plant(h)
	cs := h.connect(t, tokenGrace)
	for i := range 2 { // the second call is a later POST on the same session
		res, err := cs.CallTool(ctx(t), &mcp.CallToolParams{Name: "whoami", Arguments: map[string]any{"projectId": "P-1"}})
		if err != nil || res.IsError {
			t.Fatalf("call %d: %+v, %v", i, res, err)
		}
		var out plantOut
		raw, _ := json.Marshal(res.StructuredContent)
		_ = json.Unmarshal(raw, &out)
		if out.Identity != "u-grace" {
			t.Errorf("call %d: the tool saw caller %q, want u-grace", i, out.Identity)
		}
	}
}

// errorShape loads contract/errors.json's shape: the required keys and every allowed key.
func errorShape(t *testing.T) (required, allowed []string, codes map[string]bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "contract", "errors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Shape struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		} `json:"shape"`
		Codes []struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"codes"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	for k := range doc.Shape.Properties {
		allowed = append(allowed, k)
	}
	codes = map[string]bool{}
	for _, c := range doc.Codes {
		codes[c.Code] = c.Retryable
	}
	return doc.Shape.Required, allowed, codes
}

// toolError calls name and returns its structured error, failing unless the result is isError.
func toolError(t *testing.T, cs *mcp.ClientSession, name string, args any) map[string]any {
	t.Helper()
	res, err := cs.CallTool(ctx(t), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error %v, want a tool result with isError", name, err)
	}
	if !res.IsError {
		t.Fatalf("%s: isError false: %+v", name, res)
	}
	got, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("%s: structured content %T %v, want the error object", name, res.StructuredContent, res.StructuredContent)
	}
	required, allowed, codes := errorShape(t)
	for _, k := range required {
		if _, ok := got[k]; !ok {
			t.Errorf("%s: %v lacks %q", name, got, k)
		}
	}
	for k := range got {
		if !slices.Contains(allowed, k) {
			t.Errorf("%s: %v carries %q, outside contract/errors.json", name, got, k)
		}
	}
	code, _ := got["code"].(string)
	if want, ok := codes[code]; !ok || got["retryable"] != want {
		t.Errorf("%s: code %q retryable %v, want a contract code with its retryable", name, code, got["retryable"])
	}
	text := res.Content[0].(*mcp.TextContent).Text
	var fromText map[string]any
	if err := json.Unmarshal([]byte(text), &fromText); err != nil || fromText["code"] != code {
		t.Errorf("%s: text content %q is not the same error", name, text)
	}
	return got
}

func TestToolErrorShape(t *testing.T) {
	h := bootHarness(t, servicefake.New().Services(), resolver())
	plant(h)
	cs := h.connect(t, tokenAda)

	stale := toolError(t, cs, "stale", map[string]any{"projectId": "P-1"})
	if stale["code"] != "STALE" || stale["retryable"] != true {
		t.Errorf("stale = %v, want STALE retryable", stale)
	}
	if d, _ := stale["details"].(map[string]any); d["expected"] != float64(3) || d["actual"] != float64(4) {
		t.Errorf("stale details = %v, want expected 3 actual 4", stale["details"])
	}

	boom := toolError(t, cs, "boom", map[string]any{"projectId": "P-1"})
	if boom["code"] != "INTERNAL" || boom["retryable"] != true {
		t.Errorf("boom = %v, want INTERNAL retryable", boom)
	}
	logs := h.log.String()
	if !strings.Contains(logs, "tool boom panicked: planted panic") || !strings.Contains(logs, "runtime/debug.Stack") {
		t.Errorf("the panic's log line carries no stack:\n%s", logs)
	}
	// The session survives the panic.
	if _, err := cs.ListTools(ctx(t), nil); err != nil {
		t.Errorf("after the panic: %v", err)
	}

	plain := toolError(t, cs, "plain", map[string]any{"projectId": "P-1"})
	if plain["code"] != "INTERNAL" || plain["retryable"] != true || strings.Contains(fmt.Sprint(plain["message"]), "planted") {
		t.Errorf("plain error = %v, want INTERNAL retryable, its text kept out of the message", plain)
	}
	if logs := h.log.String(); !strings.Contains(logs, "mcp: tool plain failed: planted plain error") || !strings.Contains(logs, "runtime/debug.Stack") {
		t.Errorf("the plain error was not logged with a stack:\n%s", logs)
	}

	bad := toolError(t, cs, "stale", map[string]any{"projectId": 7})
	if bad["code"] != "VALIDATION" || bad["retryable"] != false {
		t.Errorf("bad arguments = %v, want VALIDATION", bad)
	}
}

func TestEveryToolCallRecordedOnce(t *testing.T) {
	f := servicefake.New()
	h := bootHarness(t, f.Services(), resolver())
	plant(h)
	AddTool(h.srv, &mcp.Tool{Name: "denied"}, nil, func(context.Context, *mcp.CallToolRequest, plantIn) (*mcp.CallToolResult, plantOut, error) {
		return nil, plantOut{}, domain.NewError(domain.CodeForbidden, "not a member", nil)
	})
	cs := h.connect(t, tokenAda)
	for _, name := range []string{"whoami", "stale", "boom", "plain", "denied"} {
		if _, err := cs.CallTool(ctx(t), &mcp.CallToolParams{Name: name, Arguments: map[string]any{"projectId": "P-1"}}); err != nil {
			t.Fatal(err)
		}
	}
	// A name nobody registered: go-sdk refuses it, and the row carries neither the caller's free text
	// nor a project, only unknownTool.
	_, _ = cs.CallTool(ctx(t), &mcp.CallToolParams{Name: "BUY-CHEAP-PILLS-example.com", Arguments: map[string]any{"projectId": "P-1"}})
	var got []string
	for _, c := range calls(f, "ToolCalls.Record") {
		r := c.Req.(service.RecordToolCallReq)
		got = append(got, c.Caller.Identity+" "+r.Tool+" "+r.Outcome+" "+r.Code+" "+r.ProjectID)
	}
	want := []string{"u-ada whoami ok OK P-1", "u-ada stale error STALE P-1", "u-ada boom error INTERNAL P-1", "u-ada plain error INTERNAL P-1",
		"u-ada denied error FORBIDDEN ", "u-ada " + unknownTool + " error INTERNAL "}
	if !slices.Equal(got, want) {
		t.Errorf("tool_calls rows = %q, want %q", got, want)
	}
}

// agentLive is whether (identity, agent) holds a live MCP session in h's table (LiveAgentSessions).
func agentLive(h *harness, identity, agent string) bool {
	return slices.Contains(h.srv.LiveAgentSessions(), [2]string{identity, agent})
}

// onlyTouches fails t when the transport asked service.Sessions for anything but a touch: the end of
// an MCP session leaves its agent_sessions row for the retention sweep (P-264, plan Clarification 7).
func onlyTouches(t *testing.T, f *servicefake.Fake) {
	t.Helper()
	for _, c := range f.Calls() {
		if strings.HasPrefix(c.Method, "Sessions.") && c.Method != "Sessions.TouchAgentSession" {
			t.Errorf("the transport called %s (%+v), want touches only: a session's end keeps its row", c.Method, c.Req)
		}
	}
}

// P-264 (tested): an agent session is live while any of its MCP sessions is, and the end of the last
// leaves its row (no deleting call).
func TestAgentSessionOutlivesItsLastMCPSession(t *testing.T) {
	f := servicefake.New()
	h := bootHarness(t, f.Services(), resolver())
	one, two := h.connect(t, tokenAda), h.connect(t, tokenAda)
	live := func() int {
		h.srv.sessions.mu.Lock()
		defer h.srv.sessions.mu.Unlock()
		return len(h.srv.sessions.live)
	}
	if !agentLive(h, "u-ada", "harness") {
		t.Fatalf("live agent sessions %v, want u-ada/harness", h.srv.LiveAgentSessions())
	}
	_ = one.Close()
	waitFor(t, func() bool { return live() == 1 })
	time.Sleep(200 * time.Millisecond) // the ended session's goroutine has decided by now
	if !agentLive(h, "u-ada", "harness") {
		t.Fatalf("after the first of two ended, live agent sessions %v, want u-ada/harness: the other still holds it", h.srv.LiveAgentSessions())
	}
	_ = two.Close()
	waitFor(t, func() bool { return !agentLive(h, "u-ada", "harness") })
	time.Sleep(200 * time.Millisecond)
	onlyTouches(t, f)
}

func TestToolListPerAudienceMemoised(t *testing.T) {
	h := bootHarness(t, servicefake.New().Services(), resolver())
	asked := 0
	AddTool(h.srv, &mcp.Tool{Name: "admin_only"}, func(admin bool) bool { asked++; return admin },
		func(context.Context, *mcp.CallToolRequest, plantIn) (*mcp.CallToolResult, plantOut, error) {
			return nil, plantOut{}, nil
		})
	names := func(cs *mcp.ClientSession) []string {
		res, err := cs.ListTools(ctx(t), nil)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, tl := range res.Tools {
			out = append(out, tl.Name)
		}
		return out
	}
	admin, member := h.connect(t, tokenAdmin), h.connect(t, tokenAda)
	if got := names(admin); !slices.Equal(got, []string{"admin_only"}) {
		t.Errorf("admin sees %v, want [admin_only]", got)
	}
	if got := names(member); len(got) != 0 {
		t.Errorf("member sees %v, want []", got)
	}
	before := asked
	names(admin)
	names(member)
	names(h.connect(t, tokenGrace))
	if asked != before {
		t.Errorf("tools/list was filtered again (%d more), want the per-audience memo", asked-before)
	}
}

func TestSessionsCollectedWhenIdle(t *testing.T) {
	f := servicefake.New()
	h := bootHarness(t, f.Services(), resolver(), func(o *Options) { o.SessionIdle = 2 * time.Second })
	plant(h)
	idle, active := h.connect(t, tokenAda), h.connect(t, tokenGrace)
	start := len(calls(f, "Sessions.TouchAgentSession"))

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := active.CallTool(ctx(t), &mcp.CallToolParams{Name: "whoami", Arguments: map[string]any{"projectId": "P-1"}}); err != nil {
			t.Fatalf("active call: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}

	resp := rawPOST(t, h.url, tokenAda, idle.ID(), `{"jsonrpc":"2.0","id":9,"method":"tools/list"}`)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(string(body), "session not found") {
		t.Errorf("idle session after 3s: %d %q, want 404 session not found", resp.StatusCode, body)
	}
	if _, err := idle.ListTools(ctx(t), nil); err == nil {
		t.Error("the idle session's client still works, want session-not-found")
	}
	if _, err := active.ListTools(ctx(t), nil); err != nil {
		t.Errorf("the active session was collected: %v", err)
	}

	waitFor(t, func() bool { return !agentLive(h, "u-ada", "harness") })
	if !agentLive(h, "u-grace", "harness") {
		t.Errorf("live agent sessions %v after the idle collection, want u-grace/harness only", h.srv.LiveAgentSessions())
	}
	onlyTouches(t, f) // the collected session's row stays for the retention sweep (P-264)
	refreshed := 0
	for _, c := range calls(f, "Sessions.TouchAgentSession")[start:] {
		if r := c.Req.(service.TouchAgentSessionReq); r.SessionKey == "u-grace::harness" && r.Tool == "whoami" {
			refreshed++
		}
	}
	if refreshed < 5 {
		t.Errorf("the active session was touched %d times in 3s of tool calls, want each call", refreshed)
	}
}

func TestSessionCountBounded(t *testing.T) {
	f := servicefake.New()
	h := bootHarness(t, f.Services(), resolver(), func(o *Options) { o.MaxSessions = 1 })
	first := h.connect(t, tokenAda)
	if cs, err := h.dial(tokenAda); err == nil {
		t.Cleanup(func() { _ = cs.Close() }) // before the listener's Close, which waits for its stream
		t.Fatal("a second session past MaxSessions 1 was admitted")
	}
	_ = first.Close() // DELETE: the session ends and frees its slot
	waitFor(t, func() bool { return !agentLive(h, "u-ada", "harness") })
	if cs, err := h.dial(tokenAda); err != nil {
		t.Errorf("after the first closed: %v", err)
	} else {
		_ = cs.Close()
	}
	if !strings.Contains(h.log.String(), "session limit 1 reached") {
		t.Errorf("the refusal was not logged: %q", h.log.String())
	}
}

func TestBookkeepingFailureDoesNotFailTheRequest(t *testing.T) {
	f := servicefake.New()
	failing := authfake.Resolver{Callers: map[string]domain.Caller{tokenAda: {Identity: servicefake.Trigger(domain.CodeInternal)}}}
	h := bootHarness(t, f.Services(), failing)
	plant(h)
	cs := h.connect(t, tokenAda)
	if res, err := cs.CallTool(ctx(t), &mcp.CallToolParams{Name: "whoami", Arguments: map[string]any{"projectId": "P-1"}}); err != nil || res.IsError {
		t.Errorf("a tool call failed with the session touch failing: %+v %v", res, err)
	}
	if logs := h.log.String(); !strings.Contains(logs, "create agent session") || !strings.Contains(logs, "touch agent session") {
		t.Errorf("the touch failure was not logged: %q", h.log.String())
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("condition not met in 5s")
}

// A protocol error from tools/call (an unknown tool, a handler's *jsonrpc.Error) reaches the
// client as a JSON-RPC error, is recorded once, and leaves the server serving: go-sdk hands the
// middleware a typed-nil result beside the error.
func TestProtocolErrorsLeaveTheServerServing(t *testing.T) {
	for _, name := range []string{"nosuch", "rpcerr"} {
		t.Run(name, func(t *testing.T) {
			f := servicefake.New()
			h := bootHarness(t, f.Services(), resolver())
			plant(h)
			cs := h.connect(t, tokenAda)
			if res, err := cs.CallTool(ctx(t), &mcp.CallToolParams{Name: name, Arguments: map[string]any{"projectId": "P-1"}}); err == nil {
				t.Fatalf("%s: %+v, want a JSON-RPC error", name, res)
			}
			if _, err := cs.ListTools(ctx(t), nil); err != nil {
				t.Fatalf("after %s the server stopped answering: %v", name, err)
			}
			var rows []string
			for _, c := range calls(f, "ToolCalls.Record") {
				r := c.Req.(service.RecordToolCallReq)
				rows = append(rows, r.Tool+" "+r.Outcome+" "+r.Code)
			}
			recorded := name
			if name == "nosuch" {
				recorded = unknownTool // the caller's free text is never stored
			}
			if want := []string{recorded + " error INTERNAL"}; !slices.Equal(rows, want) {
				t.Errorf("tool_calls rows = %q, want %q", rows, want)
			}
		})
	}
}

// panicky is a ToolCalls whose Record panics.
type panicky struct{}

func (panicky) Record(context.Context, domain.Caller, service.RecordToolCallReq) (service.RecordToolCallResp, error) {
	panic("planted record panic")
}

// A panic in the tool-call bookkeeping is logged and never fails the call or the server.
func TestRecordPanicIsContained(t *testing.T) {
	svc := servicefake.New().Services()
	svc.ToolCalls = panicky{}
	h := bootHarness(t, svc, resolver())
	plant(h)
	cs := h.connect(t, tokenAda)
	res, err := cs.CallTool(ctx(t), &mcp.CallToolParams{Name: "whoami", Arguments: map[string]any{"projectId": "P-1"}})
	if err != nil || res.IsError {
		t.Fatalf("whoami with Record panicking: %+v %v", res, err)
	}
	if _, err := cs.ListTools(ctx(t), nil); err != nil {
		t.Fatalf("the server stopped answering: %v", err)
	}
	if logs := h.log.String(); !strings.Contains(logs, "mcp: recording tool call whoami panicked: planted record panic") {
		t.Errorf("the record panic was not logged:\n%s", logs)
	}
}

// Close ends a session holding an open GET stream, leaves its agent session's row (P-264) and admits
// no new one, well inside a shutdown budget.
func TestCloseEndsLiveSessions(t *testing.T) {
	f := servicefake.New()
	h := bootHarness(t, f.Services(), resolver())
	h.connect(t, tokenAda) // the go-sdk client opens its standalone GET stream after initialize
	c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := h.srv.Close(c); err != nil {
		t.Fatalf("Close: %v after %s", err, time.Since(start))
	}
	if got := h.srv.LiveAgentSessions(); len(got) != 0 {
		t.Errorf("live agent sessions %v after Close, want none", got)
	}
	onlyTouches(t, f)
	if cs, err := h.dial(tokenAda); err == nil {
		t.Cleanup(func() { _ = cs.Close() })
		t.Error("a session was admitted after Close")
	}
}

// initialized opens a session over raw POSTs and returns its id.
func initialized(t *testing.T, h *harness, bearer string) string {
	t.Helper()
	sid := rawPOST(t, h.url, bearer, "", initBody).Header.Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatal("initialize answered no session id")
	}
	rawPOST(t, h.url, bearer, sid, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	return sid
}

// A second initialize on a live session is refused by go-sdk and leaves the session in the
// table, so Close still ends it, and its agent session was counted once (none is left live).
func TestDuplicateInitializeKeepsTheSession(t *testing.T) {
	f := servicefake.New()
	h := bootHarness(t, f.Services(), resolver())
	sid := initialized(t, h, tokenAda)
	dup, _ := io.ReadAll(rawPOST(t, h.url, tokenAda, sid, initBody).Body)
	if !strings.Contains(string(dup), `duplicate \"initialize\"`) {
		t.Fatalf("duplicate initialize answered %s, want go-sdk's refusal", dup)
	}
	h.srv.sessions.mu.Lock()
	_, live := h.srv.sessions.live[sid]
	h.srv.sessions.mu.Unlock()
	if !live {
		t.Error("the duplicate initialize evicted its live session from the table")
	}
	c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if err := h.srv.Close(c); err != nil {
		t.Fatalf("Close: %v after %s", err, time.Since(start))
	}
	if got := h.srv.LiveAgentSessions(); len(got) != 0 {
		t.Errorf("live agent sessions %v after Close, want none: the duplicate initialize was counted", got)
	}
	if n := len(calls(f, "Sessions.TouchAgentSession")); n != 1 {
		t.Errorf("%d agent session writes, want 1: the duplicate initialize wrote a connection", n)
	}
	onlyTouches(t, f)
}

// A duplicate initialize frees no slot: with MaxSessions 1 the next session is still refused,
// and admitted once the first ends.
func TestDuplicateInitializeKeepsTheBound(t *testing.T) {
	f := servicefake.New()
	h := bootHarness(t, f.Services(), resolver(), func(o *Options) { o.MaxSessions = 1 })
	sid := initialized(t, h, tokenAda)
	for range 3 {
		_, _ = io.ReadAll(rawPOST(t, h.url, tokenAda, sid, initBody).Body)
	}
	if cs, err := h.dial(tokenGrace); err == nil {
		t.Cleanup(func() { _ = cs.Close() })
		t.Fatal("a second session past MaxSessions 1 was admitted after duplicate initializes")
	}
	del, err := http.NewRequest(http.MethodDelete, h.url, nil)
	if err != nil {
		t.Fatal(err)
	}
	del.Header.Set("Authorization", "Bearer "+tokenAda)
	del.Header.Set("Mcp-Session-Id", sid)
	del.Header.Set("Mcp-Protocol-Version", "2025-11-25")
	resp, err := http.DefaultClient.Do(del)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	waitFor(t, func() bool { return !agentLive(h, "u-ada", "harness") })
	if cs, err := h.dial(tokenGrace); err != nil {
		t.Errorf("after the first session ended: %v", err)
	} else {
		_ = cs.Close()
	}
}

// parked registers a tool that signals entered and then waits for release, or for its context
// when honour is set.
func parked(h *harness, honour bool) (entered, release, cancelled chan struct{}) {
	entered, release, cancelled = make(chan struct{}), make(chan struct{}), make(chan struct{})
	AddTool(h.srv, &mcp.Tool{Name: "park"}, nil, func(ctx context.Context, _ *mcp.CallToolRequest, _ plantIn) (*mcp.CallToolResult, plantOut, error) {
		close(entered)
		done := ctx.Done()
		if !honour {
			done = nil
		}
		select {
		case <-done:
			close(cancelled)
		case <-release:
		}
		return nil, plantOut{}, nil
	})
	return entered, release, cancelled
}

// Close returns ctx's error at its deadline while a handler that ignores its context is still
// running: a session's end waits for its handlers, Close does not.
func TestCloseBoundedByAParkedHandler(t *testing.T) {
	h := bootHarness(t, servicefake.New().Services(), resolver())
	entered, release, _ := parked(h, false)
	cs := h.connect(t, tokenAda)
	t.Cleanup(func() { close(release) }) // first, so the client's DELETE and the listener can end
	go func() {
		_, _ = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "park", Arguments: map[string]any{"projectId": "P-1"}})
	}()
	<-entered
	c, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- h.srv.Close(c) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Close = %v after %s, want context.DeadlineExceeded", err, time.Since(start))
		}
		if took := time.Since(start); took > 1500*time.Millisecond {
			t.Errorf("Close took %s with a 500ms ctx", took)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close(500ms ctx) still blocked after 3s behind a parked handler")
	}
}

// At its deadline, not before, Close cancels the context of every handler still running, so one
// that honours it returns rather than outliving the stop; until then it may finish.
func TestCloseCancelsHandlersAtItsDeadline(t *testing.T) {
	h := bootHarness(t, servicefake.New().Services(), resolver())
	entered, release, cancelled := parked(h, true)
	cs := h.connect(t, tokenAda)
	t.Cleanup(func() {
		select {
		case <-cancelled:
		default:
			close(release)
		}
	})
	go func() {
		_, _ = cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "park", Arguments: map[string]any{"projectId": "P-1"}})
	}()
	<-entered
	c, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.srv.Close(c) }()
	select {
	case <-cancelled:
		t.Fatal("the handler was cancelled before Close's deadline")
	case <-time.After(200 * time.Millisecond):
	}
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Close = %v, want context.DeadlineExceeded", err)
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("the parked handler's context was not cancelled at Close's deadline")
	}
}
