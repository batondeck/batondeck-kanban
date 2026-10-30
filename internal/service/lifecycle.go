package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	mrand "math/rand/v2"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// lifecycle is service.Lifecycle: the lease and its outcomes (conductor src/tools/lifecycle.ts).
// Every write goes through mutate. A lease-guarded outcome checks the caller's lease against the
// task mutate loaded (leaseHeld) and lands the task with ONE versioned store write (ClaimTask or
// SetStatus), so the version moves once. A call that carries no expected version (every one here
// but cancel_task) is re-judged when that write meets a concurrent one (retryStale).
type lifecycle struct{ c *Core }

var _ Lifecycle = lifecycle{}

// Lifecycle is the lease lifecycle service.
func (c *Core) Lifecycle() Lifecycle { return lifecycle{c} }

const (
	// DefaultLeaseSeconds is conductor's LEASE_DEFAULT_SECONDS (300); leaseSeconds is at most a day.
	// TODO(spec): no core config carries it yet (P-81).
	DefaultLeaseSeconds = 300
	maxLeaseSeconds     = 86_400
	// Conductor's MAX_REASON_CHARS (src/config/constants.ts); the label and reviewer bounds are
	// tasks.go's.
	maxReasonChars = 4_000
	// staleAttempts bounds retryStale.
	staleAttempts = 3
	// next_task's inboxLimit (default 20, max 100).
	inboxDefaultLimit = 20
	inboxMaxLimit     = 100
	// checkpointWindow is each of the two context-item pages the checkpoint gap reads (conductor's
	// readCheckpointBodies: newest 50 and oldest 50).
	checkpointWindow = 50
	// labelNoAlternatives exempts a task from the checkpoint gap (conductor CHECKPOINT_EXEMPT_LABEL).
	labelNoAlternatives = "no-alternatives"
)

// rejectedSection is conductor's REJECTED_SECTION (src/domain/checkpoints.ts:55).
var rejectedSection = regexp.MustCompile(`(?im)^[ \t]*(?:\*{1,2}|#{1,6}[ \t]*)?REJECTED\b`)

// errEmptyFrontier rolls back a claim_next that won nothing, so an empty claim writes no row.
var errEmptyFrontier = domain.NewError(domain.CodeNotFound, "the claim frontier is empty", nil)

// errReapResolved rolls back the reap of a task resolved since ListExpiredLeases read it.
var errReapResolved = domain.NewError(domain.CodeLeaseExpired, "the lease was resolved before the reap", nil)

func newLeaseID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "L-" + hex.EncodeToString(b[:])
}

func leaseSeconds(v int64) (int64, error) {
	switch {
	case v == 0:
		return DefaultLeaseSeconds, nil
	case v < 0 || v > maxLeaseSeconds:
		return 0, domain.Validation("leaseSeconds must be a positive integer of at most 86400", map[string]any{"field": "leaseSeconds"})
	}
	return v, nil
}

func reasonArg(field, v string, min bool) error {
	if min {
		if err := required(field, v); err != nil {
			return err
		}
	}
	return maxChars(field, v, maxReasonChars)
}

// unsupported refuses an input the store cannot serve yet, naming it, rather than ignoring it.
func unsupported(field, why string) error {
	return domain.Validation(field+" is not supported yet: "+why, map[string]any{"field": field})
}

// leaseHeld is conductor's assertLeaseHeld: the caller holds this live lease. Any mismatch (no
// lease, another lease id, another owner, past its expiry) is LEASE_EXPIRED, indistinguishable from a
// lease that is gone, so a leaseId lifted from a board read drives nothing.
func leaseHeld(t store.Task, leaseID string, caller domain.Caller, now int64) error {
	c := t.Claim
	if c == nil || c.LeaseID != leaseID || c.Owner != caller.Identity || c.ExpiresAt <= now {
		return store.LeaseExpired(t.ID)
	}
	return nil
}

func liveLease(t store.Task, now int64) bool { return t.Claim != nil && t.Claim.ExpiresAt > now }

// heldBy is whether t is IN_PROGRESS under a live lease of the caller that store.ClaimTask would
// replace as the caller's own (the same identity under the same agent, claimInput's AgentID), on a
// task it would still claim.
func heldBy(t store.Task, caller domain.Caller, now int64) bool {
	return t.Status == domain.InProgress && liveLease(t, now) && t.Claim.Owner == caller.Identity &&
		sameAgentID(t.Claim.AgentID, callerAgent(caller)) && !t.Racing && len(t.BlockedBy) == 0
}

