package service

import (
	"reflect"
	"testing"
	"time"

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

// P-399: cancel_task and move_task, which take no lease id, follow P-320's rule through leaseYields.
// On every engine, on agent-a's live lease: worker bob's agent-b, and worker bob's agentless caller,
// are FORBIDDEN cancel_task (move_task is master tier); master mia's agent-b, and master carol, are
// CONFLICT_LOCKED on cancel_task and on move_task out of IN_PROGRESS; each leaves the task as it
// was. The controls: the lease's own agent cancels and moves; master mia's agentless caller (her
// browser) moves and cancels her agent's live task; a lease that recorded no agent is any caller's
// of its identity; a lapsed lease blocks no one, and reap_stale_leases still reaps one.
func TestCancelAndMoveYieldOnlyToTheLeasesAgent(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		mia := e.member(t, e.project, "mia", domain.RoleMaster)
		carol := e.member(t, e.project, "carol", domain.RoleMaster)
		named := func(c domain.Caller, agent string) domain.Caller {
			c.Agent, c.Labelled, c.Via = agent, true, domain.ViaAPIToken
			return c
		}
		bobA, bobB := named(bob, "agent-a"), named(bob, "agent-b")
		miaA, miaB := named(mia, "agent-a"), named(mia, "agent-b")
		bobBrowser, miaBrowser := bob, mia // a browser session: no agent, Via ""
		lc, ts := e.c.Lifecycle(), e.c.Tasks()

		cancel := func(who domain.Caller, id string) error {
			_, err := lc.Cancel(e.ctx, who, CancelTaskReq{ProjectID: e.project, TaskID: id, Version: e.get(t, id).Version, Reason: "mine now"})
			return err
		}
		move := func(who domain.Caller, id string, to domain.Status) error {
			_, err := ts.Move(e.ctx, who, MoveTaskReq{ProjectID: e.project, TaskID: id, Version: e.get(t, id).Version, ToStatus: to})
			return err
		}
		type snap struct {
			version int64
			status  domain.Status
			claim   store.TaskClaim
			labels  []string
			changes int
		}
		look := func(t *testing.T, id string) snap {
			t.Helper()
			got := e.get(t, id)
			if got.Claim == nil {
				t.Fatalf("%s has no claim: %+v", id, got)
			}
			return snap{got.Version, got.Status, *got.Claim, got.Labels, e.changeCount(t)}
		}
		type try struct {
			step string
			do   func() error
			want domain.Code
		}
		refused := func(t *testing.T, id string, tries []try) {
			t.Helper()
			before := look(t, id)
			for _, x := range tries {
				err := x.do()
				t.Logf("%s on %s: %v", x.step, id, err)
				wantCode(t, err, x.want)
			}
			if after := look(t, id); !reflect.DeepEqual(after, before) {
				t.Fatalf("%s: the task moved: %+v, was %+v", id, after, before)
			}
		}
		allowed := func(t *testing.T, step string, err error, id string, want domain.Status) {
			t.Helper()
			got := e.get(t, id)
			t.Logf("%s on %s: %v -> %s claim %v labels %v", step, id, err, got.Status, got.Claim, got.Labels)
			if err != nil || got.Status != want || got.Claim != nil {
				t.Fatalf("%s on %s: %v, left %s claim %+v; want %s with no claim", step, id, err, got.Status, got.Claim, want)
			}
		}

		// AC1, below master: REVIEW-L4 probe P1, worker bob's agent-b on agent-a's live task.
		w := e.task(t, domain.Todo)
		e.claim(t, bobA, w.ID, 600)
		refused(t, w.ID, []try{
			{"worker agent-b cancel_task", func() error { return cancel(bobB, w.ID) }, domain.CodeForbidden},
			{"worker agentless cancel_task", func() error { return cancel(bobBrowser, w.ID) }, domain.CodeForbidden},
			{"worker agent-b move_task", func() error { return move(bobB, w.ID, domain.Todo) }, domain.CodeForbidden},
		})
		allowed(t, "AC2 worker agent-a cancels its own", cancel(bobA, w.ID), w.ID, domain.Done)

		// AC1, at master: mia's agent-b, and carol, on mia's agent-a's live task.
		m := e.task(t, domain.Todo)
		e.claim(t, miaA, m.ID, 600)
		refused(t, m.ID, []try{
			{"master agent-b cancel_task", func() error { return cancel(miaB, m.ID) }, domain.CodeConflictLocked},
			{"master agent-b move_task to TODO", func() error { return move(miaB, m.ID, domain.Todo) }, domain.CodeConflictLocked},
			{"master agent-b move_task to REVIEW", func() error { return move(miaB, m.ID, domain.Review) }, domain.CodeConflictLocked},
			{"carol cancel_task", func() error { return cancel(carol, m.ID) }, domain.CodeConflictLocked},
			{"carol move_task to TODO", func() error { return move(carol, m.ID, domain.Todo) }, domain.CodeConflictLocked},
		})
		// AC2: the master's browser is the way out of a task its own agent holds.
		allowed(t, "AC2 master agentless move_task", move(miaBrowser, m.ID, domain.Todo), m.ID, domain.Todo)
		own := e.task(t, domain.Todo)
		e.claim(t, miaA, own.ID, 600)
		allowed(t, "AC2 master agent-a moves its own", move(miaA, own.ID, domain.Review), own.ID, domain.Review)
		mc := e.task(t, domain.Todo)
		e.claim(t, miaA, mc.ID, 600)
		allowed(t, "AC2 master agentless cancel_task", cancel(miaBrowser, mc.ID), mc.ID, domain.Done)

		// AC2: a lease that recorded no agent is any caller's of its identity.
		free := e.task(t, domain.Todo)
		bobNone := bob
		bobNone.Via = domain.ViaAPIToken
		e.claim(t, bobNone, free.ID, 600)
		if got := e.get(t, free.ID).Claim; got == nil || got.AgentID != nil {
			t.Fatalf("bob's agentless claim recorded agent %+v", got)
		}
		allowed(t, "AC2 worker agent-b cancels an agentless lease", cancel(bobB, free.ID), free.ID, domain.Done)

		// AC3: a lapsed lease blocks no one; the reaper still reaps one.
		var lapsed []string
		for range 4 {
			x := e.task(t, domain.Todo)
			e.claim(t, miaA, x.ID, 1)
			lapsed = append(lapsed, x.ID)
		}
		e.clock.advance(2 * time.Second)
		allowed(t, "AC3 master agent-b move_task on a lapsed lease", move(miaB, lapsed[0], domain.Todo), lapsed[0], domain.Todo)
		allowed(t, "AC3 master agent-b cancel_task on a lapsed lease", cancel(miaB, lapsed[1]), lapsed[1], domain.Done)
		allowed(t, "AC3 carol move_task on a lapsed lease", move(carol, lapsed[2], domain.Todo), lapsed[2], domain.Todo)
		r, err := lc.Reap(e.ctx, carol, ReapStaleLeasesReq{ProjectID: e.project, BoardID: e.board.ID})
		allowed(t, "AC3 reap_stale_leases", err, lapsed[3], domain.Todo)
		if r.Reaped != 1 {
			t.Fatalf("reap_stale_leases reaped %d, want 1", r.Reaped)
		}
	})
}

