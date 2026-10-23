package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The doorbell kinds a change-log row carries (plan §Notifications path C), and the kind of a row
// that rings nobody: every mutation writes at least one row, so the bus (resources/updated, the
// board-snapshot cursor) sees every change.
const (
	DoorbellAssigned  = "assigned"
	DoorbellBroadcast = "broadcast"
	ChangeKindChanged = "changed"
)

// BroadcastScope is the token scope of a project's broadcast doorbell and of a change that rings
// nobody: every member's /wake and every resource subscriber of the project reads it besides its
// own identity's scope. An assigned doorbell is scoped to the assignee's identity, which is the
// identity's token scope (internal/auth: TokenScope = the identity id).
func BroadcastScope(projectID string) string { return "project:" + projectID }

// AgentScope is the token scope of an assigned doorbell whose assignee is a free-text agent label
// rather than a member identity (conductor allows both: handoff_task's toAgent): the sessions of
// that agent read it besides their identity's scope.
func AgentScope(label string) string { return "agent:" + label }

// Doorbell is one ring: Kind assigned (to Agent, the assignee identity) or broadcast (to the
// project's open frontier). The payload is zero-authority: it names a task, never grants anything.
type Doorbell struct {
	Kind    string
	Agent   string
	TaskID  string
	BoardID string
}

// doorbellsFor is the F4 doorbell rule for one task going from before (nil: it did not exist) to
// after: assigned when it gains an assignee; broadcast when it becomes TODO with no assignee, or
// a TODO task loses its assignee; and, when unblocked (an auto-unblock moved it), assigned to the
// assignee it already had when it becomes TODO.
func doorbellsFor(before *store.Task, after store.Task, unblocked bool) []Doorbell {
	ring := func(kind, agent string) Doorbell {
		return Doorbell{Kind: kind, Agent: agent, TaskID: after.ID, BoardID: after.BoardID}
	}
	gained := after.Assignee != nil && (before == nil || before.Assignee == nil || *before.Assignee != *after.Assignee)
	becameTodo := after.Status == domain.Todo && (before == nil || before.Status != domain.Todo)
	lostAssignee := before != nil && before.Assignee != nil && after.Assignee == nil
	var out []Doorbell
	switch {
	case gained:
		out = append(out, ring(DoorbellAssigned, *after.Assignee))
	case after.Assignee == nil && after.Status == domain.Todo && (becameTodo || lostAssignee):
		out = append(out, ring(DoorbellBroadcast, ""))
	case unblocked && becameTodo && after.Assignee != nil:
		out = append(out, ring(DoorbellAssigned, *after.Assignee))
	}
	return out
}

// mutation names one mutating call. Tool is the tool (or route) name: the audit descriptor is
// domain.DescribeAudit(Tool, Args), and Args are the call's arguments. TaskID "" is a mutation
// that loads no task (a create, a project or board change). Version is the caller's expected
// version of that task; nil only on the paths that are not version-checked (the lease-guarded
// ones). A supplied 0 is checked and is STALE, since versions start at 1, as conductor's
// nonnegative version is. IdempotencyKey "" keeps nothing; a key is kept per tool. Access is authorize's decision; a grant check it left pending is
// finished against the loaded task.
type mutation struct {
	Tool           string
	Args           map[string]any
	ProjectID      string
	TaskID         string
	Version        *int64
	IdempotencyKey string
	Access         access
	// LockProject serialises the mutation with every other that sets it in the project: the
	// project lock (store.Locks) is the transaction's first statement, before the task is loaded or
	// any task row locked, so lock order is project then tasks. add_dependency and remove_dependency
	// set it: under Postgres READ COMMITTED two of them on disjoint pairs could each pass
	// domain.CheckCycle and together close a cycle (P-110).
	LockProject bool
}

