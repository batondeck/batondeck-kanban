package memstore

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The claims area of contract_claims.go. One mutex makes every claim one writer's: ClaimNext's
// select and write cannot interleave with another caller's.

func validClaim(in store.ClaimInput) error {
	if in.Owner == "" || in.LeaseID == "" {
		return domain.Validation("a claim needs an owner and a lease id", nil)
	}
	if in.ExpiresAt <= in.Now {
		return domain.Validation("a lease must expire after now", map[string]any{"expiresAt": in.ExpiresAt, "now": in.Now})
	}
	return nil
}

func millis(ms int64) time.Time { return time.UnixMilli(ms) }

// view is the task as domain.IsClaimable reads it.
func view(t store.Task) domain.ClaimView {
	v := domain.ClaimView{Status: t.Status, BlockedBy: t.BlockedBy, Racing: t.Racing}
	if t.BlockedReason != nil {
		v.BlockedReason = *t.BlockedReason
	}
	if t.Claim != nil {
		v.Claim = &domain.Lease{ExpiresAt: millis(t.Claim.ExpiresAt)}
	}
	if t.NextEligibleAt != nil {
		v.NextEligibleAt = millis(*t.NextEligibleAt)
	}
	if t.Assignee != nil {
		v.Assignee = *t.Assignee
	}
	return v
}

func live(c *store.TaskClaim, now int64) bool { return c != nil && c.ExpiresAt > now }

func sameAgent(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// claim writes the lease on t: IN_PROGRESS, in.ColumnID when given, no backoff, the version one up.
func (st *state) claim(t store.Task, in store.ClaimInput) (store.Task, error) {
	if in.ColumnID != "" {
		if err := st.columnOn(t.ProjectID, t.BoardID, in.ColumnID); err != nil {
			return store.Task{}, err
		}
		t.ColumnID = in.ColumnID
	}
	t.Status = domain.InProgress
	t.Claim = &store.TaskClaim{Owner: in.Owner, Agent: clonePtr(in.Agent), AgentID: clonePtr(in.AgentID), LeaseID: in.LeaseID, ExpiresAt: in.ExpiresAt}
	t.NextEligibleAt = nil
	t.Version++
	t.UpdatedBy, t.UpdatedAt = in.Owner, in.Now
	return st.put(t), nil
}

func validFrontier(q store.FrontierQuery) error {
	if q.BoardID == "" {
		return domain.Validation("a frontier is one board's", nil)
	}
	return nil
}

// frontier is the board's claimable tasks for q, best first.
func (st *state) frontier(q store.FrontierQuery) []store.Task {
	var out []store.Task
	now := millis(q.Now)
	for k, t := range st.tasks {
		if k[0] != q.ProjectID || t.BoardID != q.BoardID || !domain.IsClaimable(view(t), now) {
			continue
		}
		if q.Assignee != nil && (t.Assignee == nil || *t.Assignee != *q.Assignee) {
			continue
		}
		if q.Assignee == nil && !st.callers(t.Assignee, q.Caller) {
			continue
		}
		if len(q.Capabilities) > 0 && !allIn(t.RequiredCapabilities, q.Capabilities) {
			continue
		}
		if q.SprintID != "" && !slices.Contains(t.SprintIDs, q.SprintID) && !st.runInSprint(k[0], t, q.SprintID) {
			continue
		}
		if q.TaskIDs != nil && !slices.Contains(q.TaskIDs, t.ID) {
			continue
		}
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b store.Task) int {
		return cmp.Or(
			cmp.Compare(b.Score, a.Score),
			cmp.Compare(priorityRank[b.Priority], priorityRank[a.Priority]),
			strings.Compare(a.OrderKey, b.OrderKey),
			strings.Compare(a.ID, b.ID),
		)
	})
	return out
}

// callers is the routing half of the frontier without an assignee: the task is unassigned, or
// assigned to caller's id or, in any case, to its account's email.
// runInSprint reports whether t is a run of a member of sprintID: the frontier's sprint filter
// takes a raced member's runs with the member.
func (st *state) runInSprint(projectID string, t store.Task, sprintID string) bool {
	if t.RunOf == nil {
		return false
	}
	parent, ok := st.tasks[[2]string{projectID, *t.RunOf}]
	return ok && slices.Contains(parent.SprintIDs, sprintID)
}

func (st *state) callers(assignee *string, caller string) bool {
	if assignee == nil {
		return true
	}
	if caller == "" {
		return false
	}
	if *assignee == caller {
		return true
	}
	u, ok := st.users[caller]
	return ok && strings.ToLower(*assignee) == u.Email
}

func allIn(need, have []string) bool {
	for _, n := range need {
		if !slices.Contains(have, n) {
			return false
		}
	}
	return true
}

