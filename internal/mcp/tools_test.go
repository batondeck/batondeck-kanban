package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/auth/authfake"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	bk "github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/mcp/tools"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicefake"
)

const (
	bearerAda   = "bk_test_ada"
	bearerAdmin = "bk_test_admin"
)

// triggerCodes are the service errors every tool is driven to: the ones a projects or boards
// service call declares (FORBIDDEN, NOT_FOUND) and STALE, whose retryable must survive the mapping.
var triggerCodes = []domain.Code{domain.CodeForbidden, domain.CodeNotFound, domain.CodeStale}

// bearerFor is the bearer whose caller's Identity is code's servicefake trigger, for a request
// with no string field to carry one (list_projects).
func bearerFor(code domain.Code) string { return "bk_test_trigger_" + string(code) }

func toolsResolver() authfake.Resolver {
	r := authfake.Resolver{Callers: map[string]domain.Caller{
		// Every Caller field is set on one bearer or the other, so a transport dropping any fails.
		bearerAda:   {Identity: "u-ada", Agent: "agent-ada", Role: string(domain.RoleWorker), TokenScope: "u-ada"},
		bearerAdmin: {Identity: "u-admin", TokenScope: "u-admin", WorkspaceAdmin: true},
	}}
	for _, c := range triggerCodes {
		r.Callers[bearerFor(c)] = domain.Caller{Identity: servicefake.Trigger(c), TokenScope: "u-trigger"}
	}
	return r
}

// bootTools is the harness with every registered area's tools over svc.
func bootTools(t *testing.T, svc service.Services) *bk.Harness {
	t.Helper()
	h := bk.BootHarness(t, svc, toolsResolver())
	tools.Register(h.Server(), svc)
	return h
}

func testCtx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return c
}