// txn is what a mutation's fn works with: the transaction's store, the task as loaded (nil when the
// mutation names none), the time and the caller. fn records what it did through Wrote, Deleted,
// Bump, Event, BoardEvent, Ring and RingFor; mutate writes the rest.
type txn struct {
	Tx        store.Store
	Task      *store.Task
	Now       time.Time
	Caller    domain.Caller
	ProjectID string

	ctx     context.Context
	after   *store.Task
	deleted bool
	events  []pendingEvent
	bells   []Doorbell
	// boards is every (project, board) the mutation's change rows name ("" board: the project),
	// and its task's board: what mutate drops from the snapshot cache once the transaction commits.
	boards [][2]string
}

type pendingEvent struct {
	in    store.EventInput
	board bool
}

// Wrote records the task after fn's write to it. A versioned write (UpdateTask, SetStatus,
// SetOrderKey, ClaimTask) has bumped the version; an unversioned one (Heartbeat, ReleaseLease)
// has not, and mutate bumps it.
func (t *txn) Wrote(task store.Task) { t.after = &task }

// Deleted records that fn deleted the task: there is nothing to bump.
func (t *txn) Deleted() { t.deleted = true }

// Bump bumps the loaded task's version now and returns it, for an fn whose result carries the task
// after a write that does not bump (context items, a heartbeat). It counts as fn's one bump.
func (t *txn) Bump() (store.Task, error) {
	if t.Task == nil {
		return store.Task{}, internalf("mutate: Bump without a task")
	}
	base := *t.Task
	if t.after != nil {
		base = *t.after
	}
	got, err := t.Tx.UpdateTask(t.ctx, base.ProjectID, base.ID, base.Version, store.TaskPatch{UpdatedBy: t.Caller.Identity, UpdatedAt: t.Now.UnixMilli()})
	if err != nil {
		return store.Task{}, err
	}
	t.after = &got
	return got, nil
}

// Event appends a task event: TaskID "" is the mutation's task, BoardID "" the task's board, and
// ProjectID, Actor, Agent and Ts are the mutation's unless set.
func (t *txn) Event(e store.EventInput) { t.events = append(t.events, pendingEvent{in: e}) }

// BoardEvent appends an event of the board with no task (task.deleted, board.created).
func (t *txn) BoardEvent(e store.EventInput) {
	t.events = append(t.events, pendingEvent{in: e, board: true})
}

// Ring adds a doorbell.
func (t *txn) Ring(d Doorbell) { t.bells = append(t.bells, d) }

// RingFor adds the doorbells of the F4 rule for one task's change (doorbellsFor), less a
// self-assignment: a task the caller assigned to itself rings nobody (owner ruling 2026-10-23).
// An auto-unblock (unblocked) still rings its assignee, whoever caused it.
func (t *txn) RingFor(before *store.Task, after store.Task, unblocked bool) {
	for _, d := range doorbellsFor(before, after, unblocked) {
		if !unblocked && d.Kind == DoorbellAssigned && d.Agent == t.Caller.Identity {
			continue
		}
		t.bells = append(t.bells, d)
	}
}

// RingAssigned rings assignee assigned for task, unless the caller is assigning the task to itself
// (owner ruling 2026-10-23: a claim, claim_next or self-assignment rings nobody).
func (t *txn) RingAssigned(assignee string, task store.Task) {
	if assignee == t.Caller.Identity {
		return
	}
	t.Ring(Doorbell{Kind: DoorbellAssigned, Agent: assignee, TaskID: task.ID, BoardID: task.BoardID})
}

