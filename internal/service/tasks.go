package service

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// tasksSvc is service.Tasks: create, get, update, move and list here, the delete cascade in delete.go.
type tasksSvc struct{ c *Core }

var _ Tasks = tasksSvc{}

// Tasks is the task CRUD, move and delete service.
func (c *Core) Tasks() Tasks { return tasksSvc{c} }

// Conductor's remaining argument bounds (src/config/constants.ts, src/tools/schemas.ts).
const (
	maxLabelChars       = 64
	maxLabels           = 32
	maxCustomFields     = 32
	maxCustomFieldChars = 500
	maxModelChars       = 100
	maxRationaleChars   = 500
	maxReviewerChars    = 320
	maxDateChars        = 64
	listMaxLimit        = 200
)

var priorities = []domain.Priority{domain.PriorityLow, domain.PriorityNormal, domain.PriorityHigh, domain.PriorityUrgent}

func checkPriority(p domain.Priority) error {
	if p != "" && !slices.Contains(priorities, p) {
		return domain.Validation("unknown priority: "+string(p), map[string]any{"field": "priority"})
	}
	return nil
}

// checkLabelList is LabelListArg: at most 32 entries of 1 to 64 characters.
func checkLabelList(field string, ls []string) error {
	if len(ls) > maxLabels {
		return domain.Validation(field+" has more than 32 entries", map[string]any{"field": field, "max": maxLabels})
	}
	for _, l := range ls {
		if err := checkLabel(field, l); err != nil {
			return err
		}
	}
	return nil
}

// checkLabel is LabelArg: 1 to 64 characters.
func checkLabel(field, l string) error {
	if l == "" {
		return domain.Validation(field+" is empty", map[string]any{"field": field})
	}
	return maxChars(field, l, maxLabelChars)
}

// customFieldsIn is CustomFieldsArg as the store keeps it: at most 32 keys of 1 to 200 characters,
// each a string of at most 500, a number or a boolean.
func customFieldsIn(m map[string]FieldValue) (json.RawMessage, error) {
	if len(m) > maxCustomFields {
		return nil, domain.Validation("at most 32 custom fields", map[string]any{"field": "customFields", "max": maxCustomFields})
	}
	for k, v := range m {
		if k == "" {
			return nil, domain.Validation("a custom field key is empty", map[string]any{"field": "customFields"})
		}
		if err := maxChars("customFields key", k, maxKeyChars); err != nil {
			return nil, err
		}
		switch x := v.(type) {
		case string:
			if err := maxChars("customFields."+k, x, maxCustomFieldChars); err != nil {
				return nil, err
			}
		case float64, bool:
		default:
			return nil, domain.Validation("customFields."+k+" is not a string, number or boolean", map[string]any{"field": "customFields." + k})
		}
	}
	if m == nil {
		m = map[string]FieldValue{}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, internalf("customFields: %v", err)
	}
	return raw, nil
}

// modelHintIn is ModelHintSchema: model 1 to 100 characters, an effort of the enum, rationale at
// most 500.
func modelHintIn(h ModelHintInput) (json.RawMessage, error) {
	if h.Model == "" {
		return nil, domain.Validation("modelHint.model is required", map[string]any{"field": "modelHint.model"})
	}
	if err := maxChars("modelHint.model", h.Model, maxModelChars); err != nil {
		return nil, err
	}
	if h.Effort != "" && !slices.Contains(EffortValues, h.Effort) {
		return nil, domain.Validation("unknown modelHint.effort: "+string(h.Effort), map[string]any{"field": "modelHint.effort"})
	}
	if err := maxChars("modelHint.rationale", h.Rationale, maxRationaleChars); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(h)
	if err != nil {
		return nil, internalf("modelHint: %v", err)
	}
	return raw, nil
}