func callsOf(f *servicefake.Fake, method string) []servicefake.Call {
	var out []servicefake.Call
	for _, c := range f.Calls() {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

// asJSON is v as decoded JSON, for comparing a response with a tool's structured content.
func asJSON(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func with(args map[string]any, k string, v any) map[string]any {
	out := maps.Clone(args)
	out[k] = v
	return out
}

func fixedTable() map[string][]domain.Status {
	out := map[string][]domain.Status{}
	for _, s := range domain.AllStatuses() {
		out[string(s)] = domain.AllowedNext(s)
	}
	return out
}

func sampleBoard() service.Board {
	wip := 3.0
	return service.Board{
		ID: "B-1", Name: "Main", ProjectID: "P-1", Transitions: fixedTable(),
		Columns: []service.Column{
			{ID: "C-1", Name: "Ready", Order: 0, Status: domain.Todo, WIPLimit: &wip},
			{ID: "C-2", Name: "Done", Order: 1, Status: domain.Done},
		},
	}
}

// toolCase is one tool: valid arguments, the request the service must receive for them, the
// response the fake returns, and the argument a trigger value goes in ("" is the caller's Identity).
type toolCase struct {
	tool, method string
	args         map[string]any
	want         any
	resp         any
	trigger      string
}

func projectsBoardsCases() []toolCase {
	settings := service.Settings{QuotaEnabled: false, ReviewEnabled: true, AgingCeilingMs: 60000}
	one := int64(3)
	return []toolCase{
		{tool: "create_project", method: "Projects.Create", trigger: "name",
			args: map[string]any{"name": "Alpha", "idempotencyKey": "k-1"},
			want: service.CreateProjectReq{Name: "Alpha", IdempotencyKey: "k-1"},
			resp: service.CreateProjectResp{ProjectID: "P-1"}},
		{tool: "get_project", method: "Projects.Get", trigger: "projectId",
			args: map[string]any{"projectId": "P-1"},
			want: service.GetProjectReq{ProjectID: "P-1"},
			resp: service.GetProjectResp{
				MemberDisplay: map[string]string{"u-ada": "Ada"},
				Project: service.Project{ID: "P-1", Name: "Alpha", CreatedAt: "2026-10-23T00:00:00.000Z", CreatedBy: "u-ada",
					Members: map[string]domain.Role{"u-ada": domain.RoleAdmin}, Settings: settings},
			}},
		{tool: "list_projects", method: "Projects.List", trigger: "",
			args: map[string]any{},
			want: service.ListProjectsReq{},
			resp: service.ListProjectsResp{Projects: []service.ProjectListing{{ProjectID: "P-1", Name: "Alpha", Role: domain.RoleAdmin}}}},
		{tool: "update_settings", method: "Projects.UpdateSettings", trigger: "projectId",
			// false and null are values here, not absences: the tri-state fields through go-sdk's decoder.
			args: map[string]any{"projectId": "P-1", "reviewEnabled": false, "color": nil, "agingCeilingMs": 60000},
			want: service.UpdateSettingsReq{ProjectID: "P-1", ReviewEnabled: service.Some(false), Color: service.Null[string](), AgingCeilingMs: 60000},
			resp: service.UpdateSettingsResp{Settings: settings}},
		{tool: "delete_project", method: "Projects.Delete", trigger: "projectId",
			args: map[string]any{"projectId": "P-1"},
			want: service.DeleteProjectReq{ProjectID: "P-1"},
			resp: service.DeleteProjectResp{Ok: true}},
		{tool: "add_member", method: "Projects.AddMember", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "identityId": "u-grace", "role": "worker"},
			want: service.AddMemberReq{ProjectID: "P-1", IdentityID: "u-grace", Role: domain.RoleWorker},
			resp: service.AddMemberResp{Members: map[string]domain.Role{"u-ada": domain.RoleAdmin, "u-grace": domain.RoleWorker}}},
		{tool: "remove_member", method: "Projects.RemoveMember", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "identityId": "u-grace"},
			want: service.RemoveMemberReq{ProjectID: "P-1", IdentityID: "u-grace"},
			resp: service.RemoveMemberResp{Ok: true}},
		{tool: "create_board", method: "Boards.Create", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "name": "Main", "columns": []any{map[string]any{"name": "Ready", "status": "TODO", "wipLimit": 3}}},
			want: service.CreateBoardReq{ProjectID: "P-1", Name: "Main", Columns: []service.ColumnSpec{{Name: "Ready", Status: domain.Todo, WIPLimit: &one}}},
			resp: service.CreateBoardResp{Board: sampleBoard()}},
		{tool: "list_boards", method: "Boards.List", trigger: "projectId",
			args: map[string]any{"projectId": "P-1"},
			want: service.ListBoardsReq{ProjectID: "P-1"},
			resp: service.ListBoardsResp{Boards: []service.Board{sampleBoard()}}},
		{tool: "get_board", method: "Boards.Get", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "boardId": "B-1"},
			want: service.GetBoardReq{ProjectID: "P-1", BoardID: "B-1"},
			resp: service.GetBoardResp{Board: sampleBoard(), TaskCounts: map[string]float64{"C-1": 2, "C-2": 0}}},
		{tool: "add_column", method: "Boards.AddColumn", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "boardId": "B-1", "name": "QA", "status": "REVIEW", "order": 0},
			want: service.AddColumnReq{ProjectID: "P-1", BoardID: "B-1", Name: "QA", Status: domain.Review, Order: service.Some(0.0)},
			resp: service.AddColumnResp{Column: service.Column{ID: "C-3", Name: "QA", Order: 0, Status: domain.Review}}},
		{tool: "update_column", method: "Boards.UpdateColumn", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "boardId": "B-1", "columnId": "C-1", "wipLimit": nil},
			want: service.UpdateColumnReq{ProjectID: "P-1", BoardID: "B-1", ColumnID: "C-1", WIPLimit: service.Null[int64]()},
			resp: service.UpdateColumnResp{Board: sampleBoard()}},
		{tool: "remove_column", method: "Boards.RemoveColumn", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "boardId": "B-1", "columnId": "C-2"},
			want: service.RemoveColumnReq{ProjectID: "P-1", BoardID: "B-1", ColumnID: "C-2"},
			resp: service.RemoveColumnResp{Board: sampleBoard()}},
	}
}

