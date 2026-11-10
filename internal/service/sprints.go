package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// Sprints is conductor's sprints.ts: the cross-doc invariants are internal/domain/sprints.go's,
// checked inside the transaction; this file threads their inputs.
func (c *Core) Sprints() Sprints { return sprintsSvc{c} }

type sprintsSvc struct{ c *Core }

// sprintExclusive is conductor's Config.sprintExclusive at its deployment default: a task belongs
// to at most one sprint. There is no knob here (no deployment has asked for one).
const sprintExclusive = true

// sprintMemberPage is get_sprint's member read (conductor's MEMBER_PAGE): one more than it shows,
// so truncated is a tripwire on SprintMaxTasks, not pagination.
const sprintMemberPage = 200

// sprintMemoryWindow is get_sprint's include memory: the newest project memory entries.
const sprintMemoryWindow = 20

func sprintOut(sp store.Sprint) SprintDoc {
	return SprintDoc{
		Acceptance: sp.Acceptance, ApprovedBy: sp.ApprovedBy, BoardID: sp.BoardID, Checkpoint: sp.Checkpoint,
		ClosedAt: isoPtr(sp.ClosedAt), CreatedAt: isoMs(sp.CreatedAt), CreatedBy: sp.CreatedBy, EventCursor: sp.EventCursor,
		Goal: sp.Goal, ID: sp.ID, MemberCount: float64(sp.MemberCount), Name: sp.Name, ProjectID: sp.ProjectID,
		ProposalTaskID: sp.ProposalTaskID, StartedAt: isoPtr(sp.StartedAt), Status: SprintStatus(sp.Status),
		UpdatedAt: isoMs(sp.UpdatedAt), UpdatedBy: sp.UpdatedBy, Version: float64(sp.Version),
	}
}

// sprintEvent appends a board-level sprint event and rings the board.
func (t *txn) sprintEvent(sp store.Sprint, typ domain.EventType, data map[string]any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	t.BoardEvent(store.EventInput{BoardID: sp.BoardID, Type: typ, Data: raw})
	t.Ring(Doorbell{Kind: ChangeKindChanged, BoardID: sp.BoardID})
	return nil
}

// Create is create_sprint (conductor sprints.ts:82-112): PROPOSED on a board of the project, event
// sprint.created {sprintId, name}.
func (s sprintsSvc) Create(ctx context.Context, caller domain.Caller, req CreateSprintReq) (CreateSprintResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "create_sprint", domain.GrantSubject{BoardID: req.BoardID}, false)
	if err != nil {
		return CreateSprintResp{}, err
	}
	for _, f := range []struct{ name, v string }{{"boardId", req.BoardID}, {"name", req.Name}, {"goal", req.Goal}, {"acceptance", req.Acceptance}} {
		if err := required(f.name, f.v); err != nil {
			return CreateSprintResp{}, err
		}
	}
	if err := sprintText(Some(req.Name), Some(req.Goal), Some(req.Acceptance)); err != nil {
		return CreateSprintResp{}, err
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return CreateSprintResp{}, err
	}
	return mutate(ctx, s.c, caller, mutation{
		Tool: "create_sprint", Args: map[string]any{"projectId": req.ProjectID, "boardId": req.BoardID},
		ProjectID: req.ProjectID, IdempotencyKey: req.IdempotencyKey, Request: req, Access: a,
	}, func(t *txn) (CreateSprintResp, error) {
		sp, err := t.Tx.CreateSprint(ctx, store.CreateSprintInput{ProjectID: req.ProjectID, BoardID: req.BoardID, Name: req.Name,
			Goal: req.Goal, Acceptance: req.Acceptance, CreatedBy: caller.Identity, CreatedAt: t.Now.UnixMilli()})
		if err != nil {
			return CreateSprintResp{}, err
		}
		if err := t.sprintEvent(sp, domain.EventSprintCreated, map[string]any{"sprintId": sp.ID, "name": sp.Name}); err != nil {
			return CreateSprintResp{}, err
		}
		return CreateSprintResp{Sprint: sprintOut(sp)}, nil
	})
}