// isoIn is a wire time (at most 64 characters, RFC 3339) as Unix milliseconds.
// TODO(spec): conductor stores dueAt and compares updatedSince as the strings sent; the store keeps
// milliseconds, so a string that is not RFC 3339 is VALIDATION here.
func isoIn(field, v string) (int64, error) {
	if err := maxChars(field, v, maxDateChars); err != nil {
		return 0, err
	}
	t, err := time.Parse(time.RFC3339Nano, v)
	if err != nil {
		return 0, domain.Validation(field+" is not an RFC 3339 time", map[string]any{"field": field})
	}
	return t.UnixMilli(), nil
}

func callerAgent(c domain.Caller) *string {
	if c.Agent == "" {
		return nil
	}
	a := c.Agent
	return &a
}

// Create is create_task (conductor tasks.ts:102-160): the task lands TODO (ADR-0005), in columnId,
// which must be a TODO column, or the board's first TODO column, after the column's last task
// (endOfColumn); its T-n is the project's next (the store's counter, never reused). Event task.created {title}; the landing rings broadcast (it has no
// assignee). With sprintId the task is created a member of that sprint in the same transaction, as
// set_sprint add makes one (P-239): the sprint must exist (NOT_FOUND), be on the task's board
// (VALIDATION) and pass domain.CheckSprintMembership; its memberCount moves by one and the task gets
// sprint.task_added {sprintId, memberCount}. Without sprintId no statement is added.
func (s tasksSvc) Create(ctx context.Context, caller domain.Caller, req CreateTaskReq) (CreateTaskResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "create_task", domain.GrantSubject{BoardID: req.BoardID, Labels: req.Labels}, false)
	if err != nil {
		return CreateTaskResp{}, err
	}
	if err := required("boardId", req.BoardID); err != nil {
		return CreateTaskResp{}, err
	}
	if err := required("title", req.Title); err != nil {
		return CreateTaskResp{}, err
	}
	if err := maxChars("title", req.Title, maxTitleChars); err != nil {
		return CreateTaskResp{}, err
	}
	if err := maxChars("description", req.Description, maxDescriptionChars); err != nil {
		return CreateTaskResp{}, err
	}
	if err := checkPriority(req.Priority); err != nil {
		return CreateTaskResp{}, err
	}
	priority := req.Priority
	if priority == "" {
		priority = domain.PriorityNormal
	}
	if err := checkLabelList("labels", req.Labels); err != nil {
		return CreateTaskResp{}, err
	}
	if err := domain.CheckCallerLabels(nil, req.Labels); err != nil {
		return CreateTaskResp{}, err
	}
	if err := checkLabelList("requiredCapabilities", req.RequiredCapabilities); err != nil {
		return CreateTaskResp{}, err
	}
	fields, err := customFieldsIn(req.CustomFields)
	if err != nil {
		return CreateTaskResp{}, err
	}
	var hint json.RawMessage
	if req.ModelHint != (ModelHintInput{}) {
		if hint, err = modelHintIn(req.ModelHint); err != nil {
			return CreateTaskResp{}, err
		}
	}
	if req.ParentTaskID != nil {
		if err := required("parentTaskId", *req.ParentTaskID); err != nil {
			return CreateTaskResp{}, err
		}
	}
	if err := maxChars("sprintId", req.SprintID, 64); err != nil {
		return CreateTaskResp{}, err
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return CreateTaskResp{}, err
	}
	return mutate(ctx, s.c, caller, mutation{
		Tool: "create_task", Args: map[string]any{"projectId": req.ProjectID, "boardId": req.BoardID},
		ProjectID: req.ProjectID, IdempotencyKey: req.IdempotencyKey, Request: req, Access: a,
	}, func(t *txn) (CreateTaskResp, error) {
		board, _, err := t.Tx.GetBoard(ctx, req.ProjectID, req.BoardID)
		if err != nil {
			return CreateTaskResp{}, err
		}
		var col *store.Column
		for i := range board.Columns { // the store keeps them in (order, id)
			if req.ColumnID == "" && board.Columns[i].Status == domain.Todo || req.ColumnID != "" && board.Columns[i].ID == req.ColumnID {
				col = &board.Columns[i]
				break
			}
		}
		if col == nil {
			return CreateTaskResp{}, domain.Validation("Board has no TODO column / column not found", map[string]any{"columnId": req.ColumnID})
		}
		// ADR-0005: a created task is never a draft, so it lands TODO and nowhere else.
		if col.Status != domain.Todo {
			return CreateTaskResp{}, domain.Validation(fmt.Sprintf("A task is created in a TODO column; column %s is %s", col.ID, col.Status), map[string]any{"columnId": col.ID, "status": col.Status})
		}
		if req.ParentTaskID != nil {
			if _, err := t.Tx.GetTask(ctx, req.ProjectID, *req.ParentTaskID); err != nil {
				return CreateTaskResp{}, err
			}
		}
		var sprintIDs []string
		if req.SprintID != "" {
			sprint, err := t.Tx.GetSprint(ctx, req.ProjectID, req.SprintID)
			if err != nil {
				return CreateTaskResp{}, err
			}
			if sprint.BoardID != board.ID {
				return CreateTaskResp{}, domain.Validation(fmt.Sprintf("Sprint %s is on board %s, not %s — sprint members must live on the sprint's own board",
					sprint.ID, sprint.BoardID, board.ID), map[string]any{"taskBoardId": board.ID, "sprintBoardId": sprint.BoardID})
			}
			if err := domain.CheckSprintMembership(domain.SprintMembership{SprintID: sprint.ID, SprintStatus: sprint.Status,
				MemberCount: sprint.MemberCount, Add: true, Exclusive: sprintExclusive}); err != nil {
				return CreateTaskResp{}, err
			}
			sprintIDs = []string{sprint.ID}
		}
		order, err := endOfColumn(ctx, t.Tx, req.ProjectID, board.ID, col.ID)
		if err != nil {
			return CreateTaskResp{}, err
		}
		task, err := t.Tx.CreateTask(ctx, store.CreateTaskInput{
			ProjectID: req.ProjectID, BoardID: board.ID, ColumnID: col.ID, Title: req.Title, Description: req.Description,
			Status: col.Status, Priority: priority, Labels: req.Labels, RequiredCapabilities: req.RequiredCapabilities,
			ParentTaskID: req.ParentTaskID, OrderKey: order, CustomFields: fields, ModelHint: hint, SprintIDs: sprintIDs,
			CreatedBy: caller.Identity, CreatedByAgent: callerAgent(caller), CreatedAt: t.Now.UnixMilli(),
		})
		if err != nil {
			return CreateTaskResp{}, err
		}
		data, err := json.Marshal(map[string]any{"title": task.Title})
		if err != nil {
			return CreateTaskResp{}, err
		}
		t.Event(store.EventInput{TaskID: task.ID, BoardID: task.BoardID, Type: domain.EventTaskCreated, Data: data})
		if len(sprintIDs) > 0 {
			moved, err := t.Tx.AddSprintMembers(ctx, req.ProjectID, sprintIDs[0], 1)
			if err != nil {
				return CreateTaskResp{}, err
			}
			if err := t.event(task, domain.EventSprintTaskAdded, map[string]any{"sprintId": sprintIDs[0], "memberCount": moved.MemberCount}); err != nil {
				return CreateTaskResp{}, err
			}
		}
		t.Wrote(task)
		t.RingFor(nil, task, false)
		out, err := taskOut(task, "")
		if err != nil {
			return CreateTaskResp{}, err
		}
		return CreateTaskResp{Task: out}, nil
	})
}