// P-399 round 2: delete_task, which takes no lease id and destroys the task with its subtasks, follows
// the same rule through leaseYields. On every engine, on mia's agent-a's live lease: mia's agent-b and
// carol (masters) are CONFLICT_LOCKED, and the task and its subtask are left as they were. The
// controls: agent-a deletes its own; mia's browser and her agentless token delete her agent's live
// task; a lapsed lease blocks neither mia's agent-b nor carol.
func TestDeleteYieldsOnlyToTheLeasesAgent(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		mia := e.member(t, e.project, "mia", domain.RoleMaster)
		carol := e.member(t, e.project, "carol", domain.RoleMaster)
		named := func(c domain.Caller, agent string) domain.Caller {
			c.Agent, c.Labelled, c.Via = agent, true, domain.ViaAPIToken
			return c
		}
		miaA, miaB := named(mia, "agent-a"), named(mia, "agent-b")
		miaBrowser, miaNone := mia, mia // a browser session (Via ""), and a token that names no agent
		miaNone.Via = domain.ViaAPIToken
		del := func(who domain.Caller, id string) error {
			_, err := e.c.Tasks().Delete(e.ctx, who, DeleteTaskReq{ProjectID: e.project, TaskID: id, Version: e.get(t, id).Version})
			return err
		}
		held := func(t *testing.T, secs int64) (string, string) {
			t.Helper()
			x := e.task(t, domain.Todo)
			e.claim(t, miaA, x.ID, secs)
			sub, err := e.c.Context().AddSubtask(e.ctx, e.alice, AddSubtaskReq{ProjectID: e.project, ParentTaskID: x.ID, Title: "child"})
			if err != nil {
				t.Fatal(err)
			}
			return x.ID, sub.Task.ID
		}
		deleted := func(t *testing.T, step string, err error, ids ...string) {
			t.Helper()
			t.Logf("%s on %v: %v", step, ids, err)
			if err != nil {
				t.Fatalf("%s: %v", step, err)
			}
			for _, id := range ids {
				e.tkGone(t, id)
			}
		}

		// AC1: the sibling named master agent, and another identity, are refused; nothing changes.
		id, child := held(t, 600)
		before, beforeChild, changes := e.get(t, id), e.get(t, child), e.changeCount(t)
		if before.Claim == nil || before.Claim.AgentID == nil || *before.Claim.AgentID != "agent-a" {
			t.Fatalf("agent-a's claim recorded %+v", before.Claim)
		}
		for _, who := range []domain.Caller{miaB, carol} {
			err := del(who, id)
			t.Logf("delete_task as %s/%q on agent-a's live %s: %v", who.Identity, who.Agent, id, err)
			wantCode(t, err, domain.CodeConflictLocked)
		}
		if after, afterChild := e.get(t, id), e.get(t, child); !reflect.DeepEqual(after, before) || !reflect.DeepEqual(afterChild, beforeChild) || e.changeCount(t) != changes {
			t.Fatalf("a refused delete changed the task or its subtask: %+v / %+v, was %+v / %+v", after, afterChild, before, beforeChild)
		}
		// AC2: the lease's own agent deletes it, with its subtask.
		deleted(t, "AC2 agent-a deletes its own", del(miaA, id), id, child)
		// AC2: the master's browser, and her token that names no agent, delete her agent's live task.
		id, child = held(t, 600)
		deleted(t, "AC2 master browser delete_task", del(miaBrowser, id), id, child)
		id, child = held(t, 600)
		deleted(t, "AC2 master agentless token delete_task", del(miaNone, id), id, child)

		// AC3: a lapsed lease blocks no one.
		l1, c1 := held(t, 1)
		l2, c2 := held(t, 1)
		e.clock.advance(2 * time.Second)
		deleted(t, "AC3 master agent-b delete_task on a lapsed lease", del(miaB, l1), l1, c1)
		deleted(t, "AC3 carol delete_task on a lapsed lease", del(carol, l2), l2, c2)
	})
}

