package mcp_test

import (
	"reflect"
	"slices"
	"testing"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	bk "github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicefake"
)

func sampleTask() service.Task {
	ada := "u-ada"
	return service.Task{
		Assignee: &ada, BlockedBy: []string{}, Blocks: []string{"T-2"}, BoardID: "B-1", ColumnID: "C-1",
		CreatedAt: "2026-10-23T00:00:00.000Z", CreatedBy: "u-ada", CustomFields: map[string]service.FieldValue{"team": "core"},
		ID: "T-1", Labels: []string{"api"}, Order: "a0", Priority: domain.PriorityHigh, ProjectID: "P-1",
		RequiredCapabilities: []string{}, Status: domain.Todo, Title: "Write the docs",
		UpdatedAt: "2026-10-23T00:00:00.000Z", UpdatedBy: "u-ada", Version: 1,
	}
}

// taskCase is a toolCase with the service errors that tool declares.
type taskCase struct {
	toolCase
	codes []domain.Code
}

// tasksCases are the six task tools. codes are the errors each service call can return
// (internal/service/tasks.go and delete.go), each provoked through the fake.
func tasksCases() []taskCase {
	read := []domain.Code{domain.CodeValidation, domain.CodeForbidden, domain.CodeNotFound}
	versioned := append(slices.Clone(read), domain.CodeStale)
	locked := append(slices.Clone(versioned), domain.CodeConflictLocked)
	moved := append(slices.Clone(locked), domain.CodeInvalidTransition, domain.CodeWIPExceeded)
	parent, est := "T-9", 2.5
	return []taskCase{
		{toolCase{tool: "create_task", method: "Tasks.Create", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "boardId": "B-1", "title": "Write the docs", "columnId": "C-1",
				"description": "d", "labels": []any{"api"}, "priority": "high", "parentTaskId": "T-9",
				"customFields": map[string]any{"team": "core", "points": 3, "urgent": true},
				"modelHint":    map[string]any{"model": "opus", "effort": "high"}, "idempotencyKey": "k-1"},
			want: service.CreateTaskReq{ProjectID: "P-1", BoardID: "B-1", Title: "Write the docs", ColumnID: "C-1",
				Description: "d", Labels: []string{"api"}, Priority: domain.PriorityHigh, ParentTaskID: &parent,
				CustomFields:   map[string]service.FieldValue{"team": "core", "points": 3.0, "urgent": true},
				ModelHint:      service.ModelHintInput{Model: "opus", Effort: "high"},
				IdempotencyKey: "k-1"},
			resp: service.CreateTaskResp{Task: sampleTask()}}, read},
		{toolCase{tool: "get_task", method: "Tasks.Get", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "taskId": "T-1"},
			want: service.GetTaskReq{ProjectID: "P-1", TaskID: "T-1"},
			resp: service.GetTaskResp{Task: sampleTask()}}, []domain.Code{domain.CodeForbidden, domain.CodeNotFound}},
		{toolCase{tool: "update_task", method: "Tasks.Update", trigger: "projectId",
			// null clears, a value sets, an absent key leaves alone: the tri-state through go-sdk's decoder.
			args: map[string]any{"projectId": "P-1", "taskId": "T-1", "version": 1,
				"patch": map[string]any{"assignee": nil, "estimate": 2.5, "description": "", "title": "Renamed", "reviewer": "u-grace"}},
			want: service.UpdateTaskReq{ProjectID: "P-1", TaskID: "T-1", Version: 1, Patch: service.TaskPatch{
				Assignee: service.Null[string](), Estimate: service.Value(est), Description: service.Some(""),
				Title: "Renamed", Reviewer: service.Value("u-grace")}},
			resp: service.UpdateTaskResp{Task: sampleTask()}}, versioned},
		{toolCase{tool: "move_task", method: "Tasks.Move", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "taskId": "T-1", "version": 1, "toStatus": "IN_PROGRESS", "toColumnId": "C-2", "order": "a1"},
			want: service.MoveTaskReq{ProjectID: "P-1", TaskID: "T-1", Version: 1, ToStatus: domain.InProgress, ToColumnID: "C-2", Order: "a1"},
			resp: service.MoveTaskResp{Task: sampleTask(), Warnings: []string{"w"}}}, moved},
		{toolCase{tool: "list_tasks", method: "Tasks.List", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "boardId": "B-1", "assignee": nil, "status": "TODO", "label": "api", "limit": 10, "cursor": "c-1", "unblockedOnly": true},
			want: service.ListTasksReq{ProjectID: "P-1", BoardID: "B-1", Assignee: service.Null[string](), Status: domain.Todo, Label: "api", Limit: 10, Cursor: "c-1", UnblockedOnly: true},
			resp: service.ListTasksResp{Tasks: []service.Task{sampleTask()}, NextCursor: "c-2"}}, read},
		{toolCase{tool: "delete_task", method: "Tasks.Delete", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "taskId": "T-1", "version": 3},
			want: service.DeleteTaskReq{ProjectID: "P-1", TaskID: "T-1", Version: 3},
			resp: service.DeleteTaskResp{Ok: true}}, locked},
	}
}