// mutate is the only composition of a mutation (ADR-0003), in one store transaction: the
// idempotency replay (before any precondition, as conductor's runTaskTxnIdem: a replayed call
// answers what it answered, even if the task has moved since), then load the task → STALE when
// its version is not *m.Version → the pending grant check → fn → the version bump (exactly one;
// mutate makes it when fn did not) → the task_events rows → the audit row when
// domain.DescribeAudit names the tool → the change-log rows (one per doorbell, else one
// ChangeKindChanged row) → the idempotency entry. Any error, from fn or any step, rolls all of it
// back. After the commit the boards it touched leave the snapshot cache (read-your-writes before the
// bus delivers the rows); a replay touched none. A panic in fn is INTERNAL with its stack logged; a
// *domain.BatonDeckError passes through unchanged and any other error is INTERNAL.
func mutate[R any](ctx context.Context, c *Core, caller domain.Caller, m mutation, fn func(t *txn) (R, error)) (R, error) {
	var zero R
	var touched [][2]string // the last run's, so a retried transaction drops what committed
	run := func(tx store.Store) (r R, err error) {
		touched = nil
		defer c.recovered(m.Tool, &err)
		t := &txn{Tx: tx, Now: c.now(), Caller: caller, ProjectID: m.ProjectID, ctx: ctx}
		if m.LockProject {
			if err := tx.LockProject(ctx, m.ProjectID); err != nil {
				return zero, err
			}
		}
		if m.TaskID != "" {
			cur, err := tx.GetTask(ctx, m.ProjectID, m.TaskID)
			if err != nil {
				return zero, err
			}
			if m.Version != nil && cur.Version != *m.Version {
				return zero, domain.Stale(*m.Version, cur.Version)
			}
			t.Task = &cur
		}
		if err := m.Access.permit(t.Task); err != nil {
			return zero, err
		}
		r, err = fn(t)
		if err != nil {
			return zero, err
		}
		if err := t.finish(c, m); err != nil {
			return zero, err
		}
		touched = t.boards
		return r, nil
	}
	committed := func() {
		for _, b := range touched {
			c.dropSnapshots(b[0], b[1])
		}
	}

	if m.IdempotencyKey == "" {
		var out R
		err := c.store.Atomically(ctx, func(tx store.Store) error {
			r, err := run(tx)
			out = r
			return err
		})
		if err != nil {
			return zero, c.mapErr(m.Tool, err)
		}
		committed()
		return out, nil
	}
	// The key is namespaced by tool, as conductor's `${tool}:${key}` (common.ts:216-219): one key
	// sent to two tools must not replay one's result as the other's.
	scope := m.ProjectID
	if scope == "" {
		scope = store.GlobalScope
	}
	raw, err := c.store.WithIdempotency(ctx, scope, caller.Identity, m.Tool+":"+m.IdempotencyKey, c.idemTTL, func(tx store.Store) ([]byte, error) {
		r, err := run(tx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(r)
	})
	if err != nil {
		return zero, c.mapErr(m.Tool, err)
	}
	committed()
	var out R
	if err := json.Unmarshal(raw, &out); err != nil {
		return zero, c.mapErr(m.Tool, err)
	}
	return out, nil
}

// finish writes what fn did not: the bump, the events, the audit row and the change-log rows.
func (t *txn) finish(c *Core, m mutation) error {
	if t.Task != nil && !t.deleted {
		switch {
		case t.after == nil || t.after.Version == t.Task.Version:
			if _, err := t.Bump(); err != nil {
				if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeStale {
					return internalf("mutate %s: fn wrote the task without recording it (Wrote)", m.Tool)
				}
				return err
			}
		case t.after.Version != t.Task.Version+1:
			return internalf("mutate %s: the task moved from version %d to %d; a mutation bumps it once", m.Tool, t.Task.Version, t.after.Version)
		}
	}
	ms := t.Now.UnixMilli()
	for _, p := range t.events {
		e := p.in
		if e.ProjectID == "" {
			e.ProjectID = t.ProjectID
		}
		if e.Actor == "" {
			e.Actor = t.Caller.Identity
		}
		if e.Agent == nil && t.Caller.Agent != "" {
			agent := t.Caller.Agent
			e.Agent = &agent
		}
		if e.Ts == 0 {
			e.Ts = ms
		}
		var err error
		if p.board {
			if e.BoardID == "" && t.Task != nil {
				e.BoardID = t.Task.BoardID
			}
			_, err = t.Tx.AppendBoardEvent(t.ctx, e)
		} else {
			if e.TaskID == "" && t.Task != nil {
				e.TaskID = t.Task.ID
			}
			_, err = t.Tx.AppendTaskEvent(t.ctx, e)
		}
		if err != nil {
			return err
		}
	}
	if d, ok := domain.DescribeAudit(m.Tool, m.Args); ok {
		data, err := json.Marshal(d.Data)
		if err != nil {
			return err
		}
		if _, err := t.Tx.AppendAudit(t.ctx, store.AuditRow{
			TS: ms, Action: d.Action, Actor: t.Caller.Identity, Agent: t.Caller.Agent, Subject: d.Subject,
			ProjectID: t.ProjectID, Result: store.AuditOK, Data: data,
		}); err != nil {
			return err
		}
	}
	// The task's own board too: a move to DONE that unblocks a dependent on another board rings only
	// the dependent, so no row names the board the moved task left.
	for _, x := range []*store.Task{t.Task, t.after} {
		if x != nil {
			t.boards = append(t.boards, [2]string{t.ProjectID, x.BoardID})
		}
	}
	return t.writeChanges(m.Tool, ms)
}

// changePayload is a change-log row's payload: the /wake frame's fields but the cursor, which is
// the row's id.
type changePayload struct {
	Kind      string `json:"kind"`
	Agent     string `json:"agent,omitempty"`
	TaskID    string `json:"taskId,omitempty"`
	ProjectID string `json:"projectId,omitempty"`
	BoardID   string `json:"boardId,omitempty"`
	Tool      string `json:"tool,omitempty"`
	// Actor is the caller's identity: the directory rings no master for its own change (R0.25). It
	// never reaches a doorbell frame, which is contentless.
	Actor string `json:"actor,omitempty"`
}

// assigneeScope is the token scope an assigned doorbell rings: the assignee's identity when it is a
// member of the project (one GetMember, inside the transaction), else AgentScope(assignee), since an
// assignee may be a free-text agent label (conductor's handoff_task toAgent).
func (t *txn) assigneeScope(assignee string) (string, error) {
	_, err := t.Tx.GetMember(t.ctx, t.ProjectID, assignee)
	if err == nil {
		return assignee, nil
	}
	if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeNotFound {
		return AgentScope(assignee), nil
	}
	return "", err
}

func (t *txn) writeChanges(tool string, ms int64) error {
	bells := t.bells
	if len(bells) == 0 {
		d := Doorbell{Kind: ChangeKindChanged}
		switch {
		case t.after != nil:
			d.TaskID, d.BoardID = t.after.ID, t.after.BoardID
		case t.Task != nil:
			d.TaskID, d.BoardID = t.Task.ID, t.Task.BoardID
		}
		bells = []Doorbell{d}
	}
	for _, d := range bells {
		scope := BroadcastScope(t.ProjectID)
		if d.Kind == DoorbellAssigned {
			if d.Agent == "" {
				return internalf("mutate %s: an assigned doorbell names no assignee", tool)
			}
			var err error
			if scope, err = t.assigneeScope(d.Agent); err != nil {
				return err
			}
		}
		if t.ProjectID == "" {
			return internalf("mutate %s: a change names no project", tool)
		}
		payload, err := json.Marshal(changePayload{Kind: d.Kind, Agent: d.Agent, TaskID: d.TaskID, ProjectID: t.ProjectID, BoardID: d.BoardID, Tool: tool, Actor: t.Caller.Identity})
		if err != nil {
			return err
		}
		if _, err := t.Tx.AppendChange(t.ctx, store.ChangeInput{Scope: scope, ProjectID: t.ProjectID, Kind: d.Kind, Payload: payload, Ts: ms}); err != nil {
			return err
		}
		t.boards = append(t.boards, [2]string{t.ProjectID, d.BoardID})
	}
	return nil
}