// Get is get_task: two statements, the task and its summary (the get_task pin), the membership from
// the access cache.
func (s tasksSvc) Get(ctx context.Context, caller domain.Caller, req GetTaskReq) (GetTaskResp, error) {
	if _, err := s.c.authorize(ctx, caller, req.ProjectID, "get_task", domain.GrantSubject{}, true); err != nil {
		return GetTaskResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return GetTaskResp{}, err
	}
	task, err := s.c.store.GetTask(ctx, req.ProjectID, req.TaskID)
	if err != nil {
		return GetTaskResp{}, s.c.mapErr("get_task", err)
	}
	sum, err := s.c.store.GetSummary(ctx, req.ProjectID, req.TaskID)
	if err != nil {
		return GetTaskResp{}, s.c.mapErr("get_task", err)
	}
	out, err := taskOut(task, sum.Body)
	if err != nil {
		return GetTaskResp{}, s.c.mapErr("get_task", err)
	}
	// The drawer's evidence list reads task.artifacts (R2.1): one more statement.
	if out, err = withArtifacts(ctx, s.c.store, out); err != nil {
		return GetTaskResp{}, s.c.mapErr("get_task", err)
	}
	return GetTaskResp{Task: out}, nil
}

// Update is update_task (conductor tasks.ts:258-343): version-checked by mutate (STALE), the named
// patch fields written, absent ones kept, a null clearing a nullable one. customFields replaces the
// map whole, as conductor's txn.set does. Event task.updated {fields}, the fields in conductor's
// order. Doorbell: assigned on a new assignee; broadcast when a TODO task loses its assignee.
// autoAssign's resolution is not carried (no agent-profile store): autoAssign beside patch.assignee
// is conductor's VALIDATION, and autoAssign alone is VALIDATION too.
func (s tasksSvc) Update(ctx context.Context, caller domain.Caller, req UpdateTaskReq) (UpdateTaskResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "update_task", domain.GrantSubject{Labels: req.Patch.Labels}, true)
	if err != nil {
		return UpdateTaskResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return UpdateTaskResp{}, err
	}
	if req.Version < 0 {
		return UpdateTaskResp{}, domain.Validation("version must be a nonnegative integer", map[string]any{"field": "version"})
	}
	if req.AutoAssign && req.Patch.Assignee.Set {
		return UpdateTaskResp{}, domain.Validation("autoAssign and patch.assignee both set — pick one. `autoAssign: true` chooses the assignee; `patch.assignee` states it.", nil)
	}
	if req.AutoAssign {
		// TODO(spec): conductor's resolveAutoAssignee reads agent profiles, which have no store yet.
		return UpdateTaskResp{}, domain.Validation("autoAssign is not supported yet: set patch.assignee", map[string]any{"field": "autoAssign"})
	}
	p, fields, err := patchIn(req.Patch)
	if err != nil {
		return UpdateTaskResp{}, err
	}
	version := req.Version
	return mutate(ctx, s.c, caller, mutation{
		Tool: "update_task", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
		ProjectID: req.ProjectID, TaskID: req.TaskID, Version: &version, Access: a,
	}, func(t *txn) (UpdateTaskResp, error) {
		if p.Labels != nil {
			if err := domain.CheckCallerLabels(t.Task.Labels, *p.Labels); err != nil {
				return UpdateTaskResp{}, err
			}
		}
		p.UpdatedBy, p.UpdatedAt = caller.Identity, t.Now.UnixMilli()
		after, err := t.Tx.UpdateTask(ctx, req.ProjectID, req.TaskID, t.Task.Version, p)
		if err != nil {
			return UpdateTaskResp{}, err
		}
		t.Wrote(after)
		data, err := json.Marshal(map[string]any{"fields": fields})
		if err != nil {
			return UpdateTaskResp{}, err
		}
		t.Event(store.EventInput{Type: domain.EventTaskUpdated, Data: data})
		t.RingFor(t.Task, after, false)
		sum, err := t.Tx.GetSummary(ctx, req.ProjectID, req.TaskID)
		if err != nil {
			return UpdateTaskResp{}, err
		}
		out, err := taskOut(after, sum.Body)
		if err != nil {
			return UpdateTaskResp{}, err
		}
		return UpdateTaskResp{Task: out}, nil
	})
}

