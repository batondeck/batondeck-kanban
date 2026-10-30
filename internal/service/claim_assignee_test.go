package service

import (
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// P-334, P-297 (owner ruling 2026-10-30): claim_task {taskId} applies claim_next's assignee rule,
// ownAssignee, inside its transaction. Below master, a task assigned to another member's id, to
// another user's id or login, or to another identity's label (live, revoked or expired) is
// FORBIDDEN and nothing is written; an unassigned task, the caller's own id or login (any case), its
// own label on that label's token and a string naming nobody are claimed, and the admin claims any.
// A task reassigned to another after the caller claimed it is not re-claimed, while the lease the
// caller holds still heartbeats.
func TestClaimTaskByIDKeepsTheAssigneeRule(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		carol := e.member(t, e.project, "carol", domain.RoleWorker)
		now := e.clock.now().UnixMilli()
		for _, u := range []store.User{
			{ID: "bob", Email: "bob@example.com", DisplayName: "bob", PasswordHash: "x", CreatedAt: now},
			// dave is an account, not a member of this project.
			{ID: "U-dave", Email: "dave@example.com", DisplayName: "dave", PasswordHash: "x", CreatedAt: now},
		} {
			if err := e.st.CreateUser(e.ctx, u); err != nil {
				t.Fatal(err)
			}
		}
		hour := time.Hour.Milliseconds()
		for _, tok := range []store.Token{
			{ID: "tok-a", IdentityID: "bob", Subject: "bob", SecretHash: "h-a", Label: "agent-a", CreatedAt: now, ExpiresAt: now + hour},
			{ID: "tok-c", IdentityID: "carol", Subject: "carol", SecretHash: "h-c", Label: "agent-c", CreatedAt: now, ExpiresAt: now + hour},
			{ID: "tok-dead", IdentityID: "carol", Subject: "carol", SecretHash: "h-d", Label: "agent-dead", CreatedAt: now, ExpiresAt: now + hour},
			{ID: "tok-old", IdentityID: "carol", Subject: "carol", SecretHash: "h-o", Label: "agent-old", CreatedAt: now - 2*hour, ExpiresAt: now - hour},
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
		lc := e.c.Lifecycle()
		assigned := func(t *testing.T, to string) store.Task {
			t.Helper()
			task := e.task(t, domain.Todo)
			if to == "" {
				return task
			}
			got, err := e.st.UpdateTask(e.ctx, e.project, task.ID, task.Version, store.TaskPatch{Assignee: &store.Nullable[string]{Value: ptr(to)}, UpdatedBy: "alice", UpdatedAt: now})
			if err != nil {
				t.Fatal(err)
			}
			return got
		}

		// AC1: each refused below master, with no claim, no event and no change row written.
		refused := map[string]store.Task{}
		for _, c := range []struct {
			name, to, field string
			who             domain.Caller
		}{
			{"another member's id", "carol", "", bob},
			{"another user's id", "U-dave", "", bob},
			{"another user's login", "dave@example.com", "", bob},
			{"another user's login, upper case", "DAVE@EXAMPLE.COM", "", bob},
			{"another identity's live label", "agent-c", "agent-c", agentA},
			{"another identity's revoked label", "agent-dead", "agent-dead", agentA},
			{"another identity's expired label", "agent-old", "agent-old", bob},
			// A browser session names no agent, so it is not agent-a's token (P-277).
			{"its own label, from no token", "agent-a", "agent-a", bob},
		} {
			task := assigned(t, c.to)
			events, changes := len(e.events(t, task.ID)), e.changeCount(t)
			_, err := lc.Claim(e.ctx, c.who, ClaimTaskReq{ProjectID: e.project, TaskID: task.ID})
			t.Logf("AC1 %s (%q) as %q: %v", c.name, c.to, c.who.Agent, err)
			be := wantCode(t, err, domain.CodeForbidden)
			if be.Details["field"] != "assignee" || be.Details["requiredRole"] != domain.RoleMaster {
				t.Fatalf("AC1 %s: details %v", c.name, be.Details)
			}
			if c.field != "" && be.Details["label"] != c.field {
				t.Fatalf("AC1 %s: details %v, want label %s", c.name, be.Details, c.field)
			}
			got := e.get(t, task.ID)
			if got.Claim != nil || got.Status != domain.Todo || got.Version != task.Version {
				t.Fatalf("AC1 %s: the refused claim wrote the task: %+v", c.name, got)
			}
			if n, m := len(e.events(t, task.ID)), e.changeCount(t); n != events || m != changes {
				t.Fatalf("AC1 %s: events %d -> %d, change rows %d -> %d", c.name, events, n, changes, m)
			}
			refused[c.name] = task
		}

		// AC2: the controls claim.
		for _, c := range []struct {
			name, to string
			who      domain.Caller
		}{
			{"unassigned", "", bob},
			{"its own id", "bob", bob},
			{"its own login, upper case", "BOB@EXAMPLE.COM", bob},
			{"its own label on its labelled token", "agent-a", agentA},
			{"a string naming nobody", "reviewer-bot", bob},
		} {
			task := assigned(t, c.to)
			r, err := lc.Claim(e.ctx, c.who, ClaimTaskReq{ProjectID: e.project, TaskID: task.ID})
			if err != nil || r.LeaseID == "" || r.Task.Status != domain.InProgress {
				t.Fatalf("AC2 %s (%q) as %q: %+v %v", c.name, c.to, c.who.Agent, r, err)
			}
			t.Logf("AC2 %s (%q) as %q: claimed, lease %s", c.name, c.to, c.who.Agent, r.LeaseID)
		}
		for name, task := range refused {
			if r, err := lc.Claim(e.ctx, e.alice, ClaimTaskReq{ProjectID: e.project, TaskID: task.ID}); err != nil || r.LeaseID == "" {
				t.Fatalf("AC2 the admin claims the task refused as %s: %+v %v", name, r, err)
			}
		}
		t.Logf("AC2 the admin claimed all %d refused tasks", len(refused))

		// AC3: bob claims, the admin reassigns the task to carol, and bob's re-claim is refused while
		// his lease still heartbeats.
		task := assigned(t, "")
		c := e.claim(t, bob, task.ID, 600)
		cur := e.get(t, task.ID)
		if _, err := e.c.Tasks().Update(e.ctx, e.alice, UpdateTaskReq{ProjectID: e.project, TaskID: task.ID, Version: cur.Version, Patch: TaskPatch{Assignee: Nullable[string]{Set: true, V: "carol"}}}); err != nil {
			t.Fatalf("AC3 the admin reassigns to carol: %v", err)
		}
		_, err := lc.Claim(e.ctx, bob, ClaimTaskReq{ProjectID: e.project, TaskID: task.ID, LeaseSeconds: 3600})
		t.Logf("AC3 bob's re-claim of the task now carol's: %v", err)
		wantCode(t, err, domain.CodeForbidden)
		if got := e.get(t, task.ID); got.Claim == nil || got.Claim.LeaseID != c.LeaseID || got.Claim.ExpiresAt != cur.Claim.ExpiresAt {
			t.Fatalf("AC3: the refused re-claim moved the lease: %+v, was %+v", got.Claim, cur.Claim)
		}
		hb, err := lc.Heartbeat(e.ctx, bob, HeartbeatTaskReq{ProjectID: e.project, TaskID: task.ID, LeaseID: c.LeaseID, LeaseSeconds: 3600})
		if err != nil {
			t.Fatalf("AC3 bob's heartbeat on his lease: %v", err)
		}
		t.Logf("AC3 bob's heartbeat: expires %s", hb.ExpiresAt)
		if _, err := lc.Claim(e.ctx, carol, ClaimTaskReq{ProjectID: e.project, TaskID: task.ID}); err == nil {
			t.Fatal("AC3: carol claimed a task bob's live lease holds")
		}
	})
}
