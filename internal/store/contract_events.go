package store

import (
	"context"
	"encoding/json"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Event is one row of task_events: something that happened to a task, or on a board when TaskID
// is nil. Ts is Unix milliseconds; Data is a JSON object.
type Event struct {
	ProjectID string
	ID        string
	BoardID   *string
	TaskID    *string
	Actor     string
	Agent     *string
	Type      domain.EventType
	Data      json.RawMessage
	Ts        int64
}

// EventInput is an event to append. Data nil is {}. Ts is the caller's clock.
type EventInput struct {
	ProjectID string
	BoardID   string
	TaskID    string
	Actor     string
	Agent     *string
	Type      domain.EventType
	Data      json.RawMessage
	Ts        int64
}

// EventPage is one page of events; NextCursor is "" on the last page.
type EventPage struct {
	Events     []Event
	NextCursor string
}

// Events is the event log: append-only, never updated. Keys: an event is (project_id, id), its id
// minted here ("E-" and random hex). Errors: a missing task is domain.NotFound("Task", id) and a
// missing board domain.NotFound("Board", id), a project that does not exist holding neither; an
// unknown type (domain.IsEventType), an empty actor, Ts <= 0, Data that is not a JSON object and a
// malformed cursor are domain.Validation. Reads are newest first, (ts, id) descending, paged by a
// base64url JSON {"order","id"} cursor (EncodeCursor, order the decimal ts) read strictly after; a
// limit <= 0 is 50 and above 200 is 200. Deleting a task deletes its events (the schema cascades),
// so a task.deleted event is appended as a board event.
type Events interface {
	// AppendTaskEvent appends an event of the task (TaskID required), on the task's board: BoardID
	// "" takes the task's, another board is VALIDATION.
	AppendTaskEvent(ctx context.Context, in EventInput) (Event, error)
	// AppendBoardEvent appends an event of the board with no task (TaskID must be "").
	AppendBoardEvent(ctx context.Context, in EventInput) (Event, error)
	// ListTaskEvents is one page of the task's events. A missing task is NOT_FOUND.
	ListTaskEvents(ctx context.Context, projectID, taskID, cursor string, limit int) (EventPage, error)
	// ListBoardFeed is one page of every event on the board, its tasks' and its own, over the feed
	// index (board_id, ts, id). A missing board is NOT_FOUND.
	ListBoardFeed(ctx context.Context, projectID, boardID, cursor string, limit int) (EventPage, error)
}

var (
	_ Events = (*SQLite)(nil)
	_ Events = (*Postgres)(nil)
)