// patchIn is update_task's patch validated and as the store writes it, with the names of the fields
// it sets in conductor's order (tasks.ts:322-333).
func patchIn(in TaskPatch) (store.TaskPatch, []string, error) {
	var p store.TaskPatch
	fields := []string{}
	if in.Title != "" {
		if err := maxChars("patch.title", in.Title, maxTitleChars); err != nil {
			return p, nil, err
		}
		p.Title = &in.Title
		fields = append(fields, "title")
	}
	if in.Description.Set {
		if err := maxChars("patch.description", in.Description.V, maxDescriptionChars); err != nil {
			return p, nil, err
		}
		p.Description = &in.Description.V
		fields = append(fields, "description")
	}
	if in.Priority != "" {
		if err := checkPriority(in.Priority); err != nil {
			return p, nil, err
		}
		p.Priority = &in.Priority
		fields = append(fields, "priority")
	}
	if in.Labels != nil {
		if err := checkLabelList("patch.labels", in.Labels); err != nil {
			return p, nil, err
		}
		p.Labels = &in.Labels
		fields = append(fields, "labels")
	}
	if in.Assignee.Set {
		if !in.Assignee.Null {
			if err := checkLabel("patch.assignee", in.Assignee.V); err != nil {
				return p, nil, err
			}
		}
		p.Assignee = &store.Nullable[string]{Value: in.Assignee.Ptr()}
		fields = append(fields, "assignee")
	}
	if in.Estimate.Set {
		p.Estimate = &store.Nullable[float64]{Value: in.Estimate.Ptr()}
		fields = append(fields, "estimate")
	}
	if in.DueAt.Set {
		var due *int64
		if !in.DueAt.Null {
			ms, err := isoIn("patch.dueAt", in.DueAt.V)
			if err != nil {
				return p, nil, err
			}
			due = &ms
		}
		p.DueAt = &store.Nullable[int64]{Value: due}
		fields = append(fields, "dueAt")
	}
	if in.RequiredCapabilities != nil {
		if err := checkLabelList("patch.requiredCapabilities", in.RequiredCapabilities); err != nil {
			return p, nil, err
		}
		p.RequiredCapabilities = &in.RequiredCapabilities
		fields = append(fields, "requiredCapabilities")
	}
	if in.CustomFields != nil {
		raw, err := customFieldsIn(in.CustomFields)
		if err != nil {
			return p, nil, err
		}
		p.CustomFields = &raw
		fields = append(fields, "customFields")
	}
	if in.ModelHint.Set {
		var raw *json.RawMessage
		if !in.ModelHint.Null {
			h, err := modelHintIn(in.ModelHint.V)
			if err != nil {
				return p, nil, err
			}
			raw = &h
		}
		p.ModelHint = &store.Nullable[json.RawMessage]{Value: raw}
		fields = append(fields, "modelHint")
	}
	if in.Reviewer.Set {
		if !in.Reviewer.Null {
			if in.Reviewer.V == "" {
				return p, nil, domain.Validation("patch.reviewer is empty", map[string]any{"field": "patch.reviewer"})
			}
			if err := maxChars("patch.reviewer", in.Reviewer.V, maxReviewerChars); err != nil {
				return p, nil, err
			}
		}
		p.Reviewer = &store.Nullable[string]{Value: in.Reviewer.Ptr()}
		fields = append(fields, "reviewer")
	}
	return p, fields, nil
}

