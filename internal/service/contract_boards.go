package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Boards is the boards and columns service.
type Boards interface {
	// Create is create_board.
	// Tier: master (the owner's ruling of 2026-11-10). Event: board.created. Audit: none. Doorbell: none. Replays on idempotencyKey.
	Create(ctx context.Context, caller domain.Caller, req CreateBoardReq) (CreateBoardResp, error)

	// List is list_boards.
	// Tier: read. Writes nothing.
	List(ctx context.Context, caller domain.Caller, req ListBoardsReq) (ListBoardsResp, error)

	// Get is get_board.
	// Tier: read. Writes nothing; served from the board snapshot cache.
	Get(ctx context.Context, caller domain.Caller, req GetBoardReq) (GetBoardResp, error)

	// AddColumn is add_column.
	// Tier: master (the owner's ruling of 2026-11-10). Event: none. Audit: none. Doorbell: none.
	AddColumn(ctx context.Context, caller domain.Caller, req AddColumnReq) (AddColumnResp, error)

	// UpdateColumn is update_column.
	// Tier: structural. Event: none. Audit: none. Doorbell: none.
	UpdateColumn(ctx context.Context, caller domain.Caller, req UpdateColumnReq) (UpdateColumnResp, error)

	// RemoveColumn is remove_column.
	// Tier: structural. Event: none. Audit: none. Doorbell: none.
	RemoveColumn(ctx context.Context, caller domain.Caller, req RemoveColumnReq) (RemoveColumnResp, error)
}

// CreateBoardReq is create_board's input.
type CreateBoardReq struct {
	Name           string       `json:"name"`
	ProjectID      string       `json:"projectId"`
	Columns        []ColumnSpec `json:"columns,omitzero"`
	IdempotencyKey string       `json:"idempotencyKey,omitzero"`
}

// CreateBoardResp is create_board's output.
type CreateBoardResp struct {
	Board Board `json:"board"`
}

// ListBoardsReq is list_boards's input.
type ListBoardsReq struct {
	ProjectID string `json:"projectId"`
}

// ListBoardsResp is list_boards's output.
type ListBoardsResp struct {
	Boards []Board `json:"boards"`
}

// GetBoardReq is get_board's input.
type GetBoardReq struct {
	BoardID   string `json:"boardId"`
	ProjectID string `json:"projectId"`
}

// GetBoardResp is get_board's output.
type GetBoardResp struct {
	Board      Board              `json:"board"`
	TaskCounts map[string]float64 `json:"taskCounts"`
}

// AddColumnReq is add_column's input.
type AddColumnReq struct {
	BoardID        string            `json:"boardId"`
	Name           string            `json:"name"`
	ProjectID      string            `json:"projectId"`
	Status         domain.Status     `json:"status"`
	IdempotencyKey string            `json:"idempotencyKey,omitzero"`
	Order          Optional[float64] `json:"order,omitzero"`
	WIPLimit       *int64            `json:"wipLimit,omitzero"`
}

// AddColumnResp is add_column's output.
type AddColumnResp struct {
	Column Column `json:"column"`
}

// UpdateColumnReq is update_column's input.
type UpdateColumnReq struct {
	BoardID   string            `json:"boardId"`
	ColumnID  string            `json:"columnId"`
	ProjectID string            `json:"projectId"`
	Name      string            `json:"name,omitzero"`
	Order     Optional[float64] `json:"order,omitzero"`
	Status    domain.Status     `json:"status,omitzero"`
	WIPLimit  Nullable[int64]   `json:"wipLimit,omitzero"`
}

// UpdateColumnResp is update_column's output.
type UpdateColumnResp struct {
	Board Board `json:"board"`
}

// RemoveColumnReq is remove_column's input.
type RemoveColumnReq struct {
	BoardID   string `json:"boardId"`
	ColumnID  string `json:"columnId"`
	ProjectID string `json:"projectId"`
}

// RemoveColumnResp is remove_column's output.
type RemoveColumnResp struct {
	Board Board `json:"board"`
}

// ColumnSpec is create_board's input columns[], first seen there. It is an open object: conductor's schema allows properties it does not name.
type ColumnSpec struct {
	Name     string        `json:"name"`
	Status   domain.Status `json:"status"`
	WIPLimit *int64        `json:"wipLimit,omitzero"`
}

// Board is create_board's output board, first seen there.
type Board struct {
	Columns     []Column                   `json:"columns"`
	ID          string                     `json:"id"`
	Name        string                     `json:"name"`
	ProjectID   string                     `json:"projectId"`
	Transitions map[string][]domain.Status `json:"transitions,omitzero"`
}

// Column is create_board's output board.columns[], first seen there.
type Column struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Order    float64       `json:"order"`
	Status   domain.Status `json:"status"`
	WIPLimit *float64      `json:"wipLimit"`
}
