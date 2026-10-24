package service

import (
	"context"
	"encoding/json"
	"log"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The R1.5 tests run on memstore and on both engines (engineCases): the Postgres leg skips without
// BATONDECK_TEST_POSTGRES_DSN. The Core runs on a settable clock, which the store judges leases by
// (every lease decision takes the service's now), so expiry needs no sleep.

type lcEnv struct {
	*env
	clock *clock
}

func newLC(t *testing.T, st store.Store) *lcEnv {
	t.Helper()
	e := newEnv(t, st)
	clk := &clock{t: time.Now()}
	_ = e.c.Close(context.Background())
	e.c = New(st, Options{Logger: log.New(&logBuf{}, "", 0), Now: clk.now, ToolCallFlush: -1})
	t.Cleanup(func() { _ = e.c.Close(context.Background()) })
	return &lcEnv{env: e, clock: clk}
}

func onLC(t *testing.T, fn func(t *testing.T, e *lcEnv)) {
	for _, ec := range engineCases {
		t.Run(ec.name, func(t *testing.T) { fn(t, newLC(t, ec.open(t))) })
	}
}

func (e *lcEnv) settings(t *testing.T, kv map[string]any) {
	t.Helper()
	patch := store.SettingsPatch{}
	for k, v := range kv {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		patch[k] = raw
	}
	if _, err := e.st.UpdateSettings(e.ctx, e.project, patch); err != nil {
		t.Fatal(err)
	}
}

// policy3 poisons on the third failure; no jitter, so the backoff is exact.
var policy3 = domain.RetryPolicy{BaseDelayMs: 1000, MaxDelayMs: 60_000, Jitter: 0, PoisonAfterAttempts: 3}

func (e *lcEnv) claim(t *testing.T, who domain.Caller, id string, secs int64) ClaimTaskResp {
	t.Helper()
	r, err := e.c.Lifecycle().Claim(e.ctx, who, ClaimTaskReq{ProjectID: e.project, TaskID: id, LeaseSeconds: secs})
	if err != nil {
		t.Fatalf("claim %s: %v", id, err)
	}
	return r
}

func (e *lcEnv) dependOn(t *testing.T, blocker, dependent string) {
	t.Helper()
	if _, err := e.c.Context().AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: blocker, ToTaskID: dependent}); err != nil {
		t.Fatal(err)
	}
}

// lastChange is the newest change-log row and its payload.
func (e *lcEnv) lastChange(t *testing.T) (store.Change, changePayload) {
	t.Helper()
	ch, err := e.st.ReadChanges(e.ctx, 0, 1000)
	if err != nil || len(ch) == 0 {
		t.Fatalf("changes: %v %d", err, len(ch))
	}
	// The last doorbell or change row: a notification row (R2.3) is the recipient's feed, not one.
	last := ch[len(ch)-1]
	for i := len(ch) - 1; i >= 0 && last.Kind == ChangeKindNotification; i-- {
		last = ch[i]
	}
	var p changePayload
	_ = json.Unmarshal(last.Payload, &p)
	return last, p
}

func (e *lcEnv) changeCount(t *testing.T) int {
	t.Helper()
	ch, err := e.st.ReadChanges(e.ctx, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return len(ch)
}

func reasonIs(task store.Task, r string) bool { return reasonOf(task) == r }

// AC1: three failures under poisonAfterAttempts 3 poison the task (BLOCKED, reason poison,
// claimAttempts 3, label poisoned, one task.poisoned event), and ClaimNext never returns it while it
// returns the other TODO task (the control). retryable:false poisons at the first failure.
func TestFailPoisonsAtTheThresholdAndClaimNextSkipsIt(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		e.settings(t, map[string]any{"retryPolicy": policy3})
		lc := e.c.Lifecycle()
		task := e.task(t, domain.Todo)
		for i := 1; i <= 3; i++ {
			c := e.claim(t, e.alice, task.ID, 0)
			r, err := lc.Fail(e.ctx, e.alice, FailTaskReq{ProjectID: e.project, TaskID: task.ID, LeaseID: c.LeaseID, Reason: "boom"})
			if err != nil {
				t.Fatalf("fail %d: %v", i, err)
			}
			got := e.get(t, task.ID)
			t.Logf("fail %d: status %s reason %q attempts %d nextEligibleAt %v labels %v", i, got.Status, reasonOf(got), got.ClaimAttempts, got.NextEligibleAt, got.Labels)
			if i < 3 {
				want := e.clock.now().UnixMilli() + 1000<<(i-1)
				if got.Status != domain.Todo || got.ClaimAttempts != int64(i) || got.NextEligibleAt == nil || *got.NextEligibleAt != want || got.Claim != nil {
					t.Fatalf("fail %d: %+v, want TODO attempts %d nextEligibleAt %d", i, got, i, want)
				}
				continue
			}
			if got.Status != domain.Blocked || !reasonIs(got, domain.BlockedReasonPoison) || got.ClaimAttempts != 3 ||
				!slices.Contains(got.Labels, domain.LabelPoisoned) || got.NextEligibleAt != nil || got.Claim != nil || got.ColumnID != e.column(t, domain.Blocked) {
				t.Fatalf("third fail: %+v", got)
			}
			if r.Task.Status != domain.Blocked {
				t.Fatalf("response task %s", r.Task.Status)
			}
		}
		evs := e.events(t, task.ID)
		if n, f := len(ofType(evs, domain.EventTaskPoisoned)), len(ofType(evs, domain.EventTaskFailed)); n != 1 || f != 2 {
			t.Fatalf("task.poisoned %d task.failed %d, want 1 and 2", n, f)
		}
		other := e.task(t, domain.Todo)
		next, err := lc.ClaimNext(e.ctx, e.alice, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID})
		if err != nil {
			t.Fatal(err)
		}
		if next.Task == nil || next.Task.ID != other.ID {
			t.Fatalf("claim_next = %+v, want the control %s", next.Task, other.ID)
		}
		rowsBefore := e.changeCount(t)
		next, err = lc.ClaimNext(e.ctx, e.alice, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID})
		if rows := e.changeCount(t) - rowsBefore; rows != 0 {
			t.Fatalf("an empty claim_next wrote %d change rows", rows)
		}
		if err != nil || next.Task != nil || next.LeaseID != nil {
			t.Fatalf("claim_next with only the poisoned task left = %+v, %v; want no task", next.Task, err)
		}
		t.Logf("claim_next: control %s won, then no task (poisoned %s skipped)", other.ID, task.ID)

		// retryable:false poisons at once.
		once := e.task(t, domain.Todo)
		c := e.claim(t, e.alice, once.ID, 0)
		if _, err := lc.Fail(e.ctx, e.alice, FailTaskReq{ProjectID: e.project, TaskID: once.ID, LeaseID: c.LeaseID, Reason: "fatal", Retryable: Some(false)}); err != nil {
			t.Fatal(err)
		}
		got := e.get(t, once.ID)
		if !domain.IsPoisoned(got.Status, reasonOf(got)) || got.ClaimAttempts != 1 {
			t.Fatalf("retryable:false: %+v", got)
		}
	})
}