// P-399 round 3: start_runs, which takes no lease id and drops the parent's lease, follows the same
// rule through leaseYields. On every engine, on mia's agent-a's live lease: mia's agent-b and carol
// (masters) are CONFLICT_LOCKED, and the task keeps its lease, status and version, with no run opened
// and no change written. The controls: agent-a races its own task; mia's browser races her agent's
// live task; a lapsed lease blocks neither mia's agent-b nor carol.
func TestStartRunsYieldsOnlyToTheLeasesAgent(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		mia := e.member(t, e.project, "mia", domain.RoleMaster)
		carol := e.member(t, e.project, "carol", domain.RoleMaster)
		named := func(c domain.Caller, agent string) domain.Caller {
			c.Agent, c.Labelled, c.Via = agent, true, domain.ViaAPIToken
			return c
		}
		miaA, miaB := named(mia, "agent-a"), named(mia, "agent-b")
		miaBrowser := mia // a browser session: no agent, Via ""
		start := func(who domain.Caller, id string) error {
			_, err := e.c.Runs().Start(e.ctx, who, StartRunsReq{ProjectID: e.project, TaskID: id, Version: e.get(t, id).Version, Count: 2})
			return err
		}
		held := func(t *testing.T, secs int64) string {
			t.Helper()
			x := e.task(t, domain.Todo)
			e.claim(t, miaA, x.ID, secs)
			return x.ID
		}
		raced := func(t *testing.T, step string, err error, id string) {
			t.Helper()
			got := e.get(t, id)
			runs := e.runIDs(t, id)
			t.Logf("%s on %s: %v -> %s racing %v claim %v runs %v", step, id, err, got.Status, got.Racing, got.Claim, runs)
			if err != nil || !got.Racing || got.Claim != nil || got.Status != domain.Todo || len(runs) != 2 {
				t.Fatalf("%s on %s: %v, left %s racing %v claim %+v runs %v; want a racing TODO parent with no claim and 2 runs",
					step, id, err, got.Status, got.Racing, got.Claim, runs)
			}
		}

		// AC10: the sibling named master agent, and another identity, are refused; nothing changes.
		id := held(t, 600)
		before, changes, tasks := e.get(t, id), e.changeCount(t), e.taskCount(t)
		if before.Claim == nil || before.Claim.AgentID == nil || *before.Claim.AgentID != "agent-a" {
			t.Fatalf("agent-a's claim recorded %+v", before.Claim)
		}
		for _, who := range []domain.Caller{miaB, carol} {
			err := start(who, id)
			t.Logf("start_runs as %s/%q on agent-a's live %s: %v", who.Identity, who.Agent, id, err)
			if be := wantCode(t, err, domain.CodeConflictLocked); be.Details["taskId"] != id || be.Details["owner"] != mia.Identity {
				t.Fatalf("CONFLICT_LOCKED names %v, want %s held by %s", be.Details, id, mia.Identity)
			}
		}
		if after := e.get(t, id); !reflect.DeepEqual(after, before) || e.changeCount(t) != changes || e.taskCount(t) != tasks || len(e.runIDs(t, id)) != 0 {
			t.Fatalf("a refused start_runs changed the board: %+v, was %+v; changes %d, was %d; tasks %d, was %d",
				after, before, e.changeCount(t), changes, e.taskCount(t), tasks)
		}
		// AC11: the lease's own agent, and the master's browser, race the task.
		raced(t, "AC11 agent-a start_runs on its own", start(miaA, id), id)
		id = held(t, 600)
		raced(t, "AC11 master browser start_runs", start(miaBrowser, id), id)
		// AC11: a lapsed lease blocks no one.
		l1, l2 := held(t, 1), held(t, 1)
		e.clock.advance(2 * time.Second)
		raced(t, "AC11 master agent-b start_runs on a lapsed lease", start(miaB, l1), l1)
		raced(t, "AC11 carol start_runs on a lapsed lease", start(carol, l2), l2)
	})
}

