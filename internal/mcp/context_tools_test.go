package mcp_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	bk "github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicefake"
)

// contextCodes are the codes every context tool is driven to through the fake: the R1.6 list less
// CYCLE_DETECTED, which contextExtra gives the one tool that declares it.
var (
	contextCodes = []domain.Code{domain.CodeStale, domain.CodeForbidden, domain.CodeNotFound, domain.CodeValidation}
	contextExtra = map[string][]domain.Code{"add_dependency": {domain.CodeCycleDetected}}
)

// sampleBriefing is a get_task_context answer with every required slice and map set and the
// nullable sections (modelHint, resume, review) null: resume is null until P-117.
func sampleBriefing() service.GetTaskContextResp {
	edge := service.DependencyEdge{ID: "E-1", ProjectID: "P-1", FromTaskID: "T-2", ToTaskID: "T-1", Type: "blocks", CreatedBy: "u-ada", Ts: "2026-10-23T00:00:00.000Z"}
	return service.GetTaskContextResp{
		Artifacts:     []service.Artifact{},
		Attachments:   []service.AttachmentBrief{},
		Deps:          service.TaskDeps{BlockedBy: []string{"T-2"}, Blocks: []string{}, Edges: []service.DependencyEdge{edge}},
		Fields:        map[string]service.FieldValue{"estimate": 3.0},
		Items:         []service.ContextItem{{ID: "CI-1", TaskID: "T-1", Kind: "decision", Body: "use pgx", Author: "u-ada", Ts: "2026-10-23T00:00:00.000Z"}},
		Memory:        []service.MemoryEntry{},
		OpenFollowUps: []service.OpenFollowUp{},
		Summary:       "half done",
		Workflow: service.Workflow{Status: domain.Todo, AllowedMoves: domain.AllowedNext(domain.Todo),
			Lanes: []service.Column{{ID: "C-1", Name: "Ready", Order: 0, Status: domain.Todo}}},
	}
}

func contextCases() []toolCase {
	sub := lcTask(domain.Todo)
	sub.ID = "T-7"
	parent := "T-1"
	sub.ParentTaskID = &parent
	mem := service.MemoryEntry{ID: "M-1", Key: "runbook", Scope: domain.MemoryScopeProject, Value: "make check", Author: "u-ada", Ts: "2026-10-23T00:00:00.000Z"}
	return []toolCase{
		{tool: "add_context_item", method: "Context.AddItem", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "taskId": "T-1", "kind": "decision", "body": "use pgx", "idempotencyKey": "k-1"},
			want: service.AddContextItemReq{ProjectID: "P-1", TaskID: "T-1", Kind: "decision", Body: "use pgx", IdempotencyKey: "k-1"},
			resp: service.AddContextItemResp{Item: sampleBriefing().Items[0], Warnings: []string{"body trimmed"}}},
		{tool: "get_task_context", method: "Context.GetTaskContext", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "taskId": "T-1", "include": []any{"items", "deps"}, "includeUpstream": true,
				"includeProjectMemory": true, "limit": 20, "cursor": "c-1"},
			want: service.GetTaskContextReq{ProjectID: "P-1", TaskID: "T-1", Include: []service.ContextSection{"items", "deps"},
				IncludeUpstream: true, IncludeProjectMemory: true, Limit: 20, Cursor: "c-1"},
			resp: sampleBriefing()},
		{tool: "set_summary", method: "Context.SetSummary", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "taskId": "T-1", "summary": "half done", "version": 2},
			want: service.SetSummaryReq{ProjectID: "P-1", TaskID: "T-1", Summary: "half done", Version: 2},
			resp: service.SetSummaryResp{Task: lcTask(domain.Todo)}},
		{tool: "write_memory", method: "Context.WriteMemory", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "taskId": "T-1", "scope": "task", "key": "plan", "value": map[string]any{"n": 1},
				"ttl": "1h", "idempotencyKey": "k-2"},
			// A JSON number decodes as float64 into Value (any).
			want: service.WriteMemoryReq{ProjectID: "P-1", TaskID: "T-1", Scope: domain.MemoryScopeTask, Key: "plan",
				Value: map[string]any{"n": float64(1)}, TTL: "1h", IdempotencyKey: "k-2"},
			resp: service.WriteMemoryResp{EntryID: "M-1"}},
		{tool: "read_memory", method: "Context.ReadMemory", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "taskId": "T-1", "scope": "agent", "key": "plan"},
			want: service.ReadMemoryReq{ProjectID: "P-1", TaskID: "T-1", Scope: "agent", Key: "plan"},
			resp: service.ReadMemoryResp{Entries: []service.MemoryEntry{mem}}},
		{tool: "recall_memory", method: "Context.RecallMemory", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "scope": "project", "key": "runbook", "k": 5},
			want: service.RecallMemoryReq{ProjectID: "P-1", Scope: "project", Key: "runbook", K: 5},
			resp: service.RecallMemoryResp{Entries: []service.MemoryEntry{mem}}},
		{tool: "add_dependency", method: "Context.AddDependency", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "fromTaskId": "T-2", "toTaskId": "T-1", "type": "blocks", "idempotencyKey": "k-3"},
			want: service.AddDependencyReq{ProjectID: "P-1", FromTaskID: "T-2", ToTaskID: "T-1", Type: "blocks", IdempotencyKey: "k-3"},
			resp: service.AddDependencyResp{Edge: sampleBriefing().Deps.Edges[0]}},
		{tool: "remove_dependency", method: "Context.RemoveDependency", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "edgeId": "E-1"},
			want: service.RemoveDependencyReq{ProjectID: "P-1", EdgeID: "E-1"},
			resp: service.RemoveDependencyResp{Ok: true}},
		{tool: "add_subtask", method: "Context.AddSubtask", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "parentTaskId": "T-1", "title": "write docs", "description": "the README",
				"priority": "high", "idempotencyKey": "k-4"},
			want: service.AddSubtaskReq{ProjectID: "P-1", ParentTaskID: "T-1", Title: "write docs", Description: "the README",
				Priority: domain.PriorityHigh, IdempotencyKey: "k-4"},
			resp: service.AddSubtaskResp{Task: sub}},
		{tool: "list_subtasks", method: "Context.ListSubtasks", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "parentTaskId": "T-1"},
			want: service.ListSubtasksReq{ProjectID: "P-1", ParentTaskID: "T-1"},
			resp: service.ListSubtasksResp{Tasks: []service.Task{sub}}},
	}
}