// AC2's claim half: once a master un-poisons the task (SetStatus Unpoison, the write R1.3's move_task
// makes), ClaimNext returns it with claimAttempts 0. The move_task gate itself is R1.3's; here its
// posture is shown refusing a worker and admitting a master.
func TestUnpoisonedTaskIsClaimableAgain(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		e.settings(t, map[string]any{"retryPolicy": policy3})
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		carol := e.member(t, e.project, "carol", domain.RoleMaster)
		lc := e.c.Lifecycle()
		task := e.task(t, domain.Todo)
		c := e.claim(t, bob, task.ID, 0)
		if _, err := lc.Fail(e.ctx, bob, FailTaskReq{ProjectID: e.project, TaskID: task.ID, LeaseID: c.LeaseID, Reason: "x", Retryable: Some(false)}); err != nil {
			t.Fatal(err)
		}
		_, err := e.c.authorize(e.ctx, bob, e.project, "move_task", domain.GrantSubject{ToStatus: domain.Todo}, true)
		wantCode(t, err, domain.CodeForbidden)
		if _, err := e.c.authorize(e.ctx, carol, e.project, "move_task", domain.GrantSubject{ToStatus: domain.Todo}, true); err != nil {
			t.Fatalf("master move_task posture: %v", err)
		}
		poisoned := e.get(t, task.ID)
		if _, err := e.st.SetStatus(e.ctx, e.project, task.ID, poisoned.Version, store.StatusChange{Status: domain.Todo, ColumnID: e.column(t, domain.Todo), Unpoison: true, UpdatedBy: "carol", UpdatedAt: time.Now().UnixMilli()}); err != nil {
			t.Fatal(err)
		}
		next, err := lc.ClaimNext(e.ctx, bob, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID})
		if err != nil || next.Task == nil || next.Task.ID != task.ID || next.Task.ClaimAttempts != 0 || slices.Contains(next.Task.Labels, domain.LabelPoisoned) {
			t.Fatalf("claim_next after un-poison = %+v, %v", next.Task, err)
		}
		t.Logf("worker move_task posture FORBIDDEN, master admitted; after un-poison claim_next won %s with claimAttempts %v", next.Task.ID, next.Task.ClaimAttempts)
	})
}

// AC3: the last dependency completing through Complete leaves a poisoned dependent BLOCKED with its
// reason (the refusal) and moves a plain blocked one to TODO (the control).
func TestCompleteUnblocksDependentsButNeverAPoisonedOne(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		e.settings(t, map[string]any{"reviewEnabled": false})
		blocker := e.task(t, domain.Todo)
		poisoned := e.blocked(t, domain.BlockedReasonPoison)
		plain := e.blocked(t, "waiting")
		e.dependOn(t, blocker.ID, poisoned.ID)
		e.dependOn(t, blocker.ID, plain.ID)
		c := e.claim(t, e.alice, blocker.ID, 0)
		r, err := e.c.Lifecycle().Complete(e.ctx, e.alice, CompleteTaskReq{ProjectID: e.project, TaskID: blocker.ID, LeaseID: c.LeaseID, Deliverable: "done it"})
		if err != nil {
			t.Fatal(err)
		}
		if r.Task.Status != domain.Done || r.Task.Claim != nil || r.Task.Deliverable == nil || *r.Task.Deliverable != "done it" {
			t.Fatalf("completed: %+v", r.Task)
		}
		p, n := e.get(t, poisoned.ID), e.get(t, plain.ID)
		t.Logf("poisoned dependent: %s reason %q blockedBy %v; plain dependent: %s blockedBy %v", p.Status, reasonOf(p), p.BlockedBy, n.Status, n.BlockedBy)
		if p.Status != domain.Blocked || !reasonIs(p, domain.BlockedReasonPoison) || len(p.BlockedBy) != 0 {
			t.Fatalf("poison refusal: %+v", p)
		}
		if n.Status != domain.Todo || len(n.BlockedBy) != 0 || n.ColumnID != e.column(t, domain.Todo) {
			t.Fatalf("control: %+v", n)
		}
		for id, auto := range map[string]bool{poisoned.ID: false, plain.ID: true} {
			evs := ofType(e.events(t, id), domain.EventTaskDependencyCleared)
			if len(evs) != 1 || !strings.Contains(string(evs[0].Data), `"autoReady":`+map[bool]string{true: "true", false: "false"}[auto]) {
				t.Fatalf("%s dependency_cleared %v", id, evs)
			}
		}
	})
}

// AC4: reviewEnabled on, Complete lands REVIEW and the dependent stays blocked on it; off, DONE and
// the dependent unblocks in the same call. The REVIEW handover assigns and rings the reviewer.
func TestCompleteLandsOnReviewOrDone(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		carol := e.member(t, e.project, "carol", domain.RoleMaster)
		lc := e.c.Lifecycle()
		blocker, dep := e.task(t, domain.Todo), e.blocked(t, "")
		e.dependOn(t, blocker.ID, dep.ID)
		c := e.claim(t, e.alice, blocker.ID, 0)
		r, err := lc.Complete(e.ctx, e.alice, CompleteTaskReq{ProjectID: e.project, TaskID: blocker.ID, LeaseID: c.LeaseID, Reviewer: carol.Identity})
		if err != nil {
			t.Fatal(err)
		}
		d := e.get(t, dep.ID)
		ch, p := e.lastChange(t)
		t.Logf("reviewEnabled on: %s handover %+v; dependent %s blockedBy %v; doorbell %s scope %s", r.Task.Status, r.Handover, d.Status, d.BlockedBy, p.Kind, ch.Scope)
		if r.Task.Status != domain.Review || r.Task.Assignee == nil || *r.Task.Assignee != "carol" || r.Task.Review == nil ||
			r.Task.Review.Reviewer == nil || *r.Task.Review.Reviewer != "carol" || r.Handover.Reviewer == nil || r.Handover.Status != domain.Review {
			t.Fatalf("review landing: %+v %+v", r.Task, r.Handover)
		}
		if d.Status != domain.Blocked || !slices.Equal(d.BlockedBy, []string{blocker.ID}) {
			t.Fatalf("dependent moved on REVIEW: %+v", d)
		}
		if p.Kind != DoorbellAssigned || ch.Scope != "carol" || p.TaskID != blocker.ID || p.Tool != "complete_task" {
			t.Fatalf("reviewer doorbell %+v scope %s", p, ch.Scope)
		}
		if rows := e.assignedRows(t); len(rows) != 1 || rows[0] != "complete_task@carol" {
			t.Fatalf("assigned rows %v, want exactly the reviewer's from complete_task", rows)
		}
		if len(ofType(e.events(t, blocker.ID), domain.EventTaskReviewRequested)) != 1 {
			t.Fatal("no task.review_requested")
		}

		e.settings(t, map[string]any{"reviewEnabled": false})
		b2, d2 := e.task(t, domain.Todo), e.blocked(t, "")
		e.dependOn(t, b2.ID, d2.ID)
		c2 := e.claim(t, e.alice, b2.ID, 0)
		r2, err := lc.Complete(e.ctx, e.alice, CompleteTaskReq{ProjectID: e.project, TaskID: b2.ID, LeaseID: c2.LeaseID})
		if err != nil {
			t.Fatal(err)
		}
		got := e.get(t, d2.ID)
		t.Logf("reviewEnabled off: %s; dependent %s", r2.Task.Status, got.Status)
		if r2.Task.Status != domain.Done || got.Status != domain.Todo || r2.Handover != (Handover{}) {
			t.Fatalf("done landing %s, dependent %s, handover %+v", r2.Task.Status, got.Status, r2.Handover)
		}
	})
}