// TestProjectsBoardsTools drives each of the 15 projects, boards and media tools through a real
// client against servicefake:
// the happy path for a non-admin and a workspace-admin bearer (the decoded request and the
// resolver's caller, field for field, reach the service; its response comes back as structured
// content), an unknown key (VALIDATION, the service never called) and each service error (the
// contract shape with its code and retryable).
func TestProjectsBoardsTools(t *testing.T) {
	f := servicefake.New()
	h := bootTools(t, f.Services())
	ada, admin := h.Connect(t, bearerAda), h.Connect(t, bearerAdmin)
	callers := map[*sdk.ClientSession]domain.Caller{ada: toolsResolver().Callers[bearerAda], admin: toolsResolver().Callers[bearerAdmin]}
	triggered := map[domain.Code]*sdk.ClientSession{}
	for _, c := range triggerCodes {
		triggered[c] = h.Connect(t, bearerFor(c))
	}
	cases := append(projectsBoardsCases(), mediaCases()...)
	if len(cases) != 15 {
		t.Fatalf("%d cases, want the 13 projects and boards tools and the 2 media tools", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			// Each bearer's caller reaches the service whole: the workspace-admin flag, the token's
			// role cap and the rest are the service's to read, so the transport may drop none of them.
			for _, b := range []struct {
				name string
				cs   *sdk.ClientSession
			}{{"happy", ada}, {"happy_admin", admin}} {
				t.Run(b.name, func(t *testing.T) {
					f.Reset()
					f.Set(tc.method, tc.resp, nil)
					res, err := b.cs.CallTool(testCtx(t), &sdk.CallToolParams{Name: tc.tool, Arguments: tc.args})
					if err != nil || res.IsError {
						t.Fatalf("%s: %+v, %v", tc.tool, res, err)
					}
					got := callsOf(f, tc.method)
					if len(got) != 1 {
						t.Fatalf("%s called %s %d times, want 1", tc.tool, tc.method, len(got))
					}
					if !reflect.DeepEqual(got[0].Req, tc.want) {
						t.Errorf("%s reached the service as %#v, want %#v", tc.tool, got[0].Req, tc.want)
					}
					if want := callers[b.cs]; got[0].Caller != want {
						t.Errorf("%s reached the service as caller %+v, want the resolver's %+v", tc.tool, got[0].Caller, want)
					}
					if want := asJSON(t, tc.resp); !reflect.DeepEqual(res.StructuredContent, want) {
						t.Errorf("%s answered %v, want the service's %v", tc.tool, res.StructuredContent, want)
					}
				})
			}
			t.Run("VALIDATION_unknown_key", func(t *testing.T) {
				f.Reset()
				e := bk.ToolError(t, ada, tc.tool, with(tc.args, "unexpectedKey", true))
				if e["code"] != "VALIDATION" {
					t.Errorf("%s with an unknown key = %v, want VALIDATION", tc.tool, e)
				}
				if n := len(callsOf(f, tc.method)); n != 0 {
					t.Errorf("%s with an unknown key called the service %d times, want 0", tc.tool, n)
				}
			})
			for _, code := range triggerCodes {
				t.Run(string(code), func(t *testing.T) {
					f.Reset()
					cs, args := ada, with(tc.args, tc.trigger, servicefake.Trigger(code))
					if tc.trigger == "" {
						cs, args = triggered[code], tc.args
					}
					e := bk.ToolError(t, cs, tc.tool, args)
					if e["code"] != string(code) {
						t.Errorf("%s = %v, want %s", tc.tool, e, code)
					}
					if n := len(callsOf(f, tc.method)); n != 1 {
						t.Errorf("%s called the service %d times, want 1 (the error is the service's)", tc.tool, n)
					}
				})
			}
		})
	}
}

// TestArgumentFailuresAreValidation: every way the arguments can fail before the service — a wrong
// type, a missing required property, a value outside an enum, a null sent to an Optional, a number
// the request's integer cannot hold — is VALIDATION, and the service is never called. A key the
// contract leaves open (create_board's columns[]) is not refused.
func TestArgumentFailuresAreValidation(t *testing.T) {
	f := servicefake.New()
	h := bootTools(t, f.Services())
	cs := h.Connect(t, bearerAda)
	for _, c := range []struct {
		name, tool, method string
		args               map[string]any
	}{
		{"wrong_type", "get_project", "Projects.Get", map[string]any{"projectId": 7}},
		{"missing_required", "create_board", "Boards.Create", map[string]any{"projectId": "P-1"}},
		{"outside_enum", "add_column", "Boards.AddColumn", map[string]any{"projectId": "P-1", "boardId": "B-1", "name": "X", "status": "CANCELLED"}},
		{"null_into_optional", "update_settings", "Projects.UpdateSettings", map[string]any{"projectId": "P-1", "quotaEnabled": nil}},
		{"integer_overflow", "update_settings", "Projects.UpdateSettings", map[string]any{"projectId": "P-1", "agingCeilingMs": 1e30}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f.Reset()
			e := bk.ToolError(t, cs, c.tool, c.args)
			if e["code"] != "VALIDATION" {
				t.Errorf("%s = %v, want VALIDATION", c.name, e)
			}
			if n := len(callsOf(f, c.method)); n != 0 {
				t.Errorf("%s called the service %d times, want 0", c.name, n)
			}
		})
	}
	t.Run("open_object_keeps_extras", func(t *testing.T) {
		f.Reset()
		args := map[string]any{"projectId": "P-1", "name": "Main", "columns": []any{map[string]any{"name": "Ready", "status": "TODO", "colour": "red"}}}
		res, err := cs.CallTool(testCtx(t), &sdk.CallToolParams{Name: "create_board", Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("create_board with an extra key in an open columns[] item: %+v, %v", res, err)
		}
		got := callsOf(f, "Boards.Create")
		want := service.CreateBoardReq{ProjectID: "P-1", Name: "Main", Columns: []service.ColumnSpec{{Name: "Ready", Status: domain.Todo}}}
		if len(got) != 1 || !reflect.DeepEqual(got[0].Req, want) {
			t.Errorf("create_board reached the service as %+v, want %+v", got, want)
		}
	})
}