// P-416: delete_task removes the task's subtasks and runs too, so a live lease on any of them that
// does not yield to the caller refuses the delete, though the task itself is unleased. On every
// engine: mia's agent-a holds a subtask (and, in a racing tree, a run); mia's agent-b and carol are
// CONFLICT_LOCKED naming the held task, and the tree, the task count and the change log are as they
// were. The controls: agent-a deletes the tree whose subtask it holds; a lapsed subtask lease blocks
// neither mia's agent-b nor carol.
func TestDeleteRefusesAHeldSubtask(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		mia := e.member(t, e.project, "mia", domain.RoleMaster)
		carol := e.member(t, e.project, "carol", domain.RoleMaster)
		named := func(c domain.Caller, agent string) domain.Caller {
			c.Agent, c.Labelled, c.Via = agent, true, domain.ViaAPIToken
			return c
		}
		miaA, miaB := named(mia, "agent-a"), named(mia, "agent-b")
		del := func(who domain.Caller, id string) error {
			_, err := e.c.Tasks().Delete(e.ctx, who, DeleteTaskReq{ProjectID: e.project, TaskID: id, Version: e.get(t, id).Version})
			return err
		}
		// tree is an unleased parent with a subtask agent-a holds for secs.
		tree := func(t *testing.T, secs int64) (string, string) {
			t.Helper()
			x := e.task(t, domain.Todo)
			sub, err := e.c.Context().AddSubtask(e.ctx, e.alice, AddSubtaskReq{ProjectID: e.project, ParentTaskID: x.ID, Title: "child"})
			if err != nil {
				t.Fatal(err)
			}
			e.claim(t, miaA, sub.Task.ID, secs)
			return x.ID, sub.Task.ID
		}
		refused := func(t *testing.T, id, heldID string, ids ...string) {
			t.Helper()
			before := map[string]store.Task{}
			for _, x := range ids {
				before[x] = e.get(t, x)
			}
			changes, tasks := e.changeCount(t), e.taskCount(t)
			if p := e.get(t, id); p.Claim != nil {
				t.Fatalf("the parent %s is leased: %+v", id, p.Claim)
			}
			for _, who := range []domain.Caller{miaB, carol} {
				err := del(who, id)
				t.Logf("delete_task as %s/%q on %s, whose %s agent-a holds: %v", who.Identity, who.Agent, id, heldID, err)
				if be := wantCode(t, err, domain.CodeConflictLocked); be.Details["taskId"] != heldID || be.Details["owner"] != mia.Identity {
					t.Fatalf("CONFLICT_LOCKED names %v, want %s held by %s", be.Details, heldID, mia.Identity)
				}
			}
			for _, x := range ids {
				if after := e.get(t, x); !reflect.DeepEqual(after, before[x]) {
					t.Fatalf("a refused delete changed %s: %+v, was %+v", x, after, before[x])
				}
			}
			if e.changeCount(t) != changes || e.taskCount(t) != tasks {
				t.Fatalf("a refused delete wrote: changes %d, was %d; tasks %d, was %d", e.changeCount(t), changes, e.taskCount(t), tasks)
			}
		}
		deleted := func(t *testing.T, step string, err error, ids ...string) {
			t.Helper()
			t.Logf("%s on %v: %v", step, ids, err)
			if err != nil {
				t.Fatalf("%s: %v", step, err)
			}
			for _, id := range ids {
				e.tkGone(t, id)
			}
		}

		// AC12: a held subtask under an unleased parent.
		id, child := tree(t, 600)
		refused(t, id, child, id, child)
		// AC12: a held run under a racing parent.
		rp := e.task(t, domain.Todo)
		if _, err := e.c.Runs().Start(e.ctx, e.alice, StartRunsReq{ProjectID: e.project, TaskID: rp.ID, Version: e.get(t, rp.ID).Version, Count: 2}); err != nil {
			t.Fatal(err)
		}
		runs := e.runIDs(t, rp.ID)
		e.claim(t, miaA, runs[1], 600)
		refused(t, rp.ID, runs[1], append([]string{rp.ID}, runs...)...)
		// AC13: the subtask's own agent deletes the tree; so does a racing tree's run holder.
		deleted(t, "AC13 agent-a deletes the tree whose subtask it holds", del(miaA, id), id, child)
		deleted(t, "AC13 agent-a deletes the race whose run it holds", del(miaA, rp.ID), append([]string{rp.ID}, runs...)...)
		// AC13: a lapsed subtask lease blocks no one.
		l1, c1 := tree(t, 1)
		l2, c2 := tree(t, 1)
		e.clock.advance(2 * time.Second)
		deleted(t, "AC13 master agent-b delete_task over a lapsed subtask lease", del(miaB, l1), l1, c1)
		deleted(t, "AC13 carol delete_task over a lapsed subtask lease", del(carol, l2), l2, c2)
	})
}
