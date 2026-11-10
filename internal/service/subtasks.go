package service

import (
	"cmp"
	"context"
	"encoding/json"
	"math/rand/v2"
	"slices"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// listByIDsMax is store.Tasks.ListTasksByIDs's most ids per call.
const listByIDsMax = 200

// AddSubtask is add_subtask (conductor context.ts:534-560): a child task with parentTaskId, in the
// parent's board's first column with that column's status. It adds no edge and leaves the parent as
// it is (version included): mutate loads no task, and the only event is the child's task.created.
// The parent's BLOCKED landing is orchestrate_subtasks (R2.3), not this. A grant check pending on a
// task is finished against the parent.
func (s contextSvc) AddSubtask(ctx context.Context, caller domain.Caller, req AddSubtaskReq) (AddSubtaskResp, error) {
	a, err := s.c.authorize(ctx, caller, req.ProjectID, "add_subtask",
		domain.GrantSubject{Priorities: []domain.Priority{cmp.Or(req.Priority, domain.PriorityNormal)}}, true)
	if err != nil {
		return AddSubtaskResp{}, err
	}
	if err := required("parentTaskId", req.ParentTaskID); err != nil {
		return AddSubtaskResp{}, err
	}
	if err := required("title", req.Title); err != nil {
		return AddSubtaskResp{}, err
	}
	if err := maxChars("title", req.Title, maxTitleChars); err != nil {
		return AddSubtaskResp{}, err
	}
	if err := maxChars("description", req.Description, maxDescriptionChars); err != nil {
		return AddSubtaskResp{}, err
	}
	priority := req.Priority
	if priority == "" {
		priority = domain.PriorityNormal
	}
	if !slices.Contains([]domain.Priority{domain.PriorityLow, domain.PriorityNormal, domain.PriorityHigh, domain.PriorityUrgent}, priority) {
		return AddSubtaskResp{}, domain.Validation("unknown priority: "+string(priority), map[string]any{"field": "priority"})
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return AddSubtaskResp{}, err
	}
	pend := a
	a.pending = false
	return mutate(ctx, s.c, caller, mutation{
		Tool: "add_subtask", Args: map[string]any{"projectId": req.ProjectID, "parentTaskId": req.ParentTaskID},
		ProjectID: req.ProjectID, IdempotencyKey: req.IdempotencyKey, Access: a,
	}, func(t *txn) (AddSubtaskResp, error) {
		parent, err := t.Tx.GetTask(ctx, req.ProjectID, req.ParentTaskID)
		if err != nil {
			return AddSubtaskResp{}, err
		}
		if err := pend.permit(&parent); err != nil {
			return AddSubtaskResp{}, err
		}
		board, _, err := t.Tx.GetBoard(ctx, req.ProjectID, parent.BoardID)
		if err != nil {
			return AddSubtaskResp{}, err
		}
		// ADR-0005: a created task lands TODO, in the board's first TODO column.
		i := slices.IndexFunc(board.Columns, func(c store.Column) bool { return c.Status == domain.Todo })
		if i < 0 {
			return AddSubtaskResp{}, domain.Validation("Board has no TODO column", nil)
		}
		col := board.Columns[i] // the store keeps them in (order, id)
		order, err := headOfColumn(ctx, t.Tx, req.ProjectID, board.ID, col.ID)
		if err != nil {
			return AddSubtaskResp{}, err
		}
		var agent *string
		if caller.Agent != "" {
			agent = &caller.Agent
		}
		parentID := parent.ID
		child, err := t.Tx.CreateTask(ctx, store.CreateTaskInput{
			ProjectID: req.ProjectID, BoardID: board.ID, ColumnID: col.ID, Title: req.Title, Description: req.Description,
			Status: col.Status, Priority: priority, ParentTaskID: &parentID, OrderKey: order,
			CreatedBy: caller.Identity, CreatedByAgent: agent, CreatedAt: t.Now.UnixMilli(),
		})
		if err != nil {
			return AddSubtaskResp{}, err
		}
		data, err := json.Marshal(map[string]any{"title": child.Title})
		if err != nil {
			return AddSubtaskResp{}, err
		}
		t.Event(store.EventInput{TaskID: child.ID, BoardID: child.BoardID, Type: domain.EventTaskCreated, Data: data})
		t.Wrote(child)
		t.RingFor(nil, child, false)
		out, err := taskOut(child, "")
		if err != nil {
			return AddSubtaskResp{}, err
		}
		return AddSubtaskResp{Task: out}, nil
	})
}

// headOfColumn is an order key before the column's first task, one bounded read, spread
// (domain.OrderBetweenSpread) so writers that place tasks at the head of one column at once (two
// creates on Postgres read the head before either commits) mint different keys: two of them draw the
// same spread about once in 14 million.
// ponytail: conductor appends at the column's END (maxOrderInColumn); the store has no last-key read
// (P-69) and paging to the end is unbounded, so the child lands at the head until P-69 lands.
func headOfColumn(ctx context.Context, tx store.Store, projectID, boardID, columnID string) (string, error) {
	page, err := tx.ListTasks(ctx, projectID, store.TaskFilter{BoardID: boardID, ColumnID: columnID}, "", 1)
	if err != nil {
		return "", err
	}
	first := ""
	if len(page.Tasks) > 0 {
		first = page.Tasks[0].OrderKey
	}
	key, err := domain.OrderBetweenSpread("", first, rand.Uint32())
	if err != nil {
		return "", internalf("order key before %q: %v", first, err)
	}
	return key, nil
}

// ListSubtasks is list_subtasks: the parent's children in task-number order, read in pages of at
// most 200 ids. contextSummary is "" on each: the store has no batch summary read, and one
// GetSummary per child would make the call's cost grow with the children.
// ponytail: summaries are not read here; a batch summary read in the store fills them.
func (s contextSvc) ListSubtasks(ctx context.Context, caller domain.Caller, req ListSubtasksReq) (ListSubtasksResp, error) {
	if _, err := s.c.authorize(ctx, caller, req.ProjectID, "list_subtasks", domain.GrantSubject{}, true); err != nil {
		return ListSubtasksResp{}, err
	}
	if err := required("parentTaskId", req.ParentTaskID); err != nil {
		return ListSubtasksResp{}, err
	}
	ids, err := s.c.store.ListSubtasks(ctx, req.ProjectID, req.ParentTaskID)
	if err != nil {
		return ListSubtasksResp{}, s.c.mapErr("list_subtasks", err)
	}
	out := ListSubtasksResp{Tasks: make([]Task, 0, len(ids))}
	for chunk := range slices.Chunk(ids, listByIDsMax) {
		tasks, err := s.c.store.ListTasksByIDs(ctx, req.ProjectID, chunk)
		if err != nil {
			return ListSubtasksResp{}, s.c.mapErr("list_subtasks", err)
		}
		for _, t := range tasks {
			dto, err := taskOut(t, "")
			if err != nil {
				return ListSubtasksResp{}, s.c.mapErr("list_subtasks", err)
			}
			out.Tasks = append(out.Tasks, dto)
		}
	}
	return out, nil
}
