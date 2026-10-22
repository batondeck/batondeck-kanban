package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Feed is the board read side behind the board and board-feed resources and the SPA's stream: tier
// read on the board's project, and it writes nothing.
type Feed interface {
	// BoardFeed is a board's events, oldest first, after cursor (the base64url {order, id} of the last
	// event seen; "" from the start).
	BoardFeed(ctx context.Context, caller domain.Caller, req BoardFeedReq) (BoardFeedResp, error)

	// BoardSnapshot is a board with its per-column task counts, cached by change-log cursor: two reads
	// at one cursor cost one composition.
	BoardSnapshot(ctx context.Context, caller domain.Caller, req BoardSnapshotReq) (BoardSnapshotResp, error)
}

// BoardFeedReq is a page of a board's feed.
type BoardFeedReq struct {
	ProjectID string `json:"projectId"`
	BoardID   string `json:"boardId"`
	Cursor    string `json:"cursor,omitzero"`
	Limit     int64  `json:"limit,omitzero"`
}

// BoardFeedResp is a page of events and the cursor after the last one ("" when there are none).
type BoardFeedResp struct {
	Events     []BoardEvent `json:"events"`
	NextCursor string       `json:"nextCursor"`
}

// BoardEvent is one event of a board's or one of its tasks' log.
type BoardEvent struct {
	ID        string           `json:"id"`
	ProjectID string           `json:"projectId"`
	BoardID   string           `json:"boardId"`
	TaskID    string           `json:"taskId,omitzero"`
	Type      domain.EventType `json:"type"`
	Actor     string           `json:"actor"`
	Agent     string           `json:"agent,omitzero"`
	Data      map[string]any   `json:"data"`
	Ts        string           `json:"ts"`
}

// BoardSnapshotReq names a board.
type BoardSnapshotReq struct {
	ProjectID string `json:"projectId"`
	BoardID   string `json:"boardId"`
}

// BoardSnapshotResp is get_board's answer and the change-log cursor it was composed at.
type BoardSnapshotResp struct {
	Board      Board              `json:"board"`
	TaskCounts map[string]float64 `json:"taskCounts"`
	Cursor     int64              `json:"cursor"`
}
