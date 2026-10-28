package mcp_test

import (
	"reflect"
	"slices"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	bk "github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicefake"
)

// lifecycleCodes are the codes every lifecycle tool is driven to through the fake: the R1.4 list.
// lifecycleExtra are the further codes a tool's service call can return (store.Claims.ClaimTask's
// INVALID_TRANSITION, the WIP check of a claim, block_task's dependency edges).
var (
	lifecycleCodes = []domain.Code{
		domain.CodeConflictLocked, domain.CodeLeaseExpired, domain.CodeStale,
		domain.CodeForbidden, domain.CodeValidation, domain.CodeNotFound,
	}
	lifecycleExtra = map[string][]domain.Code{
		"claim_task": {domain.CodeInvalidTransition, domain.CodeWIPExceeded},
		"claim_next": {domain.CodeWIPExceeded},
		"block_task": {domain.CodeCycleDetected},
	}
)

func lcTask(s domain.Status) service.Task {
	return service.Task{
		ID: "T-1", ProjectID: "P-1", BoardID: "B-1", ColumnID: "C-1", Title: "Ship it", Status: s,
		Priority: domain.PriorityNormal, Order: "a0", Version: 2,
		CreatedAt: "2026-10-23T00:00:00.000Z", CreatedBy: "u-ada", UpdatedAt: "2026-10-23T00:00:00.000Z", UpdatedBy: "u-ada",
		BlockedBy: []string{}, Blocks: []string{}, Labels: []string{}, RequiredCapabilities: []string{},
		CustomFields: map[string]service.FieldValue{},
	}
}

func lifecycleCases() []toolCase {
	lease := "L-1"
	claimed := lcTask(domain.InProgress)
	claimed.Claim = &service.TaskClaim{ExpiresAt: "2026-10-23T00:15:00.000Z", Owner: "u-ada"}
	poisoned := lcTask(domain.Blocked)
	poisoned.Labels, poisoned.ClaimAttempts = []string{"poisoned"}, 3
	review := lcTask(domain.Review)
	rev := "u-grace"
	review.Assignee = &rev
	cancelled := lcTask(domain.Done)
	cancelled.Labels = []string{"cancelled"}
	return []toolCase{
		{tool: "claim_task", method: "Lifecycle.Claim", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "taskId": "T-1", "leaseSeconds": 900, "idempotencyKey": "k-1"},
			want: service.ClaimTaskReq{ProjectID: "P-1", TaskID: "T-1", LeaseSeconds: 900, IdempotencyKey: "k-1"},
			resp: service.ClaimTaskResp{LeaseID: lease, Task: claimed}},
		{tool: "claim_next", method: "Lifecycle.ClaimNext", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "boardId": "B-1", "assignee": "agent-ada", "capabilities": []any{"go"},
				"leaseSeconds": 600, "idempotencyKey": "k-2", "shard": map[string]any{"index": 0, "count": 1}, "useProfile": true},
			want: service.ClaimNextReq{ProjectID: "P-1", BoardID: "B-1", Assignee: "agent-ada", Capabilities: []string{"go"},
				LeaseSeconds: 600, IdempotencyKey: "k-2", Shard: service.Shard{Index: 0, Count: 1}, UseProfile: true},
			resp: service.ClaimNextResp{Attempts: 1, LeaseID: &lease, Task: &claimed}},
		{tool: "heartbeat_task", method: "Lifecycle.Heartbeat", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "taskId": "T-1", "leaseId": lease, "leaseSeconds": 300},
			want: service.HeartbeatTaskReq{ProjectID: "P-1", TaskID: "T-1", LeaseID: lease, LeaseSeconds: 300},
			resp: service.HeartbeatTaskResp{ExpiresAt: "2026-10-23T00:20:00.000Z"}},
		{tool: "release_task", method: "Lifecycle.Release", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "taskId": "T-1", "leaseId": lease, "idempotencyKey": "k-3"},
			want: service.ReleaseTaskReq{ProjectID: "P-1", TaskID: "T-1", LeaseID: lease, IdempotencyKey: "k-3"},
			resp: service.ReleaseTaskResp{Task: lcTask(domain.Todo)}},
		{tool: "next_task", method: "Lifecycle.Next", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "boardId": "B-1", "assignee": "agent-ada", "capabilities": []any{"go"},
				"includeInbox": true, "inboxLimit": 5, "strategy": "fifo"},
			want: service.NextTaskReq{ProjectID: "P-1", BoardID: "B-1", Assignee: "agent-ada", Capabilities: []string{"go"},
				IncludeInbox: true, InboxLimit: 5, Strategy: "fifo"},
			resp: service.NextTaskResp{Task: ptr(lcTask(domain.Todo)),
				Inbox: service.Inbox{Blocked: []service.Task{}, Poisoned: []service.Task{poisoned}, Review: []service.Task{review}}}},
		{tool: "complete_task", method: "Lifecycle.Complete", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "taskId": "T-1", "leaseId": lease, "deliverable": "PR #9", "reviewer": "u-grace", "idempotencyKey": "k-4"},
			want: service.CompleteTaskReq{ProjectID: "P-1", TaskID: "T-1", LeaseID: lease, Deliverable: "PR #9", Reviewer: "u-grace", IdempotencyKey: "k-4"},
			resp: service.CompleteTaskResp{Task: review, Handover: service.Handover{Reviewer: &rev, Status: domain.Review}}},
		{tool: "block_task", method: "Lifecycle.Block", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "taskId": "T-1", "leaseId": lease, "reason": "waiting on T-2", "blockedBy": []any{"T-2"}, "idempotencyKey": "k-5"},
			want: service.BlockTaskReq{ProjectID: "P-1", TaskID: "T-1", LeaseID: lease, Reason: "waiting on T-2", BlockedBy: []string{"T-2"}, IdempotencyKey: "k-5"},
			resp: service.BlockTaskResp{Task: lcTask(domain.Blocked)}},
		{tool: "fail_task", method: "Lifecycle.Fail", trigger: "projectId",
			// false is a value, not an absence: retryable false poisons at once, absent is true.
			args: map[string]any{"projectId": "P-1", "taskId": "T-1", "leaseId": lease, "reason": "disk full", "retryable": false, "idempotencyKey": "k-6"},
			want: service.FailTaskReq{ProjectID: "P-1", TaskID: "T-1", LeaseID: lease, Reason: "disk full", Retryable: service.Some(false), IdempotencyKey: "k-6"},
			resp: service.FailTaskResp{Task: poisoned}},
		{tool: "reap_stale_leases", method: "Lifecycle.Reap", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "boardId": "B-1"},
			want: service.ReapStaleLeasesReq{ProjectID: "P-1", BoardID: "B-1"},
			resp: service.ReapStaleLeasesResp{Reaped: 2}},
		{tool: "handoff_task", method: "Lifecycle.Handoff", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "taskId": "T-1", "leaseId": lease, "toAgent": "agent-grace", "memoryNote": "tests pass, docs left"},
			want: service.HandoffTaskReq{ProjectID: "P-1", TaskID: "T-1", LeaseID: lease, ToAgent: "agent-grace", MemoryNote: "tests pass, docs left"},
			resp: service.HandoffTaskResp{Task: lcTask(domain.Todo), Warnings: []string{"agent-grace is not a member"}}},
		{tool: "cancel_task", method: "Lifecycle.Cancel", trigger: "projectId",
			args: map[string]any{"projectId": "P-1", "taskId": "T-1", "version": 2, "reason": "superseded by T-9"},
			want: service.CancelTaskReq{ProjectID: "P-1", TaskID: "T-1", Version: 2, Reason: "superseded by T-9"},
			resp: service.CancelTaskResp{Task: cancelled}},
	}
}