// A reviewer who is not a member still gets the ticket (agent names route by assignment) but no
// recorded handover, and a warning says so; the doorbell rings the agent scope.
func TestCompleteWithANonMemberReviewerWarns(t *testing.T) {
	e := newLC(t, engineCases[0].open(t))
	task := e.task(t, domain.Todo)
	c := e.claim(t, e.alice, task.ID, 0)
	r, err := e.c.Lifecycle().Complete(e.ctx, e.alice, CompleteTaskReq{ProjectID: e.project, TaskID: task.ID, LeaseID: c.LeaseID, Reviewer: "qa-bot"})
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := e.lastChange(t)
	if len(r.Warnings) != 1 || r.Task.Review.Reviewer != nil || *r.Task.Assignee != "qa-bot" || ch.Scope != AgentScope("qa-bot") {
		t.Fatalf("warnings %v review %+v assignee %v scope %s", r.Warnings, r.Task.Review, r.Task.Assignee, ch.Scope)
	}
}

// AC5: Reap on two expired leases, one past the poison threshold, yields one TODO-with-backoff and
// one BLOCKED-poison; the summary's bucket is "poisoned", and so is next_task's inbox bucket.
func TestReapRetriesOneAndPoisonsTheOther(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		e.settings(t, map[string]any{"retryPolicy": policy3})
		near, fresh := e.task(t, domain.Todo), e.task(t, domain.Todo)
		two := int64(2)
		if _, err := e.st.SetStatus(e.ctx, e.project, near.ID, near.Version, store.StatusChange{Status: domain.Todo, ClaimAttempts: &two, UpdatedBy: "alice", UpdatedAt: time.Now().UnixMilli()}); err != nil {
			t.Fatal(err)
		}
		e.claim(t, e.alice, near.ID, 1)
		e.claim(t, e.alice, fresh.ID, 1)
		e.clock.advance(2 * time.Second)
		a, err := e.c.authorize(e.ctx, e.alice, e.project, "reap_stale_leases", domain.GrantSubject{BoardID: e.board.ID}, false)
		if err != nil {
			t.Fatal(err)
		}
		sum, err := e.c.reap(e.ctx, e.alice, a, e.project, e.board.ID)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(sum)
		t.Logf("reap summary %s", raw)
		if !slices.Equal(sum.Poisoned, []string{near.ID}) || !slices.Equal(sum.Retried, []string{fresh.ID}) ||
			!strings.Contains(string(raw), `"poisoned"`) || strings.Contains(string(raw), "deadLetter") {
			t.Fatalf("summary %s", raw)
		}
		n, f := e.get(t, near.ID), e.get(t, fresh.ID)
		if !domain.IsPoisoned(n.Status, reasonOf(n)) || n.ClaimAttempts != 3 || !slices.Contains(n.Labels, domain.LabelPoisoned) {
			t.Fatalf("poisoned: %+v", n)
		}
		if f.Status != domain.Todo || f.ClaimAttempts != 1 || f.NextEligibleAt == nil || *f.NextEligibleAt <= e.clock.now().UnixMilli() || f.Claim != nil {
			t.Fatalf("retried: %+v", f)
		}
		if len(ofType(e.events(t, fresh.ID), domain.EventTaskLeaseReaped)) != 1 || len(ofType(e.events(t, near.ID), domain.EventTaskPoisoned)) != 1 {
			t.Fatal("reap events")
		}
		again, err := e.c.Lifecycle().Reap(e.ctx, e.alice, ReapStaleLeasesReq{ProjectID: e.project, BoardID: e.board.ID})
		if err != nil || again.Reaped != 0 {
			t.Fatalf("second reap %+v %v", again, err)
		}
		nx, err := e.c.Lifecycle().Next(e.ctx, e.alice, NextTaskReq{ProjectID: e.project, BoardID: e.board.ID, IncludeInbox: true})
		if err != nil {
			t.Fatal(err)
		}
		inbox, _ := json.Marshal(nx.Inbox)
		if len(nx.Inbox.Poisoned) != 1 || nx.Inbox.Poisoned[0].ID != near.ID || len(nx.Inbox.Blocked) != 0 || strings.Contains(string(inbox), "deadLetter") {
			t.Fatalf("inbox %s", inbox)
		}
		t.Logf("next_task inbox keys: %s", inbox[:min(len(inbox), 60)])
	})
}

// AC6: Cancel by the lease holder lands DONE with the label cancelled and the reason as a note; a
// worker who does not hold the lease is FORBIDDEN; a stale version is STALE; a master is
// CONFLICT_LOCKED by another identity's live lease and may cancel an unleased task.
func TestCancel(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		dave := e.member(t, e.project, "dave", domain.RoleWorker)
		carol := e.member(t, e.project, "carol", domain.RoleMaster)
		lc := e.c.Lifecycle()
		task, dep := e.task(t, domain.Todo), e.blocked(t, "")
		e.dependOn(t, task.ID, dep.ID)
		e.claim(t, bob, task.ID, 0)
		cur := e.get(t, task.ID)
		_, err := lc.Cancel(e.ctx, dave, CancelTaskReq{ProjectID: e.project, TaskID: task.ID, Version: cur.Version, Reason: "no"})
		wantCode(t, err, domain.CodeForbidden)
		_, err = lc.Cancel(e.ctx, carol, CancelTaskReq{ProjectID: e.project, TaskID: task.ID, Version: cur.Version, Reason: "no"})
		wantCode(t, err, domain.CodeConflictLocked)
		_, err = lc.Cancel(e.ctx, bob, CancelTaskReq{ProjectID: e.project, TaskID: task.ID, Version: cur.Version - 1, Reason: "no"})
		wantCode(t, err, domain.CodeStale)
		r, err := lc.Cancel(e.ctx, bob, CancelTaskReq{ProjectID: e.project, TaskID: task.ID, Version: cur.Version, Reason: "superseded by T-9"})
		if err != nil {
			t.Fatal(err)
		}
		items, err := e.st.ListContextItems(e.ctx, e.project, task.ID, store.ContextItemsQuery{})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("cancelled: %s labels %v claim %v; note %q; dependent %s", r.Task.Status, r.Task.Labels, r.Task.Claim, items.Items[0].Body, e.get(t, dep.ID).Status)
		if r.Task.Status != domain.Done || !slices.Contains(r.Task.Labels, domain.LabelCancelled) || r.Task.Claim != nil ||
			len(items.Items) != 1 || items.Items[0].Body != "superseded by T-9" || items.Items[0].Kind != store.ContextNote {
			t.Fatalf("cancel: %+v items %+v", r.Task, items.Items)
		}
		moved := ofType(e.events(t, task.ID), domain.EventTaskMoved)
		if len(moved) != 1 || !strings.Contains(string(moved[0].Data), `"cancelled":true`) {
			t.Fatalf("task.moved %v", moved)
		}
		if got := e.get(t, dep.ID); got.Status != domain.Todo {
			t.Fatalf("cancel did not unblock the dependent: %s", got.Status)
		}
		idle := e.task(t, domain.Todo)
		if _, err := lc.Cancel(e.ctx, carol, CancelTaskReq{ProjectID: e.project, TaskID: idle.ID, Version: idle.Version, Reason: "dup"}); err != nil {
			t.Fatalf("master cancel: %v", err)
		}
		_, err = lc.Cancel(e.ctx, carol, CancelTaskReq{ProjectID: e.project, TaskID: "T-999", Version: 1, Reason: "x"})
		wantCode(t, err, domain.CodeNotFound)
		_, err = lc.Cancel(e.ctx, carol, CancelTaskReq{ProjectID: e.project, TaskID: idle.ID, Version: 1})
		wantCode(t, err, domain.CodeValidation)
	})
}

