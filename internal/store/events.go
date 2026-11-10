package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The events area of contract_events.go, on both engines. An append is one INSERT … SELECT from the
// task or board it hangs off, so a missing one is no row; a page is one statement, and an empty
// page asks whether the task or board exists.

type eventQueries interface {
	AppendTaskEvent(ctx context.Context, arg sqlitedb.AppendTaskEventParams) (sqlitedb.TaskEvent, error)
	AppendBoardEvent(ctx context.Context, arg sqlitedb.AppendBoardEventParams) (sqlitedb.TaskEvent, error)
	ListTaskEvents(ctx context.Context, arg sqlitedb.ListTaskEventsParams) ([]sqlitedb.TaskEvent, error)
	ListBoardFeed(ctx context.Context, arg sqlitedb.ListBoardFeedParams) ([]sqlitedb.TaskEvent, error)
}

func (s *SQLite) AppendTaskEvent(ctx context.Context, in EventInput) (Event, error) {
	return appendTaskEvent(ctx, s, in)
}

func (s *Postgres) AppendTaskEvent(ctx context.Context, in EventInput) (Event, error) {
	return appendTaskEvent(ctx, s, in)
}

func (s *SQLite) AppendBoardEvent(ctx context.Context, in EventInput) (Event, error) {
	return appendBoardEvent(ctx, s, in)
}

func (s *Postgres) AppendBoardEvent(ctx context.Context, in EventInput) (Event, error) {
	return appendBoardEvent(ctx, s, in)
}

func (s *SQLite) ListTaskEvents(ctx context.Context, projectID, taskID, cursor string, limit int) (EventPage, error) {
	return listTaskEvents(ctx, s, projectID, taskID, cursor, limit)
}

func (s *Postgres) ListTaskEvents(ctx context.Context, projectID, taskID, cursor string, limit int) (EventPage, error) {
	return listTaskEvents(ctx, s, projectID, taskID, cursor, limit)
}

func (s *SQLite) ListBoardFeed(ctx context.Context, projectID, boardID, cursor string, limit int) (EventPage, error) {
	return listBoardFeed(ctx, s, projectID, boardID, cursor, limit)
}

func (s *Postgres) ListBoardFeed(ctx context.Context, projectID, boardID, cursor string, limit int) (EventPage, error) {
	return listBoardFeed(ctx, s, projectID, boardID, cursor, limit)
}

// object is a JSON object column value: nil is {}, anything else must be an object.
func object(field string, raw json.RawMessage) (json.RawMessage, error) {
	if raw == nil {
		return json.RawMessage(`{}`), nil
	}
	if !isObject(raw) {
		return nil, domain.Validation(field+" must be a JSON object", map[string]any{"field": field})
	}
	return raw, nil
}

func validEvent(in EventInput) (json.RawMessage, error) {
	if !domain.IsEventType(string(in.Type)) {
		return nil, domain.Validation("unknown event type: "+string(in.Type), map[string]any{"type": in.Type})
	}
	if in.Actor == "" || in.Ts <= 0 {
		return nil, domain.Validation("an event needs an actor and a time", nil)
	}
	return object("data", in.Data)
}

func appendTaskEvent(ctx context.Context, e engine, in EventInput) (Event, error) {
	if in.TaskID == "" {
		return Event{}, domain.Validation("a task event needs its task", nil)
	}
	data, err := validEvent(in)
	if err != nil {
		return Event{}, err
	}
	q := e.queries(true)
	row, err := q.AppendTaskEvent(ctx, sqlitedb.AppendTaskEventParams{
		ID: NewEventID(), Actor: in.Actor, Agent: nullStr(in.Agent), Type: string(in.Type), Data: string(data), Ts: in.Ts,
		ProjectID: in.ProjectID, TaskID: in.TaskID, BoardID: in.BoardID,
	})
	switch {
	case isNoRows(err):
		if _, err := q.GetTask(ctx, sqlitedb.GetTaskParams{ProjectID: in.ProjectID, ID: in.TaskID}); err != nil {
			return Event{}, found(err, "Task", in.TaskID)
		}
		return Event{}, domain.Validation("the task is on another board", map[string]any{"boardId": in.BoardID})
	case err != nil:
		return Event{}, fmt.Errorf("store: append event: %w", err)
	}
	return eventFromRow(row), nil
}

func appendBoardEvent(ctx context.Context, e engine, in EventInput) (Event, error) {
	if in.TaskID != "" || in.BoardID == "" {
		return Event{}, domain.Validation("a board event names its board and no task", nil)
	}
	data, err := validEvent(in)
	if err != nil {
		return Event{}, err
	}
	row, err := e.queries(true).AppendBoardEvent(ctx, sqlitedb.AppendBoardEventParams{
		ID: NewEventID(), Actor: in.Actor, Agent: nullStr(in.Agent), Type: string(in.Type), Data: string(data), Ts: in.Ts,
		ProjectID: in.ProjectID, BoardID: in.BoardID,
	})
	if err != nil {
		return Event{}, found(err, "Board", in.BoardID)
	}
	return eventFromRow(row), nil
}

