package mcp_test

import (
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/auth/authfake"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	bk "github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/mcp/tools"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// P-399: REVIEW-L4 probe P1 over /mcp. Two labelled tokens of one identity, agent-a and agent-b, on
// every selected engine: agent-a claims, and agent-b's cancel_task and move_task are refused (worker
// bob: FORBIDDEN; master ada: CONFLICT_LOCKED), the task stays agent-a's under the same lease, and
// agent-a then completes it with that lease.
const (
	siblingAdaA = "bk_sibling_ada_a"
	siblingAdaB = "bk_sibling_ada_b"
	siblingBobA = "bk_sibling_bob_a"
	siblingBobB = "bk_sibling_bob_b"
)

func TestASiblingAgentCannotEndAnotherAgentsLease(t *testing.T) {
	labelled := func(identity, agent string) domain.Caller {
		return domain.Caller{Identity: identity, TokenScope: identity, Agent: agent, Labelled: true, Via: domain.ViaAPIToken}
	}
	res := authfake.Resolver{Callers: map[string]domain.Caller{
		siblingAdaA: labelled("u-ada", "agent-a"), siblingAdaB: labelled("u-ada", "agent-b"),
		siblingBobA: labelled("u-bob", "agent-a"), siblingBobB: labelled("u-bob", "agent-b"),
	}}
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		h := bk.BootHarness(t, s.Services, res)
		tools.Register(h.Server(), s.Services)
		conn := func(tok, client string) *caller {
			return &caller{t: t, cs: h.ConnectAs(t, tok, client), called: map[string]int{}}
		}
		adaA, adaB := conn(siblingAdaA, "client-a"), conn(siblingAdaB, "client-b")
		bobA, bobB := conn(siblingBobA, "client-a"), conn(siblingBobB, "client-b")
		p := str(adaA.call("create_project", map[string]any{"name": "Siblings"})["projectId"])
		b := str(obj(adaA.call("create_board", map[string]any{"projectId": p, "name": "Main"})["board"])["id"])
		adaA.call("add_member", map[string]any{"projectId": p, "identityId": "u-bob", "role": "worker"})

		get := func(id string) map[string]any {
			return obj(adaA.call("get_task", map[string]any{"projectId": p, "taskId": id})["task"])
		}
		refused := func(leg string, c *caller, tool string, args map[string]any, code string) {
			t.Helper()
			e := bk.ToolError(t, c.cs, tool, args)
			t.Logf("%s %s: agent-b %s: %v %v", s.Engine, leg, tool, e["code"], e["message"])
			if e["code"] != code {
				t.Fatalf("%s %v: %v, want %s", tool, args, e, code)
			}
		}
		for _, leg := range []struct {
			name   string
			a, b   *caller
			cancel string // agent-b's cancel_task refusal
			move   string // agent-b's move_task refusal
		}{
			{"worker bob", bobA, bobB, "FORBIDDEN", "FORBIDDEN"},
			{"master ada", adaA, adaB, "CONFLICT_LOCKED", "CONFLICT_LOCKED"},
		} {
			id := str(obj(adaA.call("create_task", map[string]any{"projectId": p, "boardId": b, "title": leg.name})["task"])["id"])
			lease := str(leg.a.call("claim_task", map[string]any{"projectId": p, "taskId": id})["leaseId"])
			before := get(id)
			claim := obj(before["claim"])
			if str(claim["agentId"]) != "agent-a" || before["status"] != "IN_PROGRESS" {
				t.Fatalf("%s: agent-a's claim %v on %v", leg.name, claim, before["status"])
			}
			refused(leg.name, leg.b, "cancel_task", map[string]any{"projectId": p, "taskId": id, "version": before["version"], "reason": "mine now"}, leg.cancel)
			refused(leg.name, leg.b, "move_task", map[string]any{"projectId": p, "taskId": id, "version": before["version"], "toStatus": "TODO"}, leg.move)
			after := get(id)
			if after["version"] != before["version"] || after["status"] != "IN_PROGRESS" || !jsonEqual(after["claim"], before["claim"]) {
				t.Fatalf("%s: agent-b's refused calls moved the task: %v, was %v", leg.name, after, before)
			}
			done := obj(leg.a.call("complete_task", map[string]any{"projectId": p, "taskId": id, "leaseId": lease, "deliverable": "mine"})["task"])
			t.Logf("%s %s: agent-a complete_task with its lease -> %v", s.Engine, leg.name, done["status"])
			if done["status"] != "REVIEW" && done["status"] != "DONE" {
				t.Fatalf("%s: agent-a's complete_task left %v", leg.name, done["status"])
			}
		}
	})
}