// AC7 (expiry): a 1 s lease heartbeat after 2 s is LEASE_EXPIRED, and another identity's Claim then
// wins with no sweeper having run (no task.lease_reaped event).
func TestExpiredLeaseIsFreeOnTheNextClaim(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		lc := e.c.Lifecycle()
		task := e.task(t, domain.Todo)
		c := e.claim(t, e.alice, task.ID, 1)
		hb, err := lc.Heartbeat(e.ctx, e.alice, HeartbeatTaskReq{ProjectID: e.project, TaskID: task.ID, LeaseID: c.LeaseID, LeaseSeconds: 1})
		if err != nil {
			t.Fatalf("live heartbeat: %v", err)
		}
		if got := e.get(t, task.ID); float64(got.Version) != c.Task.Version || isoMs(got.Claim.ExpiresAt) != hb.ExpiresAt {
			t.Fatalf("heartbeat moved the version %d -> %d, or expiry %s vs %+v", int64(c.Task.Version), got.Version, hb.ExpiresAt, got.Claim)
		}
		before := e.get(t, task.ID).Version
		e.clock.advance(2 * time.Second)
		_, err = lc.Heartbeat(e.ctx, e.alice, HeartbeatTaskReq{ProjectID: e.project, TaskID: task.ID, LeaseID: c.LeaseID})
		wantCode(t, err, domain.CodeLeaseExpired)
		won := e.claim(t, bob, task.ID, 0)
		got := e.get(t, task.ID)
		t.Logf("heartbeat after 2s: LEASE_EXPIRED; bob claimed %s lease %s; lease_reaped events %d", task.ID, won.LeaseID, len(ofType(e.events(t, task.ID), domain.EventTaskLeaseReaped)))
		if got.Claim == nil || got.Claim.Owner != "bob" || len(ofType(e.events(t, task.ID), domain.EventTaskLeaseReaped)) != 0 || got.Version != before+1 {
			t.Fatalf("after reclaim: %+v", got)
		}
	})
}

// claimGate holds the first two GetTask calls made inside a transaction until both have arrived (or
// 2 s pass, where the engine serialises its writers), so two claims load the task at the same version.
type claimGate struct {
	store.Store
	n       *atomic.Int32
	arrived chan struct{}
	release chan struct{}
	inTx    bool
}

func (g *claimGate) Atomically(ctx context.Context, fn func(tx store.Store) error) error {
	return g.Store.Atomically(ctx, func(tx store.Store) error {
		return fn(&claimGate{Store: tx, n: g.n, arrived: g.arrived, release: g.release, inTx: true})
	})
}

func (g *claimGate) GetTask(ctx context.Context, p, id string) (store.Task, error) {
	t, err := g.Store.GetTask(ctx, p, id)
	if g.inTx && g.n.Add(1) <= 2 {
		g.arrived <- struct{}{}
		<-g.release
	}
	return t, err
}

// gateBoth waits for both gated arrivals, or 2 s, and opens the gate.
func gateBoth(arrived chan struct{}, release chan struct{}) int {
	both := 0
	timeout := time.After(2 * time.Second)
	for both < 2 {
		select {
		case <-arrived:
			both++
		case <-timeout:
			close(release)
			return both
		}
	}
	close(release)
	return both
}

// AC7 (race): two concurrent Claim calls on one task give exactly one lease and one
// CONFLICT_LOCKED. On Postgres both load the same version; the loser's versioned write meets the
// winner's row (STALE) and retryStale re-judges it against the committed lease.
func TestConcurrentClaimHasOneWinner(t *testing.T) {
	for _, ec := range engineCases {
		t.Run(ec.name, func(t *testing.T) {
			st := ec.open(t)
			e := newLC(t, st)
			bob := e.member(t, e.project, "bob", domain.RoleWorker)
			task := e.task(t, domain.Todo)
			g := &claimGate{Store: st, n: &atomic.Int32{}, arrived: make(chan struct{}, 2), release: make(chan struct{})}
			c := New(g, Options{Logger: log.New(&logBuf{}, "", 0), Now: e.clock.now, ToolCallFlush: -1})
			t.Cleanup(func() { _ = c.Close(context.Background()) })
			errs := make([]error, 2)
			var wg sync.WaitGroup
			for i, who := range []domain.Caller{e.alice, bob} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, errs[i] = c.Lifecycle().Claim(e.ctx, who, ClaimTaskReq{ProjectID: e.project, TaskID: task.ID})
				}()
			}
			both := gateBoth(g.arrived, g.release)
			wg.Wait()
			wins, locked := 0, 0
			for _, err := range errs {
				switch be, _ := domain.AsBatonDeckError(err); {
				case err == nil:
					wins++
				case be != nil && be.Code == domain.CodeConflictLocked:
					locked++
				default:
					t.Fatalf("unexpected error %v", err)
				}
			}
			got := e.get(t, task.ID)
			t.Logf("loaded together %d/2; errs %v; winner %s; version %d", both, errs, got.Claim.Owner, got.Version)
			if wins != 1 || locked != 1 || len(ofType(e.events(t, task.ID), domain.EventTaskClaimed)) != 1 {
				t.Fatalf("wins %d locked %d (errs %v)", wins, locked, errs)
			}
		})
	}
}

// Two concurrent claim_next on a board with one TODO task: one winner, the other no task.
func TestConcurrentClaimNextHasOneWinner(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		e.task(t, domain.Todo)
		got := make([]ClaimNextResp, 2)
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i, who := range []domain.Caller{e.alice, bob} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got[i], errs[i] = e.c.Lifecycle().ClaimNext(e.ctx, who, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID})
			}()
		}
		wg.Wait()
		if errs[0] != nil || errs[1] != nil || (got[0].Task == nil) == (got[1].Task == nil) {
			t.Fatalf("claim_next pair: %v / %+v %+v", errs, got[0].Task, got[1].Task)
		}
	})
}

// Requirement 4: a claim writes, inside the transaction, an assigned change row scoped to the
// winner's identity; a release rings broadcast; a failure into a backoff rings nothing directed; a
// handoff rings the new assignee.
func TestLifecycleDoorbells(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		e.settings(t, map[string]any{"retryPolicy": policy3})
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		lc := e.c.Lifecycle()
		task := e.task(t, domain.Todo)
		next, err := lc.ClaimNext(e.ctx, bob, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID})
		if err != nil || next.Task == nil {
			t.Fatal(err)
		}
		ch, p := e.lastChange(t)
		if p.Kind != ChangeKindChanged || ch.Scope != BroadcastScope(e.project) || p.TaskID != task.ID || p.Tool != "claim_next" {
			t.Fatalf("claim change row %+v scope %s, want changed", p, ch.Scope)
		}
		if _, err := lc.Release(e.ctx, bob, ReleaseTaskReq{ProjectID: e.project, TaskID: task.ID, LeaseID: *next.LeaseID}); err != nil {
			t.Fatal(err)
		}
		if ch, p = e.lastChange(t); p.Kind != DoorbellBroadcast || ch.Scope != BroadcastScope(e.project) {
			t.Fatalf("release doorbell %+v scope %s", p, ch.Scope)
		}
		c := e.claim(t, bob, task.ID, 0)
		if _, err := lc.Fail(e.ctx, bob, FailTaskReq{ProjectID: e.project, TaskID: task.ID, LeaseID: c.LeaseID, Reason: "r"}); err != nil {
			t.Fatal(err)
		}
		if _, p = e.lastChange(t); p.Kind != ChangeKindChanged {
			t.Fatalf("fail into a backoff rang %+v", p)
		}
		c = e.claim(t, bob, task.ID, 0)
		h, err := lc.Handoff(e.ctx, bob, HandoffTaskReq{ProjectID: e.project, TaskID: task.ID, LeaseID: c.LeaseID, ToAgent: "reviewer-bot", MemoryNote: "over to you"})
		if err != nil {
			t.Fatal(err)
		}
		ch, p = e.lastChange(t)
		if p.Kind != DoorbellAssigned || ch.Scope != AgentScope("reviewer-bot") || h.Task.Status != domain.Todo || *h.Task.Assignee != "reviewer-bot" || h.Task.Claim != nil {
			t.Fatalf("handoff %+v doorbell %+v scope %s", h.Task, p, ch.Scope)
		}
		mem, err := e.st.ReadMemory(e.ctx, e.project, task.ID, store.ReadMemoryQuery{Key: "handoff", Identity: "bob"})
		if err != nil || len(mem) != 1 || !strings.Contains(string(mem[0].Value), "over to you") {
			t.Fatalf("handoff memory %+v %v", mem, err)
		}
		t.Logf("claim→changed (no ring), release→broadcast, fail→changed, handoff→assigned@%s with memory %s", AgentScope("reviewer-bot"), mem[0].Value)
	})
}