// Move is move_task (conductor tasks.ts:345-470) over domain.DecideMove: the fixed table
// (INVALID_TRANSITION with allowed and path), a same-status move a reorder, an order key alone a
// reorder within the task's column (P-437: no project lock, as it neither enters IN_PROGRESS nor
// lands DONE; the tier and lease checks are a move's), a promotion of a
// still-blocked task landing on BLOCKED, the WIP limit counted by status inside the transaction
// (WIP_EXCEEDED). A racing task, or a live lease that does not yield to the caller (leaseYields:
// another identity's, or another agent's of the caller's identity, P-399), is CONFLICT_LOCKED. Leaving
// IN_PROGRESS drops the lease; entering it clears the retry backoff. Leaving a poisoned BLOCKED is the
// un-poison (plan §Fold 1): claimAttempts to 0, the poisoned label and the reason cleared, through
// SetStatus(Unpoison) — also when accrued blockers redirect it back to BLOCKED (P-42). Its master
// gate is move_task's own tier (grants.go), checked by authorize. Event task.moved {from, to,
// columnId}; landing DONE unblocks the dependents (unblockDependentsOf). Doorbells per doorbellsFor.
func (s tasksSvc) Move(ctx context.Context, caller domain.Caller, req MoveTaskReq) (MoveTaskResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "move_task", domain.GrantSubject{ToStatus: req.ToStatus, ToColumnID: req.ToColumnID}, true)
	if err != nil {
		return MoveTaskResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return MoveTaskResp{}, err
	}
	if req.Version < 0 {
		return MoveTaskResp{}, domain.Validation("version must be a nonnegative integer", map[string]any{"field": "version"})
	}
	if req.ToStatus != "" {
		if _, err := domain.ParseStatus(string(req.ToStatus)); err != nil {
			return MoveTaskResp{}, err
		}
	}
	if req.Order != "" && !domain.IsValidOrder(req.Order) {
		return MoveTaskResp{}, domain.Validation("Invalid order key: "+req.Order, map[string]any{"field": "order"})
	}
	version := req.Version
	// The project lock, first in the transaction (before mutate's FOR UPDATE read of the task, so the
	// order is project then tasks, as add/remove_dependency take it): a move that may enter
	// IN_PROGRESS counts the lane under it, so two moves cannot both take the last WIP slot (P-146);
	// one that may land on DONE writes its dependents under it, so it cannot deadlock against
	// lockPair's id order. A toColumnId-only move resolves its status inside, so it locks too.
	lock := req.ToColumnID != "" || req.ToStatus == domain.InProgress || req.ToStatus == domain.Done
	return mutate(ctx, s.c, caller, mutation{
		Tool: "move_task", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
		ProjectID: req.ProjectID, TaskID: req.TaskID, Version: &version, Access: a, LockProject: lock,
	}, func(t *txn) (MoveTaskResp, error) {
		cur := *t.Task
		if cur.Racing {
			return MoveTaskResp{}, domain.NewError(domain.CodeConflictLocked, "Task "+cur.ID+" is racing; resolve via pick_run/cancel_runs",
				map[string]any{"taskId": cur.ID, "reason": "race"})
		}
		if !leaseYields(cur, caller, a.Role, t.Now.UnixMilli()) {
			return MoveTaskResp{}, domain.ConflictLocked(cur.ID, cur.Claim.Owner)
		}
		board, _, err := t.Tx.GetBoard(ctx, req.ProjectID, cur.BoardID)
		if err != nil {
			return MoveTaskResp{}, err
		}
		db := domainBoard(board)
		mr := domain.MoveRequest{
			Board: db, FromStatus: cur.Status, FromColumnID: cur.ColumnID, OpenBlockers: len(cur.BlockedBy),
			ToColumnID: req.ToColumnID, ToStatus: req.ToStatus, Order: req.Order,
		}
		if target, err := domain.ResolveColumnAndStatus(db, req.ToColumnID, req.ToStatus, cur.ColumnID); err == nil && target.Status == domain.InProgress && cur.Status != domain.InProgress {
			if !lock {
				return MoveTaskResp{}, internalf("move_task: counting the WIP lane without the project lock")
			}
			ip := domain.InProgress
			n, err := t.Tx.CountTasks(ctx, req.ProjectID, store.TaskFilter{BoardID: cur.BoardID, Status: &ip})
			if err != nil {
				return MoveTaskResp{}, err
			}
			mr.InProgressCount = int(n)
		}
		d, err := domain.DecideMove(mr)
		if err != nil {
			return MoveTaskResp{}, err
		}
		order := d.Order
		if order == "" && d.NeedsEndOfColumnOrder {
			if order, err = endOfColumn(ctx, t.Tx, req.ProjectID, cur.BoardID, d.ColumnID); err != nil {
				return MoveTaskResp{}, err
			}
		}
		change := store.StatusChange{Status: d.Status, ColumnID: d.ColumnID, UpdatedBy: caller.Identity, UpdatedAt: t.Now.UnixMilli()}
		if order != "" {
			change.OrderKey = &order
		}
		poisoned := domain.IsPoisoned(cur.Status, reasonOf(cur))
		switch {
		case poisoned && (d.Status != domain.Blocked || d.RedirectedToBlocked):
			change.Unpoison = true
		case d.Status == domain.Blocked && cur.Status == domain.Blocked:
			change.BlockedReason = reasonOf(cur) // a reorder within the lane keeps why it is blocked
		}
		change.ClearClaim = cur.Status == domain.InProgress && d.Status != domain.InProgress && cur.Claim != nil
		if d.Status == domain.InProgress && cur.Status != domain.InProgress {
			change.NextEligibleAt = &store.Nullable[int64]{}
		}
		after, err := t.Tx.SetStatus(ctx, req.ProjectID, cur.ID, cur.Version, change)
		if err != nil {
			return MoveTaskResp{}, err
		}
		t.Wrote(after)
		data, err := json.Marshal(map[string]any{"from": cur.Status, "to": d.Status, "columnId": d.ColumnID})
		if err != nil {
			return MoveTaskResp{}, err
		}
		t.Event(store.EventInput{Type: domain.EventTaskMoved, Data: data})
		t.RingFor(&cur, after, false)
		if after.Status == domain.Done && cur.Status != domain.Done {
			// Its own row first, as complete_task: a dependent's doorbell names only its own board (P-187).
			t.ringChanged(after)
			if err := t.unblockDependentsOf(cur.ID, nil); err != nil {
				return MoveTaskResp{}, err
			}
		}
		sum, err := t.Tx.GetSummary(ctx, req.ProjectID, cur.ID)
		if err != nil {
			return MoveTaskResp{}, err
		}
		out, err := taskOut(after, sum.Body)
		if err != nil {
			return MoveTaskResp{}, err
		}
		// TODO(spec): conductor's self-approval policy on REVIEW → DONE (tasks.ts:432-452, its warnings
		// and task.self_approved) has no domain port yet.
		return MoveTaskResp{Task: out}, nil
	})
}