// TestTasksTools drives each of the six task tools through a real client against servicefake: the
// happy path for a non-admin and a workspace-admin bearer (the decoded request and the resolver's
// caller, field for field, reach the service; its response comes back as structured content), an
// unknown key (VALIDATION, the service never called), and each error the service call declares
// (the contract shape with its code and retryable, the service called once).
func TestTasksTools(t *testing.T) {
	f := servicefake.New()
	h := bootTools(t, f.Services())
	ada, admin := h.Connect(t, bearerAda), h.Connect(t, bearerAdmin)
	callers := map[*sdk.ClientSession]domain.Caller{ada: toolsResolver().Callers[bearerAda], admin: toolsResolver().Callers[bearerAdmin]}
	cases := tasksCases()
	if len(cases) != 6 {
		t.Fatalf("%d cases, want the six task tools", len(cases))
	}
	for _, tc := range cases {
		t.Run(tc.tool, func(t *testing.T) {
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
			for _, code := range tc.codes {
				t.Run(string(code), func(t *testing.T) {
					f.Reset()
					e := bk.ToolError(t, ada, tc.tool, with(tc.args, tc.trigger, servicefake.Trigger(code)))
					if e["code"] != string(code) || e["retryable"] != domain.NewError(code, "", nil).Retryable {
						t.Errorf("%s = %v, want %s with retryable %v", tc.tool, e, code, domain.NewError(code, "", nil).Retryable)
					}
					if n := len(callsOf(f, tc.method)); n != 1 {
						t.Errorf("%s called the service %d times, want 1 (the error is the service's)", tc.tool, n)
					}
				})
			}
		})
	}
}

// TestTaskToolSurface: create_task's served input schema has no status (a created task lands TODO,
// ADR-0005), and each task tool's description, as a client lists it, is at most 200 characters
// (ADR-0004), its length logged.
func TestTaskToolSurface(t *testing.T) {
	h := bootTools(t, servicefake.New().Services())
	listed, err := h.Connect(t, bearerAda).ListTools(testCtx(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	served := map[string]*sdk.Tool{}
	for _, tl := range listed.Tools {
		served[tl.Name] = tl
	}
	for _, tc := range tasksCases() {
		tl, ok := served[tc.tool]
		if !ok {
			t.Errorf("%s is not served", tc.tool)
			continue
		}
		n := utf8.RuneCountInString(tl.Description)
		if n > 200 {
			t.Errorf("%s: description is %d characters, over 200", tc.tool, n)
		}
		t.Logf("%s description: %d characters", tc.tool, n)
	}
	in, _ := asJSON(t, served["create_task"].InputSchema).(map[string]any)
	props, _ := in["properties"].(map[string]any)
	if len(props) == 0 {
		t.Fatalf("create_task input schema has no properties: %v", in)
	}
	if _, ok := props["status"]; ok {
		t.Errorf("create_task's input schema has a status property: %v", props["status"])
	}
}

// TestTaskPatchKeysAreCaseSensitive: a patch key that differs from a declared one only in case
// (ASSIGNEE, TITLE) is an undeclared key of the open patch object. It is accepted and never decoded
// into the field it resembles, so the service sees no assignee or title change.
func TestTaskPatchKeysAreCaseSensitive(t *testing.T) {
	f := servicefake.New()
	h := bootTools(t, f.Services())
	cs := h.Connect(t, bearerAda)
	f.Set("Tasks.Update", service.UpdateTaskResp{Task: sampleTask()}, nil)
	args := map[string]any{"projectId": "P-1", "taskId": "T-1", "version": 1,
		"patch": map[string]any{"ASSIGNEE": "u-mallory", "Title": "Hijacked", "description": "kept"}}
	res, err := cs.CallTool(testCtx(t), &sdk.CallToolParams{Name: "update_task", Arguments: args})
	if err != nil || res.IsError {
		t.Fatalf("update_task: %+v, %v", res, err)
	}
	got := callsOf(f, "Tasks.Update")
	want := service.UpdateTaskReq{ProjectID: "P-1", TaskID: "T-1", Version: 1, Patch: service.TaskPatch{Description: service.Some("kept")}}
	if len(got) != 1 || !reflect.DeepEqual(got[0].Req, want) {
		t.Errorf("update_task reached the service as %#v, want %#v", got, want)
	}
}

// TestTaskPatchModelHintKeysFoldCase pins P-139 as it stands: patch.modelHint is a
// Nullable[ModelHintInput], whose UnmarshalJSON decodes the inner object with encoding/json, which
// matches struct keys case-insensitively, so an undeclared EFFORT lands as effort. The service still
// checks the value against the enum (internal/service/tasks.go modelHintIn).
// DEVIATION: P-139 is open; once Optional/Nullable decode strictly, want Effort "" here.
func TestTaskPatchModelHintKeysFoldCase(t *testing.T) {
	f := servicefake.New()
	h := bootTools(t, f.Services())
	cs := h.Connect(t, bearerAda)
	f.Set("Tasks.Update", service.UpdateTaskResp{Task: sampleTask()}, nil)
	args := map[string]any{"projectId": "P-1", "taskId": "T-1", "version": 1,
		"patch": map[string]any{"modelHint": map[string]any{"model": "opus", "EFFORT": "low"}}}
	res, err := cs.CallTool(testCtx(t), &sdk.CallToolParams{Name: "update_task", Arguments: args})
	if err != nil || res.IsError {
		t.Fatalf("update_task: %+v, %v", res, err)
	}
	got := callsOf(f, "Tasks.Update")
	want := service.UpdateTaskReq{ProjectID: "P-1", TaskID: "T-1", Version: 1,
		Patch: service.TaskPatch{ModelHint: service.Value(service.ModelHintInput{Model: "opus", Effort: "low"})}}
	if len(got) != 1 || !reflect.DeepEqual(got[0].Req, want) {
		t.Errorf("update_task reached the service as %#v, want %#v", got, want)
	}
}