func ptr[T any](v T) *T { return &v }

// TestLifecycleTools drives each of the 11 lifecycle tools through a real client against
// servicefake: the happy path for a non-admin and a workspace-admin bearer (the decoded request and
// the resolver's caller reach Lifecycle field for field; its response comes back as structured
// content), an unknown key (VALIDATION, the service never called), each lifecycleCodes code and the
// tool's lifecycleExtra codes from the service (isError, the contract shape, the code's retryable),
// and each description is at most 200 characters (ADR-0004).
func TestLifecycleTools(t *testing.T) {
	f := servicefake.New()
	h := bootTools(t, f.Services())
	ada, admin := h.Connect(t, bearerAda), h.Connect(t, bearerAdmin)
	callers := map[*sdk.ClientSession]domain.Caller{ada: atService(toolsResolver().Callers[bearerAda]), admin: atService(toolsResolver().Callers[bearerAdmin])}
	cases := lifecycleCases()
	if len(cases) != 11 {
		t.Fatalf("%d cases, want the 11 lifecycle tools", len(cases))
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
			for _, code := range append(slices.Clone(lifecycleCodes), lifecycleExtra[tc.tool]...) {
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

// TestLifecycleFailRetryable: fail_task's retryable is tri-state on the wire: absent reaches the
// service unset (the service reads it as true), true and false as set values, and null is
// VALIDATION with the service never called.
func TestLifecycleFailRetryable(t *testing.T) {
	f := servicefake.New()
	h := bootTools(t, f.Services())
	cs := h.Connect(t, bearerAda)
	base := map[string]any{"projectId": "P-1", "taskId": "T-1", "leaseId": "L-1", "reason": "flaky"}
	for _, c := range []struct {
		name string
		args map[string]any
		want service.Optional[bool]
	}{
		{"absent", base, service.Optional[bool]{}},
		{"true", with(base, "retryable", true), service.Some(true)},
		{"false", with(base, "retryable", false), service.Some(false)},
	} {
		t.Run(c.name, func(t *testing.T) {
			f.Reset()
			f.Set("Lifecycle.Fail", service.FailTaskResp{Task: lcTask(domain.Todo)}, nil)
			if res, err := cs.CallTool(testCtx(t), &sdk.CallToolParams{Name: "fail_task", Arguments: c.args}); err != nil || res.IsError {
				t.Fatalf("fail_task: %+v, %v", res, err)
			}
			got := callsOf(f, "Lifecycle.Fail")
			if len(got) != 1 || got[0].Req.(service.FailTaskReq).Retryable != c.want {
				t.Errorf("fail_task retryable %s reached the service as %+v, want %+v", c.name, got, c.want)
			}
		})
	}
	t.Run("null", func(t *testing.T) {
		f.Reset()
		if e := bk.ToolError(t, cs, "fail_task", with(base, "retryable", nil)); e["code"] != "VALIDATION" {
			t.Errorf("fail_task retryable null = %v, want VALIDATION", e)
		}
		if n := len(callsOf(f, "Lifecycle.Fail")); n != 0 {
			t.Errorf("fail_task retryable null called the service %d times, want 0", n)
		}
	})
}