// sameAgentID is store's sameAgent: two agent handles, nil only equal to nil.
func sameAgentID(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// retryStale re-runs a call that carries no expected version when its versioned write lost a race
// (STALE): the next run re-reads the task and judges it again, so a losing claim answers
// CONFLICT_LOCKED, as Firestore's retried transaction did for conductor.
func retryStale[R any](f func() (R, error)) (R, error) {
	var r R
	var err error
	for range staleAttempts {
		r, err = f()
		if e, ok := domain.AsBatonDeckError(err); !ok || e.Code != domain.CodeStale {
			return r, err
		}
	}
	return r, err
}

// statusColumn is the board's first column of status s, or "" (the task keeps its column), as
// conductor's columnIdForStatus.
func statusColumn(b store.Board, s domain.Status) string {
	for _, c := range b.Columns {
		if c.Status == s {
			return c.ID
		}
	}
	return ""
}

// claimInput is the caller's claim. Its AgentID is the caller's agent (Caller.Agent), so two agents
// of one identity are two holders (CONFLICT_LOCKED), while the sessions of one agent share its lease.
func claimInput(caller domain.Caller, leaseID string, now, secs int64, columnID string) store.ClaimInput {
	return store.ClaimInput{Owner: caller.Identity, Agent: callerAgent(caller), AgentID: callerAgent(caller), LeaseID: leaseID, ExpiresAt: now + secs*1000, Now: now, ColumnID: columnID}
}

func claimView(t store.Task) domain.ClaimView {
	v := domain.ClaimView{Status: t.Status, BlockedBy: t.BlockedBy, BlockedReason: reasonOf(t), Racing: t.Racing}
	if t.Claim != nil {
		v.Claim = &domain.Lease{ExpiresAt: time.UnixMilli(t.Claim.ExpiresAt)}
	}
	if t.NextEligibleAt != nil {
		v.NextEligibleAt = time.UnixMilli(*t.NextEligibleAt)
	}
	if t.Assignee != nil {
		v.Assignee = *t.Assignee
	}
	return v
}

// ringFrontier rings broadcast when task is open-frontier work (claimable now, assigned to nobody):
// a TODO task still inside its retry backoff rings nothing, or every woken agent finds nothing.
func (t *txn) ringFrontier(task store.Task) {
	if domain.IsOpenFrontier(claimView(task), t.Now) {
		t.Ring(Doorbell{Kind: DoorbellBroadcast, TaskID: task.ID, BoardID: task.BoardID})
	}
}

func (t *txn) ringChanged(task store.Task) {
	t.Ring(Doorbell{Kind: ChangeKindChanged, TaskID: task.ID, BoardID: task.BoardID})
}

func (t *txn) event(task store.Task, typ domain.EventType, data map[string]any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	t.Event(store.EventInput{TaskID: task.ID, BoardID: task.BoardID, Type: typ, Data: raw})
	return nil
}

// checkWIP is the IN_PROGRESS column's wipLimit for a task entering IN_PROGRESS from from: the
// project lock first (so two claims cannot both count the last slot under READ COMMITTED), then the
// count, inside the transaction. A board with no limit costs nothing.
func (t *txn) checkWIP(board store.Board, from domain.Status) error {
	if from == domain.InProgress {
		return nil
	}
	for _, c := range board.Columns {
		if c.Status != domain.InProgress {
			continue
		}
		if c.WIPLimit == nil {
			return nil
		}
		if err := t.Tx.LockProject(t.ctx, t.ProjectID); err != nil {
			return err
		}
		inProgress := domain.InProgress
		n, err := t.Tx.CountTasks(t.ctx, t.ProjectID, store.TaskFilter{BoardID: board.ID, Status: &inProgress})
		if err != nil {
			return err
		}
		if n >= *c.WIPLimit {
			return domain.WIPExceeded(c.ID, int(*c.WIPLimit))
		}
		return nil
	}
	return nil
}

// withSummary is task as the wire carries it, its contextSummary read (one statement).
func withSummary(ctx context.Context, st store.Store, task store.Task) (Task, error) {
	sum, err := st.GetSummary(ctx, task.ProjectID, task.ID)
	if err != nil {
		return Task{}, err
	}
	return taskOut(task, sum.Body)
}

// replayedLease is conductor's replayedClaim: an idempotent replay hands back the ORIGINAL lease, and
// one that has expired since is LEASE_EXPIRED, not a usable result.
func replayedLease(task *Task, now time.Time) error {
	if task == nil || task.Claim == nil {
		return internalf("a replayed claim carries no lease")
	}
	at, err := time.Parse(time.RFC3339Nano, task.Claim.ExpiresAt)
	if err != nil || !at.After(now) {
		return store.LeaseExpired(task.ID)
	}
	return nil
}

// Claim is claim_task. ClaimTaskReq carries no version (conductor's schema): the store write is
// checked against the version mutate loaded, and a lost race is re-judged (retryStale), so the loser
// of two concurrent claims answers CONFLICT_LOCKED. The task gains no assignee (conductor's
// performClaim sets none). It rings nobody (owner ruling 2026-10-23): mutate's change row is
// ChangeKindChanged. A claim of a task whose live lease the caller already holds answers that lease
// and writes nothing to the task but a longer expiry, when this claim's leaseSeconds asks for one
// (G6): one agent in several sessions never mints a second lease that would silently end the
// first. Another agent of the same identity (Caller.Agent, P-257) is another holder and is refused
// CONFLICT_LOCKED, as conductor does for two distinct handles.
func (l lifecycle) Claim(ctx context.Context, caller domain.Caller, req ClaimTaskReq) (ClaimTaskResp, error) {
	a, err := l.c.authorize(ctx, caller, req.ProjectID, "claim_task", domain.GrantSubject{}, true)
	if err != nil {
		return ClaimTaskResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return ClaimTaskResp{}, err
	}
	secs, err := leaseSeconds(req.LeaseSeconds)
	if err != nil {
		return ClaimTaskResp{}, err
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return ClaimTaskResp{}, err
	}
	leaseID := newLeaseID()
	resp, err := retryStale(func() (ClaimTaskResp, error) {
		return mutate(ctx, l.c, caller, mutation{
			Tool: "claim_task", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
			ProjectID: req.ProjectID, TaskID: req.TaskID, IdempotencyKey: req.IdempotencyKey, Access: a, KeepVersion: true,
		}, func(t *txn) (ClaimTaskResp, error) {
			cur := *t.Task
			if now := t.Now.UnixMilli(); heldBy(cur, caller, now) {
				// The second claim's leaseSeconds lengthens the shared lease, never shortens it.
				held, err := t.Tx.Heartbeat(ctx, req.ProjectID, cur.ID, cur.Claim.LeaseID, caller.Identity, now+secs*1000, now, true)
				if err != nil {
					return ClaimTaskResp{}, err
				}
				out, err := withSummary(ctx, t.Tx, held)
				if err != nil {
					return ClaimTaskResp{}, err
				}
				return ClaimTaskResp{LeaseID: cur.Claim.LeaseID, Task: out}, nil
			}
			board, _, err := t.Tx.GetBoard(ctx, req.ProjectID, cur.BoardID)
			if err != nil {
				return ClaimTaskResp{}, err
			}
			if cur.Status == domain.Todo {
				if err := t.checkWIP(board, cur.Status); err != nil {
					return ClaimTaskResp{}, err
				}
			}
			now := t.Now.UnixMilli()
			task, err := t.Tx.ClaimTask(ctx, req.ProjectID, cur.ID, cur.Version, claimInput(caller, leaseID, now, secs, statusColumn(board, domain.InProgress)))
			if err != nil {
				return ClaimTaskResp{}, err
			}
			t.Wrote(task)
			if err := t.event(task, domain.EventTaskClaimed, map[string]any{"leaseSeconds": secs}); err != nil {
				return ClaimTaskResp{}, err
			}
			out, err := withSummary(ctx, t.Tx, task)
			if err != nil {
				return ClaimTaskResp{}, err
			}
			// TODO(spec): resume is conductor's resumeState, unported (P-117); sprint is F6's.
			return ClaimTaskResp{LeaseID: leaseID, Task: out}, nil
		})
	})
	if err != nil {
		return ClaimTaskResp{}, err
	}
	if resp.LeaseID != leaseID {
		if err := replayedLease(&resp.Task, l.c.now()); err != nil {
			return ClaimTaskResp{}, err
		}
	}
	return resp, nil
}

// ClaimNext is claim_next: the frontier's best task (store.Claims.ClaimNext, one statement, score
// then priority then order; poison, blocked, leased and backed-off tasks are never in it), claimed
// inside mutate. An empty frontier writes nothing; it reaps the board's expired leases (lazy
// recovery, no sweeper) and, when any were reaped, tries once more. Statements when the board has no
// WIP limit and the membership is cached: the board (GetBoard's 2, or 0 from the snapshot cache), the
// claim (1) and the summary (1). It rings nobody, as Claim. shard lanes, sprintId and maxConcurrency are refused, not ignored.
func (l lifecycle) ClaimNext(ctx context.Context, caller domain.Caller, req ClaimNextReq) (ClaimNextResp, error) {
	a, err := l.c.authorize(ctx, caller, req.ProjectID, "claim_next", domain.GrantSubject{BoardID: req.BoardID}, false)
	if err != nil {
		return ClaimNextResp{}, err
	}
	if err := required("boardId", req.BoardID); err != nil {
		return ClaimNextResp{}, err
	}
	q, err := l.frontierQuery(ctx, caller, a, req.ProjectID, req.BoardID, req.Assignee, req.Capabilities, req.SprintID)
	if err != nil {
		return ClaimNextResp{}, err
	}
	if req.Shard != (Shard{}) {
		if req.Shard.Count < 1 || req.Shard.Index < 0 || req.Shard.Index >= req.Shard.Count {
			return ClaimNextResp{}, domain.Validation("shard.index must be < shard.count", map[string]any{"field": "shard"})
		}
		if req.Shard.Count > 1 {
			return ClaimNextResp{}, unsupported("shard", "the claim is one statement over the whole frontier")
		}
	}
	if req.MaxConcurrency != 0 {
		return ClaimNextResp{}, unsupported("maxConcurrency", "no per-agent claim ceiling is built")
	}
	// useProfile: no agent profile can be registered yet (R2.3), and conductor falls back to the
	// capabilities argument when the caller has none.
	secs, err := leaseSeconds(req.LeaseSeconds)
	if err != nil {
		return ClaimNextResp{}, err
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return ClaimNextResp{}, err
	}
	snap, err := l.c.boardSnapshot(ctx, req.ProjectID, req.BoardID)
	if err != nil {
		return ClaimNextResp{}, err
	}
	board := storeBoard(snap.Board)
	resp, err := l.claimNext(ctx, caller, a, req, q, board, secs)
	if errors.Is(err, errEmptyFrontier) {
		var sum reapSummary
		if sum, err = l.c.reap(ctx, caller, a, req.ProjectID, req.BoardID); err != nil {
			return ClaimNextResp{}, err
		}
		if sum.reaped() == 0 {
			return ClaimNextResp{}, nil
		}
		if resp, err = l.claimNext(ctx, caller, a, req, q, board, secs); errors.Is(err, errEmptyFrontier) {
			return ClaimNextResp{}, nil
		}
	}
	return resp, err
}

func (l lifecycle) claimNext(ctx context.Context, caller domain.Caller, a access, req ClaimNextReq, q store.FrontierQuery, board store.Board, secs int64) (ClaimNextResp, error) {
	leaseID := newLeaseID()
	resp, err := mutate(ctx, l.c, caller, mutation{
		Tool: "claim_next", Args: map[string]any{"projectId": req.ProjectID, "boardId": req.BoardID},
		ProjectID: req.ProjectID, IdempotencyKey: req.IdempotencyKey, Access: a,
	}, func(t *txn) (ClaimNextResp, error) {
		if err := t.checkWIP(board, domain.Todo); err != nil {
			return ClaimNextResp{}, err
		}
		now := t.Now.UnixMilli()
		q.Now = now
		task, ok, err := t.Tx.ClaimNext(ctx, q, claimInput(caller, leaseID, now, secs, statusColumn(board, domain.InProgress)))
		if err != nil {
			return ClaimNextResp{}, err
		}
		if !ok {
			return ClaimNextResp{}, errEmptyFrontier
		}
		t.Wrote(task)
		if err := t.event(task, domain.EventTaskClaimed, map[string]any{"leaseSeconds": secs}); err != nil {
			return ClaimNextResp{}, err
		}
		out, err := withSummary(ctx, t.Tx, task)
		if err != nil {
			return ClaimNextResp{}, err
		}
		id := leaseID
		return ClaimNextResp{Attempts: 1, LeaseID: &id, Task: &out}, nil
	})
	if err != nil {
		return ClaimNextResp{}, err
	}
	if resp.LeaseID != nil && *resp.LeaseID != leaseID {
		if err := replayedLease(resp.Task, l.c.now()); err != nil {
			return ClaimNextResp{}, err
		}
		resp.Attempts = 0 // attempts diagnoses this call, which tried nothing (conductor)
	}
	return resp, nil
}

// storeBoard is the cached snapshot's board in the store's shape, for statusColumn and checkWIP.
func storeBoard(b Board) store.Board {
	out := store.Board{ID: b.ID, ProjectID: b.ProjectID, Name: b.Name}
	for _, c := range b.Columns {
		col := store.Column{ID: c.ID, Name: c.Name, Order: int64(c.Order), Status: c.Status}
		if c.WIPLimit != nil {
			n := int64(*c.WIPLimit)
			col.WIPLimit = &n
		}
		out.Columns = append(out.Columns, col)
	}
	return out
}

// frontierQuery is claim_next's and next_task's frontier for caller: without an assignee the
// unassigned tasks and the caller's own (the store matches its id and its login), so nobody takes
// work routed to another. An assignee naming another identity is a master's question (FORBIDDEN
// below it); conductor filters nothing without one, which let any agent claim a task handed to another.
func (l lifecycle) frontierQuery(ctx context.Context, caller domain.Caller, a access, projectID, boardID, assignee string, caps []string, sprintID string) (store.FrontierQuery, error) {
	q := store.FrontierQuery{ProjectID: projectID, BoardID: boardID, Caller: caller.Identity, Capabilities: caps}
	if assignee != "" {
		if err := checkLabel("assignee", assignee); err != nil {
			return q, err
		}
		if err := l.ownAssignee(ctx, caller, a, projectID, assignee); err != nil {
			return q, err
		}
		q.Assignee = &assignee
	}
	if err := checkLabelList("capabilities", caps); err != nil {
		return q, err
	}
	if sprintID != "" {
		return q, unsupported("sprintId", "the frontier has no sprint filter")
	}
	return q, nil
}

// ownAssignee refuses, below master, an assignee naming another identity: another account (by
// email or id) or another member of the project. Any other assignee is an agent label, refused when
// another agent holds it (ownLabel). Read only on this path, so the claim's statement pins do not
// move.
func (l lifecycle) ownAssignee(ctx context.Context, caller domain.Caller, a access, projectID, assignee string) error {
	if assignee == caller.Identity || domain.RoleMeetsTier(a.Role, domain.TierMaster) {
		return nil
	}
	other := forbidden("assignee names another identity: only a master may read or claim another's work",
		map[string]any{"field": "assignee", "requiredRole": domain.RoleMaster, "role": a.Role})
	u, err := l.c.store.GetUserByEmail(ctx, strings.ToLower(assignee))
	switch {
	case err == nil && u.ID == caller.Identity:
		return nil
	case err == nil:
		return other
	case !errors.Is(err, store.ErrNotFound):
		return l.c.mapErr("assignee", err)
	}
	switch _, err := l.c.store.GetUser(ctx, assignee); {
	case err == nil:
		return other
	case !errors.Is(err, store.ErrNotFound):
		return l.c.mapErr("assignee", err)
	}
	e, err := l.c.accessEntry(ctx, projectID, assignee)
	if err != nil {
		return err
	}
	if e.member {
		return other
	}
	return l.ownLabel(ctx, caller, a, "assignee", assignee)
}

// ownLabel refuses, below master, a label another agent holds (P-277, audit A2): a live API token's
// label (domain.TokenAgent) is the agent of whoever presents that token, so claiming or reading its
// assigned work, or handing work to it, is its own or a master's to do. The caller holds a label
// when its own bearer carries it (Caller.Labelled) and no token of another identity, live, revoked
// or expired, has carried it (P-257 B1, review round 2: minting refuses such a label, and this
// covers rows written before that rule or in a race, which stay refused to both identities); a
// name a client declared (clientInfo.name) or registered (an OAuth client_name) holds none. A
// label another identity's token has ever carried is that identity's even with no live carrier,
// so another worker cannot name it while the owner's token has lapsed (P-338, delivered in P-257
// review round 3). A string no other identity's token and no live token carries names no agent and
// passes, as before. Read only when a label is named, so the claim's statement pins do not move.
// The test itself is labelRefused, which a directory entry's agent also answers to (P-322).
func (l lifecycle) ownLabel(ctx context.Context, caller domain.Caller, a access, field, label string) error {
	if domain.RoleMeetsTier(a.Role, domain.TierMaster) {
		return nil
	}
	refused, err := l.c.labelRefused(ctx, caller, label)
	if err != nil {
		return l.c.mapErr(field, err)
	}
	if refused {
		return forbidden(field+" is another agent's label: only that agent or a master may claim, read or hand it work",
			map[string]any{"field": field, "label": label, "requiredRole": domain.RoleMaster, "role": a.Role})
	}
	return nil
}

// labelRefused is ownLabel's test without its master pass: true when label is another agent's for
// caller. It is, when another identity's token row of any status carries it (the first minter owns
// a label, R2-Q1), or when a live token carries it and caller is not that label's own token
// (Caller.Labelled with Agent == label). A string no token carries, and the caller's own lapsed
// label, are not refused. One read of every token row (REVIEW-78 A6, accepted).
func (c *Core) labelRefused(ctx context.Context, caller domain.Caller, label string) (bool, error) {
	tokens, err := c.store.ListTokens(ctx, "")
	if err != nil {
		return false, err
	}
	return labelRefusedIn(tokens, caller, label, c.nowMs()), nil
}

// labelRefusedIn is labelRefused's rule over token rows already read, so a caller inside a
// transaction (an outcome's agent key, P-340) applies it to the rows that transaction reads.
func labelRefusedIn(tokens []store.Token, caller domain.Caller, label string, now int64) bool {
	carried, others := false, false
	for _, t := range tokens {
		if domain.TokenAgent(t.Label) != label {
			continue
		}
		carried = carried || (t.RevokedAt == 0 && t.ExpiresAt > now)
		others = others || t.IdentityID != caller.Identity
	}
	carried = carried || others // another identity's row of any status: its label is that identity's (R2-Q1, P-338)
	return carried && (others || !caller.Labelled || caller.Agent != label)
}

// Heartbeat is heartbeat_task: the lease's expiry moves to now + leaseSeconds (store.Heartbeat, one
// statement while the lease is live and the caller's). With no leaseSeconds it is now + the default
// and never earlier than the expiry already held, so a heartbeat does not cut a longer claim short
// (conductor's "a heartbeat never SHORTENS a lease"); an explicit leaseSeconds is written as given. The version does not move (conductor:
// "without bumping the task version"), so mutate loads no task; the change row names the task and
// its board. Another owner's lease answers LEASE_EXPIRED, as conductor's assertLeaseHeld does.
func (l lifecycle) Heartbeat(ctx context.Context, caller domain.Caller, req HeartbeatTaskReq) (HeartbeatTaskResp, error) {
	a, err := l.c.authorize(ctx, caller, req.ProjectID, "heartbeat_task", domain.GrantSubject{}, true)
	if err != nil {
		return HeartbeatTaskResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return HeartbeatTaskResp{}, err
	}
	if err := required("leaseId", req.LeaseID); err != nil {
		return HeartbeatTaskResp{}, err
	}
	secs, err := leaseSeconds(req.LeaseSeconds)
	if err != nil {
		return HeartbeatTaskResp{}, err
	}
	pend := a
	a.pending = false
	return mutate(ctx, l.c, caller, mutation{
		Tool: "heartbeat_task", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
		ProjectID: req.ProjectID, Access: a,
	}, func(t *txn) (HeartbeatTaskResp, error) {
		now := t.Now.UnixMilli()
		task, err := t.Tx.Heartbeat(ctx, req.ProjectID, req.TaskID, req.LeaseID, caller.Identity, now+secs*1000, now, req.LeaseSeconds == 0)
		if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeConflictLocked {
			return HeartbeatTaskResp{}, store.LeaseExpired(req.TaskID)
		}
		if err != nil {
			return HeartbeatTaskResp{}, err
		}
		if err := pend.permit(&task); err != nil {
			return HeartbeatTaskResp{}, err
		}
		t.ringChanged(task)
		return HeartbeatTaskResp{ExpiresAt: isoMs(task.Claim.ExpiresAt)}, nil
	})
}

// Release is release_task: the caller's live lease dropped, the task TODO in its board's TODO
// column with no backoff. Broadcast when it is open-frontier work.
func (l lifecycle) Release(ctx context.Context, caller domain.Caller, req ReleaseTaskReq) (ReleaseTaskResp, error) {
	a, err := l.leaseArgs(ctx, caller, "release_task", req.ProjectID, req.TaskID, req.LeaseID, domain.GrantSubject{})
	if err != nil {
		return ReleaseTaskResp{}, err
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return ReleaseTaskResp{}, err
	}
	return retryStale(func() (ReleaseTaskResp, error) {
		return mutate(ctx, l.c, caller, mutation{
			Tool: "release_task", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
			ProjectID: req.ProjectID, TaskID: req.TaskID, IdempotencyKey: req.IdempotencyKey, Access: a,
		}, func(t *txn) (ReleaseTaskResp, error) {
			cur := *t.Task
			if err := leaseHeld(cur, req.LeaseID, caller, t.Now.UnixMilli()); err != nil {
				return ReleaseTaskResp{}, err
			}
			board, _, err := t.Tx.GetBoard(ctx, req.ProjectID, cur.BoardID)
			if err != nil {
				return ReleaseTaskResp{}, err
			}
			after, err := t.Tx.SetStatus(ctx, req.ProjectID, cur.ID, cur.Version, store.StatusChange{
				Status: domain.Todo, ColumnID: statusColumn(board, domain.Todo), ClearClaim: true,
				NextEligibleAt: &store.Nullable[int64]{}, UpdatedBy: caller.Identity, UpdatedAt: t.Now.UnixMilli(),
			})
			if err != nil {
				return ReleaseTaskResp{}, err
			}
			t.Wrote(after)
			if err := t.event(after, domain.EventTaskReleased, map[string]any{}); err != nil {
				return ReleaseTaskResp{}, err
			}
			t.ringFrontier(after)
			out, err := withSummary(ctx, t.Tx, after)
			return ReleaseTaskResp{Task: out}, err
		})
	})
}

// leaseArgs is the authorize and the required ids every lease-guarded outcome shares.
func (l lifecycle) leaseArgs(ctx context.Context, caller domain.Caller, tool, projectID, taskID, leaseID string, s domain.GrantSubject) (access, error) {
	a, err := l.c.authorize(ctx, caller, projectID, tool, s, true)
	if err != nil {
		return access{}, err
	}
	if err := required("taskId", taskID); err != nil {
		return access{}, err
	}
	if err := required("leaseId", leaseID); err != nil {
		return access{}, err
	}
	return a, nil
}

// Next is next_task: the frontier's best task, claiming nothing (store PreviewFrontier, limit 1),
// and its summary: two statements, three when the frontier is empty (the lazy reap's
// ListExpiredLeases). includeInbox adds two (REVIEW, then BLOCKED split into blocked and poisoned).
// strategy "score" and explain are refused: conductor's scorer is not ported, and the frontier's
// order is the store's (score, priority, order key).
func (l lifecycle) Next(ctx context.Context, caller domain.Caller, req NextTaskReq) (NextTaskResp, error) {
	a, err := l.c.authorize(ctx, caller, req.ProjectID, "next_task", domain.GrantSubject{BoardID: req.BoardID}, false)
	if err != nil {
		return NextTaskResp{}, err
	}
	if err := required("boardId", req.BoardID); err != nil {
		return NextTaskResp{}, err
	}
	q, err := l.frontierQuery(ctx, caller, a, req.ProjectID, req.BoardID, req.Assignee, req.Capabilities, req.SprintID)
	if err != nil {
		return NextTaskResp{}, err
	}
	switch req.Strategy {
	case "", "fifo":
	case "score":
		return NextTaskResp{}, unsupported("strategy", "the cost-aware scorer is not ported")
	default:
		return NextTaskResp{}, domain.Validation("unknown strategy: "+string(req.Strategy), map[string]any{"field": "strategy"})
	}
	if req.Explain {
		return NextTaskResp{}, unsupported("explain", "the cost-aware scorer is not ported")
	}
	limit := int(req.InboxLimit)
	switch {
	case limit == 0:
		limit = inboxDefaultLimit
	case limit < 0 || limit > inboxMaxLimit:
		return NextTaskResp{}, domain.Validation("inboxLimit must be 1 to 100", map[string]any{"field": "inboxLimit"})
	}
	st := l.c.store
	q.Now = l.c.nowMs()
	head, err := st.PreviewFrontier(ctx, q, 1)
	if err != nil {
		return NextTaskResp{}, l.c.mapErr("next_task", err)
	}
	if len(head) == 0 {
		sum, err := l.c.reap(ctx, caller, a, req.ProjectID, req.BoardID)
		if err != nil {
			return NextTaskResp{}, err
		}
		if sum.reaped() > 0 {
			q.Now = l.c.nowMs()
			if head, err = st.PreviewFrontier(ctx, q, 1); err != nil {
				return NextTaskResp{}, l.c.mapErr("next_task", err)
			}
		}
	}
	var resp NextTaskResp
	if len(head) > 0 {
		out, err := withSummary(ctx, st, head[0])
		if err != nil {
			return NextTaskResp{}, l.c.mapErr("next_task", err)
		}
		resp.Task = &out
	}
	if req.IncludeInbox {
		if resp.Inbox, err = l.inbox(ctx, req, q.Assignee, limit); err != nil {
			return NextTaskResp{}, l.c.mapErr("next_task", err)
		}
	}
	return resp, nil
}

// inbox is next_task's inbox: the board's REVIEW and BLOCKED tasks (of assignee, when given), the
// BLOCKED ones split by blockedReason into blocked and poisoned (plan §Fold 1: conductor's
// deadLetter bucket), each at most limit; truncated says a bucket was cut. contextSummary is "" on
// each (no batch summary read, P-118).
func (l lifecycle) inbox(ctx context.Context, req NextTaskReq, assignee *string, limit int) (Inbox, error) {
	in := Inbox{Blocked: []Task{}, Poisoned: []Task{}, Review: []Task{}}
	review, blocked := domain.Review, domain.Blocked
	rp, err := l.c.store.ListTasks(ctx, req.ProjectID, store.TaskFilter{BoardID: req.BoardID, Status: &review, Assignee: assignee}, "", limit+1)
	if err != nil {
		return Inbox{}, err
	}
	bp, err := l.c.store.ListTasks(ctx, req.ProjectID, store.TaskFilter{BoardID: req.BoardID, Status: &blocked, Assignee: assignee}, "", 200)
	if err != nil {
		return Inbox{}, err
	}
	in.Truncated = len(rp.Tasks) > limit || bp.NextCursor != ""
	add := func(dst *[]Task, t store.Task) error {
		if len(*dst) == limit {
			in.Truncated = true
			return nil
		}
		out, err := taskOut(t, "")
		if err != nil {
			return err
		}
		*dst = append(*dst, out)
		return nil
	}
	for _, t := range rp.Tasks {
		if err := add(&in.Review, t); err != nil {
			return Inbox{}, err
		}
	}
	for _, t := range bp.Tasks {
		dst := &in.Blocked
		if domain.IsPoisoned(t.Status, reasonOf(t)) {
			dst = &in.Poisoned
		}
		if err := add(dst, t); err != nil {
			return Inbox{}, err
		}
	}
	return in, nil
}

// Complete is complete_task: the caller's live lease dropped and the task landed on REVIEW when the
// project's reviewEnabled is on, else DONE (conductor lifecycle.ts:782). REVIEW is a handover: the
// reviewer (the argument, else the task's reviewer, else the project's defaultReviewer) becomes the
// assignee and is rung assigned, and the review record names it when it is a member (a non-member is
// warned, as conductor's T-246). DONE runs the poison-aware unblock over every dependent, under the
// project lock (a dependent's row is locked after it, never before, so add_dependency cannot
// deadlock with it). checkpointPolicy warn/enforce reads two context-item pages. artifacts merge into
// the task's own by add_artifact's rules (normalised, de-duplicated against the live entries, the 50
// cap counting tombstones) in the same transaction, before the status change, and artifactPolicy is
// judged after the merge: a task with no live artifact is refused under enforce and warned under
// warn, which an unset policy reads as (get_sprint's orWarn); off judges nothing. The answer's task
// carries its artifacts.
func (l lifecycle) Complete(ctx context.Context, caller domain.Caller, req CompleteTaskReq) (CompleteTaskResp, error) {
	proj, perr := l.c.store.GetProject(ctx, req.ProjectID)
	subject := domain.GrantSubject{}
	if perr == nil {
		rev := proj.Settings.ReviewEnabled
		subject.ReviewEnabled = &rev
	}
	a, err := l.leaseArgs(ctx, caller, "complete_task", req.ProjectID, req.TaskID, req.LeaseID, subject)
	if err != nil {
		return CompleteTaskResp{}, err
	}
	if perr != nil {
		return CompleteTaskResp{}, l.c.mapErr("complete_task", perr)
	}
	if err := maxChars("deliverable", req.Deliverable, maxSummaryChars); err != nil {
		return CompleteTaskResp{}, err
	}
	if jsLen(req.Reviewer) > maxReviewerChars {
		return CompleteTaskResp{}, domain.Validation("reviewer must be 1 to 320 characters", map[string]any{"field": "reviewer"})
	}
	incoming, err := normalizeArtifacts(req.Artifacts, false, req.ProjectID, req.TaskID, caller)
	if err != nil {
		return CompleteTaskResp{}, err
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return CompleteTaskResp{}, err
	}
	settings, err := settingsOut(proj.Settings)
	if err != nil {
		return CompleteTaskResp{}, l.c.mapErr("complete_task", err)
	}
	artifactPolicy := orWarn(settings.ArtifactPolicy)
	target := domain.Done
	if proj.Settings.ReviewEnabled {
		target = domain.Review
	}
	return retryStale(func() (CompleteTaskResp, error) {
		return mutate(ctx, l.c, caller, mutation{
			Tool: "complete_task", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
			ProjectID: req.ProjectID, TaskID: req.TaskID, IdempotencyKey: req.IdempotencyKey, Access: a,
			LockProject: target == domain.Done,
		}, func(t *txn) (CompleteTaskResp, error) {
			cur := *t.Task
			now := t.Now.UnixMilli()
			if err := leaseHeld(cur, req.LeaseID, caller, now); err != nil {
				return CompleteTaskResp{}, err
			}
			var warnings []string
			cpGap, err := t.checkpointGap(cur, settings.CheckpointPolicy)
			if err != nil {
				return CompleteTaskResp{}, err
			}
			if cpGap != "" {
				if settings.CheckpointPolicy == "enforce" {
					return CompleteTaskResp{}, domain.Validation(cpGap, map[string]any{"taskId": cur.ID})
				}
				warnings = append(warnings, cpGap)
			}
			added, artGap, err := t.completeArtifacts(cur, incoming, artifactPolicy)
			if err != nil {
				return CompleteTaskResp{}, err
			}
			if artGap != "" {
				warnings = append(warnings, artGap)
			}
			board, _, err := t.Tx.GetBoard(ctx, req.ProjectID, cur.BoardID)
			if err != nil {
				return CompleteTaskResp{}, err
			}
			ch := store.StatusChange{Status: target, ColumnID: statusColumn(board, target), ClearClaim: true, UpdatedBy: caller.Identity, UpdatedAt: now}
			if req.Deliverable != "" {
				d := req.Deliverable
				ch.Deliverable = &store.Nullable[string]{Value: &d}
			}
			reviewer, member := "", false
			var review TaskReview
			if target == domain.Review {
				reviewer = req.Reviewer
				if reviewer == "" && cur.Reviewer != nil {
					reviewer = *cur.Reviewer
				}
				if reviewer == "" && settings.DefaultReviewer != nil {
					reviewer = *settings.DefaultReviewer
				}
				if reviewer != "" {
					r := reviewer
					ch.Assignee = &store.Nullable[string]{Value: &r}
					if member, err = t.isMember(reviewer); err != nil {
						return CompleteTaskResp{}, err
					}
				}
				review = TaskReview{CompletedBy: cur.Claim.Owner, CompletedByAgent: cur.Claim.Agent, RequestedBy: caller.Identity, RequestedAt: isoMs(now)}
				if member {
					r := reviewer
					review.Reviewer = &r
				}
				raw, err := json.Marshal(review)
				if err != nil {
					return CompleteTaskResp{}, err
				}
				rm := json.RawMessage(raw)
				ch.Review = &store.Nullable[json.RawMessage]{Value: &rm}
			}
			after, err := t.Tx.SetStatus(ctx, req.ProjectID, cur.ID, cur.Version, ch)
			if err != nil {
				return CompleteTaskResp{}, err
			}
			t.Wrote(after)
			if err := t.rollUpSkill(cur, true); err != nil {
				return CompleteTaskResp{}, err
			}
			if err := t.event(after, domain.EventTaskCompleted, map[string]any{"to": target, "hasDeliverable": req.Deliverable != "", "artifacts": added}); err != nil {
				return CompleteTaskResp{}, err
			}
			if artGap != "" {
				if err := t.event(after, domain.EventTaskArtifactMissing, map[string]any{"policy": artifactPolicy}); err != nil {
					return CompleteTaskResp{}, err
				}
			}
			if cpGap != "" {
				if err := t.event(after, domain.EventTaskCheckpointMissing, map[string]any{"policy": settings.CheckpointPolicy}); err != nil {
					return CompleteTaskResp{}, err
				}
			}
			resp := CompleteTaskResp{}
			if target == domain.Review {
				var named, target2 any
				if reviewer != "" {
					named = reviewer
				}
				if member {
					target2 = reviewer
				}
				if err := t.event(after, domain.EventTaskReviewRequested, map[string]any{"targetIdentity": target2, "reviewer": named}); err != nil {
					return CompleteTaskResp{}, err
				}
				if reviewer != "" {
					t.RingAssigned(reviewer, after) // conductor lifecycle.ts:935
					if !member {
						warnings = append(warnings, "Reviewer '"+reviewer+"' is not a member of "+req.ProjectID+": the handover was not recorded. "+
							"The ticket is still assigned to '"+reviewer+"' (agent names route via assignment); add_member them to get a recorded handover.")
					}
				} else {
					t.ringChanged(after)
				}
				resp.Handover = Handover{Reviewer: review.Reviewer, Status: after.Status}
			} else {
				t.ringChanged(after)
				if err := t.unblockDependentsOf(cur.ID, nil); err != nil {
					return CompleteTaskResp{}, err
				}
			}
			if resp.Task, err = t.taskWithContext(after); err != nil {
				return CompleteTaskResp{}, err
			}
			resp.Warnings = warnings
			return resp, nil
		})
	})
}

// isMember reports whether identity is a member of the mutation's project (one GetMember).
func (t *txn) isMember(identity string) (bool, error) {
	_, err := t.Tx.GetMember(t.ctx, t.ProjectID, identity)
	if err == nil {
		return true, nil
	}
	if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeNotFound {
		return false, nil
	}
	return false, err
}

// checkpointGap is conductor's checkpointGap: under policy warn or enforce, a task not labelled
// no-alternatives whose context items (the newest 50 and the oldest 50) carry no REJECTED section
// has a gap, returned as its message; "" is none. policy "" and "off" read nothing.
func (t *txn) checkpointGap(task store.Task, policy PolicyMode) (string, error) {
	if policy != "warn" && policy != "enforce" || slices.Contains(task.Labels, labelNoAlternatives) {
		return "", nil
	}
	for _, asc := range []bool{false, true} {
		page, err := t.Tx.ListContextItems(t.ctx, task.ProjectID, task.ID, store.ContextItemsQuery{Limit: checkpointWindow, Ascending: asc})
		if err != nil {
			return "", err
		}
		for _, it := range page.Items {
			if rejectedSection.MatchString(it.Body) {
				return "", nil
			}
		}
	}
	return task.ID + " is being completed with no REJECTED checkpoint. Record what you tried and abandoned, " +
		"and why — as a context item with `REJECTED` as its own heading. Label the task `" + labelNoAlternatives +
		"` if nothing was genuinely weighed.", nil
}

// completeArtifacts is complete_task's artifacts: incoming merged into task's (mergeArtifacts), then
// artifactPolicy judged over the result. The gap is a task with no live artifact once the merge is
// done: under enforce it is the VALIDATION (the transaction writes nothing), under warn it is the
// warning returned; off reads nothing when nothing comes in. added is the number of entries written.
func (t *txn) completeArtifacts(task store.Task, incoming []store.AddArtifactInput, policy PolicyMode) (added int, gap string, err error) {
	if len(incoming) == 0 && policy != "warn" && policy != "enforce" {
		return 0, "", nil
	}
	fresh, have, err := t.mergeArtifacts(task.ID, incoming)
	if err != nil {
		return 0, "", err
	}
	live := len(fresh)
	for _, a := range have {
		if a.Retraction == nil {
			live++
		}
	}
	if live > 0 || policy != "warn" && policy != "enforce" {
		return len(fresh), "", nil
	}
	gap = task.ID + " is being completed with no artifact. Bind the evidence (a pr, branch, commit or file entry) " +
		"with add_artifact or complete_task's artifacts."
	if policy == "enforce" {
		return 0, "", domain.Validation(gap+" The project's artifactPolicy is enforce.", map[string]any{"taskId": task.ID})
	}
	return len(fresh), gap, nil
}

// Block is block_task: the caller's live lease dropped and the task BLOCKED with the reason. Each
// blockedBy id becomes a blocks edge (store.AddEdge: the cycle check and both caches) in the SAME
// transaction, under the project lock taken first, so a cycle or a missing blocker writes nothing at
// all (conductor wrote the edges in their own transactions and could leave some behind). A reason of
// "poison" is refused: only the retry policy poisons a task.
func (l lifecycle) Block(ctx context.Context, caller domain.Caller, req BlockTaskReq) (BlockTaskResp, error) {
	a, err := l.leaseArgs(ctx, caller, "block_task", req.ProjectID, req.TaskID, req.LeaseID, domain.GrantSubject{})
	if err != nil {
		return BlockTaskResp{}, err
	}
	if err := reasonArg("reason", req.Reason, true); err != nil {
		return BlockTaskResp{}, err
	}
	if strings.EqualFold(strings.TrimSpace(req.Reason), domain.BlockedReasonPoison) {
		return BlockTaskResp{}, domain.Validation(`reason "poison" is reserved: only the retry policy poisons a task`, map[string]any{"field": "reason"})
	}
	if len(req.BlockedBy) > maxLabels {
		return BlockTaskResp{}, domain.Validation("blockedBy has more than 32 entries", map[string]any{"field": "blockedBy"})
	}
	var blockers []string
	for _, b := range req.BlockedBy {
		if b == "" || len(b) > 64 {
			return BlockTaskResp{}, domain.Validation("blockedBy entries must be 1 to 64 characters", map[string]any{"field": "blockedBy"})
		}
		if b != req.TaskID && !slices.Contains(blockers, b) {
			blockers = append(blockers, b)
		}
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return BlockTaskResp{}, err
	}
	return retryStale(func() (BlockTaskResp, error) {
		return mutate(ctx, l.c, caller, mutation{
			Tool: "block_task", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
			ProjectID: req.ProjectID, TaskID: req.TaskID, IdempotencyKey: req.IdempotencyKey, Access: a,
			LockProject: len(blockers) > 0,
		}, func(t *txn) (BlockTaskResp, error) {
			cur := *t.Task
			now := t.Now.UnixMilli()
			if err := leaseHeld(cur, req.LeaseID, caller, now); err != nil {
				return BlockTaskResp{}, err
			}
			for _, b := range blockers {
				if _, err := t.Tx.AddEdge(ctx, store.AddEdgeInput{ProjectID: req.ProjectID, FromTaskID: b, ToTaskID: cur.ID, Type: store.DependencyBlocks, CreatedBy: caller.Identity}); err != nil {
					return BlockTaskResp{}, err
				}
				if slices.Contains(cur.BlockedBy, b) {
					continue
				}
				if err := t.bumpOther(b); err != nil {
					return BlockTaskResp{}, err
				}
				if err := t.event(cur, domain.EventDependencyAdded, map[string]any{"fromTaskId": b, "type": "blocks"}); err != nil {
					return BlockTaskResp{}, err
				}
			}
			board, _, err := t.Tx.GetBoard(ctx, req.ProjectID, cur.BoardID)
			if err != nil {
				return BlockTaskResp{}, err
			}
			after, err := t.Tx.SetStatus(ctx, req.ProjectID, cur.ID, cur.Version, store.StatusChange{
				Status: domain.Blocked, ColumnID: statusColumn(board, domain.Blocked), BlockedReason: req.Reason, ClearClaim: true,
				UpdatedBy: caller.Identity, UpdatedAt: now,
			})
			if err != nil {
				return BlockTaskResp{}, err
			}
			t.Wrote(after)
			if err := t.rollUpSkill(cur, false); err != nil {
				return BlockTaskResp{}, err
			}
			if err := t.event(after, domain.EventTaskBlocked, map[string]any{"reason": req.Reason}); err != nil {
				return BlockTaskResp{}, err
			}
			out, err := withSummary(ctx, t.Tx, after)
			return BlockTaskResp{Task: out}, err
		})
	})
}

// Fail is fail_task: the caller's live lease dropped and domain.ApplyFailure applied under the
// project's retry policy. retryable absent is true; retryable false poisons at once.
func (l lifecycle) Fail(ctx context.Context, caller domain.Caller, req FailTaskReq) (FailTaskResp, error) {
	a, err := l.leaseArgs(ctx, caller, "fail_task", req.ProjectID, req.TaskID, req.LeaseID, domain.GrantSubject{})
	if err != nil {
		return FailTaskResp{}, err
	}
	if err := reasonArg("reason", req.Reason, true); err != nil {
		return FailTaskResp{}, err
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return FailTaskResp{}, err
	}
	proj, err := l.c.store.GetProject(ctx, req.ProjectID)
	if err != nil {
		return FailTaskResp{}, l.c.mapErr("fail_task", err)
	}
	policy := domain.RetryPolicyOrDefault(proj.Settings.RetryPolicy)
	retryable := !req.Retryable.Set || req.Retryable.V
	return retryStale(func() (FailTaskResp, error) {
		return mutate(ctx, l.c, caller, mutation{
			Tool: "fail_task", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
			ProjectID: req.ProjectID, TaskID: req.TaskID, IdempotencyKey: req.IdempotencyKey, Access: a,
		}, func(t *txn) (FailTaskResp, error) {
			cur := *t.Task
			if err := leaseHeld(cur, req.LeaseID, caller, t.Now.UnixMilli()); err != nil {
				return FailTaskResp{}, err
			}
			after, err := t.applyFailure(cur, policy, req.Reason, domain.EventTaskFailed, retryable)
			if err != nil {
				return FailTaskResp{}, err
			}
			out, err := withSummary(ctx, t.Tx, after)
			return FailTaskResp{Task: out}, err
		})
	})
}

// applyFailure is conductor's applyFailure over domain.ApplyFailure: the lease dropped, claimAttempts
// counted, and either TODO with a backoff (nextEligibleAt) or BLOCKED with blockedReason "poison" and
// the poisoned label, never a third state; the outcome's event (retryEvent, or task.poisoned). It
// rings broadcast only when the task is claimable at once (a zero backoff).
func (t *txn) applyFailure(cur store.Task, policy domain.RetryPolicy, reason string, retryEvent domain.EventType, retryable bool) (store.Task, error) {
	o := domain.ApplyFailure(domain.Failure{ClaimAttempts: int(cur.ClaimAttempts), Labels: cur.Labels, Retryable: retryable, Reason: reason, RetryEvent: retryEvent}, policy, t.Now, mrand.Float64())
	board, _, err := t.Tx.GetBoard(t.ctx, cur.ProjectID, cur.BoardID)
	if err != nil {
		return store.Task{}, err
	}
	attempts := int64(o.ClaimAttempts)
	ch := store.StatusChange{
		Status: o.Status, ColumnID: statusColumn(board, o.Status), BlockedReason: o.BlockedReason, ClaimAttempts: &attempts,
		NextEligibleAt: &store.Nullable[int64]{}, Labels: nonNil(o.Labels), ClearClaim: true, UpdatedBy: t.Caller.Identity, UpdatedAt: t.Now.UnixMilli(),
	}
	if o.Status == domain.Todo {
		ms := o.NextEligibleAt.UnixMilli()
		ch.NextEligibleAt = &store.Nullable[int64]{Value: &ms}
	}
	after, err := t.Tx.SetStatus(t.ctx, cur.ProjectID, cur.ID, cur.Version, ch)
	if err != nil {
		return store.Task{}, err
	}
	t.Wrote(after)
	if err := t.event(after, o.Event, o.EventData); err != nil {
		return store.Task{}, err
	}
	t.ringFrontier(after)
	return after, nil
}

// reapSummary is what one reap did: the tasks it returned to TODO with a backoff and the ones it
// poisoned. The poisoned bucket is conductor's deadLetter (plan §Fold 1).
type reapSummary struct {
	Retried  []string `json:"retried"`
	Poisoned []string `json:"poisoned"`
}

func (s reapSummary) reaped() int { return len(s.Retried) + len(s.Poisoned) }

// Reap is reap_stale_leases: every IN_PROGRESS task of the board whose lease has expired, each in its
// own mutation (conductor: one transaction per task), back to TODO with a backoff or poisoned.
func (l lifecycle) Reap(ctx context.Context, caller domain.Caller, req ReapStaleLeasesReq) (ReapStaleLeasesResp, error) {
	a, err := l.c.authorize(ctx, caller, req.ProjectID, "reap_stale_leases", domain.GrantSubject{BoardID: req.BoardID}, false)
	if err != nil {
		return ReapStaleLeasesResp{}, err
	}
	if err := required("boardId", req.BoardID); err != nil {
		return ReapStaleLeasesResp{}, err
	}
	sum, err := l.c.reap(ctx, caller, a, req.ProjectID, req.BoardID)
	if err != nil {
		return ReapStaleLeasesResp{}, err
	}
	return ReapStaleLeasesResp{Reaped: int64(sum.reaped())}, nil
}

// reap is the one reaper: reap_stale_leases, and the lazy recovery of claim_next and next_task
// when their frontier is empty (no background sweeper). One ListExpiredLeases (at most 200 tasks,
// oldest expiry first); the project's retry policy is read only when there is something to reap. A
// task resolved since the read (heartbeat, completion, a new claim) is skipped. The access is the
// calling tool's: a grant check left pending is finished against each reaped task.
func (c *Core) reap(ctx context.Context, caller domain.Caller, a access, projectID, boardID string) (reapSummary, error) {
	sum := reapSummary{Retried: []string{}, Poisoned: []string{}}
	expired, err := c.store.ListExpiredLeases(ctx, projectID, boardID, c.nowMs(), 200)
	if err != nil {
		return sum, c.mapErr("reap_stale_leases", err)
	}
	if len(expired) == 0 {
		return sum, nil
	}
	proj, err := c.store.GetProject(ctx, projectID)
	if err != nil {
		return sum, c.mapErr("reap_stale_leases", err)
	}
	policy := domain.RetryPolicyOrDefault(proj.Settings.RetryPolicy)
	for _, x := range expired {
		after, err := mutate(ctx, c, caller, mutation{
			Tool: "reap_stale_leases", Args: map[string]any{"projectId": projectID, "boardId": boardID},
			ProjectID: projectID, TaskID: x.ID, Access: a,
		}, func(t *txn) (store.Task, error) {
			cur := *t.Task
			if cur.Status != domain.InProgress || cur.Claim == nil || liveLease(cur, t.Now.UnixMilli()) {
				return store.Task{}, errReapResolved
			}
			return t.applyFailure(cur, policy, "lease expired", domain.EventTaskLeaseReaped, true)
		})
		if errors.Is(err, errReapResolved) {
			continue
		}
		if e, ok := domain.AsBatonDeckError(err); ok && (e.Code == domain.CodeStale || e.Code == domain.CodeNotFound) {
			continue // written or deleted since the read: someone else resolved it
		}
		if err != nil {
			return sum, err
		}
		if domain.IsPoisoned(after.Status, reasonOf(after)) {
			sum.Poisoned = append(sum.Poisoned, after.ID)
		} else {
			sum.Retried = append(sum.Retried, after.ID)
		}
	}
	return sum, nil
}

// Handoff is handoff_task: the caller's live lease dropped, the task TODO with toAgent as its
// assignee and no backoff, the note kept as the task-scope memory entry "handoff" {note, toAgent,
// from} (conductor SRS §8.4), all in one transaction. toAgent is rung assigned, even when it was
// already the assignee (conductor's publishAssignment). A toAgent that is another agent's label is
// FORBIDDEN below master (ownLabel, P-277).
func (l lifecycle) Handoff(ctx context.Context, caller domain.Caller, req HandoffTaskReq) (HandoffTaskResp, error) {
	a, err := l.leaseArgs(ctx, caller, "handoff_task", req.ProjectID, req.TaskID, req.LeaseID, domain.GrantSubject{})
	if err != nil {
		return HandoffTaskResp{}, err
	}
	if err := checkLabel("toAgent", req.ToAgent); err != nil {
		return HandoffTaskResp{}, err
	}
	if err := l.ownLabel(ctx, caller, a, "toAgent", req.ToAgent); err != nil {
		return HandoffTaskResp{}, err
	}
	if err := reasonArg("memoryNote", req.MemoryNote, false); err != nil {
		return HandoffTaskResp{}, err
	}
	// TODO(spec): conductor's enforceTokenBudget warnings are not ported (as add_context_item).
	return retryStale(func() (HandoffTaskResp, error) {
		return mutate(ctx, l.c, caller, mutation{
			Tool: "handoff_task", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
			ProjectID: req.ProjectID, TaskID: req.TaskID, Access: a,
		}, func(t *txn) (HandoffTaskResp, error) {
			cur := *t.Task
			now := t.Now.UnixMilli()
			if err := leaseHeld(cur, req.LeaseID, caller, now); err != nil {
				return HandoffTaskResp{}, err
			}
			board, _, err := t.Tx.GetBoard(ctx, req.ProjectID, cur.BoardID)
			if err != nil {
				return HandoffTaskResp{}, err
			}
			to := req.ToAgent
			after, err := t.Tx.SetStatus(ctx, req.ProjectID, cur.ID, cur.Version, store.StatusChange{
				Status: domain.Todo, ColumnID: statusColumn(board, domain.Todo), ClearClaim: true, NextEligibleAt: &store.Nullable[int64]{},
				Assignee: &store.Nullable[string]{Value: &to}, UpdatedBy: caller.Identity, UpdatedAt: now,
			})
			if err != nil {
				return HandoffTaskResp{}, err
			}
			t.Wrote(after)
			// The receiver's handoff notification (P-275): toAgent as a member key, or the member whose
			// login it is; an agent label or a non-member notifies nobody.
			target, err := t.handoffTarget(to)
			if err != nil {
				return HandoffTaskResp{}, err
			}
			if err := t.event(after, domain.EventTaskHandoff, map[string]any{"toAgent": to, "note": req.MemoryNote, "targetIdentity": target}); err != nil {
				return HandoffTaskResp{}, err
			}
			value, err := json.Marshal(map[string]any{"note": req.MemoryNote, "toAgent": to, "from": caller.Identity})
			if err != nil {
				return HandoffTaskResp{}, err
			}
			if _, err := t.Tx.WriteMemory(ctx, store.WriteMemoryInput{ProjectID: req.ProjectID, TaskID: cur.ID, Scope: store.MemoryTask, Key: "handoff", Value: value, Author: caller.Identity}); err != nil {
				return HandoffTaskResp{}, err
			}
			t.RingAssigned(to, after)
			out, err := withSummary(ctx, t.Tx, after)
			return HandoffTaskResp{Task: out}, err
		})
	})
}

// handoffTarget is the member identity toAgent names: itself when a member, else the member whose
// account email it is (a handoff stores the login as given), else nil.
func (t *txn) handoffTarget(to string) (*string, error) {
	id, err := t.memberOrNil(to)
	if id != nil || err != nil {
		return id, err
	}
	u, err := t.Tx.GetUserByEmail(t.ctx, strings.ToLower(to))
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return t.memberOrNil(u.ID)
}

// Cancel is cancel_task (ADR-0001): DONE with the system label cancelled and the reason as a note
// context item, from any status (a server action, like auto-unblock, not a move through the fixed
// table). The master role, or the holder of the task's live lease; a master is CONFLICT_LOCKED by
// another identity's live lease. Version-checked by mutate (STALE before any other judgement). As a
// landing on DONE it runs the poison-aware unblock under the project lock. A task already DONE is
// INVALID_TRANSITION (it was delivered: relabelling it would take it out of every done figure), and
// a racing parent is CONFLICT_LOCKED as move_task refuses it, so no race is left open under a DONE
// parent (ADR-0001 Decision 1, P-150). Both refusals write nothing.
func (l lifecycle) Cancel(ctx context.Context, caller domain.Caller, req CancelTaskReq) (CancelTaskResp, error) {
	a, err := l.c.authorize(ctx, caller, req.ProjectID, "cancel_task", domain.GrantSubject{}, true)
	if err != nil {
		return CancelTaskResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return CancelTaskResp{}, err
	}
	if err := reasonArg("reason", req.Reason, true); err != nil {
		return CancelTaskResp{}, err
	}
	version := req.Version
	return mutate(ctx, l.c, caller, mutation{
		Tool: "cancel_task", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
		ProjectID: req.ProjectID, TaskID: req.TaskID, Version: &version, Access: a, LockProject: true,
	}, func(t *txn) (CancelTaskResp, error) {
		cur := *t.Task
		now := t.Now.UnixMilli()
		owner := liveLease(cur, now) && cur.Claim.Owner == caller.Identity
		if !owner && !domain.RoleMeetsTier(a.Role, domain.TierMaster) {
			return CancelTaskResp{}, forbidden("cancel_task needs the master role or the task's live lease",
				map[string]any{"tool": "cancel_task", "role": a.Role, "requiredRole": domain.TierMinRole(domain.TierMaster)})
		}
		if cur.Status == domain.Done {
			return CancelTaskResp{}, domain.InvalidTransition(cur.Status, "cancelled", nil, nil)
		}
		if cur.Racing {
			return CancelTaskResp{}, domain.NewError(domain.CodeConflictLocked, "Task "+cur.ID+" is racing; resolve via pick_run/cancel_runs",
				map[string]any{"taskId": cur.ID, "reason": "race"})
		}
		if !owner && liveLease(cur, now) {
			return CancelTaskResp{}, domain.ConflictLocked(cur.ID, cur.Claim.Owner)
		}
		board, _, err := t.Tx.GetBoard(ctx, req.ProjectID, cur.BoardID)
		if err != nil {
			return CancelTaskResp{}, err
		}
		after, err := t.Tx.SetStatus(ctx, req.ProjectID, cur.ID, cur.Version, store.StatusChange{
			Status: domain.Done, ColumnID: statusColumn(board, domain.Done), ClearClaim: true,
			Labels: domain.WithLabel(nonNil(cur.Labels), domain.LabelCancelled), UpdatedBy: caller.Identity, UpdatedAt: now,
		})
		if err != nil {
			return CancelTaskResp{}, err
		}
		t.Wrote(after)
		if _, err := t.Tx.AddContextItem(ctx, store.AddContextItemInput{ProjectID: req.ProjectID, TaskID: cur.ID, Kind: store.ContextNote, Body: req.Reason, Author: caller.Identity}); err != nil {
			return CancelTaskResp{}, err
		}
		if err := t.event(after, domain.EventTaskMoved, map[string]any{"from": cur.Status, "to": domain.Done, "cancelled": true, "reason": req.Reason}); err != nil {
			return CancelTaskResp{}, err
		}
		if err := t.event(after, domain.EventContextAdded, map[string]any{"kind": "note"}); err != nil {
			return CancelTaskResp{}, err
		}
		t.ringChanged(after)
		if err := t.unblockDependentsOf(cur.ID, nil); err != nil {
			return CancelTaskResp{}, err
		}
		out, err := withSummary(ctx, t.Tx, after)
		return CancelTaskResp{Task: out}, err
	})
}