// eventCursor is a feed cursor: base64url {order: the decimal ts, id}.
func eventCursor(cursor string) (ts int64, id string, err error) {
	if cursor == "" {
		return 0, "", nil
	}
	order, id, err := DecodeCursor(cursor)
	if err != nil {
		return 0, "", err
	}
	ts, perr := strconv.ParseInt(order, 10, 64)
	if perr != nil {
		return 0, "", domain.Validation("Invalid cursor", nil)
	}
	return ts, id, nil
}

// eventPage is rows (limit+1 read) as one page: the next cursor only when a row lies beyond it.
func eventPage(rows []sqlitedb.TaskEvent, limit int) EventPage {
	page := EventPage{Events: make([]Event, 0, min(len(rows), limit))}
	for i, r := range rows {
		if i == limit {
			last := page.Events[limit-1]
			page.NextCursor = EncodeCursor(strconv.FormatInt(last.Ts, 10), last.ID)
			break
		}
		page.Events = append(page.Events, eventFromRow(r))
	}
	return page
}

func listTaskEvents(ctx context.Context, e engine, projectID, taskID, cursor string, limit int) (EventPage, error) {
	afterTs, afterID, err := eventCursor(cursor)
	if err != nil {
		return EventPage{}, err
	}
	limit = clamp(limit, 50, 200)
	q := e.queries(false)
	rows, err := q.ListTaskEvents(ctx, sqlitedb.ListTaskEventsParams{
		ProjectID: projectID, TaskID: nullStr(&taskID), AfterID: afterID, AfterTs: afterTs, PageLimit: int64(limit + 1),
	})
	if err != nil {
		return EventPage{}, fmt.Errorf("store: task events: %w", err)
	}
	if len(rows) == 0 {
		if _, err := q.GetTask(ctx, sqlitedb.GetTaskParams{ProjectID: projectID, ID: taskID}); err != nil {
			return EventPage{}, found(err, "Task", taskID)
		}
	}
	return eventPage(rows, limit), nil
}

func listBoardFeed(ctx context.Context, e engine, projectID, boardID, cursor string, limit int) (EventPage, error) {
	afterTs, afterID, err := eventCursor(cursor)
	if err != nil {
		return EventPage{}, err
	}
	limit = clamp(limit, 50, 200)
	q := e.queries(false)
	rows, err := q.ListBoardFeed(ctx, sqlitedb.ListBoardFeedParams{
		BoardID: nullStr(&boardID), ProjectID: projectID, AfterID: afterID, AfterTs: afterTs, PageLimit: int64(limit + 1),
	})
	if err != nil {
		return EventPage{}, fmt.Errorf("store: board feed: %w", err)
	}
	if len(rows) == 0 {
		if _, err := q.GetBoard(ctx, sqlitedb.GetBoardParams{ProjectID: projectID, ID: boardID}); err != nil {
			return EventPage{}, found(err, "Board", boardID)
		}
	}
	return eventPage(rows, limit), nil
}

func eventFromRow(r sqlitedb.TaskEvent) Event {
	return Event{
		ProjectID: r.ProjectID, ID: r.ID, BoardID: strPtr(r.BoardID), TaskID: strPtr(r.TaskID), Actor: r.Actor,
		Agent: strPtr(r.Agent), Type: domain.EventType(r.Type), Data: json.RawMessage(r.Data), Ts: r.Ts,
	}
}

func (p pgQueries) AppendTaskEvent(ctx context.Context, arg sqlitedb.AppendTaskEventParams) (sqlitedb.TaskEvent, error) {
	r, err := p.q.AppendTaskEvent(ctx, pgdb.AppendTaskEventParams(arg))
	return sqlitedb.TaskEvent(r), err
}

func (p pgQueries) AppendBoardEvent(ctx context.Context, arg sqlitedb.AppendBoardEventParams) (sqlitedb.TaskEvent, error) {
	r, err := p.q.AppendBoardEvent(ctx, pgdb.AppendBoardEventParams(arg))
	return sqlitedb.TaskEvent(r), err
}

func (p pgQueries) ListTaskEvents(ctx context.Context, arg sqlitedb.ListTaskEventsParams) ([]sqlitedb.TaskEvent, error) {
	rows, err := p.q.ListTaskEvents(ctx, pgdb.ListTaskEventsParams(arg))
	return convert(rows, func(r pgdb.TaskEvent) sqlitedb.TaskEvent { return sqlitedb.TaskEvent(r) }), err
}

func (p pgQueries) ListBoardFeed(ctx context.Context, arg sqlitedb.ListBoardFeedParams) ([]sqlitedb.TaskEvent, error) {
	rows, err := p.q.ListBoardFeed(ctx, pgdb.ListBoardFeedParams(arg))
	return convert(rows, func(r pgdb.TaskEvent) sqlitedb.TaskEvent { return sqlitedb.TaskEvent(r) }), err
}
