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

// P-257 B1 (review): a worker who carries another identity's label on a token of his own (a row
// written before the mint refused it, or in a race) still does not hold it: claim_next and handoff
// naming it are FORBIDDEN, as for any other caller below master, and still once the other token
// is revoked (review round 2: every row counts).
func TestASelfMintedLabelIsStillAnotherAgents(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		e.member(t, e.project, "carol", domain.RoleWorker)
		now := e.clock.now().UnixMilli()
		for _, tok := range []store.Token{
			{ID: "tok-carol-b", IdentityID: "carol", Subject: "carol", SecretHash: "h-cb", Label: "agent-b", CreatedAt: now, ExpiresAt: now + time.Hour.Milliseconds()},
			{ID: "tok-bob-b", IdentityID: "bob", Subject: "bob", SecretHash: "h-bb", Label: "agent-b", CreatedAt: now, ExpiresAt: now + time.Hour.Milliseconds()},
		} {
			if err := e.st.CreateToken(e.ctx, tok); err != nil {
				t.Fatal(err)
			}
		}
		lc := e.c.Lifecycle()
		task := e.task(t, domain.Todo)
		c := e.claim(t, e.alice, task.ID, 0)
		if _, err := lc.Handoff(e.ctx, e.alice, HandoffTaskReq{ProjectID: e.project, TaskID: task.ID, LeaseID: c.LeaseID, ToAgent: "agent-b", MemoryNote: "for carol's agent"}); err != nil {
			t.Fatalf("admin handoff to carol's agent-b: %v", err)
		}
		bobAsB := bob
		bobAsB.Agent, bobAsB.Labelled, bobAsB.Via = "agent-b", true, domain.ViaAPIToken
		_, err := lc.ClaimNext(e.ctx, bobAsB, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, Assignee: "agent-b"})
		if be := wantCode(t, err, domain.CodeForbidden); be.Details["label"] != "agent-b" {
			t.Fatalf("claim_next assignee agent-b on bob's self-labelled token: details %v", be.Details)
		}
		mine := e.task(t, domain.Todo)
		mc := e.claim(t, bobAsB, mine.ID, 0)
		_, err = lc.Handoff(e.ctx, bobAsB, HandoffTaskReq{ProjectID: e.project, TaskID: mine.ID, LeaseID: mc.LeaseID, ToAgent: "agent-b", MemoryNote: "over"})
		wantCode(t, err, domain.CodeForbidden)
		// Revoking carol's token does not hand bob the label (review round 2): her row still counts,
		// so a label two identities carry stays refused to both below master.
		if err := e.st.RevokeToken(e.ctx, "tok-carol-b", now); err != nil {
			t.Fatal(err)
		}
		_, err = lc.ClaimNext(e.ctx, bobAsB, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, Assignee: "agent-b"})
		wantCode(t, err, domain.CodeForbidden)
	})
}

// P-257 R2-B1 (review round 2, the adopted LapsedLabelTakeover probe, its service half; the mint
// half is internal/api TestALapsedLabelStaysItsMintersOnTheStack, since the service's tests may not
// import internal/auth): carol's agent-b token expires while work handed to agent-b waits, and her
// next agent-b token claims it. A second identity's agent-b row refuses both below master, revoked
// or not.
func TestALapsedLabelStaysItsOwners(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		carol := e.member(t, e.project, "carol", domain.RoleWorker)
		row := func(id, identity string) {
			t.Helper()
			now := e.clock.now().UnixMilli()
			if err := e.st.CreateToken(e.ctx, store.Token{ID: id, IdentityID: identity, Subject: identity, SecretHash: "h-" + id,
				Label: "agent-b", CreatedAt: now, ExpiresAt: now + time.Hour.Milliseconds()}); err != nil {
				t.Fatal(err)
			}
		}
		row("tok-carol-1", "carol")
		lc := e.c.Lifecycle()
		handed := map[string]bool{}
		for range 2 {
			task := e.task(t, domain.Todo)
			c := e.claim(t, e.alice, task.ID, 3600)
			if _, err := lc.Handoff(e.ctx, e.alice, HandoffTaskReq{ProjectID: e.project, TaskID: task.ID, LeaseID: c.LeaseID, ToAgent: "agent-b", MemoryNote: "for carol's agent"}); err != nil {
				t.Fatal(err)
			}
			handed[task.ID] = true
		}
		e.clock.advance(2 * time.Hour) // carol's token lapses
		row("tok-carol-2", "carol")
		carolAsB := carol
		carolAsB.Agent, carolAsB.Labelled, carolAsB.Via = "agent-b", true, domain.ViaAPIToken
		resp, err := lc.ClaimNext(e.ctx, carolAsB, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, Assignee: "agent-b"})
		if err != nil || resp.Task == nil || !handed[resp.Task.ID] {
			t.Fatalf("carol's next agent-b token claims work handed to agent-b: %+v %v", resp, err)
		}
		// A second identity's row (from before the mint refused it, or a race) refuses both, and its
		// revoke does not free the label: the race fails closed. A master still names it.
		row("tok-bob-b", "bob")
		bobAsB := bob
		bobAsB.Agent, bobAsB.Labelled, bobAsB.Via = "agent-b", true, domain.ViaAPIToken
		_, err = lc.ClaimNext(e.ctx, bobAsB, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, Assignee: "agent-b"})
		wantCode(t, err, domain.CodeForbidden)
		if err := e.st.RevokeToken(e.ctx, "tok-bob-b", e.clock.now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
		_, err = lc.ClaimNext(e.ctx, carolAsB, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, Assignee: "agent-b"})
		wantCode(t, err, domain.CodeForbidden)
		if resp, err := lc.ClaimNext(e.ctx, e.alice, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, Assignee: "agent-b"}); err != nil || resp.Task == nil || !handed[resp.Task.ID] {
			t.Fatalf("a master names any label: %+v %v", resp, err)
		}
	})
}

// P-257 B2 (review): a follow-up on a task its agent holds targets that agent. The holder's own
// account with no agent (its browser session, which the drawer offers Ack) acks it, as on main;
// another agent of the same identity does not, so the narrowing between agents stays.
func TestTheHoldersAccountAcksItsAgentsFollowUp(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		agent, other := bob, bob
		agent.Agent, other.Agent = "agent-b", "agent-x"
		task := e.task(t, domain.Todo)
		e.claim(t, agent, task.ID, 0)
		fu, err := e.c.FollowUps().AddFollowUp(e.ctx, e.alice, AddFollowUpReq{ProjectID: e.project, TaskID: task.ID, Body: "status?"})
		if err != nil {
			t.Fatal(err)
		}
		if fu.FollowUp.Target == nil || *fu.FollowUp.Target != "agent-b" {
			t.Fatalf("the follow-up's target %v, want the holding agent agent-b", fu.FollowUp.Target)
		}
		ack := AckFollowUpReq{ProjectID: e.project, TaskID: task.ID, FollowUpID: fu.FollowUp.ID}
		_, err = e.c.FollowUps().AckFollowUp(e.ctx, other, ack)
		wantCode(t, err, domain.CodeForbidden)
		if _, err := e.c.FollowUps().AckFollowUp(e.ctx, bob, ack); err != nil {
			t.Fatalf("bob's browser session acks the follow-up aimed at his agent: %v", err)
		}
	})
}