// domainBoard is the part of a store board the move decision reads.
func domainBoard(b store.Board) domain.Board {
	out := domain.Board{ID: b.ID, ProjectID: b.ProjectID, Name: b.Name, Columns: make([]domain.Column, 0, len(b.Columns))}
	for _, c := range b.Columns {
		col := domain.Column{ID: c.ID, Name: c.Name, Order: int(c.Order), Status: c.Status}
		if c.WIPLimit != nil {
			n := int(*c.WIPLimit)
			col.WIPLimit = &n
		}
		out.Columns = append(out.Columns, col)
	}
	return out
}

// unblockDependentsOf clears clearedID (a blocker that reached DONE, or is being deleted) from each of
// its dependents through unblockDependent, the auto-unblock entry: every task whose blockedBy names it,
// re-read inside the transaction. skip leaves out the dependents the caller is deleting too (nil: none).
func (t *txn) unblockDependentsOf(clearedID string, skip map[string]bool) error {
	edges, err := t.Tx.ListDependents(t.ctx, t.ProjectID, clearedID)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, e := range edges {
		if e.Type != store.DependencyBlocks || seen[e.ToTaskID] || skip[e.ToTaskID] {
			continue
		}
		seen[e.ToTaskID] = true
		if err := t.unblockOne(e.ToTaskID, clearedID); err != nil {
			return err
		}
	}
	return nil
}

