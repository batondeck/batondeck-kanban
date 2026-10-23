package mcp_test

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/auth/authfake"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	bk "github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/mcp/tools"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicefake"
	"github.com/batondeck/batondeck-kanban/internal/store/memstore"
)

// TestSearchTasksTool drives search_tasks through a real client against servicefake: every input
// field reaches Media.SearchTasks as the request and the caller as the resolver's, its response
// comes back as structured content; an unknown key and each input the served schema refuses are
// VALIDATION with the service never called (k's and query's bounds are the service's, below); each
// error the service declares comes back in the contract shape.
func TestSearchTasksTool(t *testing.T) {
	f := servicefake.New()
	h := bootTools(t, f.Services())
	ada, admin := h.Connect(t, bearerAda), h.Connect(t, bearerAdmin)
	args := map[string]any{"projectId": "P-1", "query": "login sso", "boardId": "B-1", "k": 5, "mode": "keyword"}
	resp := service.SearchTasksResp{Mode: "keyword", Results: []service.SearchResult{{Score: 1.25, Task: sampleTask()}}}
	for _, b := range []struct {
		name   string
		cs     *sdk.ClientSession
		bearer string
	}{{"happy", ada, bearerAda}, {"happy_admin", admin, bearerAdmin}} {
		t.Run(b.name, func(t *testing.T) {
			f.Reset()
			f.Set("Media.SearchTasks", resp, nil)
			res, err := b.cs.CallTool(testCtx(t), &sdk.CallToolParams{Name: "search_tasks", Arguments: args})
			if err != nil || res.IsError {
				t.Fatalf("search_tasks: %+v, %v", res, err)
			}
			got := callsOf(f, "Media.SearchTasks")
			want := service.SearchTasksReq{ProjectID: "P-1", Query: "login sso", BoardID: "B-1", K: 5, Mode: "keyword"}
			if len(got) != 1 || !reflect.DeepEqual(got[0].Req, want) {
				t.Fatalf("search_tasks reached the service as %+v, want %#v", got, want)
			}
			if c := toolsResolver().Callers[b.bearer]; got[0].Caller != c {
				t.Errorf("caller %+v, want the resolver's %+v", got[0].Caller, c)
			}
			if w := asJSON(t, resp); !reflect.DeepEqual(res.StructuredContent, w) {
				t.Errorf("answered %v, want the service's %v", res.StructuredContent, w)
			}
		})
	}
	for _, c := range []struct {
		name string
		args map[string]any
	}{
		{"unknown_key", with(args, "cursor", "c-1")},
		{"mode_outside_enum", with(args, "mode", "fuzzy")},
		{"k_not_integer", with(args, "k", 2.5)},
	} {
		t.Run("VALIDATION_"+c.name, func(t *testing.T) {
			f.Reset()
			if e := bk.ToolError(t, ada, "search_tasks", c.args); e["code"] != "VALIDATION" {
				t.Errorf("%s = %v, want VALIDATION", c.name, e)
			}
			if n := len(callsOf(f, "Media.SearchTasks")); n != 0 {
				t.Errorf("%s called the service %d times, want 0", c.name, n)
			}
		})
	}
	for _, code := range []domain.Code{domain.CodeValidation, domain.CodeForbidden, domain.CodeInternal} {
		t.Run(string(code), func(t *testing.T) {
			f.Reset()
			e := bk.ToolError(t, ada, "search_tasks", with(args, "projectId", servicefake.Trigger(code)))
			if e["code"] != string(code) || e["retryable"] != domain.NewError(code, "", nil).Retryable {
				t.Errorf("search_tasks = %v, want %s", e, code)
			}
			if n := len(callsOf(f, "Media.SearchTasks")); n != 1 {
				t.Errorf("called the service %d times, want 1", n)
			}
		})
	}
	listed, err := ada.ListTools(testCtx(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range listed.Tools {
		if tl.Name == "search_tasks" {
			n := utf8.RuneCountInString(tl.Description)
			if n > 200 {
				t.Errorf("search_tasks description is %d characters, over 200", n)
			}
			t.Logf("search_tasks description: %d characters", n)
		}
	}
}

// TestSearchTasksEndToEnd: search_tasks through a real client, the real service and memstore (the
// engines are judged in internal/service, which a transport test may not import: ADR-0003 row 6).
// A project's search finds its task and another project's never does; update_task and delete_task
// move the index; the four hostile inputs come back VALIDATION or a result, never INTERNAL.
func TestSearchTasksEndToEnd(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	core := service.New(st, service.Options{ToolCallFlush: -1})
	t.Cleanup(func() { _ = core.Close(context.Background()) })
	svc := service.Services{Projects: core.Projects(), Boards: core.Boards(), Tasks: core.Tasks(), Lifecycle: core.Lifecycle(),
		Context: core.Context(), Media: core.Media(), Feed: core.Feed(), Sessions: core.Sessions(), ToolCalls: core.ToolCalls()}
	carol := domain.Caller{Identity: "carol", TokenScope: "carol"}
	h := bk.BootHarness(t, svc, authfake.Resolver{Callers: map[string]domain.Caller{"bk_test_carol": carol}})
	tools.Register(h.Server(), svc)
	cs := h.Connect(t, "bk_test_carol")
	call := func(tool string, args map[string]any) map[string]any {
		t.Helper()
		res, err := cs.CallTool(testCtx(t), &sdk.CallToolParams{Name: tool, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s %v: %+v, %v", tool, args, res, err)
		}
		return res.StructuredContent.(map[string]any)
	}
	project := func(name, title string) string {
		p := call("create_project", map[string]any{"name": name})["projectId"].(string)
		b := call("create_board", map[string]any{"projectId": p, "name": "Main"})["board"].(map[string]any)["id"].(string)
		call("create_task", map[string]any{"projectId": p, "boardId": b, "title": title, "description": "shared words here"})
		return p
	}
	a := project("A", "Rotate the frobnicator")
	b := project("B", "Water the plants")
	search := func(p, q string) []string {
		t.Helper()
		out := call("search_tasks", map[string]any{"projectId": p, "query": q})
		if out["mode"] != "keyword" {
			t.Errorf("mode %v, want keyword", out["mode"])
		}
		ids := []string{}
		for _, r := range out["results"].([]any) {
			task := r.(map[string]any)["task"].(map[string]any)
			if task["projectId"] != p {
				t.Errorf("search in %s returned %v of %v", p, task["id"], task["projectId"])
			}
			ids = append(ids, task["id"].(string))
		}
		return ids
	}
	want := func(p, q string, ids ...string) {
		t.Helper()
		if ids == nil {
			ids = []string{}
		}
		if got := search(p, q); !slices.Equal(got, ids) {
			t.Errorf("search %s %q = %v, want %v", p, q, got, ids)
		}
	}
	want(a, "frobnicator", "T-1") // AC4 control
	want(b, "frobnicator")        // AC4 refusal
	want(b, "shared words", "T-1")
	want(a, "frob*", "T-1")

	task := call("get_task", map[string]any{"projectId": a, "taskId": "T-1"})["task"].(map[string]any)
	up := call("update_task", map[string]any{"projectId": a, "taskId": "T-1", "version": task["version"], "patch": map[string]any{"title": "Polish the widget"}})
	want(a, "frobnicator")
	want(a, "widget", "T-1")
	call("delete_task", map[string]any{"projectId": a, "taskId": "T-1", "version": up["task"].(map[string]any)["version"]})
	want(a, "widget")

	for _, q := range []string{`"`, `*`, `OR`, strings.Repeat("ab ", 166) + "ab"} {
		res, err := cs.CallTool(testCtx(t), &sdk.CallToolParams{Name: "search_tasks", Arguments: map[string]any{"projectId": b, "query": q}})
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		if res.IsError {
			if e := bk.ToolError(t, cs, "search_tasks", map[string]any{"projectId": b, "query": q}); e["code"] != "VALIDATION" {
				t.Errorf("search %q = %v, want VALIDATION or a result", q, e)
			}
		}
		t.Logf("search %.12q (%d chars): isError %v, %v", q, len(q), res.IsError, res.StructuredContent)
	}
	e := bk.ToolError(t, cs, "search_tasks", map[string]any{"projectId": a, "query": "plants", "mode": "semantic"})
	if e["code"] != "VALIDATION" || !strings.Contains(e["message"].(string), "keyword only") {
		t.Errorf("mode semantic = %v, want VALIDATION naming the keyword-only decision", e)
	}
	// The contract's bounds, which the served schema does not carry, are the service's.
	for name, args := range map[string]map[string]any{
		"k_above_50":       {"projectId": a, "query": "plants", "k": 51},
		"empty_query":      {"projectId": a, "query": ""},
		"query_above_1000": {"projectId": a, "query": strings.Repeat("a", 1001)},
	} {
		if e := bk.ToolError(t, cs, "search_tasks", args); e["code"] != "VALIDATION" {
			t.Errorf("%s = %v, want VALIDATION", name, e)
		}
	}
}
