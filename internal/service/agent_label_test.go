package service

import (
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// P-277 (audit A2): an explicit assignee or handoff target that is another agent's label (a live API
// token's, other than the default) is FORBIDDEN below master, unless the caller is that label's
// token. Its own label, a free label, a revoked token's label and a master pass.
func TestAnotherAgentsLabelIsForbiddenBelowMaster(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		e.member(t, e.project, "carol", domain.RoleWorker)
		now := e.clock.now().UnixMilli()
		for _, tok := range []store.Token{
			{ID: "tok-a", IdentityID: "bob", Subject: "bob", SecretHash: "h-a", Label: "agent-a", CreatedAt: now, ExpiresAt: now + time.Hour.Milliseconds()},
			{ID: "tok-b", IdentityID: "carol", Subject: "carol", SecretHash: "h-b", Label: "agent-b", CreatedAt: now, ExpiresAt: now + time.Hour.Milliseconds()},
			{ID: "tok-dead", IdentityID: "carol", Subject: "carol", SecretHash: "h-d", Label: "agent-dead", CreatedAt: now, ExpiresAt: now + time.Hour.Milliseconds()},
			{ID: "tok-old", IdentityID: "carol", Subject: "carol", SecretHash: "h-o", Label: "agent-old", CreatedAt: now - 2*time.Hour.Milliseconds(), ExpiresAt: now - time.Hour.Milliseconds()},
		} {
			if err := e.st.CreateToken(e.ctx, tok); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.st.RevokeToken(e.ctx, "tok-dead", now); err != nil {
			t.Fatal(err)
		}
		agentA := bob
		agentA.Agent, agentA.Labelled, agentA.Via = "agent-a", true, domain.ViaAPIToken
		// A client that calls itself agent-b (an OAuth client_name, a clientInfo.name) is not agent-b's token.
		claimsB := bob
		claimsB.Agent, claimsB.Via = "agent-b", domain.ViaOAuth
		lc := e.c.Lifecycle()

		handoff := func(who domain.Caller, to string) error {
			t.Helper()
			task := e.task(t, domain.Todo)
			c := e.claim(t, who, task.ID, 0)
			_, err := lc.Handoff(e.ctx, who, HandoffTaskReq{ProjectID: e.project, TaskID: task.ID, LeaseID: c.LeaseID, ToAgent: to, MemoryNote: "over"})
			return err
		}
		for _, who := range []domain.Caller{agentA, claimsB, bob} {
			be := wantCode(t, handoff(who, "agent-b"), domain.CodeForbidden)
			if be.Details["field"] != "toAgent" || be.Details["label"] != "agent-b" {
				t.Fatalf("handoff to agent-b as %q: details %v", who.Agent, be.Details)
			}
			_, err := lc.ClaimNext(e.ctx, who, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, Assignee: "agent-b"})
			if be := wantCode(t, err, domain.CodeForbidden); be.Details["field"] != "assignee" {
				t.Fatalf("claim_next assignee agent-b as %q: details %v", who.Agent, be.Details)
			}
			_, err = lc.Next(e.ctx, who, NextTaskReq{ProjectID: e.project, BoardID: e.board.ID, Assignee: "agent-b"})
			wantCode(t, err, domain.CodeForbidden)
		}
		// The controls: agent-a's own label, a label no live token holds, and a master.
		if err := handoff(agentA, "agent-a"); err != nil {
			t.Fatalf("handoff to its own label: %v", err)
		}
		if _, err := lc.ClaimNext(e.ctx, agentA, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, Assignee: "agent-a"}); err != nil {
			t.Fatalf("claim_next of its own label: %v", err)
		}
		for _, free := range []string{"reviewer-bot", "agent-dead", "agent-old", domain.DefaultTokenLabel} {
			if err := handoff(agentA, free); err != nil {
				t.Fatalf("handoff to %q, which no live token's agent holds: %v", free, err)
			}
		}
		if err := handoff(e.alice, "agent-b"); err != nil {
			t.Fatalf("the project's admin hands agent-b work: %v", err)
		}
		if _, err := lc.ClaimNext(e.ctx, e.alice, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, Assignee: "agent-b"}); err != nil {
			t.Fatalf("the project's admin reads agent-b's frontier: %v", err)
		}
	})
}

// G6 with P-278: a claim belongs to its agent, so the same identity's other agent claiming the task
// is refused CONFLICT_LOCKED, and the same agent's second claim shares the lease.
func TestALeaseIsItsAgentsNotItsIdentitys(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		a, b := bob, bob
		a.Agent, b.Agent = "agent-a", "agent-b"
		task := e.task(t, domain.Todo)
		c := e.claim(t, a, task.ID, 0)
		lc := e.c.Lifecycle()
		_, err := lc.Claim(e.ctx, b, ClaimTaskReq{ProjectID: e.project, TaskID: task.ID})
		wantCode(t, err, domain.CodeConflictLocked)
		if _, err := lc.Heartbeat(e.ctx, a, HeartbeatTaskReq{ProjectID: e.project, TaskID: task.ID, LeaseID: c.LeaseID}); err != nil {
			t.Fatalf("the holding agent's heartbeat: %v", err)
		}
		again := e.claim(t, a, task.ID, 0)
		if again.LeaseID != c.LeaseID {
			t.Fatalf("the same agent's second claim got lease %q, want the shared %q", again.LeaseID, c.LeaseID)
		}
	})
}
