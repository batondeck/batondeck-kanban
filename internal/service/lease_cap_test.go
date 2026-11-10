package service

import (
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// A lease is not handed to a caller by a name its client chose. On every engine:
//   - a caller capped below the write tier (a viewer OAuth grant) is never the holder cancel_task
//     lets through, even when its client's name is the claim's agent;
//   - a client that registered or declared the name a live API token of the same identity carries
//     as its label (not Labelled) is not that token's agent (P-277): cancel_task refuses it below
//     master, and cancel_task, move_task, delete_task and start_runs answer CONFLICT_LOCKED at
//     master.
//
// The controls: the labelled token cancels its own lease, and an unlabelled client whose name no
// token carries still cancels the lease it claimed.
func TestALeaseIsNotHandedOverByAClientsName(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		bob := e.member(t, e.project, "bob", domain.RoleMaster)
		carol := e.member(t, e.project, "carol", domain.RoleWorker)
		now := e.clock.now().UnixMilli()
		if err := e.st.CreateToken(e.ctx, store.Token{ID: "tok-builder", IdentityID: "bob", Subject: "bob", SecretHash: "h-builder",
			Label: "builder", CreatedAt: now, ExpiresAt: now + 24*time.Hour.Milliseconds()}); err != nil {
			t.Fatal(err)
		}
		as := func(c domain.Caller, agent, role string, labelled bool) domain.Caller {
			c.Agent, c.Role, c.Labelled = agent, role, labelled
			c.Via = domain.ViaAPIToken
			if !labelled && role != "" {
				c.Via = domain.ViaOAuth
			}
			return c
		}
		builder := as(bob, "builder", "", true)
		lc, tasks := e.c.Lifecycle(), e.c.Tasks()
		cancel := func(who domain.Caller, id string) error {
			t.Helper()
			_, err := lc.Cancel(e.ctx, who, CancelTaskReq{ProjectID: e.project, TaskID: id, Version: e.get(t, id).Version, Reason: "stop"})
			return err
		}
		claimed := func(who domain.Caller) string {
			t.Helper()
			task := e.task(t, domain.Todo)
			e.claim(t, who, task.ID, 0)
			return task.ID
		}

		held := claimed(builder)
		for _, c := range []struct {
			who  domain.Caller
			code domain.Code
		}{
			{as(bob, "builder", "viewer", false), domain.CodeForbidden},
			{as(bob, "builder", "worker", false), domain.CodeForbidden},
			{as(bob, "builder", "master", false), domain.CodeConflictLocked},
			{as(bob, "builder", "", false), domain.CodeConflictLocked}, // a clientInfo.name on a default-label token
		} {
			if err := cancel(c.who, held); err == nil {
				t.Fatalf("cancel_task as %q (role %q, not labelled) on builder's lease: done, want %s", c.who.Agent, c.who.Role, c.code)
			} else {
				wantCode(t, err, c.code)
			}
		}
		masterApp := as(bob, "builder", "master", false)
		_, err := tasks.Move(e.ctx, masterApp, MoveTaskReq{ProjectID: e.project, TaskID: held, Version: e.get(t, held).Version, ToStatus: domain.Todo})
		wantCode(t, err, domain.CodeConflictLocked)
		_, err = tasks.Delete(e.ctx, masterApp, DeleteTaskReq{ProjectID: e.project, TaskID: held, Version: e.get(t, held).Version})
		wantCode(t, err, domain.CodeConflictLocked)
		_, err = e.c.Runs().Start(e.ctx, masterApp, StartRunsReq{ProjectID: e.project, TaskID: held, Version: e.get(t, held).Version, Count: 2})
		wantCode(t, err, domain.CodeConflictLocked)
		if got := e.get(t, held); got.Status != domain.InProgress || got.Claim == nil {
			t.Fatalf("builder's task after the refusals: %+v", got)
		}
		if err := cancel(builder, held); err != nil {
			t.Fatalf("builder cancels its own lease: %v", err)
		}

		// A name no token carries is the client's own: carol's session claims and cancels, but a
		// viewer grant of hers under the same name is no holder.
		session := as(carol, "claude-code", "", false)
		mine := claimed(session)
		if err := cancel(as(carol, "claude-code", "viewer", false), mine); err == nil {
			t.Fatal("a viewer grant cancelled the lease its client's name recorded")
		} else {
			wantCode(t, err, domain.CodeForbidden)
		}
		if err := cancel(session, mine); err != nil {
			t.Fatalf("carol's session cancels the lease it claimed: %v", err)
		}
	})
}

// claim_task's re-claim hands a held lease back only to its holder. A client that registered or
// declared the label a live API token of the same identity carries (not Labelled) is not that
// token's agent (P-277): its claim_task on the token's held task is CONFLICT_LOCKED at every tier
// that may claim, is not handed the token's leaseId, and does not replace the token's lease with
// its own (store.ClaimTask's same-agent rule). The controls: the token's own re-claim answers its
// lease, and an unlabelled client whose name no token carries re-claims the lease it claimed.
func TestAReclaimDoesNotHandALeaseToAClientsName(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		bob := e.member(t, e.project, "bob", domain.RoleMaster)
		now := e.clock.now().UnixMilli()
		if err := e.st.CreateToken(e.ctx, store.Token{ID: "tok-builder", IdentityID: "bob", Subject: "bob", SecretHash: "h-builder",
			Label: "builder", CreatedAt: now, ExpiresAt: now + 24*time.Hour.Milliseconds()}); err != nil {
			t.Fatal(err)
		}
		as := func(agent, role string, labelled bool, via string) domain.Caller {
			c := bob
			c.Agent, c.Role, c.Labelled, c.Via = agent, role, labelled, via
			return c
		}
		builder := as("builder", "", true, domain.ViaAPIToken)
		lc := e.c.Lifecycle()
		task := e.task(t, domain.Todo)
		lease := e.claim(t, builder, task.ID, 0).LeaseID

		for _, who := range []domain.Caller{
			as("builder", "worker", false, domain.ViaOAuth),
			as("builder", "master", false, domain.ViaOAuth),
			as("builder", "", false, domain.ViaAPIToken), // a clientInfo.name on a default-label token
		} {
			r, err := lc.Claim(e.ctx, who, ClaimTaskReq{ProjectID: e.project, TaskID: task.ID})
			if err == nil {
				t.Fatalf("claim_task as %q (role %q, via %s, not labelled) on builder's lease: handed lease %q (builder's is %q), want CONFLICT_LOCKED",
					who.Agent, who.Role, who.Via, r.LeaseID, lease)
			}
			wantCode(t, err, domain.CodeConflictLocked)
			if got := e.get(t, task.ID); got.Status != domain.InProgress || got.Claim == nil || got.Claim.LeaseID != lease {
				t.Fatalf("builder's task after a refused re-claim by %q via %s: %+v, want builder's lease %q", who.Agent, who.Via, got, lease)
			}
		}
		if got := e.claim(t, builder, task.ID, 0).LeaseID; got != lease {
			t.Fatalf("builder's own re-claim: lease %q, want its own %q", got, lease)
		}

		session := as("claude-code", "", false, domain.ViaAPIToken)
		mine := e.task(t, domain.Todo)
		first := e.claim(t, session, mine.ID, 0).LeaseID
		if got := e.claim(t, session, mine.ID, 0).LeaseID; got != first {
			t.Fatalf("an unlabelled client's re-claim of its own lease: %q, want %q", got, first)
		}
	})
}