// Block with blockedBy writes the edges and the BLOCKED landing in one transaction: a cycle or a
// missing blocker leaves no edge and the task IN_PROGRESS with its lease.
func TestBlockWritesEdgesAtomically(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		lc := e.c.Lifecycle()
		a, b := e.task(t, domain.Todo), e.task(t, domain.Todo)
		e.dependOn(t, a.ID, b.ID) // a blocks b
		c := e.claim(t, e.alice, a.ID, 0)
		_, err := lc.Block(e.ctx, e.alice, BlockTaskReq{ProjectID: e.project, TaskID: a.ID, LeaseID: c.LeaseID, Reason: "wait", BlockedBy: []string{b.ID}})
		wantCode(t, err, domain.CodeCycleDetected)
		_, err = lc.Block(e.ctx, e.alice, BlockTaskReq{ProjectID: e.project, TaskID: a.ID, LeaseID: c.LeaseID, Reason: "wait", BlockedBy: []string{"T-404"}})
		wantCode(t, err, domain.CodeNotFound)
		if got := e.get(t, a.ID); got.Status != domain.InProgress || got.Claim == nil || len(got.BlockedBy) != 0 || len(e.edges(t)) != 1 {
			t.Fatalf("a refused block wrote: %+v edges %d", got, len(e.edges(t)))
		}
		x := e.task(t, domain.Todo)
		r, err := lc.Block(e.ctx, e.alice, BlockTaskReq{ProjectID: e.project, TaskID: a.ID, LeaseID: c.LeaseID, Reason: "needs x", BlockedBy: []string{x.ID, x.ID, a.ID}})
		if err != nil {
			t.Fatal(err)
		}
		got := e.get(t, a.ID)
		if r.Task.Status != domain.Blocked || !reasonIs(got, "needs x") || !slices.Equal(got.BlockedBy, []string{x.ID}) || got.Claim != nil || float64(got.Version) != c.Task.Version+1 {
			t.Fatalf("blocked: %+v", got)
		}
		if bx := e.get(t, x.ID); !slices.Equal(bx.Blocks, []string{a.ID}) || bx.Version != x.Version+1 {
			t.Fatalf("blocker cache %+v", bx)
		}
	})
}

// The block_task P-110 shape on Postgres: over B→C and D→A, block_task B {blockedBy:[A]} and
// block_task D {blockedBy:[C]} at once would close A→B→C→D→A. The project lock (taken first, and
// inside AddEdge) admits one: exactly one CYCLE_DETECTED and no cycle.
func TestConcurrentBlockClosesNoCycle(t *testing.T) {
	for _, ec := range engineCases {
		t.Run(ec.name, func(t *testing.T) {
			st := ec.open(t)
			e := newLC(t, st)
			A, B, C, D := e.task(t, domain.Todo), e.task(t, domain.Todo), e.task(t, domain.Todo), e.task(t, domain.Todo)
			e.dependOn(t, B.ID, C.ID)
			e.dependOn(t, D.ID, A.ID)
			cb, cd := e.claim(t, e.alice, B.ID, 0), e.claim(t, e.alice, D.ID, 0)
			g := &gateStore{Store: st, arrived: make(chan struct{}, 2), release: make(chan struct{})}
			c := New(g, Options{Logger: log.New(&logBuf{}, "", 0), Now: e.clock.now, ToolCallFlush: -1})
			t.Cleanup(func() { _ = c.Close(context.Background()) })
			reqs := []BlockTaskReq{
				{ProjectID: e.project, TaskID: B.ID, LeaseID: cb.LeaseID, Reason: "r", BlockedBy: []string{A.ID}},
				{ProjectID: e.project, TaskID: D.ID, LeaseID: cd.LeaseID, Reason: "r", BlockedBy: []string{C.ID}},
			}
			errs := make([]error, 2)
			var wg sync.WaitGroup
			for i, r := range reqs {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, errs[i] = c.Lifecycle().Block(e.ctx, e.alice, r)
				}()
			}
			both := gateBoth(g.arrived, g.release)
			wg.Wait()
			edges := e.edges(t)
			t.Logf("inside AddEdge at once: %d/2; errs %v; edges %d; cycle %v", both, errs, len(edges), hasCycle(edges))
			if hasCycle(edges) {
				t.Fatalf("block_task committed a cycle")
			}
			cycles := 0
			for _, err := range errs {
				if be, ok := domain.AsBatonDeckError(err); ok && be.Code == domain.CodeCycleDetected {
					cycles++
				} else if err != nil {
					t.Fatalf("unexpected %v", err)
				}
			}
			if cycles != 1 {
				t.Fatalf("%d CYCLE_DETECTED, want 1", cycles)
			}
		})
	}
}

