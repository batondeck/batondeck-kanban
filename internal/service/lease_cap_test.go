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
//     master, and cancel_task, move_task and delete_task answer CONFLICT_LOCKED at master.
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