// slowProjects delays Get, so the recorded latency is a measurement rather than a zero.
type slowProjects struct {
	service.Projects
	delay time.Duration
}

func (s slowProjects) Get(ctx context.Context, c domain.Caller, req service.GetProjectReq) (service.GetProjectResp, error) {
	time.Sleep(s.delay)
	return s.Projects.Get(ctx, c, req)
}

// TestEveryToolCallRecordsOneRow: each call of a registered tool writes one tool_calls row through
// service.ToolCalls.Record: outcome ok with code OK, else error with the code the client got,
// and the call's latency.
func TestEveryToolCallRecordsOneRow(t *testing.T) {
	f := servicefake.New()
	svc := f.Services()
	const delay = 30 * time.Millisecond
	svc.Projects = slowProjects{Projects: svc.Projects, delay: delay}
	h := bootTools(t, svc)
	cs := h.Connect(t, bearerAda)
	for _, args := range []map[string]any{
		{"projectId": "P-1"},
		{"projectId": servicefake.Trigger(domain.CodeForbidden)},
		{"projectId": "P-1", "unexpectedKey": 1},
	} {
		if _, err := cs.CallTool(testCtx(t), &sdk.CallToolParams{Name: "get_project", Arguments: args}); err != nil {
			t.Fatal(err)
		}
	}
	rows := callsOf(f, "ToolCalls.Record")
	if len(rows) != 3 {
		t.Fatalf("%d tool_calls rows for 3 calls", len(rows))
	}
	want := []struct {
		outcome, code string
		slow          bool
	}{{"ok", "OK", true}, {"error", "FORBIDDEN", true}, {"error", "VALIDATION", false}}
	for i, r := range rows {
		row := r.Req.(service.RecordToolCallReq)
		if row.Tool != "get_project" || row.Outcome != want[i].outcome || row.Code != want[i].code || r.Caller.Identity != "u-ada" {
			t.Errorf("row %d = %+v by %s, want get_project %s %s by u-ada", i, row, r.Caller.Identity, want[i].outcome, want[i].code)
		}
		if want[i].slow && row.LatencyMs < delay.Milliseconds() {
			t.Errorf("row %d latencyMs %d, want at least the service's %d", i, row.LatencyMs, delay.Milliseconds())
		}
		if !want[i].slow && row.LatencyMs >= delay.Milliseconds() {
			t.Errorf("row %d latencyMs %d for a call refused before the service", i, row.LatencyMs)
		}
	}
}

// TestGetBoardCarriesTheTransitionTable: get_board's board.transitions is the table the service
// returns, every status with its allowed next statuses.
func TestGetBoardCarriesTheTransitionTable(t *testing.T) {
	f := servicefake.New()
	h := bootTools(t, f.Services())
	cs := h.Connect(t, bearerAda)
	f.Set("Boards.Get", service.GetBoardResp{Board: sampleBoard(), TaskCounts: map[string]float64{}}, nil)
	res, err := cs.CallTool(testCtx(t), &sdk.CallToolParams{Name: "get_board", Arguments: map[string]any{"projectId": "P-1", "boardId": "B-1"}})
	if err != nil || res.IsError {
		t.Fatalf("get_board: %+v, %v", res, err)
	}
	board, _ := res.StructuredContent.(map[string]any)["board"].(map[string]any)
	got := board["transitions"]
	if want := asJSON(t, fixedTable()); !reflect.DeepEqual(got, want) {
		t.Errorf("get_board transitions = %v, want %v", got, want)
	}
	if len(got.(map[string]any)) != len(domain.AllStatuses()) {
		t.Errorf("transitions has %d statuses, want %d", len(got.(map[string]any)), len(domain.AllStatuses()))
	}
}

