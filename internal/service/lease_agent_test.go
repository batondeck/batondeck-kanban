package service

import (
	"reflect"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// P-320 (G6): a lease that recorded an agent is that agent's. On every engine, another agent of the
// same identity holding the lease id, and that identity with no agent named, get LEASE_EXPIRED on
// heartbeat, release, complete, block, fail, handoff and orchestrate_subtasks, and the task is left
// as it was. The claiming agent acts; a lease that recorded no agent is the owning identity's on any
// of its callers; another identity is refused as before. bob and carol are masters, so
// orchestrate_subtasks reaches its lease check and a master is seen to get no pass.
func TestALeaseOutcomeIsItsAgentsOwn(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		bob := e.member(t, e.project, "bob", domain.RoleMaster)
		carol := e.member(t, e.project, "carol", domain.RoleMaster)
		named := func(c domain.Caller, agent string) domain.Caller {
			c.Agent, c.Labelled, c.Via = agent, true, domain.ViaAPIToken
			return c
		}
		agentA, agentB := named(bob, "agent-a"), named(bob, "agent-b")
		bobNone := bob
		bobNone.Via = domain.ViaAPIToken
		lc, rk := e.c.Lifecycle(), e.c.Ranking()

		type outcome struct {
			name string
			do   func(who domain.Caller, taskID, leaseID string) error
		}
		outcomes := []outcome{
			{"heartbeat_task", func(who domain.Caller, id, lease string) error {
				_, err := lc.Heartbeat(e.ctx, who, HeartbeatTaskReq{ProjectID: e.project, TaskID: id, LeaseID: lease, LeaseSeconds: 7200})
				return err
			}},
			{"release_task", func(who domain.Caller, id, lease string) error {
				_, err := lc.Release(e.ctx, who, ReleaseTaskReq{ProjectID: e.project, TaskID: id, LeaseID: lease})
				return err
			}},
			{"complete_task", func(who domain.Caller, id, lease string) error {
				_, err := lc.Complete(e.ctx, who, CompleteTaskReq{ProjectID: e.project, TaskID: id, LeaseID: lease})
				return err
			}},
			{"block_task", func(who domain.Caller, id, lease string) error {
				_, err := lc.Block(e.ctx, who, BlockTaskReq{ProjectID: e.project, TaskID: id, LeaseID: lease, Reason: "stuck"})
				return err
			}},
			{"fail_task", func(who domain.Caller, id, lease string) error {
				_, err := lc.Fail(e.ctx, who, FailTaskReq{ProjectID: e.project, TaskID: id, LeaseID: lease, Reason: "broke"})
				return err
			}},
			{"handoff_task", func(who domain.Caller, id, lease string) error {
				_, err := lc.Handoff(e.ctx, who, HandoffTaskReq{ProjectID: e.project, TaskID: id, LeaseID: lease, ToAgent: "reviewer-bot", MemoryNote: "over"})
				return err
			}},
			{"orchestrate_subtasks", func(who domain.Caller, id, lease string) error {
				_, err := rk.Orchestrate(e.ctx, who, OrchestrateSubtasksReq{ProjectID: e.project, ParentTaskID: id, LeaseID: lease, Children: []OrchestrateChild{{Title: "child"}}})
				return err
			}},
		}
		type snap struct {
			version int64
			status  domain.Status
			claim   store.TaskClaim
			changes int
		}
		look := func(t *testing.T, id string) snap {
			t.Helper()
			got := e.get(t, id)
			if got.Claim == nil {
				t.Fatalf("%s has no claim: %+v", id, got)
			}
			return snap{got.Version, got.Status, *got.Claim, e.changeCount(t)}
		}
		refusedAll := func(t *testing.T, step string, who domain.Caller, id, lease string) {
			t.Helper()
			before := look(t, id)
			for _, o := range outcomes {
				err := o.do(who, id, lease)
				t.Logf("%s %s as %s/%q: %v", step, o.name, who.Identity, who.Agent, err)
				wantCode(t, err, domain.CodeLeaseExpired)
			}
			if after := look(t, id); !reflect.DeepEqual(after, before) {
				t.Fatalf("%s: the task moved: %+v, was %+v", step, after, before)
			}
		}

		// AC1: agent-a claims; agent-b, holding the lease id, is refused every outcome.
		task := e.task(t, domain.Todo)
		c := e.claim(t, agentA, task.ID, 600)
		if got := e.get(t, task.ID).Claim; got == nil || got.AgentID == nil || *got.AgentID != "agent-a" {
			t.Fatalf("agent-a's claim recorded agent %+v", got)
		}
		refusedAll(t, "AC1", agentB, task.ID, c.LeaseID)
		// AC2: bob with no agent named, and carol, another identity, are refused too.
		refusedAll(t, "AC2 agentless", bobNone, task.ID, c.LeaseID)
		refusedAll(t, "AC2 carol", carol, task.ID, c.LeaseID)
		// The control: agent-a heartbeats, then completes.
		if err := outcomes[0].do(agentA, task.ID, c.LeaseID); err != nil {
			t.Fatalf("AC2 agent-a heartbeats its lease: %v", err)
		}
		if err := outcomes[2].do(agentA, task.ID, c.LeaseID); err != nil {
			t.Fatalf("AC2 agent-a completes: %v", err)
		}
		if got := e.get(t, task.ID); got.Claim != nil || (got.Status != domain.Done && got.Status != domain.Review) {
			t.Fatalf("AC2 agent-a's complete left %s %+v", got.Status, got.Claim)
		}
		t.Logf("AC2 agent-a heartbeat and complete: %s", e.get(t, task.ID).Status)

		// AC2: a lease that recorded no agent is the owning identity's on any of its callers.
		free := e.task(t, domain.Todo)
		fc := e.claim(t, bobNone, free.ID, 600)
		if got := e.get(t, free.ID).Claim; got == nil || got.AgentID != nil {
			t.Fatalf("bob's agentless claim recorded agent %+v", got)
		}
		refusedAll(t, "AC2 carol on bob's agentless lease", carol, free.ID, fc.LeaseID)
		for _, i := range []int{0, 1} {
			if err := outcomes[i].do(agentB, free.ID, fc.LeaseID); err != nil {
				t.Fatalf("AC2 agent-b %s on bob's agentless lease: %v", outcomes[i].name, err)
			}
			t.Logf("AC2 agent-b %s on bob's agentless lease: ok", outcomes[i].name)
		}
		if got := e.get(t, free.ID); got.Claim != nil || got.Status != domain.Todo {
			t.Fatalf("AC2 agent-b's release left %s %+v", got.Status, got.Claim)
		}
	})
}
