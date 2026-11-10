package conformance

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("claims", claims) }

func probeClaims(s store.Store) error {
	_, err := s.PreviewFrontier(context.Background(), store.FrontierQuery{ProjectID: "P-probe", BoardID: "B-probe", Now: taskClock}, 1)
	return err
}

// leaseFor is owner's claim (agent agentID) of one minute at taskClock, landing in the IN_PROGRESS
// column.
func (f taskBoard) leaseFor(owner, agentID, leaseID string) store.ClaimInput {
	return store.ClaimInput{
		Owner: owner, Agent: ptr("agent " + agentID), AgentID: ptr(agentID), LeaseID: leaseID,
		ExpiresAt: taskClock + 60_000, Now: taskClock, ColumnID: f.col[domain.InProgress],
	}
}

func (f taskBoard) frontier() store.FrontierQuery {
	return store.FrontierQuery{ProjectID: f.project, BoardID: f.board, Now: taskClock}
}

// claims is the lease area: claim by id and from the frontier, heartbeat, release, the expired
// leases and the frontier itself, each with its refusals, and one winner per task under concurrency.
func claims(t *testing.T, newStore Factory) {
	ctx := context.Background()
	requireBuilt(t, newStore, probeTasks, probeClaims)

	t.Run("ClaimTaskTakesTheLeaseMovesToInProgressAndBumps", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		task, err := f.s.SetStatus(ctx, f.project, task.ID, 1, store.StatusChange{Status: domain.Todo, NextEligibleAt: &store.Nullable[int64]{Value: ptr(taskClock - 1)}, UpdatedBy: "alice", UpdatedAt: taskClock})
		mustOK(t, err)
		got, err := f.s.ClaimTask(ctx, f.project, task.ID, 2, f.leaseFor("bob", "a1", "L-1"))
		mustOK(t, err)
		want := task
		want.Status, want.ColumnID, want.NextEligibleAt, want.Version = domain.InProgress, f.col[domain.InProgress], nil, 3
		// It lands at the head of the IN_PROGRESS column with a key of its own (empty there: any
		// well-formed key), not the TODO key it carried.
		if !domain.IsValidOrder(got.OrderKey) || got.OrderKey == task.OrderKey {
			t.Fatalf("claimed into IN_PROGRESS at %q, want a fresh well-formed key, not %q", got.OrderKey, task.OrderKey)
		}
		want.OrderKey = got.OrderKey
		want.Claim = &store.TaskClaim{Owner: "bob", Agent: ptr("agent a1"), AgentID: ptr("a1"), LeaseID: "L-1", ExpiresAt: taskClock + 60_000}
		want.UpdatedBy, want.UpdatedAt = "bob", taskClock
		sameTask(t, got, want)
		read, err := f.s.GetTask(ctx, f.project, task.ID)
		mustOK(t, err)
		sameTask(t, read, want)
	})

	t.Run("ClaimTaskByTheSameAgentReplacesItsLeaseAndAnExpiredLeaseIsFree", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		_, err := f.s.ClaimTask(ctx, f.project, task.ID, 1, f.leaseFor("bob", "a1", "L-1"))
		mustOK(t, err)
		again, err := f.s.ClaimTask(ctx, f.project, task.ID, 2, f.leaseFor("bob", "a1", "L-2"))
		mustOK(t, err)
		if again.Claim.LeaseID != "L-2" || again.Version != 3 {
			t.Fatalf("re-claim: %+v", again.Claim)
		}
		late := f.leaseFor("carol", "c1", "L-3")
		late.Now, late.ExpiresAt = taskClock+60_000, taskClock+120_000 // bob's lease ends at taskClock+60_000
		taken, err := f.s.ClaimTask(ctx, f.project, task.ID, 3, late)
		mustOK(t, err)
		if taken.Claim.Owner != "carol" || taken.Claim.LeaseID != "L-3" {
			t.Fatalf("after expiry: %+v", taken.Claim)
		}
	})

	t.Run("ClaimTaskRefusals", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		held := f.add(t, "held", nil)
		_, err := f.s.ClaimTask(ctx, f.project, held.ID, 1, f.leaseFor("bob", "a1", "L-1"))
		mustOK(t, err)
		// Another owner, and the same owner under another agent, hit the live lease.
		_, err = f.s.ClaimTask(ctx, f.project, held.ID, 2, f.leaseFor("carol", "c1", "L-2"))
		wantCode(t, err, domain.CodeConflictLocked, "")
		if e, _ := domain.AsBatonDeckError(err); e.Details["owner"] != "bob" || e.Details["taskId"] != held.ID {
			t.Fatalf("CONFLICT_LOCKED details %v", e.Details)
		}
		_, err = f.s.ClaimTask(ctx, f.project, held.ID, 2, f.leaseFor("bob", "a2", "L-2"))
		wantCode(t, err, domain.CodeConflictLocked, "")

		racing := f.add(t, "racing", nil)
		racing, err = f.s.UpdateTask(ctx, f.project, racing.ID, 1, store.TaskPatch{Racing: ptr(true), UpdatedBy: "alice", UpdatedAt: taskClock})
		mustOK(t, err)
		_, err = f.s.ClaimTask(ctx, f.project, racing.ID, 2, f.leaseFor("bob", "a1", "L-3"))
		wantCode(t, err, domain.CodeConflictLocked, "")
		if e, _ := domain.AsBatonDeckError(err); e.Details["owner"] != "race" {
			t.Fatalf("a racing parent: %v", e.Details)
		}

		for _, status := range []domain.Status{domain.Blocked, domain.Review, domain.Done} {
			task := f.add(t, string(status), func(in *store.CreateTaskInput) { in.Status = status })
			_, err := f.s.ClaimTask(ctx, f.project, task.ID, 1, f.leaseFor("bob", "a1", "L-4"))
			wantCode(t, err, domain.CodeInvalidTransition, "")
		}

		free := f.add(t, "free", nil)
		_, err = f.s.ClaimTask(ctx, f.project, free.ID, 9, f.leaseFor("bob", "a1", "L-5"))
		wantStale(t, err, 9, 1)
		_, err = f.s.ClaimTask(ctx, f.project, "T-99", 1, f.leaseFor("bob", "a1", "L-5"))
		wantCode(t, err, domain.CodeNotFound, "Task")
		for name, edit := range map[string]func(*store.ClaimInput){
			"no owner":            func(in *store.ClaimInput) { in.Owner = "" },
			"no lease id":         func(in *store.ClaimInput) { in.LeaseID = "" },
			"an expired lease":    func(in *store.ClaimInput) { in.ExpiresAt = in.Now },
			"another board's col": func(in *store.ClaimInput) { in.ColumnID = "C-missing" },
		} {
			in := f.leaseFor("bob", "a1", "L-6")
			edit(&in)
			if _, err := f.s.ClaimTask(ctx, f.project, free.ID, 1, in); err == nil {
				t.Fatalf("%s: claimed", name)
			}
		}
		// Positive control: the free task is claimable after every refusal.
		if got, err := f.s.ClaimTask(ctx, f.project, free.ID, 1, f.leaseFor("bob", "a1", "L-7")); err != nil || got.Version != 2 {
			t.Fatalf("the control claim: %+v, %v", got, err)
		}
	})

	t.Run("HeartbeatMovesTheExpiryAndNotTheVersion", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		task, err := f.s.ClaimTask(ctx, f.project, task.ID, 1, f.leaseFor("bob", "a1", "L-1"))
		mustOK(t, err)
		got, err := f.s.Heartbeat(ctx, f.project, task.ID, "L-1", "bob", taskClock+120_000, taskClock+30_000, false)
		mustOK(t, err)
		if got.Claim.ExpiresAt != taskClock+120_000 || got.Version != task.Version || got.UpdatedAt != task.UpdatedAt {
			t.Fatalf("heartbeat: %+v version %d", got.Claim, got.Version)
		}
		read, _ := f.s.GetTask(ctx, f.project, task.ID)
		sameTask(t, read, got)
		_, err = f.s.Heartbeat(ctx, f.project, task.ID, "L-1", "bob", taskClock+30_000, taskClock+30_000, false)
		wantCode(t, err, domain.CodeValidation, "")
	})

	// G7: keepLonger never shortens the lease and still lengthens it; without it the expiry is
	// expiresAt either way.
	t.Run("HeartbeatKeepLongerNeverShortens", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		_, err := f.s.ClaimTask(ctx, f.project, task.ID, 1, f.leaseFor("bob", "a1", "L-1"))
		mustOK(t, err)
		got, err := f.s.Heartbeat(ctx, f.project, task.ID, "L-1", "bob", taskClock+30_000, taskClock+1, true)
		mustOK(t, err)
		if got.Claim.ExpiresAt != taskClock+60_000 {
			t.Fatalf("keepLonger shortened the lease to %d, want %d", got.Claim.ExpiresAt, taskClock+60_000)
		}
		got, err = f.s.Heartbeat(ctx, f.project, task.ID, "L-1", "bob", taskClock+90_000, taskClock+1, true)
		mustOK(t, err)
		if got.Claim.ExpiresAt != taskClock+90_000 {
			t.Fatalf("keepLonger did not lengthen the lease: %d, want %d", got.Claim.ExpiresAt, taskClock+90_000)
		}
		// Positive control: without keepLonger a shorter expiry is written.
		got, err = f.s.Heartbeat(ctx, f.project, task.ID, "L-1", "bob", taskClock+30_000, taskClock+1, false)
		mustOK(t, err)
		if got.Claim.ExpiresAt != taskClock+30_000 {
			t.Fatalf("an explicit expiry was not written: %d, want %d", got.Claim.ExpiresAt, taskClock+30_000)
		}
	})

	leaseRefusals := func(t *testing.T, call func(s store.Store, projectID, taskID, leaseID, owner string, now int64) error) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		unclaimed := f.add(t, "unclaimed", nil)
		_, err := f.s.ClaimTask(ctx, f.project, task.ID, 1, f.leaseFor("bob", "a1", "L-1"))
		mustOK(t, err)
		wantCode(t, call(f.s, f.project, "T-99", "L-1", "bob", taskClock), domain.CodeNotFound, "Task")
		wantCode(t, call(f.s, f.project, task.ID, "L-1", "carol", taskClock), domain.CodeConflictLocked, "")
		wantCode(t, call(f.s, f.project, task.ID, "L-other", "bob", taskClock), domain.CodeLeaseExpired, "")
		err = call(f.s, f.project, task.ID, "L-1", "bob", taskClock+60_000)
		wantCode(t, err, domain.CodeLeaseExpired, "")
		if e, _ := domain.AsBatonDeckError(err); e.Details["taskId"] != task.ID {
			t.Fatalf("LEASE_EXPIRED details %v", e.Details)
		}
		wantCode(t, call(f.s, f.project, unclaimed.ID, "L-1", "bob", taskClock), domain.CodeLeaseExpired, "")
		// Positive control: the lease itself, while live.
		mustOK(t, call(f.s, f.project, task.ID, "L-1", "bob", taskClock+1))
	}

	t.Run("HeartbeatRefusals", func(t *testing.T) {
		leaseRefusals(t, func(s store.Store, projectID, taskID, leaseID, owner string, now int64) error {
			_, err := s.Heartbeat(ctx, projectID, taskID, leaseID, owner, now+60_000, now, false)
			return err
		})
	})

	t.Run("ReleaseLeaseDropsTheLeaseAndNotTheVersion", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		task, err := f.s.ClaimTask(ctx, f.project, task.ID, 1, f.leaseFor("bob", "a1", "L-1"))
		mustOK(t, err)
		got, err := f.s.ReleaseLease(ctx, f.project, task.ID, "L-1", "bob", taskClock+1)
		mustOK(t, err)
		if got.Claim != nil || got.Version != task.Version || got.Status != domain.InProgress {
			t.Fatalf("released: %+v", got)
		}
		_, err = f.s.Heartbeat(ctx, f.project, task.ID, "L-1", "bob", taskClock+2, taskClock+1, false)
		wantCode(t, err, domain.CodeLeaseExpired, "")
	})

	t.Run("ReleaseLeaseRefusals", func(t *testing.T) {
		leaseRefusals(t, func(s store.Store, projectID, taskID, leaseID, owner string, now int64) error {
			_, err := s.ReleaseLease(ctx, projectID, taskID, leaseID, owner, now)
			return err
		})
	})

	t.Run("ListExpiredLeasesIsTheExpiredOldestFirst", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		second := newBoard(t, f.s, f.project, nil)
		claim := func(task store.Task, expires int64, col string) {
			in := f.leaseFor("bob", "a1", "L-"+task.ID)
			in.ExpiresAt, in.ColumnID = expires, col
			_, err := f.s.ClaimTask(ctx, f.project, task.ID, 1, in)
			mustOK(t, err)
		}
		claim(f.add(t, "late", nil), taskClock+20, "")        // T-1
		claim(f.add(t, "early", nil), taskClock+10, "")       // T-2
		claim(f.add(t, "live", nil), taskClock+1_000_000, "") // T-3
		f.add(t, "unclaimed", nil)                            // T-4
		elsewhere := f.add(t, "elsewhere", func(in *store.CreateTaskInput) {
			in.BoardID, in.ColumnID = second.ID, second.Columns[1].ID
		}) // T-5
		claim(elsewhere, taskClock+5, second.Columns[2].ID)
		now := taskClock + 100
		all, err := f.s.ListExpiredLeases(ctx, f.project, "", now, 0)
		mustOK(t, err)
		if got := taskIDs(all); !reflect.DeepEqual(got, []string{"T-5", "T-2", "T-1"}) {
			t.Fatalf("expired in the project: %v", got)
		}
		one, err := f.s.ListExpiredLeases(ctx, f.project, f.board, now, 1)
		mustOK(t, err)
		if got := taskIDs(one); !reflect.DeepEqual(got, []string{"T-2"}) {
			t.Fatalf("expired on the board, limit 1: %v", got)
		}
		// At exactly a lease's expiry it is expired: a lease is live only before ExpiresAt.
		edge, err := f.s.ListExpiredLeases(ctx, f.project, f.board, taskClock+10, 10)
		mustOK(t, err)
		if got := taskIDs(edge); !reflect.DeepEqual(got, []string{"T-2"}) {
			t.Fatalf("at the expiry instant: %v", got)
		}
		other := taskBoardOn(t, f.s)
		if none, err := f.s.ListExpiredLeases(ctx, other.project, "", now, 10); err != nil || len(none) != 0 {
			t.Fatalf("another project: %v %v", taskIDs(none), err)
		}
	})

	t.Run("PreviewFrontierIsTheClaimableBestFirst", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		add := func(title string, edit func(*store.CreateTaskInput)) store.Task { return f.add(t, title, edit) }
		lowFirst := add("low", func(in *store.CreateTaskInput) { in.Priority = domain.PriorityLow; in.OrderKey = "a0" })     // T-1
		urgent := add("urgent", func(in *store.CreateTaskInput) { in.Priority = domain.PriorityUrgent; in.OrderKey = "a5" }) // T-2
		normalB := add("normal b", func(in *store.CreateTaskInput) { in.OrderKey = "a2" })                                   // T-3
		normalA := add("normal a", func(in *store.CreateTaskInput) { in.OrderKey = "a1" })                                   // T-4
		scored := add("scored", func(in *store.CreateTaskInput) { in.Priority = domain.PriorityLow; in.OrderKey = "a9" })    // T-5
		_, err := f.s.UpdateTask(ctx, f.project, scored.ID, 1, store.TaskPatch{Score: ptr(5.0), UpdatedBy: "alice", UpdatedAt: taskClock})
		mustOK(t, err)
		// Not claimable: in review, a live lease, a backoff ahead, poisoned, racing.
		add("in review", func(in *store.CreateTaskInput) { in.Status = domain.Review }) // T-6
		held := add("held", nil)                                                        // T-7: TODO again, its lease still live
		_, err = f.s.ClaimTask(ctx, f.project, held.ID, 1, f.leaseFor("bob", "a1", "L-1"))
		mustOK(t, err)
		_, err = f.s.SetStatus(ctx, f.project, held.ID, 2, store.StatusChange{Status: domain.Todo, ColumnID: f.col[domain.Todo], UpdatedBy: "alice", UpdatedAt: taskClock})
		mustOK(t, err)
		backoff := add("backoff", nil) // T-8
		_, err = f.s.SetStatus(ctx, f.project, backoff.ID, 1, store.StatusChange{Status: domain.Todo, NextEligibleAt: &store.Nullable[int64]{Value: ptr(taskClock + 1)}, UpdatedBy: "alice", UpdatedAt: taskClock})
		mustOK(t, err)
		poison := add("poison", nil) // T-9
		_, err = f.s.SetStatus(ctx, f.project, poison.ID, 1, store.StatusChange{Status: domain.Blocked, BlockedReason: domain.BlockedReasonPoison, UpdatedBy: "alice", UpdatedAt: taskClock})
		mustOK(t, err)
		racing := add("racing", nil) // T-10
		_, err = f.s.UpdateTask(ctx, f.project, racing.ID, 1, store.TaskPatch{Racing: ptr(true), UpdatedBy: "alice", UpdatedAt: taskClock})
		mustOK(t, err)
		// A backoff that has passed does not hold a task back.
		past := add("past backoff", func(in *store.CreateTaskInput) { in.OrderKey = "a3" }) // T-11
		_, err = f.s.SetStatus(ctx, f.project, past.ID, 1, store.StatusChange{Status: domain.Todo, NextEligibleAt: &store.Nullable[int64]{Value: ptr(taskClock)}, UpdatedBy: "alice", UpdatedAt: taskClock})
		mustOK(t, err)

		want := []string{scored.ID, urgent.ID, normalA.ID, normalB.ID, past.ID, lowFirst.ID}
		got, err := f.s.PreviewFrontier(ctx, f.frontier(), 0)
		mustOK(t, err)
		if ids := taskIDs(got); !reflect.DeepEqual(ids, want) {
			t.Fatalf("frontier %v, want %v", ids, want)
		}
		top, err := f.s.PreviewFrontier(ctx, f.frontier(), 2)
		mustOK(t, err)
		if ids := taskIDs(top); !reflect.DeepEqual(ids, want[:2]) {
			t.Fatalf("frontier limit 2: %v", ids)
		}
		// Later, the backoff has passed too.
		later := f.frontier()
		later.Now = taskClock + 1
		got, err = f.s.PreviewFrontier(ctx, later, 50)
		mustOK(t, err)
		if len(got) != len(want)+1 {
			t.Fatalf("after the backoff: %v", taskIDs(got))
		}
		// Previewing claimed nothing.
		if read, _ := f.s.GetTask(ctx, f.project, scored.ID); read.Claim != nil || read.Version != 2 {
			t.Fatalf("preview wrote %+v", read)
		}
	})

	t.Run("PreviewFrontierBreaksTiesByIDAsBytes", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		for i := range 10 {
			f.add(t, fmt.Sprint("tie ", i+1), nil) // T-1..T-10: one score, priority and order key
		}
		got, err := f.s.PreviewFrontier(ctx, f.frontier(), 3)
		mustOK(t, err)
		if ids := taskIDs(got); !reflect.DeepEqual(ids, []string{"T-1", "T-10", "T-2"}) {
			t.Fatalf("frontier %v, want [T-1 T-10 T-2]", ids)
		}
		won, ok, err := f.s.ClaimNext(ctx, f.frontier(), f.leaseFor("bob", "a1", "L-1"))
		mustOK(t, err)
		claimed, _, err := f.s.ClaimNext(ctx, f.frontier(), f.leaseFor("bob", "a2", "L-2"))
		mustOK(t, err)
		if !ok || won.ID != "T-1" || claimed.ID != "T-10" {
			t.Fatalf("claimed %s then %s, want T-1 then T-10", won.ID, claimed.ID)
		}
	})

	t.Run("PreviewFrontierFiltersByAssigneeAndCapabilities", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		f.add(t, "go", func(in *store.CreateTaskInput) { in.RequiredCapabilities = []string{"go"}; in.OrderKey = "a1" })
		f.add(t, "go+sql", func(in *store.CreateTaskInput) { in.RequiredCapabilities = []string{"go", "sql"}; in.OrderKey = "a2" })
		f.add(t, "anyone", func(in *store.CreateTaskInput) { in.OrderKey = "a3" })
		f.add(t, "bob's", func(in *store.CreateTaskInput) { in.Assignee = ptr("bob"); in.OrderKey = "a4" })
		for name, c := range map[string]struct {
			edit func(*store.FrontierQuery)
			want []string
		}{
			"everything":         {func(*store.FrontierQuery) {}, []string{"T-1", "T-2", "T-3", "T-4"}},
			"bob's only":         {func(q *store.FrontierQuery) { q.Assignee = ptr("bob") }, []string{"T-4"}},
			"go":                 {func(q *store.FrontierQuery) { q.Capabilities = []string{"go"} }, []string{"T-1", "T-3", "T-4"}},
			"go and sql":         {func(q *store.FrontierQuery) { q.Capabilities = []string{"sql", "go"} }, []string{"T-1", "T-2", "T-3", "T-4"}},
			"none of them":       {func(q *store.FrontierQuery) { q.Capabilities = []string{"rust"} }, []string{"T-3", "T-4"}},
			"another board":      {func(q *store.FrontierQuery) { q.BoardID = "B-missing" }, []string{}},
			"another project":    {func(q *store.FrontierQuery) { q.ProjectID = "P-missing" }, []string{}},
			"an absent assignee": {func(q *store.FrontierQuery) { q.Assignee = ptr("nobody") }, []string{}},
		} {
			q := f.frontier()
			q.Caller = "bob" // T-4 is bob's: the frontier without an assignee is the caller's and the unassigned
			c.edit(&q)
			got, err := f.s.PreviewFrontier(ctx, q, 50)
			mustOK(t, err)
			if ids := taskIDs(got); !reflect.DeepEqual(ids, c.want) {
				t.Fatalf("%s: %v, want %v", name, ids, c.want)
			}
		}
		_, err := f.s.PreviewFrontier(ctx, store.FrontierQuery{ProjectID: f.project, Now: taskClock}, 10)
		wantCode(t, err, domain.CodeValidation, "")
	})

	// P-275 (audit A1): without an assignee nobody's frontier holds work routed to another, on the
	// preview and on the claim statement alike; the caller's own (by id, or by its login in any case)
	// and the unassigned stay in it.
	t.Run("FrontierWithoutAssigneeIsTheCallersAndTheUnassigned", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		u := testUser("U-bob", "bob@x.test", 1)
		mustOK(t, f.s.CreateUser(ctx, u))
		f.add(t, "dave's", func(in *store.CreateTaskInput) { in.Assignee = ptr("dave@x.test"); in.OrderKey = "a1" })
		f.add(t, "bob's by id", func(in *store.CreateTaskInput) { in.Assignee = ptr("U-bob"); in.OrderKey = "a2" })
		f.add(t, "bob's by login", func(in *store.CreateTaskInput) { in.Assignee = ptr("Bob@X.test"); in.OrderKey = "a3" })
		f.add(t, "anyone", func(in *store.CreateTaskInput) { in.OrderKey = "a4" })
		for caller, want := range map[string][]string{
			"U-bob":  {"T-2", "T-3", "T-4"},
			"U-carl": {"T-4"},
			"":       {"T-4"},
		} {
			q := f.frontier()
			q.Caller = caller
			got, err := f.s.PreviewFrontier(ctx, q, 50)
			mustOK(t, err)
			if ids := taskIDs(got); !reflect.DeepEqual(ids, want) {
				t.Fatalf("caller %q: %v, want %v", caller, ids, want)
			}
		}
		q := f.frontier()
		q.Caller = "U-bob"
		var won []string
		for i := range 4 {
			got, ok, err := f.s.ClaimNext(ctx, q, f.leaseFor("U-bob", "a", fmt.Sprintf("L-%d", i)))
			mustOK(t, err)
			if ok {
				won = append(won, got.ID)
			}
		}
		if !reflect.DeepEqual(won, []string{"T-2", "T-3", "T-4"}) {
			t.Fatalf("U-bob claimed %v, want [T-2 T-3 T-4] and never dave's T-1", won)
		}
		dave, err := f.s.GetTask(ctx, f.project, "T-1")
		mustOK(t, err)
		if dave.Status != domain.Todo || dave.Claim != nil {
			t.Fatalf("dave's T-1 is %s claimed by %+v, want TODO and unclaimed", dave.Status, dave.Claim)
		}
	})

	// P-239: SprintID narrows the frontier to the sprint's members (its id in SprintIDs, matched
	// whole, not as a prefix), on the preview and on the claim statement alike, past a better
	// non-member; "" filters nothing.
	t.Run("FrontierNarrowsToASprint", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		f.add(t, "urgent, no sprint", func(in *store.CreateTaskInput) { in.Priority = domain.PriorityUrgent; in.OrderKey = "a1" })
		f.add(t, "in S-a", func(in *store.CreateTaskInput) { in.SprintIDs = []string{"S-a"}; in.OrderKey = "a2" })
		f.add(t, "in S-ab", func(in *store.CreateTaskInput) { in.SprintIDs = []string{"S-ab"}; in.OrderKey = "a3" })
		f.add(t, "in S-x and S-a", func(in *store.CreateTaskInput) { in.SprintIDs = []string{"S-x", "S-a"}; in.OrderKey = "a4" })
		for sprint, want := range map[string][]string{
			"":     {"T-1", "T-2", "T-3", "T-4"},
			"S-a":  {"T-2", "T-4"},
			"S-ab": {"T-3"},
			"S-no": {},
		} {
			q := f.frontier()
			q.SprintID = sprint
			got, err := f.s.PreviewFrontier(ctx, q, 50)
			mustOK(t, err)
			if ids := taskIDs(got); !reflect.DeepEqual(ids, want) {
				t.Fatalf("sprint %q: %v, want %v", sprint, ids, want)
			}
		}
		q := f.frontier()
		q.SprintID = "S-a"
		var won []string
		for i := range 3 {
			got, ok, err := f.s.ClaimNext(ctx, q, f.leaseFor("bob", "a", fmt.Sprintf("L-%d", i)))
			mustOK(t, err)
			if ok {
				won = append(won, got.ID)
			}
		}
		if !reflect.DeepEqual(won, []string{"T-2", "T-4"}) {
			t.Fatalf("claimed %v in S-a, want [T-2 T-4] and never the urgent non-member T-1", won)
		}
		first, err := f.s.GetTask(ctx, f.project, "T-1")
		mustOK(t, err)
		if first.Status != domain.Todo || first.Claim != nil {
			t.Fatalf("the non-member T-1 is %s claimed by %+v, want TODO and unclaimed", first.Status, first.Claim)
		}
	})

	t.Run("FrontierNarrowsToTaskIDs", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		f.add(t, "urgent, not named", func(in *store.CreateTaskInput) { in.Priority = domain.PriorityUrgent; in.OrderKey = "a1" })
		f.add(t, "named", func(in *store.CreateTaskInput) { in.OrderKey = "a2" })
		f.add(t, "named, not claimable", func(in *store.CreateTaskInput) { in.Status = domain.Review; in.OrderKey = "a3" })
		f.add(t, "named too", func(in *store.CreateTaskInput) { in.OrderKey = "a4" })
		for _, c := range []struct {
			ids  []string
			want []string
		}{
			{nil, []string{"T-1", "T-2", "T-4"}},
			{[]string{"T-4", "T-2", "T-3"}, []string{"T-2", "T-4"}},
			{[]string{"T-9"}, []string{}},
			{[]string{}, []string{}},
		} {
			q := f.frontier()
			q.TaskIDs = c.ids
			got, err := f.s.PreviewFrontier(ctx, q, 50)
			mustOK(t, err)
			if ids := taskIDs(got); !reflect.DeepEqual(ids, c.want) {
				t.Fatalf("taskIds %v: %v, want %v", c.ids, ids, c.want)
			}
		}
		q := f.frontier()
		q.TaskIDs = []string{"T-4", "T-2", "T-3"}
		var won []string
		for i := range 3 {
			got, ok, err := f.s.ClaimNext(ctx, q, f.leaseFor("bob", "a", fmt.Sprintf("L-%d", i)))
			mustOK(t, err)
			if ok {
				won = append(won, got.ID)
			}
		}
		if !reflect.DeepEqual(won, []string{"T-2", "T-4"}) {
			t.Fatalf("claimed %v among [T-4 T-2 T-3], want [T-2 T-4], never the urgent T-1 nor the REVIEW T-3", won)
		}
		q.TaskIDs = []string{}
		if got, ok, err := f.s.ClaimNext(ctx, q, f.leaseFor("bob", "a", "L-e")); err != nil || ok {
			t.Fatalf("an empty TaskIDs claimed %+v (%v)", got, err)
		}
		first, err := f.s.GetTask(ctx, f.project, "T-1")
		mustOK(t, err)
		if first.Status != domain.Todo || first.Claim != nil {
			t.Fatalf("the unnamed T-1 is %s claimed by %+v, want TODO and unclaimed", first.Status, first.Claim)
		}
	})

	t.Run("ClaimsLandAtTheHeadOfTheirColumnWithAFreshKey", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		f.add(t, "already there", func(in *store.CreateTaskInput) {
			in.ColumnID, in.Status, in.OrderKey = f.col[domain.InProgress], domain.InProgress, "a5"
		})
		head := "a5"
		seen := map[string]bool{head: true}
		for i := range 4 {
			task := f.add(t, fmt.Sprint("t", i), nil) // each at "a0", the key the one before carried away
			var got store.Task
			if i%2 == 0 {
				var ok bool
				var err error
				got, ok, err = f.s.ClaimNext(ctx, f.frontier(), f.leaseFor("bob", fmt.Sprint("a", i), fmt.Sprint("L-", i)))
				if err != nil || !ok || got.ID != task.ID {
					t.Fatalf("claim_next %d: %s ok %v, %v", i, got.ID, ok, err)
				}
			} else {
				var err error
				got, err = f.s.ClaimTask(ctx, f.project, task.ID, 1, f.leaseFor("bob", fmt.Sprint("a", i), fmt.Sprint("L-", i)))
				mustOK(t, err)
			}
			if got.ColumnID != f.col[domain.InProgress] || !domain.IsValidOrder(got.OrderKey) || got.OrderKey >= head || seen[got.OrderKey] {
				t.Fatalf("claim %d landed in %s at %q, want a fresh well-formed key before the IN_PROGRESS head %q (keys so far %v)", i, got.ColumnID, got.OrderKey, head, seen)
			}
			read, err := f.s.GetTask(ctx, f.project, task.ID)
			mustOK(t, err)
			sameTask(t, read, got)
			head, seen[got.OrderKey] = got.OrderKey, true
		}
	})

	t.Run("ClaimNextTakesTheFrontierInOrderThenReportsItEmpty", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		f.add(t, "second", func(in *store.CreateTaskInput) { in.OrderKey = "a2" })
		f.add(t, "first", func(in *store.CreateTaskInput) { in.OrderKey = "a1" })
		f.add(t, "skipped", func(in *store.CreateTaskInput) { in.Status = domain.Review })
		var won []string
		for i := range 3 {
			task, ok, err := f.s.ClaimNext(ctx, f.frontier(), f.leaseFor("bob", fmt.Sprint("a", i), fmt.Sprint("L-", i)))
			mustOK(t, err)
			if !ok {
				break
			}
			if task.Status != domain.InProgress || task.Claim == nil || task.Claim.LeaseID != fmt.Sprint("L-", i) ||
				task.Version != 2 || task.ColumnID != f.col[domain.InProgress] {
				t.Fatalf("claimed: %+v", task)
			}
			won = append(won, task.ID)
		}
		if !reflect.DeepEqual(won, []string{"T-2", "T-1"}) {
			t.Fatalf("won %v, want [T-2 T-1] and then an empty frontier", won)
		}
		_, _, err := f.s.ClaimNext(ctx, store.FrontierQuery{ProjectID: f.project, Now: taskClock}, f.leaseFor("bob", "a9", "L-9"))
		wantCode(t, err, domain.CodeValidation, "")
		bad := f.leaseFor("", "a9", "L-9")
		_, _, err = f.s.ClaimNext(ctx, f.frontier(), bad)
		wantCode(t, err, domain.CodeValidation, "")
	})

	t.Run("ClaimNextFiftyConcurrentCallersHaveFiftyDistinctWinners", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		const n = 50
		for i := range n {
			f.add(t, fmt.Sprint("task ", i), nil)
		}
		ids := make([]string, n)
		oks := make([]bool, n)
		errs := make([]error, n)
		var wg sync.WaitGroup
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				task, ok, err := f.s.ClaimNext(ctx, f.frontier(), f.leaseFor(fmt.Sprint("agent-", i), fmt.Sprint("a", i), fmt.Sprint("L-", i)))
				ids[i], oks[i], errs[i] = task.ID, ok, err
			}()
		}
		wg.Wait()
		seen := map[string]int{}
		for i := range n {
			if errs[i] != nil || !oks[i] {
				t.Fatalf("caller %d: ok %v, %v", i, oks[i], errs[i])
			}
			seen[ids[i]]++
		}
		dup := 0
		for _, c := range seen {
			dup += c - 1
		}
		t.Logf("%d callers: %d distinct winners, %d duplicates", n, len(seen), dup)
		if len(seen) != n || dup != 0 {
			t.Fatalf("%d distinct winners and %d duplicates, want %d and 0", len(seen), dup, n)
		}
		// Every winner left "a0" behind and landed in IN_PROGRESS with a key of its own, though the
		// claims read that column at once.
		landed, err := f.s.ListTasks(ctx, f.project, store.TaskFilter{BoardID: f.board, ColumnID: f.col[domain.InProgress]}, "", n)
		mustOK(t, err)
		keys := map[string]bool{}
		for _, task := range landed.Tasks {
			keys[task.OrderKey] = true
		}
		if len(landed.Tasks) != n || len(keys) != n {
			t.Fatalf("%d tasks in IN_PROGRESS under %d distinct order keys, want %d and %d", len(landed.Tasks), len(keys), n, n)
		}
		if _, ok, err := f.s.ClaimNext(ctx, f.frontier(), f.leaseFor("late", "z", "L-z")); err != nil || ok {
			t.Fatalf("the 51st caller: ok %v, %v; want an empty frontier", ok, err)
		}
	})

	t.Run("ClaimTaskTwoCallersOneWinner", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = f.s.ClaimTask(ctx, f.project, task.ID, 1, f.leaseFor(fmt.Sprint("owner-", i), "a", fmt.Sprint("L-", i)))
			}()
		}
		wg.Wait()
		won := 0
		for _, err := range errs {
			if err == nil {
				won++
				continue
			}
			if e, ok := domain.AsBatonDeckError(err); !ok || (e.Code != domain.CodeStale && e.Code != domain.CodeConflictLocked) {
				t.Fatalf("the loser: %v, want STALE or CONFLICT_LOCKED", err)
			}
		}
		if won != 1 {
			t.Fatalf("%d winners, want 1", won)
		}
	})
}