// TestUnclassifiedToolIsRefused: registering a tool with no domain.ToolPosture panics, naming it,
// and a classified one registers (the positive control).
func TestUnclassifiedToolIsRefused(t *testing.T) {
	h := bk.BootHarness(t, servicefake.New().Services(), toolsResolver())
	call := func(context.Context, domain.Caller, service.GetProjectReq) (service.GetProjectResp, error) {
		return service.GetProjectResp{}, nil
	}
	func() {
		defer func() {
			if p := recover(); p == nil || !contains(p, "no_such_tool") {
				t.Errorf("registering an unclassified tool: panic %v, want one naming it", p)
			}
		}()
		bk.Tool(h.Server(), "test", "no_such_tool", "", call)
	}()
	bk.Tool(h.Server(), "projects", "get_project", "", call)
	if m := h.Server().Manifest(); len(m) != 1 || m[0].Name != "get_project" || m[0].Tier != domain.TierRead {
		t.Errorf("manifest = %+v, want get_project at tier read", m)
	}
}

func contains(p any, s string) bool { return p != nil && strings.Contains(fmt.Sprint(p), s) }

// TestAdminOnlyToolHiddenFromOthers: a tool whose posture is AdminOnlyOnly is in a workspace
// admin's tools/list and in no one else's; every other tool is in both.
func TestAdminOnlyToolHiddenFromOthers(t *testing.T) {
	f := servicefake.New()
	h := bootTools(t, f.Services())
	bk.Tool(h.Server(), "test", "list_audit_log", "", func(context.Context, domain.Caller, service.ListProjectsReq) (service.ListProjectsResp, error) {
		return service.ListProjectsResp{Projects: []service.ProjectListing{}}, nil
	})
	listed := func(bearer string) map[string]bool {
		res, err := h.Connect(t, bearer).ListTools(testCtx(t), nil)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, tl := range res.Tools {
			out[tl.Name] = true
		}
		return out
	}
	admin, ada := listed(bearerAdmin), listed(bearerAda)
	if !admin["list_audit_log"] || ada["list_audit_log"] {
		t.Errorf("list_audit_log listed to admin %v, to a non-admin %v; want true, false", admin["list_audit_log"], ada["list_audit_log"])
	}
	for _, c := range projectsBoardsCases() {
		if !admin[c.tool] || !ada[c.tool] {
			t.Errorf("%s listed to admin %v, to a non-admin %v; want both", c.tool, admin[c.tool], ada[c.tool])
		}
	}
}

// TestBadOutputIsInternal: a service response that fails the tool's own output schema (a column
// status outside the enum) is the server's bug: the client gets isError INTERNAL in the contract
// shape, not a JSON-RPC error, the cause is logged, and the call still records one row.
func TestBadOutputIsInternal(t *testing.T) {
	f := servicefake.New()
	h := bootTools(t, f.Services())
	cs := h.Connect(t, bearerAda)
	bad := sampleBoard()
	bad.Columns[0].Status = "BOGUS"
	f.Set("Boards.Get", service.GetBoardResp{Board: bad, TaskCounts: map[string]float64{}}, nil)
	e := bk.ToolError(t, cs, "get_board", map[string]any{"projectId": "P-1", "boardId": "B-1"})
	if e["code"] != "INTERNAL" || e["retryable"] != true || strings.Contains(fmt.Sprint(e["message"]), "BOGUS") {
		t.Errorf("get_board with a bad output = %v, want INTERNAL retryable, the cause kept out of the message", e)
	}
	if logs := h.Log(); !strings.Contains(logs, "mcp: tool get_board failed after its handler") || !strings.Contains(logs, "BOGUS") {
		t.Errorf("the output failure was not logged:\n%s", logs)
	}
	rows := callsOf(f, "ToolCalls.Record")
	if len(rows) != 1 || rows[0].Req.(service.RecordToolCallReq).Code != "INTERNAL" || rows[0].Req.(service.RecordToolCallReq).Outcome != "error" {
		t.Errorf("tool_calls rows = %+v, want one get_board error INTERNAL", rows)
	}
}
