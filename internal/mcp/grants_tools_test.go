package mcp_test

import (
	"context"
	"slices"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// grant_capabilities on the real stack, every selected engine (R2.4's AC1-AC3): the tier and the
// declared errors, the grant row it writes and the call it then refuses, and the tools/list it narrows.

// grantsOnTheStack is grant_capabilities once on TestEveryToolOnTheRealStack's stack: u-grace (a
// worker there) gets the worker default, then the grant is removed.
func grantsOnTheStack(t *testing.T, c *caller, p string) {
	t.Helper()
	got := c.call("grant_capabilities", map[string]any{"projectId": p, "identityId": "u-grace"})
	if g := obj(got["grant"]); got["identityId"] != "u-grace" || len(g["tools"].([]any)) != len(domain.WorkerDefaultGrant().Tools) {
		t.Fatalf("grant_capabilities with no scope: %v, want the worker default", got)
	}
	if got := c.call("grant_capabilities", map[string]any{"projectId": p, "identityId": "u-grace", "scope": nil}); got["grant"] != nil {
		t.Fatalf("grant_capabilities scope null: %v, want grant null", got)
	}
}

// AC1: a worker's and a master's grant_capabilities is FORBIDDEN (the tier is a project admin's,
// conductor's requireAdmin); the admin's writes the grant row, which then refuses grace's
// write_memory and admits her claim_task. No scope is the worker default, null deletes the row. A
// non-member, a tool no grant reaches and a project the caller is not in are each refused, and a
// grant on a project admin is not enforced.
func TestGrantCapabilities(t *testing.T) {
	raceStacks(t, func(t *testing.T, r *raceRig) {
		ctx := context.Background()
		only := map[string]any{"tools": []any{"claim_task"}}
		args := map[string]any{"projectId": r.p, "identityId": "u-grace", "scope": only}

		e := r.wantCode(r.grace, "grant_capabilities", args, "FORBIDDEN")
		t.Logf("worker u-grace grant_capabilities -> %v", e)
		r.ada.call("add_member", map[string]any{"projectId": r.p, "identityId": "u-grace", "role": "master"})
		e = r.wantCode(r.grace, "grant_capabilities", args, "FORBIDDEN")
		if obj(e["details"])["requiredRole"] != "admin" {
			t.Fatalf("master u-grace grant_capabilities: %v, want requiredRole admin", e)
		}
		r.ada.call("add_member", map[string]any{"projectId": r.p, "identityId": "u-grace", "role": "worker"})

		got := r.ada.call("grant_capabilities", args)
		if got["identityId"] != "u-grace" || !slices.Equal(obj(got["grant"])["tools"].([]any), []any{"claim_task"}) {
			t.Fatalf("admin u-ada grant_capabilities: %v", got)
		}
		row, err := r.s.Store.GetGrant(ctx, r.p, "u-grace")
		if err != nil || !slices.Equal(row.Grant.Tools, []string{"claim_task"}) || row.UpdatedBy != "u-ada" || row.UpdatedAt <= 0 {
			t.Fatalf("the grant row %+v, %v", row, err)
		}
		t.Logf("admin u-ada grant_capabilities -> %v; capability_grants row %+v", got, row)

		id := r.task("granted", nil)
		e = r.wantCode(r.grace, "write_memory", map[string]any{"projectId": r.p, "taskId": id, "scope": "task", "key": "k", "value": "v"}, "FORBIDDEN")
		if obj(e["details"])["requiredCapability"] != "tool:write_memory" {
			t.Fatalf("write_memory under {tools:[claim_task]}: %v", e)
		}
		r.grace.call("claim_task", map[string]any{"projectId": r.p, "taskId": id})

		def := r.ada.call("grant_capabilities", map[string]any{"projectId": r.p, "identityId": "u-grace"})
		want := domain.WorkerDefaultGrant()
		if g := obj(def["grant"]); len(g["tools"].([]any)) != len(want.Tools) || len(g["statuses"].([]any)) != len(want.Statuses) ||
			!slices.Equal(g["memoryScopes"].([]any), []any{"task"}) {
			t.Fatalf("no scope: %v, want the worker default %+v", def, want)
		}
		if got := r.ada.call("grant_capabilities", map[string]any{"projectId": r.p, "identityId": "u-grace", "scope": nil}); got["grant"] != nil || got["identityId"] != "u-grace" {
			t.Fatalf("scope null: %v", got)
		}
		if _, err := r.s.Store.GetGrant(ctx, r.p, "u-grace"); err == nil {
			t.Fatal("scope null left the grant row")
		}
		r.grace.call("write_memory", map[string]any{"projectId": r.p, "taskId": id, "scope": "task", "key": "k", "value": "v"})

		e = r.wantCode(r.ada, "grant_capabilities", map[string]any{"projectId": r.p, "identityId": "u-nobody", "scope": only}, "VALIDATION")
		t.Logf("a non-member -> %v", e)
		e = r.wantCode(r.ada, "grant_capabilities", map[string]any{"projectId": r.p, "identityId": "u-grace", "scope": map[string]any{"deny": []any{"list_projects"}}}, "VALIDATION")
		if obj(e["details"])["field"] != "deny" {
			t.Fatalf("deny naming an unscoped tool: %v", e)
		}
		r.wantCode(r.ada, "grant_capabilities", map[string]any{"projectId": r.q, "identityId": "u-grace", "scope": only}, "FORBIDDEN")

		// A grant on a project admin is stored and not enforced.
		r.ada.call("grant_capabilities", map[string]any{"projectId": r.p, "identityId": "u-ada", "scope": only})
		r.task("the admin's grant is not enforced", nil)
	})
}

// toolNames is the session's tools/list, by name.
func toolNames(t *testing.T, cs *sdk.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(testCtx(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, tl := range res.Tools {
		out = append(out, tl.Name)
	}
	return out
}

// AC2: each grant on u-grace sends her session notifications/tools/list_changed at its next request
// and her next tools/list follows it: {tools:[claim_task]} hides write_memory and create_task,
// adding write_memory lists it again, narrowing back removes it, and scope null restores the whole
// list; u-ada's session is told nothing. A tool any membership may call stays listed: a project of
// her own lists everything again. AC3: none of the router facade's tools is listed.
func TestGrantChangesToolsList(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := bootStack(t, s)
		notes := make(chan string, 16)
		ada := &caller{t: t, cs: h.ConnectNotified(t, stackOwner, notes), called: map[string]int{}}
		grace := &caller{t: t, cs: h.ConnectNotified(t, stackGrace, notes), called: map[string]int{}}
		p := str(ada.call("create_project", map[string]any{"name": "Granted"})["projectId"])
		ada.call("add_member", map[string]any{"projectId": p, "identityId": "u-grace", "role": "worker"})

		quiet := func(why string) {
			t.Helper()
			select {
			case b := <-notes:
				t.Fatalf("%s: %s was sent tools/list_changed", why, b)
			case <-time.After(300 * time.Millisecond):
			}
		}
		told := func(why string) {
			t.Helper()
			if err := grace.cs.Ping(testCtx(t), nil); err != nil {
				t.Fatal(err)
			}
			select {
			case b := <-notes:
				if b != stackGrace {
					t.Fatalf("%s: %s was sent tools/list_changed, want u-grace's session", why, b)
				}
			case <-time.After(5 * time.Second):
				t.Fatalf("%s: no tools/list_changed to u-grace within 5 s", why)
			}
			quiet(why + ": one change, one notification")
		}
		grant := func(scope any) {
			t.Helper()
			ada.call("grant_capabilities", map[string]any{"projectId": p, "identityId": "u-grace", "scope": scope})
		}

		full := toolNames(t, grace.cs)
		for _, name := range []string{"find_tools", "get_tool_schema", "invoke_tool"} {
			if slices.Contains(full, name) {
				t.Errorf("AC3: tools/list lists the router facade's %s", name)
			}
		}
		if !slices.Contains(full, "write_memory") || !slices.Contains(full, "create_task") || !slices.Contains(full, "grant_capabilities") {
			t.Fatalf("u-grace's list before any grant: %v", full)
		}
		if err := grace.cs.Ping(testCtx(t), nil); err != nil {
			t.Fatal(err)
		}
		quiet("no grant changed")

		grant(map[string]any{"tools": []any{"claim_task"}})
		quiet("u-grace has made no request since the grant")
		told("{tools:[claim_task]}")
		narrow := toolNames(t, grace.cs)
		if slices.Contains(narrow, "write_memory") || slices.Contains(narrow, "create_task") || !slices.Contains(narrow, "claim_task") ||
			!slices.Contains(narrow, "get_task") || !slices.Contains(narrow, "list_projects") {
			t.Fatalf("u-grace's list under {tools:[claim_task]}: %v", narrow)
		}
		t.Logf("{tools:[claim_task]}: list_changed to u-grace only; tools/list %d -> %d", len(full), len(narrow))

		grant(map[string]any{"tools": []any{"claim_task", "write_memory"}})
		told("write_memory granted")
		if got := toolNames(t, grace.cs); !slices.Contains(got, "write_memory") || len(got) != len(narrow)+1 {
			t.Fatalf("after granting write_memory: %v, want %d tools with write_memory", got, len(narrow)+1)
		}
		grant(map[string]any{"tools": []any{"claim_task"}})
		told("write_memory revoked")
		if got := toolNames(t, grace.cs); slices.Contains(got, "write_memory") || len(got) != len(narrow) {
			t.Fatalf("after revoking write_memory: %v", got)
		}
		if got := toolNames(t, ada.cs); !slices.Equal(got, full) {
			t.Fatalf("u-ada's list %v moved with u-grace's grant", got)
		}

		grant(nil)
		told("scope null")
		if got := toolNames(t, grace.cs); !slices.Equal(got, full) {
			t.Fatalf("after scope null: %d tools, want the %d of no grant", len(got), len(full))
		}

		grant(map[string]any{"tools": []any{"claim_task"}})
		told("{tools:[claim_task]} again")
		grace.call("create_project", map[string]any{"name": "Grace's own"})
		told("a project of her own")
		if got := toolNames(t, grace.cs); !slices.Equal(got, full) {
			t.Fatalf("with a project of her own: %d tools, want all %d listed", len(got), len(full))
		}
	})
}