func (s *Store) ClaimNext(ctx context.Context, q store.FrontierQuery, in store.ClaimInput) (store.Task, bool, error) {
	if err := validFrontier(q); err != nil {
		return store.Task{}, false, err
	}
	if err := validClaim(in); err != nil {
		return store.Task{}, false, err
	}
	var won bool
	t, err := get(ctx, s, func(st *state) (store.Task, error) {
		f := st.frontier(q)
		if len(f) == 0 {
			return store.Task{}, nil
		}
		won = true
		return st.claim(cloneTask(f[0]), in)
	})
	if err != nil {
		return store.Task{}, false, err
	}
	return t, won, nil
}

func (s *Store) ClaimTask(ctx context.Context, projectID, taskID string, expectedVersion int64, in store.ClaimInput) (store.Task, error) {
	if err := validClaim(in); err != nil {
		return store.Task{}, err
	}
	return get(ctx, s, func(st *state) (store.Task, error) {
		t, err := st.taskAt(projectID, taskID, expectedVersion)
		if err != nil {
			return store.Task{}, err
		}
		if t.Racing {
			return store.Task{}, domain.ConflictLocked(taskID, "race")
		}
		if len(t.BlockedBy) > 0 {
			return store.Task{}, domain.InvalidTransition(t.Status, domain.InProgress, nil, nil)
		}
		mine := t.Claim != nil && t.Claim.Owner == in.Owner && sameAgent(t.Claim.AgentID, in.AgentID)
		if live(t.Claim, in.Now) && !mine {
			return store.Task{}, domain.ConflictLocked(taskID, t.Claim.Owner)
		}
		reclaimable := t.Status == domain.Todo || (t.Status == domain.InProgress && (!live(t.Claim, in.Now) || mine))
		if !reclaimable {
			return store.Task{}, domain.InvalidTransition(t.Status, domain.InProgress, nil, nil)
		}
		return st.claim(t, in)
	})
}

// heldLease is the task whose lease is leaseID, held by owner and live at now.
func (st *state) heldLease(projectID, taskID, leaseID, owner string, now int64) (store.Task, error) {
	t, err := st.task(projectID, taskID)
	if err != nil {
		return store.Task{}, err
	}
	if t.Claim == nil {
		return store.Task{}, store.LeaseExpired(taskID)
	}
	if t.Claim.Owner != owner {
		return store.Task{}, domain.ConflictLocked(taskID, t.Claim.Owner)
	}
	if t.Claim.LeaseID != leaseID || !live(t.Claim, now) {
		return store.Task{}, store.LeaseExpired(taskID)
	}
	return t, nil
}

func (s *Store) Heartbeat(ctx context.Context, projectID, taskID, leaseID, owner string, expiresAt, now int64, keepLonger bool) (store.Task, error) {
	if expiresAt <= now {
		return store.Task{}, domain.Validation("a lease must expire after now", map[string]any{"expiresAt": expiresAt, "now": now})
	}
	return get(ctx, s, func(st *state) (store.Task, error) {
		t, err := st.heldLease(projectID, taskID, leaseID, owner, now)
		if err != nil {
			return store.Task{}, err
		}
		if !keepLonger || expiresAt > t.Claim.ExpiresAt {
			t.Claim.ExpiresAt = expiresAt
		}
		return st.put(t), nil
	})
}

func (s *Store) ReleaseLease(ctx context.Context, projectID, taskID, leaseID, owner string, now int64) (store.Task, error) {
	return get(ctx, s, func(st *state) (store.Task, error) {
		t, err := st.heldLease(projectID, taskID, leaseID, owner, now)
		if err != nil {
			return store.Task{}, err
		}
		t.Claim = nil
		return st.put(t), nil
	})
}

func (s *Store) ListExpiredLeases(ctx context.Context, projectID, boardID string, now int64, limit int) ([]store.Task, error) {
	limit = clamp(limit, 50, 200)
	return get(ctx, s, func(st *state) ([]store.Task, error) {
		out := []store.Task{}
		for k, t := range st.tasks {
			if k[0] == projectID && (boardID == "" || t.BoardID == boardID) && t.Claim != nil && !live(t.Claim, now) {
				out = append(out, cloneTask(t))
			}
		}
		slices.SortFunc(out, func(a, b store.Task) int {
			return cmp.Or(cmp.Compare(a.Claim.ExpiresAt, b.Claim.ExpiresAt), strings.Compare(a.ID, b.ID))
		})
		if len(out) > limit {
			out = out[:limit]
		}
		return out, nil
	})
}

func (s *Store) PreviewFrontier(ctx context.Context, q store.FrontierQuery, limit int) ([]store.Task, error) {
	if err := validFrontier(q); err != nil {
		return nil, err
	}
	limit = clamp(limit, 50, 200)
	return get(ctx, s, func(st *state) ([]store.Task, error) {
		f := st.frontier(q)
		if len(f) > limit {
			f = f[:limit]
		}
		out := make([]store.Task, len(f))
		for i, t := range f {
			out[i] = cloneTask(t)
		}
		return out, nil
	})
}