// sprintText holds the sprint's text fields to conductor's TitleArg and DescriptionArg bounds.
func sprintText(name, goal, acceptance Optional[string]) error {
	if name.Set {
		if name.V == "" {
			return domain.Validation("name must not be empty", map[string]any{"field": "name"})
		}
		if err := maxChars("name", name.V, maxTitleChars); err != nil {
			return err
		}
	}
	for _, f := range []struct {
		name string
		v    Optional[string]
	}{{"goal", goal}, {"acceptance", acceptance}} {
		if err := maxChars(f.name, f.v.V, maxDescriptionChars); err != nil {
			return err
		}
	}
	return nil
}

// Update is update_sprint (conductor sprints.ts:114-181, in-memory-store.ts:1645-1729), version
// checked on the sprint: proposalTaskId is patchable only while PROPOSED, and name, goal and
// acceptance only until the sprint is CLOSED or ABANDONED (VALIDATION; resending the value held
// passes, and checkpoint and eventCursor stay patchable for a resume); a status change follows the
// fixed sprint table (INVALID_TRANSITION); PROPOSED → ACTIVE passes the activation gates against
// the proposal task read in the transaction and stamps approvedBy (the proposal's reviewer, else who
// moved it to DONE, read from its events: doneBy) and startedAt; CLOSED and ABANDONED stamp
// closedAt. Closing with members not DONE warns, naming them: "open" is status != DONE (plan §Fold
// 2), so a BLOCKED-poison member is open and a cancelled one (DONE with the cancelled label,
// ADR-0001) is not. It runs under the project lock, as set_sprint and create_task {sprintId} do, so
// a member cannot join between the close and its warning.
func (s sprintsSvc) Update(ctx context.Context, caller domain.Caller, req UpdateSprintReq) (UpdateSprintResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "update_sprint", domain.GrantSubject{}, false)
	if err != nil {
		return UpdateSprintResp{}, err
	}
	if err := required("sprintId", req.SprintID); err != nil {
		return UpdateSprintResp{}, err
	}
	if req.Version < 0 {
		return UpdateSprintResp{}, domain.Validation("version must be a nonnegative integer", map[string]any{"field": "version"})
	}
	p := req.Patch
	if err := sprintText(p.Name, p.Goal, p.Acceptance); err != nil {
		return UpdateSprintResp{}, err
	}
	if err := maxChars("checkpoint", p.Checkpoint.V, maxSummaryChars); err != nil {
		return UpdateSprintResp{}, err
	}
	if jsLen(p.EventCursor.V) > 64 {
		return UpdateSprintResp{}, domain.Validation("eventCursor is at most 64 characters", map[string]any{"field": "eventCursor"})
	}
	if p.ProposalTaskID.Set && !p.ProposalTaskID.Null {
		if err := required("proposalTaskId", p.ProposalTaskID.V); err != nil {
			return UpdateSprintResp{}, err
		}
	}
	var to *domain.SprintStatus
	if p.Status.Set {
		st, err := domain.ParseSprintStatus(string(p.Status.V))
		if err != nil {
			return UpdateSprintResp{}, err
		}
		to = &st
	}
	proj, err := s.c.store.GetProject(ctx, req.ProjectID)
	if err != nil {
		return UpdateSprintResp{}, s.c.mapErr("update_sprint", err)
	}
	settings, err := settingsOut(proj.Settings)
	if err != nil {
		return UpdateSprintResp{}, s.c.mapErr("update_sprint", err)
	}
	return mutate(ctx, s.c, caller, mutation{
		Tool: "update_sprint", Args: map[string]any{"projectId": req.ProjectID, "sprintId": req.SprintID},
		ProjectID: req.ProjectID, Access: a, LockProject: true,
	}, func(t *txn) (UpdateSprintResp, error) {
		cur, err := t.Tx.GetSprint(ctx, req.ProjectID, req.SprintID)
		if err != nil {
			return UpdateSprintResp{}, err
		}
		if cur.Version != req.Version {
			return UpdateSprintResp{}, domain.Stale(req.Version, cur.Version)
		}
		if cur.Status == domain.SprintClosed || cur.Status == domain.SprintAbandoned {
			for _, f := range []struct {
				name     string
				patch    Optional[string]
				recorded string
			}{{"name", p.Name, cur.Name}, {"goal", p.Goal, cur.Goal}, {"acceptance", p.Acceptance, cur.Acceptance}} {
				if f.patch.Set && f.patch.V != f.recorded {
					return UpdateSprintResp{}, domain.Validation(fmt.Sprintf("%s is patchable only while PROPOSED or ACTIVE — sprint %s is %s and keeps the record it ended with", f.name, cur.ID, cur.Status),
						map[string]any{"sprintId": cur.ID, "sprintStatus": cur.Status, "field": f.name})
				}
			}
		}
		now := t.Now.UnixMilli()
		sp := store.SprintPatch{UpdatedBy: caller.Identity, UpdatedAt: now}
		if p.Name.Set {
			sp.Name = &p.Name.V
		}
		if p.Goal.Set {
			sp.Goal = &p.Goal.V
		}
		if p.Acceptance.Set {
			sp.Acceptance = &p.Acceptance.V
		}
		sp.Checkpoint, sp.EventCursor = nullableIn(p.Checkpoint), nullableIn(p.EventCursor)
		proposal := cur.ProposalTaskID
		if p.ProposalTaskID.Set {
			sp.ProposalTaskID = nullableIn(p.ProposalTaskID)
			proposal = sp.ProposalTaskID.Value
			if !samePtr(proposal, cur.ProposalTaskID) && cur.Status != domain.SprintProposed {
				return UpdateSprintResp{}, domain.Validation(fmt.Sprintf("proposalTaskId is patchable only while PROPOSED — repointing a %s sprint's provenance is refused", cur.Status),
					map[string]any{"sprintId": cur.ID, "sprintStatus": cur.Status})
			}
		}
		transitioning := to != nil && *to != cur.Status
		selfApproved := false
		if transitioning {
			if err := domain.CheckSprintTransition(cur.Status, *to); err != nil {
				return UpdateSprintResp{}, err
			}
			sp.Status = to
			stamp := &store.Nullable[int64]{Value: &now}
			switch *to {
			case domain.SprintActive:
				gate := domain.SprintActivation{SprintID: cur.ID, ProposalTaskID: proposal, CreatedBy: cur.CreatedBy, ActorRole: a.Role,
					ActorIsCreator: cur.CreatedBy == caller.Identity, SelfApproval: string(orWarn(settings.SelfApprovalPolicy))}
				if proposal != nil {
					if gate.Proposal, err = t.proposalOf(*proposal); err != nil {
						return UpdateSprintResp{}, err
					}
				}
				approvedBy, self, err := domain.CheckSprintActivation(gate)
				if err != nil {
					return UpdateSprintResp{}, err
				}
				if approvedBy != "" {
					sp.ApprovedBy = &store.Nullable[string]{Value: &approvedBy}
				}
				sp.StartedAt, selfApproved = stamp, self
			case domain.SprintClosed, domain.SprintAbandoned:
				sp.ClosedAt = stamp
			}
		}
		updated, err := t.Tx.UpdateSprint(ctx, req.ProjectID, cur.ID, cur.Version, sp)
		if err != nil {
			return UpdateSprintResp{}, err
		}
		typ, data := domain.EventSprintUpdated, map[string]any{"sprintId": cur.ID}
		switch {
		case transitioning && *to == domain.SprintActive:
			typ, data = domain.EventSprintActivated, map[string]any{"sprintId": cur.ID, "proposalTaskId": updated.ProposalTaskID, "approvedBy": updated.ApprovedBy}
		case transitioning && *to == domain.SprintClosed:
			typ = domain.EventSprintClosed
		case transitioning:
			typ = domain.EventSprintAbandoned
		case p.Checkpoint.Set || p.EventCursor.Set:
			typ, data = domain.EventSprintCheckpoint, map[string]any{"sprintId": cur.ID, "eventCursor": updated.EventCursor}
		}
		if err := t.sprintEvent(updated, typ, data); err != nil {
			return UpdateSprintResp{}, err
		}
		if selfApproved {
			if err := t.sprintEvent(updated, domain.EventSprintSelfApproved, map[string]any{"sprintId": cur.ID, "createdBy": cur.CreatedBy}); err != nil {
				return UpdateSprintResp{}, err
			}
		}
		resp := UpdateSprintResp{Sprint: sprintOut(updated)}
		if transitioning && *to == domain.SprintClosed {
			members, err := t.Tx.ListSprintMembers(ctx, req.ProjectID, cur.ID, sprintMemberPage)
			if err != nil {
				return UpdateSprintResp{}, err
			}
			var open []string
			for _, m := range members {
				if m.Status != domain.Done {
					open = append(open, m.ID)
				}
			}
			if len(open) > 0 {
				resp.Warnings = []string{fmt.Sprintf("%d member(s) not DONE at close: %s — they stay members of this sprint for history; set_sprint add carries each to a successor sprint.",
					len(open), strings.Join(open, ", "))}
			}
		}
		return resp, nil
	})
}

