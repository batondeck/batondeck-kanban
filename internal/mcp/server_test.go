package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/auth/authfake"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicefake"
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

func TestInitializeAnswersEmptyLists(t *testing.T) {
	f := servicefake.New()
	h := bootHarness(t, f.Services(), resolver())
	cs := h.connect(t, tokenAda)

	init := cs.InitializeResult()
	if init.ProtocolVersion != "2025-11-25" {
		t.Errorf("negotiated %q, want 2025-11-25 (a stateful server refuses 2026-07-28)", init.ProtocolVersion)
	}
	caps := init.Capabilities
	if caps.Tools == nil || !caps.Tools.ListChanged || caps.Prompts == nil || !caps.Prompts.ListChanged ||
		caps.Resources == nil || !caps.Resources.ListChanged || !caps.Resources.Subscribe {
		t.Errorf("capabilities = %+v, want tools, prompts and resources with listChanged and subscribe", caps)
	}
	if cs.ID() == "" {
		t.Error("no Mcp-Session-Id: the server is not stateful")
	}
	tools, err := cs.ListTools(ctx(t), nil)
	if err != nil || tools.Tools == nil || len(tools.Tools) != 0 {
		t.Errorf("tools/list = %+v, %v; want [] (not null)", tools, err)
	}
	prompts, err := cs.ListPrompts(ctx(t), nil)
	if err != nil || len(prompts.Prompts) != 0 {
		t.Errorf("prompts/list = %+v, %v; want []", prompts, err)
	}
	res, err := cs.ListResources(ctx(t), nil)
	if err != nil || len(res.Resources) != 0 {
		t.Errorf("resources/list = %+v, %v; want []", res, err)
	}
	tpl, err := cs.ListResourceTemplates(ctx(t), nil)
	if err != nil || len(tpl.ResourceTemplates) != 0 {
		t.Errorf("resources/templates/list = %+v, %v; want []", tpl, err)
	}
	if err := cs.Subscribe(ctx(t), &mcp.SubscribeParams{URI: "batondeck://board/B-1"}); err == nil {
		t.Error("resources/subscribe accepted a URI no resource serves; want it refused")
	}

	touches := calls(f, "Sessions.TouchAgentSession")
	if len(touches) == 0 {
		t.Fatal("initialize created no agent session")
	}
	first := touches[0]
	req := first.Req.(service.TouchAgentSessionReq)
	if first.Caller.Identity != "u-ada" || req.SessionKey != cs.ID() || req.ClientName != "harness" ||
		req.ClientVersion != "0.0.1" || req.AgentName != "harness" || req.Kind != "service_account" || req.Tool != "" {
		t.Errorf("first touch = %+v as %q, want session %s of client harness 0.0.1 as u-ada", req, first.Caller.Identity, cs.ID())
	}
}

func TestBearerResolvesOrIs401(t *testing.T) {
	h := bootHarness(t, servicefake.New().Services(), resolver())

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

	bad := toolError(t, cs, "stale", map[string]any{"projectId": 7})
	if bad["code"] != "VALIDATION" || bad["retryable"] != false {
		t.Errorf("bad arguments = %v, want VALIDATION", bad)
	}
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
	idle, active := h.connect(t, tokenAda), h.connect(t, tokenGrace)
	start := len(calls(f, "Sessions.TouchAgentSession"))

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := active.Ping(ctx(t), nil); err != nil {
			t.Fatalf("active ping: %v", err)
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

	removed := map[string]bool{}
	for _, c := range calls(f, "Sessions.RemoveAgentSession") {
		removed[c.Req.(service.RemoveAgentSessionReq).SessionKey] = true
	}
	if !removed[idle.ID()] || removed[active.ID()] {
		t.Errorf("removed %v, want the idle session %s only", removed, idle.ID())
	}
	refreshed := 0
	for _, c := range calls(f, "Sessions.TouchAgentSession")[start:] {
		if c.Req.(service.TouchAgentSessionReq).SessionKey == active.ID() {
			refreshed++
		}
	}
	if refreshed < 5 {
		t.Errorf("the active session was touched %d times in 3s of pings, want each request", refreshed)
	}
}

func TestSessionCountBounded(t *testing.T) {
	f := servicefake.New()
	h := bootHarness(t, f.Services(), resolver(), func(o *Options) { o.MaxSessions = 1 })
	first, err := h.dial(tokenAda)
	if err != nil {
		t.Fatal(err)
	}
	if cs, err := h.dial(tokenAda); err == nil {
		_ = cs.Close()
		t.Fatal("a second session past MaxSessions 1 was admitted")
	}
	_ = first.Close() // DELETE: the session ends and frees its slot
	waitFor(t, func() bool { return len(calls(f, "Sessions.RemoveAgentSession")) == 1 })
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
	cs := h.connect(t, tokenAda)
	if _, err := cs.ListTools(ctx(t), nil); err != nil {
		t.Errorf("tools/list failed with the session touch failing: %v", err)
	}
	if !strings.Contains(h.log.String(), "agent session") {
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