// Every declared refusal of the lease-guarded outcomes, and the inputs refused rather than ignored.
func TestLifecycleRefusals(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		vic := e.member(t, e.project, "vic", domain.RoleViewer)
		outsider := domain.Caller{Identity: "mallory", TokenScope: "mallory"}
		lc := e.c.Lifecycle()
		task := e.task(t, domain.Todo)
		c := e.claim(t, e.alice, task.ID, 60)
		p, id := e.project, task.ID
		lease := func(l string) []func() error {
			return []func() error{
				func() error {
					_, err := lc.Release(e.ctx, e.alice, ReleaseTaskReq{ProjectID: p, TaskID: id, LeaseID: l})
					return err
				},
				func() error {
					_, err := lc.Complete(e.ctx, e.alice, CompleteTaskReq{ProjectID: p, TaskID: id, LeaseID: l})
					return err
				},
				func() error {
					_, err := lc.Block(e.ctx, e.alice, BlockTaskReq{ProjectID: p, TaskID: id, LeaseID: l, Reason: "r"})
					return err
				},
				func() error {
					_, err := lc.Fail(e.ctx, e.alice, FailTaskReq{ProjectID: p, TaskID: id, LeaseID: l, Reason: "r"})
					return err
				},
				func() error {
					_, err := lc.Handoff(e.ctx, e.alice, HandoffTaskReq{ProjectID: p, TaskID: id, LeaseID: l, ToAgent: "x"})
					return err
				},
				func() error {
					_, err := lc.Heartbeat(e.ctx, e.alice, HeartbeatTaskReq{ProjectID: p, TaskID: id, LeaseID: l})
					return err
				},
			}
		}
		for i, f := range lease("L-wrong") {
			if err := f(); err == nil {
				t.Fatalf("outcome %d with a wrong lease id succeeded", i)
			} else {
				wantCode(t, err, domain.CodeLeaseExpired)
			}
		}
		// Another owner presenting the real lease id: LEASE_EXPIRED, never a hint that the lease lives.
		_, err := lc.Release(e.ctx, bob, ReleaseTaskReq{ProjectID: p, TaskID: id, LeaseID: c.LeaseID})
		wantCode(t, err, domain.CodeLeaseExpired)
		_, err = lc.Heartbeat(e.ctx, bob, HeartbeatTaskReq{ProjectID: p, TaskID: id, LeaseID: c.LeaseID})
		wantCode(t, err, domain.CodeLeaseExpired)
		_, err = lc.Claim(e.ctx, bob, ClaimTaskReq{ProjectID: p, TaskID: id})
		wantCode(t, err, domain.CodeConflictLocked)
		e.clock.advance(61 * time.Second)
		for i, f := range lease(c.LeaseID) {
			if err := f(); err == nil {
				t.Fatalf("outcome %d on an expired lease succeeded", i)
			} else {
				wantCode(t, err, domain.CodeLeaseExpired)
			}
		}
		for name, f := range map[string]func() error{
			"missing task": func() error {
				_, err := lc.Release(e.ctx, e.alice, ReleaseTaskReq{ProjectID: p, TaskID: "T-404", LeaseID: "L-x"})
				return err
			},
		} {
			wantCode(t, f(), domain.CodeNotFound)
			_ = name
		}
		_, err = lc.Claim(e.ctx, outsider, ClaimTaskReq{ProjectID: p, TaskID: id})
		wantCode(t, err, domain.CodeForbidden)
		_, err = lc.Claim(e.ctx, vic, ClaimTaskReq{ProjectID: p, TaskID: id})
		wantCode(t, err, domain.CodeForbidden)
		_, err = lc.Reap(e.ctx, vic, ReapStaleLeasesReq{ProjectID: p, BoardID: e.board.ID})
		wantCode(t, err, domain.CodeForbidden)
		done := e.task(t, domain.Done)
		_, err = lc.Claim(e.ctx, e.alice, ClaimTaskReq{ProjectID: p, TaskID: done.ID})
		wantCode(t, err, domain.CodeInvalidTransition)
		for name, f := range map[string]func() error{
			"poison reason": func() error {
				_, err := lc.Block(e.ctx, e.alice, BlockTaskReq{ProjectID: p, TaskID: id, LeaseID: "L", Reason: " Poison"})
				return err
			},
			"no reason": func() error {
				_, err := lc.Fail(e.ctx, e.alice, FailTaskReq{ProjectID: p, TaskID: id, LeaseID: "L"})
				return err
			},
			"lease too long": func() error {
				_, err := lc.Claim(e.ctx, e.alice, ClaimTaskReq{ProjectID: p, TaskID: id, LeaseSeconds: 86_401})
				return err
			},
			"artifacts": func() error {
				_, err := lc.Complete(e.ctx, e.alice, CompleteTaskReq{ProjectID: p, TaskID: id, LeaseID: "L", Artifacts: []ArtifactInput{{Kind: "pr", URL: "https://x"}}})
				return err
			},
			"shard lanes": func() error {
				_, err := lc.ClaimNext(e.ctx, e.alice, ClaimNextReq{ProjectID: p, BoardID: e.board.ID, Shard: Shard{Index: 0, Count: 2}})
				return err
			},
			"shard index": func() error {
				_, err := lc.ClaimNext(e.ctx, e.alice, ClaimNextReq{ProjectID: p, BoardID: e.board.ID, Shard: Shard{Index: 2, Count: 2}})
				return err
			},
			"sprintId": func() error {
				_, err := lc.ClaimNext(e.ctx, e.alice, ClaimNextReq{ProjectID: p, BoardID: e.board.ID, SprintID: "S-1"})
				return err
			},
			"maxConcurrency": func() error {
				_, err := lc.ClaimNext(e.ctx, e.alice, ClaimNextReq{ProjectID: p, BoardID: e.board.ID, MaxConcurrency: 2})
				return err
			},
			"strategy score": func() error {
				_, err := lc.Next(e.ctx, e.alice, NextTaskReq{ProjectID: p, BoardID: e.board.ID, Strategy: "score"})
				return err
			},
			"explain": func() error {
				_, err := lc.Next(e.ctx, e.alice, NextTaskReq{ProjectID: p, BoardID: e.board.ID, Explain: true})
				return err
			},
			"toAgent": func() error {
				_, err := lc.Handoff(e.ctx, e.alice, HandoffTaskReq{ProjectID: p, TaskID: id, LeaseID: "L", ToAgent: strings.Repeat("a", 65)})
				return err
			},
		} {
			if err := f(); err == nil {
				t.Fatalf("%s: no error", name)
			} else if be, ok := domain.AsBatonDeckError(err); !ok || be.Code != domain.CodeValidation {
				t.Fatalf("%s: %v, want VALIDATION", name, err)
			}
		}
		_, err = lc.ClaimNext(e.ctx, e.alice, ClaimNextReq{ProjectID: p, BoardID: "B-nope"})
		wantCode(t, err, domain.CodeNotFound)
	})
}

// The IN_PROGRESS column's wipLimit holds on claim_task and claim_next.
func TestClaimHonoursTheWIPLimit(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		col := e.column(t, domain.InProgress)
		limit := int64(1)
		if _, err := e.st.UpdateColumn(e.ctx, e.project, e.board.ID, col, store.ColumnPatch{SetWIPLimit: true, WIPLimit: &limit}); err != nil {
			t.Fatal(err)
		}
		lc := e.c.Lifecycle()
		a, b := e.task(t, domain.Todo), e.task(t, domain.Todo)
		ca := e.claim(t, e.alice, a.ID, 0)
		_, err := lc.Claim(e.ctx, e.alice, ClaimTaskReq{ProjectID: e.project, TaskID: b.ID})
		wantCode(t, err, domain.CodeWIPExceeded)
		_, err = lc.ClaimNext(e.ctx, e.alice, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID})
		wantCode(t, err, domain.CodeWIPExceeded)
		if _, err := lc.Release(e.ctx, e.alice, ReleaseTaskReq{ProjectID: e.project, TaskID: a.ID, LeaseID: ca.LeaseID}); err != nil {
			t.Fatal(err)
		}
		e.claim(t, e.alice, b.ID, 0)
	})
}

// An idempotent claim replays the ORIGINAL lease; once that lease has expired the replay is
// LEASE_EXPIRED, not a dead lease handed back as usable.
func TestClaimReplay(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		lc := e.c.Lifecycle()
		task := e.task(t, domain.Todo)
		req := ClaimTaskReq{ProjectID: e.project, TaskID: task.ID, IdempotencyKey: "k", LeaseSeconds: 5}
		first, err := lc.Claim(e.ctx, e.alice, req)
		if err != nil {
			t.Fatal(err)
		}
		again, err := lc.Claim(e.ctx, e.alice, req)
		if err != nil || again.LeaseID != first.LeaseID {
			t.Fatalf("replay %+v %v, want lease %s", again.LeaseID, err, first.LeaseID)
		}
		e.clock.advance(6 * time.Second)
		_, err = lc.Claim(e.ctx, e.alice, req)
		wantCode(t, err, domain.CodeLeaseExpired)
		e.task(t, domain.Todo)
		nreq := ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, IdempotencyKey: "n"}
		n1, err := lc.ClaimNext(e.ctx, e.alice, nreq)
		if err != nil || n1.Task == nil {
			t.Fatal(err)
		}
		n2, err := lc.ClaimNext(e.ctx, e.alice, nreq)
		if err != nil || n2.Task == nil || n2.Task.ID != n1.Task.ID || *n2.LeaseID != *n1.LeaseID || n2.Attempts != 0 {
			t.Fatalf("claim_next replay %+v %v", n2, err)
		}
	})
}