// unblockOne reads depID and, when its blockedBy names clearedID, clears it (unblockDependent).
func (t *txn) unblockOne(depID, clearedID string) error {
	dep, err := t.Tx.GetTask(t.ctx, t.ProjectID, depID)
	if err != nil {
		return err
	}
	d := domain.DecideUnblock(dep.BlockedBy, dep.Status, reasonOf(dep), clearedID)
	if !d.Cleared {
		return nil
	}
	_, err = t.unblockDependent(dep, clearedID, d)
	return err
}

// List is list_tasks: one statement, the board's page under status, assignee and label (the store's
// filter), then conductor's taskPostFilter over that page (store.ts:218-238) for unblockedOnly,
// missingArtifacts (one more statement, store.Artifacts.LiveArtifactTasks), sprintId and
// updatedSince, as FirestoreStore re-checks an already-narrowed page:
// a post-filtered page can be short and still carry nextCursor. contextSummary is "" on each (no batch
// summary read, P-118).
// ponytail: post-filter over a page, not a store-side filter; widening store.TaskFilter makes pages full.
func (s tasksSvc) List(ctx context.Context, caller domain.Caller, req ListTasksReq) (ListTasksResp, error) {
	if _, err := s.c.authorize(ctx, caller, req.ProjectID, "list_tasks", domain.GrantSubject{}, false); err != nil {
		return ListTasksResp{}, err
	}
	if err := required("boardId", req.BoardID); err != nil {
		return ListTasksResp{}, err
	}
	f := store.TaskFilter{BoardID: req.BoardID, Label: req.Label}
	if req.Status != "" {
		st, err := domain.ParseStatus(string(req.Status))
		if err != nil {
			return ListTasksResp{}, err
		}
		f.Status = &st
	}
	if req.Assignee.Set {
		if req.Assignee.Null {
			f.Unassigned = true
		} else {
			f.Assignee = &req.Assignee.V
		}
	}
	if req.Limit < 0 || req.Limit > listMaxLimit {
		return ListTasksResp{}, domain.Validation("limit is 1 to 200", map[string]any{"field": "limit"})
	}
	if req.Label != "" && req.SprintID != "" {
		return ListTasksResp{}, domain.Validation("label and sprintId cannot be combined in one task query", nil)
	}
	var since *int64
	if req.UpdatedSince != "" {
		ms, err := isoIn("updatedSince", req.UpdatedSince)
		if err != nil {
			return ListTasksResp{}, err
		}
		since = &ms
	}
	page, err := s.c.store.ListTasks(ctx, req.ProjectID, f, req.Cursor, int(req.Limit))
	if err != nil {
		return ListTasksResp{}, s.c.mapErr("list_tasks", err)
	}
	// missingArtifacts: the page's tasks with a live artifact, in one statement, only when asked
	// (conductor's !hasArtifact: a task whose every entry is a tombstone is missing them).
	var evidenced map[string]bool
	if req.MissingArtifacts {
		ids := make([]string, 0, len(page.Tasks))
		for _, task := range page.Tasks {
			ids = append(ids, task.ID)
		}
		if evidenced, err = s.c.store.LiveArtifactTasks(ctx, req.ProjectID, ids); err != nil {
			return ListTasksResp{}, s.c.mapErr("list_tasks", err)
		}
	}
	out := ListTasksResp{Tasks: make([]Task, 0, len(page.Tasks)), NextCursor: page.NextCursor}
	for _, task := range page.Tasks {
		switch {
		case req.UnblockedOnly && len(task.BlockedBy) > 0:
			continue
		case req.SprintID != "" && !slices.Contains(task.SprintIDs, req.SprintID):
			continue
		case since != nil && task.UpdatedAt < *since:
			continue
		case req.MissingArtifacts && evidenced[task.ID]:
			continue
		}
		dto, err := taskOut(task, "")
		if err != nil {
			return ListTasksResp{}, s.c.mapErr("list_tasks", err)
		}
		out.Tasks = append(out.Tasks, dto)
	}
	return out, nil
}
