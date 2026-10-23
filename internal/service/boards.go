package service

import (
	"context"
	"encoding/json"
	"math"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// boards is service.Boards. Every mutation goes through mutate and rings a ChangeKindChanged row
// naming its board, which is what drops that board's snapshot in every process the bus reaches.
type boards struct{ c *Core }

var _ Boards = boards{}

// Boards is the boards and columns service.
func (c *Core) Boards() Boards { return boards{c} }

// maxIdempotencyKey is conductor's idempotencyKey bound (z.string().max(128)).
const maxIdempotencyKey = 128

func checkIdempotencyKey(key string) error {
	if len(key) > maxIdempotencyKey {
		return domain.Validation("idempotencyKey is at most 128 characters", map[string]any{"field": "idempotencyKey"})
	}
	return nil
}

func required(field, v string) error {
	if v == "" {
		return domain.Validation(field+" is required", map[string]any{"field": field})
	}
	return nil
}

func checkWIPLimit(field string, v *int64) error {
	if v != nil && *v <= 0 {
		return domain.Validation(field+" must be a positive integer", map[string]any{"field": field})
	}
	return nil
}

// columnOrder is a column's order as the store keeps it: an integer. 0 is absent (the DTO's
// omitzero), so it is nil and the store places the column.
func columnOrder(v float64) (*int64, error) {
	if v == 0 {
		return nil, nil
	}
	if v != math.Trunc(v) || math.IsInf(v, 0) || math.Abs(v) > 1<<53 {
		// DEVIATION: conductor's order is any number; the store's column order is an integer.
		return nil, domain.Validation("order must be an integer", map[string]any{"field": "order"})
	}
	n := int64(v)
	return &n, nil
}

func parseStatus(field string, s domain.Status) error {
	if _, err := domain.ParseStatus(string(s)); err != nil {
		e, _ := domain.AsBatonDeckError(err)
		e.Details["field"] = field
		return e
	}
	return nil
}

func (b boards) Create(ctx context.Context, caller domain.Caller, req CreateBoardReq) (CreateBoardResp, error) {
	a, err := b.c.authorize(ctx, caller, req.ProjectID, "create_board", domain.GrantSubject{}, false)
	if err != nil {
		return CreateBoardResp{}, err
	}
	if err := required("name", req.Name); err != nil {
		return CreateBoardResp{}, err
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return CreateBoardResp{}, err
	}
	var cols []store.ColumnInput // nil: the store's default columns
	for _, c := range req.Columns {
		if err := required("columns.name", c.Name); err != nil {
			return CreateBoardResp{}, err
		}
		if err := parseStatus("columns.status", c.Status); err != nil {
			return CreateBoardResp{}, err
		}
		if err := checkWIPLimit("columns.wipLimit", c.WIPLimit); err != nil {
			return CreateBoardResp{}, err
		}
		cols = append(cols, store.ColumnInput{Name: c.Name, Status: c.Status, WIPLimit: c.WIPLimit})
	}
	resp, err := mutate(ctx, b.c, caller, mutation{
		Tool: "create_board", Args: map[string]any{"projectId": req.ProjectID, "name": req.Name},
		ProjectID: req.ProjectID, IdempotencyKey: req.IdempotencyKey, Access: a,
	}, func(t *txn) (CreateBoardResp, error) {
		board, err := t.Tx.CreateBoard(ctx, store.CreateBoardInput{ProjectID: req.ProjectID, Name: req.Name, CreatedBy: caller.Identity, Columns: cols}, store.WriteOptions{Identity: caller.Identity})
		if err != nil {
			return CreateBoardResp{}, err
		}
		data, err := json.Marshal(map[string]any{"name": req.Name})
		if err != nil {
			return CreateBoardResp{}, err
		}
		t.BoardEvent(store.EventInput{BoardID: board.ID, Type: domain.EventBoardCreated, Data: data})
		t.Ring(Doorbell{Kind: ChangeKindChanged, BoardID: board.ID})
		return CreateBoardResp{Board: boardOut(board)}, nil
	})
	if err != nil {
		return CreateBoardResp{}, err
	}
	return resp, nil
}

func (b boards) List(ctx context.Context, caller domain.Caller, req ListBoardsReq) (ListBoardsResp, error) {
	if _, err := b.c.authorize(ctx, caller, req.ProjectID, "list_boards", domain.GrantSubject{}, false); err != nil {
		return ListBoardsResp{}, err
	}
	list, err := b.c.store.ListBoards(ctx, req.ProjectID)
	if err != nil {
		return ListBoardsResp{}, b.c.mapErr("list_boards", err)
	}
	out := ListBoardsResp{Boards: make([]Board, 0, len(list))}
	for _, board := range list {
		out.Boards = append(out.Boards, boardOut(board))
	}
	return out, nil
}

// Get is get_board, served from the board snapshot cache: on a miss one store.GetBoard, the board
// and ONE grouped count of its tasks (two statements, the get_board pin).
func (b boards) Get(ctx context.Context, caller domain.Caller, req GetBoardReq) (GetBoardResp, error) {
	if _, err := b.c.authorize(ctx, caller, req.ProjectID, "get_board", domain.GrantSubject{BoardID: req.BoardID}, false); err != nil {
		return GetBoardResp{}, err
	}
	snap, err := b.c.boardSnapshot(ctx, req.ProjectID, req.BoardID)
	if err != nil {
		return GetBoardResp{}, err
	}
	return GetBoardResp{Board: snap.Board, TaskCounts: snap.TaskCounts}, nil
}

func (b boards) AddColumn(ctx context.Context, caller domain.Caller, req AddColumnReq) (AddColumnResp, error) {
	a, err := b.c.authorize(ctx, caller, req.ProjectID, "add_column", domain.GrantSubject{BoardID: req.BoardID}, false)
	if err != nil {
		return AddColumnResp{}, err
	}
	if err := required("boardId", req.BoardID); err != nil {
		return AddColumnResp{}, err
	}
	if err := required("name", req.Name); err != nil {
		return AddColumnResp{}, err
	}
	if err := parseStatus("status", req.Status); err != nil {
		return AddColumnResp{}, err
	}
	if err := checkWIPLimit("wipLimit", req.WIPLimit); err != nil {
		return AddColumnResp{}, err
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return AddColumnResp{}, err
	}
	order, err := columnOrder(req.Order)
	if err != nil {
		return AddColumnResp{}, err
	}
	resp, err := mutate(ctx, b.c, caller, mutation{
		Tool: "add_column", Args: map[string]any{"projectId": req.ProjectID, "boardId": req.BoardID},
		ProjectID: req.ProjectID, IdempotencyKey: req.IdempotencyKey, Access: a,
	}, func(t *txn) (AddColumnResp, error) {
		col, err := t.Tx.AddColumn(ctx, req.ProjectID, req.BoardID, store.ColumnInput{Name: req.Name, Status: req.Status, WIPLimit: req.WIPLimit, Order: order}, store.WriteOptions{Identity: caller.Identity})
		if err != nil {
			return AddColumnResp{}, err
		}
		t.Ring(Doorbell{Kind: ChangeKindChanged, BoardID: req.BoardID})
		return AddColumnResp{Column: columnOut(col)}, nil
	})
	if err != nil {
		return AddColumnResp{}, err
	}
	b.c.dropSnapshots(req.ProjectID, req.BoardID)
	return resp, nil
}

func (b boards) UpdateColumn(ctx context.Context, caller domain.Caller, req UpdateColumnReq) (UpdateColumnResp, error) {
	a, err := b.c.authorize(ctx, caller, req.ProjectID, "update_column", domain.GrantSubject{BoardID: req.BoardID}, false)
	if err != nil {
		return UpdateColumnResp{}, err
	}
	if err := required("boardId", req.BoardID); err != nil {
		return UpdateColumnResp{}, err
	}
	if err := required("columnId", req.ColumnID); err != nil {
		return UpdateColumnResp{}, err
	}
	var patch store.ColumnPatch
	if req.Name != "" {
		patch.Name = &req.Name
	}
	if req.Status != "" {
		if err := parseStatus("status", req.Status); err != nil {
			return UpdateColumnResp{}, err
		}
		patch.Status = &req.Status
	}
	// nil is "unchanged": the DTO cannot tell a JSON null (clear the limit) from an absent field.
	if req.WIPLimit != nil {
		if err := checkWIPLimit("wipLimit", req.WIPLimit); err != nil {
			return UpdateColumnResp{}, err
		}
		patch.SetWIPLimit, patch.WIPLimit = true, req.WIPLimit
	}
	if patch.Order, err = columnOrder(req.Order); err != nil {
		return UpdateColumnResp{}, err
	}
	resp, err := mutate(ctx, b.c, caller, mutation{
		Tool: "update_column", Args: map[string]any{"projectId": req.ProjectID, "boardId": req.BoardID, "columnId": req.ColumnID},
		ProjectID: req.ProjectID, Access: a,
	}, func(t *txn) (UpdateColumnResp, error) {
		board, err := t.Tx.UpdateColumn(ctx, req.ProjectID, req.BoardID, req.ColumnID, patch)
		if err != nil {
			return UpdateColumnResp{}, err
		}
		t.Ring(Doorbell{Kind: ChangeKindChanged, BoardID: req.BoardID})
		return UpdateColumnResp{Board: boardOut(board)}, nil
	})
	if err != nil {
		return UpdateColumnResp{}, err
	}
	b.c.dropSnapshots(req.ProjectID, req.BoardID)
	return resp, nil
}

// RemoveColumn is remove_column: the store refuses a column holding a task, and the board's last
// column, as VALIDATION.
func (b boards) RemoveColumn(ctx context.Context, caller domain.Caller, req RemoveColumnReq) (RemoveColumnResp, error) {
	a, err := b.c.authorize(ctx, caller, req.ProjectID, "remove_column", domain.GrantSubject{BoardID: req.BoardID}, false)
	if err != nil {
		return RemoveColumnResp{}, err
	}
	if err := required("boardId", req.BoardID); err != nil {
		return RemoveColumnResp{}, err
	}
	if err := required("columnId", req.ColumnID); err != nil {
		return RemoveColumnResp{}, err
	}
	resp, err := mutate(ctx, b.c, caller, mutation{
		Tool: "remove_column", Args: map[string]any{"projectId": req.ProjectID, "boardId": req.BoardID, "columnId": req.ColumnID},
		ProjectID: req.ProjectID, Access: a,
	}, func(t *txn) (RemoveColumnResp, error) {
		board, err := t.Tx.RemoveColumn(ctx, req.ProjectID, req.BoardID, req.ColumnID)
		if err != nil {
			return RemoveColumnResp{}, err
		}
		t.Ring(Doorbell{Kind: ChangeKindChanged, BoardID: req.BoardID})
		return RemoveColumnResp{Board: boardOut(board)}, nil
	})
	if err != nil {
		return RemoveColumnResp{}, err
	}
	b.c.dropSnapshots(req.ProjectID, req.BoardID)
	return resp, nil
}

// fixedTransitions is the transition table every board answers with: the table is fixed (plan
// §Removed "Outputs keep returning the fixed table").
func fixedTransitions() map[string][]domain.Status {
	out := make(map[string][]domain.Status, len(domain.AllStatuses()))
	for _, s := range domain.AllStatuses() {
		out[string(s)] = domain.AllowedNext(s)
	}
	return out
}

func boardOut(b store.Board) Board {
	out := Board{ID: b.ID, Name: b.Name, ProjectID: b.ProjectID, Columns: make([]Column, 0, len(b.Columns)), Transitions: fixedTransitions()}
	for _, c := range b.Columns {
		out.Columns = append(out.Columns, columnOut(c))
	}
	return out
}

func columnOut(c store.Column) Column {
	out := Column{ID: c.ID, Name: c.Name, Order: float64(c.Order), Status: c.Status}
	if c.WIPLimit != nil {
		w := float64(*c.WIPLimit)
		out.WIPLimit = &w
	}
	return out
}