// checkpointPolicy enforce refuses a completion with no REJECTED checkpoint and admits one with it;
// warn completes and says so.
func TestCompleteCheckpointPolicy(t *testing.T) {
	e := newLC(t, engineCases[0].open(t))
	lc := e.c.Lifecycle()
	e.settings(t, map[string]any{"checkpointPolicy": "enforce", "reviewEnabled": false})
	task := e.task(t, domain.Todo)
	c := e.claim(t, e.alice, task.ID, 0)
	_, err := lc.Complete(e.ctx, e.alice, CompleteTaskReq{ProjectID: e.project, TaskID: task.ID, LeaseID: c.LeaseID})
	wantCode(t, err, domain.CodeValidation)
	if _, err := e.c.Context().AddItem(e.ctx, e.alice, AddContextItemReq{ProjectID: e.project, TaskID: task.ID, Kind: "note", Body: "**DONE** x\n**REJECTED** y"}); err != nil {
		t.Fatal(err)
	}
	if _, err := lc.Complete(e.ctx, e.alice, CompleteTaskReq{ProjectID: e.project, TaskID: task.ID, LeaseID: c.LeaseID}); err != nil {
		t.Fatalf("with REJECTED: %v", err)
	}
	e.settings(t, map[string]any{"checkpointPolicy": "warn"})
	w := e.task(t, domain.Todo)
	cw := e.claim(t, e.alice, w.ID, 0)
	r, err := lc.Complete(e.ctx, e.alice, CompleteTaskReq{ProjectID: e.project, TaskID: w.ID, LeaseID: cw.LeaseID})
	if err != nil || len(r.Warnings) != 1 || len(ofType(e.events(t, w.ID), domain.EventTaskCheckpointMissing)) != 1 {
		t.Fatalf("warn: %v %v", r.Warnings, err)
	}
}

// lcCounting counts the lifecycle's store calls; GetBoard is two statements (the board and one
// grouped count), each other call one.
type lcCounting struct{ countingStore }

func (s *lcCounting) wrap(tx store.Store) store.Store {
	return &lcCounting{countingStore{Store: tx, n: s.n}}
}

func (s *lcCounting) Atomically(ctx context.Context, fn func(tx store.Store) error) error {
	return s.Store.Atomically(ctx, func(tx store.Store) error { return fn(s.wrap(tx)) })
}

func (s *lcCounting) GetTask(ctx context.Context, p, id string) (store.Task, error) {
	s.n.hit("GetTask")
	return s.Store.GetTask(ctx, p, id)
}

func (s *lcCounting) GetSummary(ctx context.Context, p, id string) (store.Summary, error) {
	s.n.hit("GetSummary")
	return s.Store.GetSummary(ctx, p, id)
}

func (s *lcCounting) ClaimNext(ctx context.Context, q store.FrontierQuery, in store.ClaimInput) (store.Task, bool, error) {
	s.n.hit("ClaimNext")
	return s.Store.ClaimNext(ctx, q, in)
}

func (s *lcCounting) PreviewFrontier(ctx context.Context, q store.FrontierQuery, limit int) ([]store.Task, error) {
	s.n.hit("PreviewFrontier")
	return s.Store.PreviewFrontier(ctx, q, limit)
}

func (s *lcCounting) ListExpiredLeases(ctx context.Context, p, b string, now int64, limit int) ([]store.Task, error) {
	s.n.hit("ListExpiredLeases")
	return s.Store.ListExpiredLeases(ctx, p, b, now, limit)
}

func (s *lcCounting) CountTasks(ctx context.Context, p string, f store.TaskFilter) (int64, error) {
	s.n.hit("CountTasks")
	return s.Store.CountTasks(ctx, p, f)
}

func (s *lcCounting) LockProject(ctx context.Context, p string) error {
	s.n.hit("LockProject")
	return s.Store.LockProject(ctx, p)
}

func (s *lcCounting) AppendTaskEvent(ctx context.Context, in store.EventInput) (store.Event, error) {
	s.n.hit("write:AppendTaskEvent")
	return s.Store.AppendTaskEvent(ctx, in)
}

func (s *lcCounting) AppendChange(ctx context.Context, in store.ChangeInput) (int64, error) {
	s.n.hit("write:AppendChange")
	return s.Store.AppendChange(ctx, in)
}

// reads is the counted read statements; writes are the change's own rows (the event and the
// change-log row), which conductor's fsReads pin did not count either.
func reads(m map[string]int) (r int, w int) {
	for k, v := range m {
		switch {
		case strings.HasPrefix(k, "write:"):
			w += v
		case k == "GetBoard":
			r += 2 * v
		default:
			r += v
		}
	}
	return r, w
}

// AC8: claim_next costs at most four read statements and next_task at most three, with the
// membership cached; cold (no snapshot) and warm (the bus live) are both measured.
func TestClaimNextAndNextStatementPins(t *testing.T) {
	f := newFixture(t, Options{})
	n := &calls{m: map[string]int{}}
	old := f.c
	f.c = New(&lcCounting{countingStore{Store: f.st, n: n}}, Options{Logger: log.New(f.logs, "", 0), Now: f.clock.now, ToolCallFlush: -1})
	if err := old.Close(f.ctx); err != nil {
		t.Fatal(err)
	}
	lc := f.c.Lifecycle()
	for range 3 {
		f.task(t, domain.Todo, nil)
	}
	nreq := NextTaskReq{ProjectID: f.project.ID, BoardID: f.board.ID}
	if _, err := lc.Next(f.ctx, f.alice, nreq); err != nil { // warms the membership
		t.Fatal(err)
	}
	n.take()
	cases := []struct {
		name string
		pin  int
		run  func() error
	}{
		{"claim_next cold", 4, func() error {
			r, err := lc.ClaimNext(f.ctx, f.alice, ClaimNextReq{ProjectID: f.project.ID, BoardID: f.board.ID})
			if err == nil && r.Task == nil {
				t.Fatal("claim_next won nothing")
			}
			return err
		}},
		{"next_task", 3, func() error { _, err := lc.Next(f.ctx, f.alice, nreq); return err }},
		{"claim_next warm", 4, func() error {
			f.c.ObserveChange(store.Change{ID: 1, Payload: json.RawMessage(`{}`)})
			if _, err := f.c.boardSnapshot(f.ctx, f.project.ID, f.board.ID); err != nil {
				return err
			}
			n.take()
			_, err := lc.ClaimNext(f.ctx, f.alice, ClaimNextReq{ProjectID: f.project.ID, BoardID: f.board.ID})
			return err
		}},
	}
	for _, c := range cases {
		if err := c.run(); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		m := n.take()
		r, w := reads(m)
		t.Logf("%-16s %d read statements, %d rows written: %v", c.name, r, w, m)
		if r > c.pin {
			t.Fatalf("%s issued %d read statements %v, the pin is %d", c.name, r, m, c.pin)
		}
	}
	// next_task on an empty frontier: the preview and the lazy reap's one read.
	empty, err := f.st.CreateBoard(f.ctx, store.CreateBoardInput{ProjectID: f.project.ID, Name: "E", CreatedBy: "alice"}, store.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	n.take()
	if _, err := lc.Next(f.ctx, f.alice, NextTaskReq{ProjectID: f.project.ID, BoardID: empty.ID}); err != nil {
		t.Fatal(err)
	}
	m := n.take()
	if r, _ := reads(m); r > 3 {
		t.Fatalf("empty next_task %d statements %v", r, m)
	}
	t.Logf("next_task empty   %v", m)
}

// assignedRows is every assigned change row as tool@scope, oldest first.
func (e *lcEnv) assignedRows(t *testing.T) []string {
	t.Helper()
	ch, err := e.st.ReadChanges(e.ctx, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range ch {
		var p changePayload
		_ = json.Unmarshal(c.Payload, &p)
		if p.Kind == DoorbellAssigned {
			out = append(out, p.Tool+"@"+c.Scope)
		}
	}
	return out
}

// Owner ruling 2026-10-23: a claim, a claim_next and a self-assignment ring nobody. Each still writes
// its change row (kind changed) for the bus. The control: the same handoff and review to someone
// else rings them.
func TestAClaimOrASelfAssignmentRingsNobody(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		lc := e.c.Lifecycle()
		a, b := e.task(t, domain.Todo), e.task(t, domain.Todo)
		before := e.changeCount(t)
		ca := e.claim(t, bob, a.ID, 0)
		next, err := lc.ClaimNext(e.ctx, bob, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID})
		if err != nil || next.Task == nil || next.Task.ID != b.ID {
			t.Fatalf("claim_next %+v %v", next.Task, err)
		}
		if _, err := lc.Handoff(e.ctx, bob, HandoffTaskReq{ProjectID: e.project, TaskID: a.ID, LeaseID: ca.LeaseID, ToAgent: "bob"}); err != nil {
			t.Fatal(err)
		}
		if _, err := lc.Complete(e.ctx, bob, CompleteTaskReq{ProjectID: e.project, TaskID: b.ID, LeaseID: *next.LeaseID, Reviewer: "bob"}); err != nil {
			t.Fatal(err)
		}
		if n := e.changeCount(t) - before; n != 4 {
			t.Fatalf("%d change rows for 4 mutations", n)
		}
		if rows := e.assignedRows(t); len(rows) != 0 {
			t.Fatalf("claim / claim_next / self-handoff / self-review rang %v, want nobody", rows)
		}
		// The control: handing over and reviewing to someone else rings them.
		ca = e.claim(t, bob, a.ID, 0)
		if _, err := lc.Handoff(e.ctx, bob, HandoffTaskReq{ProjectID: e.project, TaskID: a.ID, LeaseID: ca.LeaseID, ToAgent: "alice"}); err != nil {
			t.Fatal(err)
		}
		rows := e.assignedRows(t)
		t.Logf("after claim, claim_next, self-handoff, self-review: assigned rows [] ; after handoff to alice: %v", rows)
		if !slices.Equal(rows, []string{"handoff_task@alice"}) {
			t.Fatalf("control rows %v", rows)
		}
	})
}

