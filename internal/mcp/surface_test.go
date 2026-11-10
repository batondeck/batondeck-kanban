package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicefake"
)

// TestInitializeAdvertisesNoSubscription: on the wire, initialize's resources capability is exactly
// {"listChanged":true}, and a go-sdk client's resources/subscribe on a URI a template serves is
// refused as method not found (ADR-0004: resources are readable, never pushed).
func TestInitializeAdvertisesNoSubscription(t *testing.T) {
	h := bootHarness(t, servicefake.New().Services(), resolver())
	resp := rawPOST(t, h.url, tokenAda, "", initBody)
	body, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("initialize: %d %v", resp.StatusCode, err)
	}
	line := string(body)
	if i := strings.Index(line, "data: "); i >= 0 {
		line = strings.TrimSpace(line[i+len("data: "):])
	}
	var init struct {
		Result struct {
			Capabilities map[string]json.RawMessage `json:"capabilities"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(line), &init); err != nil {
		t.Fatalf("initialize body %q: %v", body, err)
	}
	if got := string(init.Result.Capabilities["resources"]); got != `{"listChanged":true}` {
		t.Errorf("capabilities.resources = %s, want {\"listChanged\":true}", got)
	}
	t.Logf("initialize capabilities.resources on the wire: %s", init.Result.Capabilities["resources"])

	cs := h.connect(t, tokenAda)
	err = cs.Subscribe(ctx(t), &mcp.SubscribeParams{URI: "batondeck://P-1/board/B-1"})
	var werr *jsonrpc.Error
	if !errors.As(err, &werr) || werr.Code != jsonrpc.CodeMethodNotFound {
		t.Fatalf("resources/subscribe = %v, want JSON-RPC %d (method not found)", err, jsonrpc.CodeMethodNotFound)
	}
	t.Logf("resources/subscribe batondeck://P-1/board/B-1: %v", err)
}

// wakeForbidden is what the instructions and prompts may not say: the dropped wakes, the six- and
// eight-state names, the Stop hook.
var wakeForbidden = regexp.MustCompile(`Pub/Sub|/wake|channel|resources/subscribe|READY|BACKLOG|CANCELLED|DEAD_LETTER|Stop hook`)

// TestInstructionsNameBothWakes: initialize carries the instructions, which name the socket
// doorbell and its two calls for a device CLI, the board taken from the doorbell (P-191), and "pull
// each turn" for a hosted client, and none of the dropped wakes or retired states.
func TestInstructionsNameBothWakes(t *testing.T) {
	h := bootHarness(t, servicefake.New().Services(), resolver())
	got := h.connect(t, tokenAda).InitializeResult().Instructions
	if got != Instructions {
		t.Fatalf("initialize instructions = %q, want Instructions", got)
	}
	for _, want := range []string{"socket doorbell", "take projectId and boardId from the doorbell and call claim_next { projectId, boardId }", "then list_notifications", "reads the doorbell's board with next_task { projectId, boardId, includeInbox: true }", "pull each turn", "calling list_notifications, then claim_next { projectId, boardId } (no assignee needed)", "never another's", "TODO, IN_PROGRESS, BLOCKED, REVIEW, DONE", "get_task_context"} {
		if !strings.Contains(got, want) {
			t.Errorf("instructions lack %q", want)
		}
	}
	if m := wakeForbidden.FindAllString(got, -1); len(m) > 0 {
		t.Errorf("instructions say %q", m)
	}
}

// snakeName is a snake_case word: in a prompt, a tool's name.
var snakeName = regexp.MustCompile(`\b[a-z]+(?:_[a-z]+)+\b`)

// TestPromptsRender: prompts/list is the four prompts with their two required arguments; each
// renders one user message carrying its arguments, naming only tools of contract/expected.json and
// none of the dropped wakes or retired states; a missing argument is invalid params.
func TestPromptsRender(t *testing.T) {
	var contract struct {
		Tools map[string]json.RawMessage `json:"tools"`
	}
	readJSON(t, filepath.Join("..", "..", "contract", "expected.json"), &contract)
	h := bootHarness(t, servicefake.New().Services(), resolver())
	cs := h.connect(t, tokenAda)
	listed, err := cs.ListPrompts(ctx(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range listed.Prompts {
		names = append(names, p.Name)
		if len(p.Arguments) != 2 || !p.Arguments[0].Required || !p.Arguments[1].Required {
			t.Errorf("%s arguments = %+v, want two required", p.Name, p.Arguments)
			continue
		}
		args := map[string]string{p.Arguments[0].Name: "P-7", p.Arguments[1].Name: "X-9"}
		got, err := cs.GetPrompt(ctx(t), &mcp.GetPromptParams{Name: p.Name, Arguments: args})
		if err != nil {
			t.Errorf("prompts/get %s: %v", p.Name, err)
			continue
		}
		if len(got.Messages) != 1 || got.Messages[0].Role != "user" {
			t.Errorf("%s messages = %+v, want one user message", p.Name, got.Messages)
			continue
		}
		text := got.Messages[0].Content.(*mcp.TextContent).Text
		if !strings.Contains(text, "P-7") || !strings.Contains(text, "X-9") {
			t.Errorf("%s does not carry its arguments: %q", p.Name, text)
		}
		if m := wakeForbidden.FindAllString(text, -1); len(m) > 0 {
			t.Errorf("%s says %q", p.Name, m)
		}
		for _, tool := range snakeName.FindAllString(text, -1) {
			if _, ok := contract.Tools[tool]; !ok {
				t.Errorf("%s names %s, which is not a tool of contract/expected.json", p.Name, tool)
			}
		}
		delete(args, p.Arguments[1].Name)
		if _, err := cs.GetPrompt(ctx(t), &mcp.GetPromptParams{Name: p.Name, Arguments: args}); err == nil {
			t.Errorf("prompts/get %s without %s rendered; want invalid params", p.Name, p.Arguments[1].Name)
		}
	}
	slices.Sort(names)
	if want := []string{"decompose_into_subtasks", "pick_up_next_task", "summarize_for_handoff", "triage_inbox"}; !slices.Equal(names, want) {
		t.Errorf("prompts/list = %v, want %v", names, want)
	}
}

// The resource templates conductor serves (contract/tools.json) that this server does not, and the
// ones it adds, each with its reason. Everything else is equal by name and URI template.
var (
	templatesDropped = map[string]string{}
	templatesAdded   = map[string]string{
		"board-feed-cursor": "batondeck://{projectId}/board/{boardId}/feed{?cursor}", // plan §Target surface: board-feed + -cursor
	}
)

// TestPromptsAndTemplatesMatchContract: the served prompts (names and arguments) equal
// contract/tools.json's exactly, and the served resource templates equal its list less
// templatesDropped plus templatesAdded, by name and URI template.
func TestPromptsAndTemplatesMatchContract(t *testing.T) {
	type arg struct {
		Name     string `json:"name"`
		Required bool   `json:"required"`
	}
	var conductor struct {
		Prompts []struct {
			Name      string `json:"name"`
			Arguments []arg  `json:"arguments"`
		} `json:"prompts"`
		ResourceTemplates []struct {
			Name        string `json:"name"`
			URITemplate string `json:"uriTemplate"`
		} `json:"resourceTemplates"`
	}
	readJSON(t, filepath.Join("..", "..", "contract", "tools.json"), &conductor)
	if len(conductor.Prompts) != 4 || len(conductor.ResourceTemplates) != 10 {
		t.Fatalf("contract/tools.json has %d prompts and %d templates, want 4 and 10", len(conductor.Prompts), len(conductor.ResourceTemplates))
	}
	h := bootHarness(t, servicefake.New().Services(), resolver())
	cs := h.connect(t, tokenAda)

	wantPrompts, gotPrompts := map[string][]arg{}, map[string][]arg{}
	for _, p := range conductor.Prompts {
		wantPrompts[p.Name] = p.Arguments
	}
	listed, err := cs.ListPrompts(ctx(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range listed.Prompts {
		for _, a := range p.Arguments {
			gotPrompts[p.Name] = append(gotPrompts[p.Name], arg{a.Name, a.Required})
		}
	}
	if !maps.EqualFunc(wantPrompts, gotPrompts, slices.Equal) {
		t.Errorf("prompts = %v, want contract/tools.json's %v", gotPrompts, wantPrompts)
	}

	want := maps.Clone(templatesAdded)
	for _, r := range conductor.ResourceTemplates {
		if _, drop := templatesDropped[r.Name]; !drop {
			want[r.Name] = r.URITemplate
		}
	}
	for name := range templatesDropped {
		if !slices.ContainsFunc(conductor.ResourceTemplates, func(r struct {
			Name        string `json:"name"`
			URITemplate string `json:"uriTemplate"`
		}) bool {
			return r.Name == name
		}) {
			t.Errorf("templatesDropped names %s, which contract/tools.json does not have", name)
		}
	}
	got := map[string]string{}
	tpl, err := cs.ListResourceTemplates(ctx(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range tpl.ResourceTemplates {
		got[r.Name] = r.URITemplate
		if r.MIMEType != "application/json" || r.Title == "" || r.Description == "" {
			t.Errorf("template %s = %+v, want a title, a description and application/json", r.Name, r)
		}
	}
	if !maps.Equal(want, got) {
		t.Errorf("resource templates = %v, want %v", got, want)
	}
	t.Logf("prompts %d equal contract/tools.json; templates %d = %d of contract/tools.json - %d dropped + %d added", len(gotPrompts), len(got), len(conductor.ResourceTemplates), len(templatesDropped), len(templatesAdded))
}

// TestResourcesRead: each template's URI reads through its one service method as the caller, with
// the ids (and the cursor) from the URI, and returns that method's response as JSON; a URI of
// another scheme or shape is not found, and a service error reaches the client with its code.
func TestResourcesRead(t *testing.T) {
	// Each response is the fake's zero one (every required slice []) with a few fields set, so the
	// content read back is recognisably the configured one.
	base, c0 := servicefake.New().Services(), context.Background()
	snapshot, _ := base.Feed.BoardSnapshot(c0, domain.Caller{}, service.BoardSnapshotReq{})
	snapshot.Board.ID, snapshot.Board.ProjectID, snapshot.TaskCounts, snapshot.Cursor = "B-1", "P-1", map[string]float64{"c-todo": 3}, 42
	task, _ := base.Tasks.Get(c0, domain.Caller{}, service.GetTaskReq{})
	task.Task.ID, task.Task.ProjectID, task.Task.Title = "T-5", "P-1", "Write it"
	briefing, _ := base.Context.GetTaskContext(c0, domain.Caller{}, service.GetTaskContextReq{})
	briefing.Summary = "half done"
	feed := service.BoardFeedResp{Events: []service.BoardEvent{{ID: "E-1", ProjectID: "P-1", BoardID: "B-1", Type: "task.created", Actor: "u-ada", Data: map[string]any{}, Ts: "2026-10-23T00:00:00.000Z"}}, NextCursor: "c2"}
	f := servicefake.New()
	f.Set("Feed.BoardSnapshot", snapshot, nil)
	f.Set("Tasks.Get", task, nil)
	f.Set("Context.GetTaskContext", briefing, nil)
	f.Set("Feed.BoardFeed", feed, nil)
	h := bootHarness(t, f.Services(), resolver())
	cs := h.connect(t, tokenAda)

	cases := []struct {
		uri, method string
		req, resp   any
	}{
		{"batondeck://P-1/board/B-1", "Feed.BoardSnapshot", service.BoardSnapshotReq{ProjectID: "P-1", BoardID: "B-1"}, snapshot},
		{"batondeck://P-1/task/T-5", "Tasks.Get", service.GetTaskReq{ProjectID: "P-1", TaskID: "T-5"}, task},
		{"batondeck://P-1/task/T-5/context", "Context.GetTaskContext", service.GetTaskContextReq{ProjectID: "P-1", TaskID: "T-5"}, briefing},
		{"batondeck://P-1/board/B-1/feed", "Feed.BoardFeed", service.BoardFeedReq{ProjectID: "P-1", BoardID: "B-1"}, feed},
		{"batondeck://P-1/board/B-1/feed?cursor=c%2F1", "Feed.BoardFeed", service.BoardFeedReq{ProjectID: "P-1", BoardID: "B-1", Cursor: "c/1"}, feed},
	}
	for _, c := range cases {
		before := len(f.Calls())
		res, err := cs.ReadResource(ctx(t), &mcp.ReadResourceParams{URI: c.uri})
		if err != nil {
			t.Errorf("read %s: %v", c.uri, err)
			continue
		}
		calls := f.Calls()[before:]
		if len(calls) != 1 || calls[0].Method != c.method || calls[0].Caller.Identity != "u-ada" || !reflect.DeepEqual(calls[0].Req, c.req) {
			t.Errorf("read %s called %+v, want one %s(%+v) as u-ada", c.uri, calls, c.method, c.req)
		}
		want, _ := json.Marshal(c.resp)
		if len(res.Contents) != 1 || res.Contents[0].URI != c.uri || res.Contents[0].MIMEType != "application/json" || res.Contents[0].Text != string(want) {
			t.Errorf("read %s = %+v, want %s", c.uri, res.Contents, want)
		}
		if res.CacheScope != "private" {
			t.Errorf("read %s cacheScope %q, want private: the content is one member's view of a project", c.uri, res.CacheScope)
		}
		t.Logf("read %s -> %s %s", c.uri, c.method, res.Contents[0].Text)
	}

	for _, uri := range []string{"conductor://P-1/board/B-1", "batondeck://P-1/board", "batondeck://P-1/board/B-1/feed/x", "batondeck://P-1/tasks/T-5", "batondeck:///board/B-1"} {
		before := len(f.Calls())
		if _, err := cs.ReadResource(ctx(t), &mcp.ReadResourceParams{URI: uri}); err == nil {
			t.Errorf("read %s succeeded; want resource not found", uri)
		}
		if n := len(f.Calls()) - before; n != 0 {
			t.Errorf("read %s made %d service calls; want none", uri, n)
		}
	}

	// The JSON-RPC code is pinned: NOT_FOUND is MCP's resource-not-found (-32602), FORBIDDEN internal.
	for code, rpc := range map[domain.Code]int64{domain.CodeForbidden: jsonrpc.CodeInternalError, domain.CodeNotFound: jsonrpc.CodeInvalidParams} {
		uri := "batondeck://P-1/task/" + strings.ReplaceAll(servicefake.Trigger(code), ":", "%3A")
		_, err := cs.ReadResource(ctx(t), &mcp.ReadResourceParams{URI: uri})
		var werr *jsonrpc.Error
		if !errors.As(err, &werr) {
			t.Errorf("read %s = %v, want a JSON-RPC error", uri, err)
			continue
		}
		if werr.Code != rpc {
			t.Errorf("read %s = JSON-RPC %d, want %d", uri, werr.Code, rpc)
		}
		var data domain.BatonDeckError
		if json.Unmarshal(werr.Data, &data) != nil || data.Code != code {
			t.Errorf("read %s error data = %s, want code %s", uri, werr.Data, code)
		}
		t.Logf("read %s -> JSON-RPC %d %s", uri, werr.Code, werr.Data)
	}
}

// flipResolver is a resolver whose callers a test may change while the server reads them.
type flipResolver struct {
	mu      sync.Mutex
	callers map[string]domain.Caller
}

func (r *flipResolver) ResolveCaller(_ context.Context, bearer string) (domain.Caller, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.callers[bearer]; ok {
		return c, nil
	}
	return domain.Caller{}, domain.NewError(domain.CodeUnauthenticated, "invalid, expired or revoked credentials", nil)
}

func (r *flipResolver) setAdmin(bearer string, admin bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.callers[bearer]
	c.WorkspaceAdmin = admin
	r.callers[bearer] = c
}

// dialNotified initialises a session as bearer whose tools/list_changed notifications count on n.
func dialNotified(t *testing.T, h *harness, bearer string, n chan<- string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "harness", Version: "0.0.1"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) { n <- bearer },
	})
	cs, err := client.Connect(ctx(t), &mcp.StreamableClientTransport{Endpoint: h.url, HTTPClient: &http.Client{Transport: bearerTransport{bearer: bearer}}, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// TestRoleChangeSendsToolsListChanged: a session whose caller becomes a workspace admin is sent
// notifications/tools/list_changed at its next request, and its next tools/list gains the admin-only
// tool; a session whose caller did not change is sent nothing. Demoted again, it is told again and
// the tool goes.
func TestRoleChangeSendsToolsListChanged(t *testing.T) {
	res := &flipResolver{callers: map[string]domain.Caller{
		tokenAda:   {Identity: "u-ada", TokenScope: "u-ada"},
		tokenGrace: {Identity: "u-grace", TokenScope: "u-grace"},
	}}
	h := bootHarness(t, servicefake.New().Services(), res)
	AddTool(h.srv, &mcp.Tool{Name: "list_audit_log", InputSchema: map[string]any{"type": "object"}}, func(admin bool) bool { return admin },
		func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{}, nil, nil
		})
	notes := make(chan string, 8)
	ada, grace := dialNotified(t, h, tokenAda, notes), dialNotified(t, h, tokenGrace, notes)
	listsAudit := func(cs *mcp.ClientSession) bool {
		r, err := cs.ListTools(ctx(t), nil)
		if err != nil {
			t.Fatal(err)
		}
		return slices.ContainsFunc(r.Tools, func(tl *mcp.Tool) bool { return tl.Name == "list_audit_log" })
	}
	quiet := func(why string) {
		select {
		case b := <-notes:
			t.Fatalf("%s: %s was sent tools/list_changed", why, b)
		case <-time.After(300 * time.Millisecond):
		}
	}
	if listsAudit(ada) || listsAudit(grace) {
		t.Fatal("list_audit_log listed to a non-admin")
	}
	for _, cs := range []*mcp.ClientSession{ada, grace} {
		if err := cs.Ping(ctx(t), nil); err != nil {
			t.Fatal(err)
		}
	}
	quiet("no caller changed")

	for _, admin := range []bool{true, false} {
		res.setAdmin(tokenAda, admin)
		if err := grace.Ping(ctx(t), nil); err != nil {
			t.Fatal(err)
		}
		quiet("only u-ada changed, and only u-grace has made a request")
		if err := ada.Ping(ctx(t), nil); err != nil {
			t.Fatal(err)
		}
		select {
		case b := <-notes:
			if b != tokenAda {
				t.Fatalf("%s was sent tools/list_changed; want u-ada's session", b)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("u-ada made workspace admin %v: no tools/list_changed within 5 s", admin)
		}
		quiet("one change, one notification")
		if got := listsAudit(ada); got != admin {
			t.Errorf("u-ada admin %v: list_audit_log listed %v", admin, got)
		}
		if listsAudit(grace) {
			t.Error("list_audit_log listed to u-grace")
		}
		t.Logf("u-ada admin=%v: tools/list_changed sent to u-ada's session only; list_audit_log listed %v", admin, admin)
	}
}

// TestEndedSessionsForgetTheirAudience: a session's audience entry goes when the session ends, so
// ended sessions leave nothing behind in the tools/list_changed bookkeeping.
func TestEndedSessionsForgetTheirAudience(t *testing.T) {
	h := bootHarness(t, servicefake.New().Services(), resolver())
	seen := func() int {
		h.srv.tools.mu.Lock()
		defer h.srv.tools.mu.Unlock()
		return len(h.srv.tools.seen)
	}
	var live []*mcp.ClientSession
	for range 3 {
		cs, err := h.dial(tokenAda)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cs.Close() })
		if err := cs.Ping(ctx(t), nil); err != nil {
			t.Fatal(err)
		}
		live = append(live, cs)
	}
	if n := seen(); n != 3 {
		t.Fatalf("%d audiences noted for 3 live sessions, want 3", n)
	}
	for _, cs := range live {
		if err := cs.Close(); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for seen() != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := seen(); n != 0 {
		t.Fatalf("%d audiences left 5 s after all 3 sessions ended, want 0", n)
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}