// TestContextTools drives each of the 10 context tools through a real client against servicefake:
// the happy path for a non-admin and a workspace-admin bearer (the decoded request and the
// resolver's caller reach Context field for field; its response comes back as structured content),
// an unknown key (VALIDATION, the service never called), each contextCodes code and the tool's
// contextExtra codes from the service (isError, the contract shape, the code's retryable), and each
// description is at most 200 characters (ADR-0004).
func TestContextTools(t *testing.T) {
	f := servicefake.New()
	h := bootTools(t, f.Services())
	ada, admin := h.Connect(t, bearerAda), h.Connect(t, bearerAdmin)
	callers := map[*sdk.ClientSession]domain.Caller{ada: atService(toolsResolver().Callers[bearerAda]), admin: atService(toolsResolver().Callers[bearerAdmin])}
	cases := contextCases()
	if len(cases) != 10 {
		t.Fatalf("%d cases, want the 10 context tools", len(cases))
	}
	listed, err := ada.ListTools(testCtx(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	desc := map[string]string{}
	for _, tl := range listed.Tools {
		desc[tl.Name] = tl.Description
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
			if d, ok := desc[tc.tool]; !ok || d == "" || len(d) > 200 {
				t.Errorf("%s description (%d chars, listed %v) must be 1 to 200 characters", tc.tool, len(d), ok)
			} else {
				t.Logf("%s description: %d characters", tc.tool, len(d))
			}
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
			for _, code := range append(slices.Clone(contextCodes), contextExtra[tc.tool]...) {
				t.Run(string(code), func(t *testing.T) {
					f.Reset()
					e := bk.ToolError(t, ada, tc.tool, with(tc.args, tc.trigger, servicefake.Trigger(code)))
					if e["code"] != string(code) || e["retryable"] != (code == domain.CodeStale) {
						t.Errorf("%s = %v, want %s retryable %v", tc.tool, e, code, code == domain.CodeStale)
					}
					if n := len(callsOf(f, tc.method)); n != 1 {
						t.Errorf("%s called the service %d times, want 1 (the error is the service's)", tc.tool, n)
					}
				})
			}
		})
	}
}

// TestContextTaskBriefingShape: get_task_context as served carries items and workflow.allowedMoves
// and no title, description or id, in its output schema and in a real answer (conductor's briefing
// quirk, kept), and resume is null (P-117 is unported) in the answer and said so in the description.
func TestContextTaskBriefingShape(t *testing.T) {
	f := servicefake.New()
	h := bootTools(t, f.Services())
	cs := h.Connect(t, bearerAda)
	listed, err := cs.ListTools(testCtx(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	var tool *sdk.Tool
	for _, tl := range listed.Tools {
		if tl.Name == "get_task_context" {
			tool = tl
		}
	}
	if tool == nil {
		t.Fatal("get_task_context is not in tools/list")
	}
	props := asJSON(t, tool.OutputSchema).(map[string]any)["properties"].(map[string]any)
	f.Set("Context.GetTaskContext", sampleBriefing(), nil)
	res, err := cs.CallTool(testCtx(t), &sdk.CallToolParams{Name: "get_task_context", Arguments: map[string]any{"projectId": "P-1", "taskId": "T-1"}})
	if err != nil || res.IsError {
		t.Fatalf("get_task_context: %+v, %v", res, err)
	}
	out := res.StructuredContent.(map[string]any)
	for _, k := range []string{"title", "description", "id", "contextItems"} {
		if _, ok := props[k]; ok {
			t.Errorf("get_task_context's output schema has %q", k)
		}
		if _, ok := out[k]; ok {
			t.Errorf("get_task_context answered with %q: %v", k, out)
		}
	}
	wf, _ := props["workflow"].(map[string]any)["properties"].(map[string]any)
	if _, ok := props["items"]; !ok || wf["allowedMoves"] == nil {
		t.Errorf("get_task_context's output schema lacks items or workflow.allowedMoves: %v", props)
	}
	if items, _ := out["items"].([]any); len(items) != 1 {
		t.Errorf("get_task_context answered items %v, want the service's one item", out["items"])
	}
	if moves, _ := out["workflow"].(map[string]any)["allowedMoves"].([]any); len(moves) != len(domain.AllowedNext(domain.Todo)) {
		t.Errorf("get_task_context answered workflow.allowedMoves %v, want %v", moves, domain.AllowedNext(domain.Todo))
	}
	if r, ok := out["resume"]; !ok || r != nil {
		t.Errorf("get_task_context answered resume %v (present %v), want null", r, ok)
	}
	if want := "resume is not yet available and is always null; do not treat null as 'start fresh'"; !strings.Contains(tool.Description, want) {
		t.Errorf("get_task_context's description %q does not say %q", tool.Description, want)
	}
	t.Logf("get_task_context output properties: %d; items, workflow.allowedMoves present; no title/description/id; resume null", len(props))
}