// proposalOf is the activation gate's view of the bound proposal task, nil when it is missing.
func (t *txn) proposalOf(taskID string) (*domain.SprintProposal, error) {
	task, err := t.Tx.GetTask(t.ctx, t.ProjectID, taskID)
	if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := &domain.SprintProposal{ID: task.ID, Status: task.Status, Cancelled: slices.Contains(task.Labels, domain.LabelCancelled)}
	if task.Status == domain.Done {
		if out.DoneBy, err = t.doneBy(task.ID); err != nil {
			return nil, err
		}
	}
	var fields map[string]any
	if len(task.CustomFields) > 0 {
		if err := json.Unmarshal(task.CustomFields, &fields); err != nil {
			return nil, err
		}
	}
	if v, ok := fields["sprintProposalFor"].(string); ok {
		out.SprintProposalFor = v
	}
	review, err := reviewOut(task.Review)
	if err != nil {
		return nil, err
	}
	if review != nil && review.Reviewer != nil {
		out.Reviewer = *review.Reviewer
	}
	return out, nil
}

// doneByPages bounds doneBy's read of a task's history, newest first, at 200 events a page.
const doneByPages = 5

// doneBy is the actor of the newest event that landed taskID DONE: task.moved from another status,
// or task.completed or run.picked, to DONE. "" when none of its newest doneByPages pages holds one.
func (t *txn) doneBy(taskID string) (string, error) {
	cursor := ""
	for range doneByPages {
		page, err := t.Tx.ListTaskEvents(t.ctx, t.ProjectID, taskID, cursor, 200)
		if err != nil {
			return "", err
		}
		for _, e := range page.Events {
			if e.Type != domain.EventTaskMoved && e.Type != domain.EventTaskCompleted && e.Type != domain.EventRunPicked {
				continue
			}
			var d struct {
				From domain.Status `json:"from"`
				To   domain.Status `json:"to"`
			}
			if err := json.Unmarshal(e.Data, &d); err != nil {
				return "", err
			}
			if d.To == domain.Done && d.From != domain.Done {
				return e.Actor, nil
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	return "", nil
}

// orWarn is a policy as conductor reads it: unset is "warn" (artifacts.ts:15,140).
func orWarn(m PolicyMode) PolicyMode {
	if m == "" {
		return "warn"
	}
	return m
}

func nullableIn(n Nullable[string]) *store.Nullable[string] {
	switch {
	case !n.Set:
		return nil
	case n.Null:
		return &store.Nullable[string]{}
	}
	v := n.V
	return &store.Nullable[string]{Value: &v}
}

func samePtr(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}

// Set is set_sprint (conductor sprints.ts:183-237, in-memory-store.ts:1731-1782): an add of a task on
// another board is VALIDATION; an add of a member, or a remove of a non-member, returns the task
// unchanged before the version check (retry-safe); otherwise the task is version-checked, the
// membership is judged (domain.CheckSprintMembership, exclusive among the sprints that have not
// ended: endedSprints), and the task's sprintIds and the sprint's member count move together, with
// sprint.task_added or sprint.task_removed on the task. It runs under the project lock, as
// update_sprint does: under Postgres READ COMMITTED a close that committed after this call read the
// sprint ACTIVE would otherwise let the task join a CLOSED sprint its close warning never named.
func (s sprintsSvc) Set(ctx context.Context, caller domain.Caller, req SetSprintReq) (SetSprintResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "set_sprint", domain.GrantSubject{}, true)
	if err != nil {
		return SetSprintResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return SetSprintResp{}, err
	}
	if err := required("sprintId", req.SprintID); err != nil {
		return SetSprintResp{}, err
	}
	if !slices.Contains(SprintActionValues, req.Action) {
		return SetSprintResp{}, domain.Validation("action must be add or remove", map[string]any{"field": "action"})
	}
	if req.Version < 0 {
		return SetSprintResp{}, domain.Validation("version must be a nonnegative integer", map[string]any{"field": "version"})
	}
	add := req.Action == "add"
	return mutate(ctx, s.c, caller, mutation{
		Tool: "set_sprint", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
		ProjectID: req.ProjectID, LockProject: true,
	}, func(t *txn) (SetSprintResp, error) {
		task, err := t.Tx.GetTask(ctx, req.ProjectID, req.TaskID)
		if err != nil {
			return SetSprintResp{}, err
		}
		if err := a.permit(&task); err != nil {
			return SetSprintResp{}, err
		}
		sprint, err := t.Tx.GetSprint(ctx, req.ProjectID, req.SprintID)
		if err != nil {
			return SetSprintResp{}, err
		}
		if add && task.BoardID != sprint.BoardID {
			return SetSprintResp{}, domain.Validation(fmt.Sprintf("Task %s is on board %s but sprint %s is on %s — sprint members must live on the sprint's own board",
				task.ID, task.BoardID, sprint.ID, sprint.BoardID), map[string]any{"taskBoardId": task.BoardID, "sprintBoardId": sprint.BoardID})
		}
		in := slices.Contains(task.SprintIDs, sprint.ID)
		after := task
		if add != in {
			if task.Version != req.Version {
				return SetSprintResp{}, domain.Stale(req.Version, task.Version)
			}
			var ended []string
			if add {
				if ended, err = t.endedSprints(task.SprintIDs, sprint.ID); err != nil {
					return SetSprintResp{}, err
				}
			}
			if err := domain.CheckSprintMembership(domain.SprintMembership{SprintID: sprint.ID, SprintStatus: sprint.Status,
				MemberCount: sprint.MemberCount, Add: add, TaskID: task.ID, TaskSprints: task.SprintIDs, EndedSprints: ended,
				Exclusive: sprintExclusive}); err != nil {
				return SetSprintResp{}, err
			}
			ids, delta, typ := append(slices.Clone(task.SprintIDs), sprint.ID), int64(1), domain.EventSprintTaskAdded
			if !add {
				ids, delta, typ = slices.DeleteFunc(slices.Clone(task.SprintIDs), func(x string) bool { return x == sprint.ID }), -1, domain.EventSprintTaskRemoved
			}
			if after, err = t.Tx.UpdateTask(ctx, req.ProjectID, task.ID, task.Version, store.TaskPatch{SprintIDs: &ids, UpdatedBy: caller.Identity, UpdatedAt: t.Now.UnixMilli()}); err != nil {
				return SetSprintResp{}, err
			}
			moved, err := t.Tx.AddSprintMembers(ctx, req.ProjectID, sprint.ID, delta)
			if err != nil {
				return SetSprintResp{}, err
			}
			if err := t.event(after, typ, map[string]any{"sprintId": sprint.ID, "memberCount": moved.MemberCount}); err != nil {
				return SetSprintResp{}, err
			}
		}
		t.ringChanged(after)
		out, err := withSummary(ctx, t.Tx, after)
		if err != nil {
			return SetSprintResp{}, err
		}
		return SetSprintResp{Task: out}, nil
	})
}

// endedSprints is the ids of sprintIDs, but except, whose sprint is CLOSED or ABANDONED (or gone):
// they stay in the task's sprintIds for history and hold it out of no other sprint
// (domain.CheckSprintMembership). One read per id; a task carries at most SprintIDsMax.
func (t *txn) endedSprints(sprintIDs []string, except string) ([]string, error) {
	var out []string
	for _, id := range sprintIDs {
		if id == except {
			continue
		}
		sp, err := t.Tx.GetSprint(t.ctx, t.ProjectID, id)
		if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeNotFound {
			out = append(out, id)
			continue
		}
		if err != nil {
			return nil, err
		}
		if sp.Status == domain.SprintClosed || sp.Status == domain.SprintAbandoned {
			out = append(out, id)
		}
	}
	return out, nil
}

// Get is get_sprint (conductor sprints.ts:239-408): the sprint, its members (one read of at most
// sprintMemberPage+1, truncated the tripwire), the derived counts, frontier (the claimable members
// and the claimable runs of its racing members, which are not members) and attention, the board's
// lanes and the fixed table, and the project policies. counts: ready is TODO, backlog is always 0
// (no parking state, ADR-0005), blocked the BLOCKED members that are not poisoned and poisoned those
// that are (plan §Fold 1), done the DONE members less the cancelled ones (ADR-0001: conductor's
// CANCELLED counted toward total only). attention names poisoned before blocked (ADR-0001 §5).
// resume is {}: conductor's resumeState is unported (P-117).
func (s sprintsSvc) Get(ctx context.Context, caller domain.Caller, req GetSprintReq) (GetSprintResp, error) {
	if _, err := s.c.authorize(ctx, caller, req.ProjectID, "get_sprint", domain.GrantSubject{}, false); err != nil {
		return GetSprintResp{}, err
	}
	if err := required("sprintId", req.SprintID); err != nil {
		return GetSprintResp{}, err
	}
	for _, in := range req.Include {
		if !slices.Contains(SprintIncludeValues, in) {
			return GetSprintResp{}, domain.Validation("include takes deliverables and memory", map[string]any{"field": "include"})
		}
	}
	fail := func(err error) (GetSprintResp, error) { return GetSprintResp{}, s.c.mapErr("get_sprint", err) }
	sp, err := s.c.store.GetSprint(ctx, req.ProjectID, req.SprintID)
	if err != nil {
		return fail(err)
	}
	members, err := s.c.store.ListSprintMembers(ctx, req.ProjectID, sp.ID, sprintMemberPage+1)
	if err != nil {
		return fail(err)
	}
	out := GetSprintResp{Sprint: sprintOut(sp), Tasks: []SprintMember{}, Frontier: []string{}, Attention: []SprintAttention{},
		Resume: map[string]*ResumeBriefing{}, Truncated: len(members) > sprintMemberPage}
	members = members[:min(len(members), sprintMemberPage)]
	now := s.c.now()
	for _, m := range members {
		v := SprintMember{Assignee: m.Assignee, BlockedBy: nonNil(m.BlockedBy), Blocks: nonNil(m.Blocks), HasDeliverable: m.Deliverable != nil,
			ID: m.ID, Priority: m.Priority, Status: m.Status, Title: m.Title}
		if liveLease(m, now.UnixMilli()) {
			v.Claim = &SprintClaim{Agent: m.Claim.Agent, ExpiresAt: isoMs(m.Claim.ExpiresAt)}
		}
		out.Tasks = append(out.Tasks, v)
		out.Counts.Total++
		poisoned := domain.IsPoisoned(m.Status, reasonOf(m))
		switch {
		case m.Status == domain.Done && !slices.Contains(m.Labels, domain.LabelCancelled):
			out.Counts.Done++
		case m.Status == domain.InProgress:
			out.Counts.InProgress++
		case m.Status == domain.Review:
			out.Counts.Review++
		case poisoned:
			out.Counts.Poisoned++
		case m.Status == domain.Blocked:
			out.Counts.Blocked++
		case m.Status == domain.Todo:
			out.Counts.Ready++
		}
		if domain.IsClaimable(claimView(m), now) {
			out.Frontier = append(out.Frontier, m.ID)
		}
		why := AttentionWhy("")
		switch {
		case m.Status == domain.Review:
			why = "review"
		case poisoned:
			why = "poisoned"
		case m.Status == domain.Blocked:
			why = "blocked"
		case m.Status == domain.InProgress && m.Claim != nil && !liveLease(m, now.UnixMilli()):
			why = "stale_lease"
		}
		if why != "" {
			out.Attention = append(out.Attention, SprintAttention{TaskID: m.ID, Why: why})
		}
	}
	// A racing member is not claimable; its runs carry its work, and claim_next {sprintId} offers
	// them, so the frontier names its claimable runs (one read per racing member).
	for _, m := range members {
		if !m.Racing {
			continue
		}
		runs, err := s.c.store.ListTasks(ctx, req.ProjectID, store.TaskFilter{BoardID: m.BoardID, RunOf: &m.ID}, "", listMaxLimit)
		if err != nil {
			return fail(err)
		}
		for _, r := range runs.Tasks {
			if domain.IsClaimable(claimView(r), now) {
				out.Frontier = append(out.Frontier, r.ID)
			}
		}
	}
	board, _, err := s.c.store.GetBoard(ctx, req.ProjectID, sp.BoardID)
	if err != nil {
		return fail(err)
	}
	out.Workflow = SprintWorkflow{Lanes: make([]Column, 0, len(board.Columns)), Transitions: fixedTransitions()}
	for _, c := range board.Columns {
		out.Workflow.Lanes = append(out.Workflow.Lanes, columnOut(c))
	}
	proj, err := s.c.store.GetProject(ctx, req.ProjectID)
	if err != nil {
		return fail(err)
	}
	settings, err := settingsOut(proj.Settings)
	if err != nil {
		return fail(err)
	}
	out.Policies = SprintPolicies{ArtifactPolicy: orWarn(settings.ArtifactPolicy), SelfApprovalPolicy: orWarn(settings.SelfApprovalPolicy),
		DefaultReviewer: settings.DefaultReviewer}
	if slices.Contains(req.Include, "deliverables") {
		out.Deliverables = []SprintDeliverable{}
		for _, m := range members {
			if m.Deliverable != nil && *m.Deliverable != "" {
				out.Deliverables = append(out.Deliverables, SprintDeliverable{TaskID: m.ID, Title: m.Title, Deliverable: *m.Deliverable})
			}
		}
	}
	if slices.Contains(req.Include, "memory") {
		rows, err := s.c.store.RecallMemory(ctx, store.RecallMemoryQuery{Scope: store.MemoryProject, ProjectID: req.ProjectID, Limit: sprintMemoryWindow})
		if err != nil {
			return fail(err)
		}
		if out.Memory, err = memoryOut(rows); err != nil {
			return fail(err)
		}
	}
	return out, nil
}

// List is list_sprints (conductor sprints.ts:410-436): the project's sprints oldest first, filtered
// by board and status, paged by cursor; limit 1 to 200 (default 50).
func (s sprintsSvc) List(ctx context.Context, caller domain.Caller, req ListSprintsReq) (ListSprintsResp, error) {
	if _, err := s.c.authorize(ctx, caller, req.ProjectID, "list_sprints", domain.GrantSubject{}, false); err != nil {
		return ListSprintsResp{}, err
	}
	if req.Limit < 0 || req.Limit > 200 {
		return ListSprintsResp{}, domain.Validation("limit must be 1 to 200", map[string]any{"field": "limit"})
	}
	f := store.SprintFilter{BoardID: req.BoardID}
	if req.Status != "" {
		st, err := domain.ParseSprintStatus(string(req.Status))
		if err != nil {
			return ListSprintsResp{}, err
		}
		f.Status = &st
	}
	page, err := s.c.store.ListSprints(ctx, req.ProjectID, f, req.Cursor, int(req.Limit))
	if err != nil {
		return ListSprintsResp{}, s.c.mapErr("list_sprints", err)
	}
	out := ListSprintsResp{Sprints: make([]SprintDoc, 0, len(page.Sprints)), NextCursor: page.NextCursor}
	for _, sp := range page.Sprints {
		out.Sprints = append(out.Sprints, sprintOut(sp))
	}
	return out, nil
}