// RingFor (the rule every other service's assignment goes through) leaves out a self-assignment and
// keeps an assignment to someone else and an auto-unblock's ring to its assignee.
func TestRingForSkipsASelfAssignment(t *testing.T) {
	e := newLC(t, engineCases[0].open(t))
	for _, c := range []struct {
		assignee  string
		unblocked bool
		want      int
	}{{"alice", false, 0}, {"bob", false, 1}, {"alice", true, 1}} {
		task := e.task(t, domain.Todo)
		before := len(e.assignedRows(t))
		_, err := mutate(e.ctx, e.c, e.alice, mutation{Tool: "update_task", ProjectID: e.project, TaskID: task.ID}, func(tx *txn) (struct{}, error) {
			after := *tx.Task
			after.Assignee = &c.assignee
			prev := *tx.Task
			if c.unblocked {
				prev.Status = domain.Blocked
				prev.Assignee = &c.assignee
			}
			tx.RingFor(&prev, after, c.unblocked)
			return struct{}{}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := len(e.assignedRows(t)) - before; got != c.want {
			t.Fatalf("RingFor to %s (unblocked %v) wrote %d assigned rows, want %d", c.assignee, c.unblocked, got, c.want)
		}
	}
}

// P-275 (audit A1): without an assignee claim_next and next_task never reach work routed to
// another identity — a handoff stores the receiver's login, so bob's own is matched by login too —
// and an assignee naming another is FORBIDDEN below master.
func TestClaimNextTakesOnlyTheCallersOrUnassigned(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		carl := e.member(t, e.project, "carl", domain.RoleWorker)
		mia := e.member(t, e.project, "mia", domain.RoleMaster)
		if err := e.st.CreateUser(e.ctx, store.User{ID: "bob", Email: "bob@x.test", DisplayName: "Bob", PasswordHash: "x", CreatedAt: 1}); err != nil {
			t.Fatal(err)
		}
		add := func(assignee *string, order string) store.Task {
			task, err := e.st.CreateTask(e.ctx, store.CreateTaskInput{
				ProjectID: e.project, BoardID: e.board.ID, ColumnID: e.column(t, domain.Todo), Title: "t", Status: domain.Todo,
				Assignee: assignee, OrderKey: order, CreatedBy: "alice", CreatedAt: time.Now().UnixMilli(),
			})
			if err != nil {
				t.Fatal(err)
			}
			return task
		}
		dave := "dave@x.test"
		bobLogin := "Bob@X.test"
		daves, bobs, open := add(&dave, "a0"), add(&bobLogin, "a1"), add(nil, "a2")
		lc := e.c.Lifecycle()
		claimNext := func(who domain.Caller, assignee string) (*Task, error) {
			r, err := lc.ClaimNext(e.ctx, who, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, Assignee: assignee})
			return r.Task, err
		}
		next := func(who domain.Caller, assignee string) (*Task, error) {
			r, err := lc.Next(e.ctx, who, NextTaskReq{ProjectID: e.project, BoardID: e.board.ID, Assignee: assignee})
			return r.Task, err
		}
		if head, err := next(carl, ""); err != nil || head == nil || head.ID != open.ID {
			t.Fatalf("carl's next_task head %+v %v, want the unassigned %s", head, err, open.ID)
		}
		if head, err := next(bob, ""); err != nil || head == nil || head.ID != bobs.ID {
			t.Fatalf("bob's next_task head %+v %v, want his own by login %s", head, err, bobs.ID)
		}
		for i, want := range []string{open.ID, ""} {
			got, err := claimNext(carl, "")
			if err != nil || (got == nil) != (want == "") || (got != nil && got.ID != want) {
				t.Fatalf("carl's claim_next #%d = %+v %v, want %q", i, got, err, want)
			}
		}
		for i, want := range []string{bobs.ID, ""} {
			got, err := claimNext(bob, "")
			if err != nil || (got == nil) != (want == "") || (got != nil && got.ID != want) {
				t.Fatalf("bob's claim_next #%d = %+v %v, want %q", i, got, err, want)
			}
		}
		if got, err := e.st.GetTask(e.ctx, e.project, daves.ID); err != nil || got.Status != domain.Todo || got.Claim != nil {
			t.Fatalf("dave's %s is %+v %v, want TODO and unclaimed", daves.ID, got, err)
		}
		_, err := claimNext(carl, dave)
		wantCode(t, err, domain.CodeForbidden)
		_, err = next(bob, dave)
		wantCode(t, err, domain.CodeForbidden)
		for _, own := range []string{"bob", "BOB@x.test"} {
			if _, err := claimNext(bob, own); err != nil {
				t.Fatalf("bob's claim_next for his own %q: %v", own, err)
			}
		}
		if head, err := next(mia, dave); err != nil || head == nil || head.ID != daves.ID {
			t.Fatalf("the master's next_task for dave = %+v %v, want %s", head, err, daves.ID)
		}
	})
}
