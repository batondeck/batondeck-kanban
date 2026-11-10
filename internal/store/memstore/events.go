package memstore

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The events area of contract_events.go and the change log of contract_changelog.go.

func cloneEvent(e store.Event) store.Event {
	e.BoardID, e.TaskID, e.Agent = clonePtr(e.BoardID), clonePtr(e.TaskID), clonePtr(e.Agent)
	e.Data = cloneBytes(e.Data)
	return e
}

func cloneChange(c store.Change) store.Change {
	c.ProjectID = clonePtr(c.ProjectID)
	c.Payload = cloneBytes(c.Payload)
	return c
}

// object is a JSON object column value: nil is {}, anything else must be an object.
func object(field string, raw json.RawMessage) (json.RawMessage, error) {
	if raw == nil {
		return json.RawMessage(`{}`), nil
	}
	if !isObject(raw) {
		return nil, domain.Validation(field+" must be a JSON object", map[string]any{"field": field})
	}
	return cloneBytes(raw), nil
}

func validEvent(in store.EventInput) (json.RawMessage, error) {
	if !domain.IsEventType(string(in.Type)) {
		return nil, domain.Validation("unknown event type: "+string(in.Type), map[string]any{"type": in.Type})
	}
	if in.Actor == "" || in.Ts <= 0 {
		return nil, domain.Validation("an event needs an actor and a time", nil)
	}
	return object("data", in.Data)
}

func (st *state) appendEvent(in store.EventInput, boardID string, taskID *string, data json.RawMessage) store.Event {
	e := store.Event{
		ProjectID: in.ProjectID, ID: store.NewEventID(), BoardID: &boardID, TaskID: clonePtr(taskID), Actor: in.Actor,
		Agent: clonePtr(in.Agent), Type: in.Type, Data: data, Ts: in.Ts,
	}
	st.events[[2]string{e.ProjectID, e.ID}] = cloneEvent(e)
	return e
}

func (s *Store) AppendTaskEvent(ctx context.Context, in store.EventInput) (store.Event, error) {
	if in.TaskID == "" {
		return store.Event{}, domain.Validation("a task event needs its task", nil)
	}
	data, err := validEvent(in)
	if err != nil {
		return store.Event{}, err
	}
	return get(ctx, s, func(st *state) (store.Event, error) {
		t, err := st.task(in.ProjectID, in.TaskID)
		if err != nil {
			return store.Event{}, err
		}
		if in.BoardID != "" && in.BoardID != t.BoardID {
			return store.Event{}, domain.Validation("the task is on another board", map[string]any{"boardId": in.BoardID})
		}
		return st.appendEvent(in, t.BoardID, &in.TaskID, data), nil
	})
}

func (s *Store) AppendBoardEvent(ctx context.Context, in store.EventInput) (store.Event, error) {
	if in.TaskID != "" || in.BoardID == "" {
		return store.Event{}, domain.Validation("a board event names its board and no task", nil)
	}
	data, err := validEvent(in)
	if err != nil {
		return store.Event{}, err
	}
	return get(ctx, s, func(st *state) (store.Event, error) {
		if _, err := st.boardOf(in.ProjectID, in.BoardID); err != nil {
			return store.Event{}, err
		}
		return st.appendEvent(in, in.BoardID, nil, data), nil
	})
}

// eventCursor is a feed cursor: base64url {order: the decimal ts, id}.
func eventCursor(cursor string) (ts int64, id string, err error) {
	order, id, err := store.DecodeCursor(cursor)
	if err != nil {
		return 0, "", err
	}
	ts, perr := strconv.ParseInt(order, 10, 64)
	if perr != nil {
		return 0, "", domain.Validation("Invalid cursor", nil)
	}
	return ts, id, nil
}

// newestFirst is (ts, id) descending.
func newestFirst(a, b store.Event) int {
	return cmp.Or(cmp.Compare(b.Ts, a.Ts), strings.Compare(b.ID, a.ID))
}

// eventPage is one page of the events keep admits, newest first, after cursor.
func (st *state) eventPage(projectID, cursor string, limit int, keep func(store.Event) bool) (store.EventPage, error) {
	var afterTs int64
	var afterID string
	if cursor != "" {
		var err error
		if afterTs, afterID, err = eventCursor(cursor); err != nil {
			return store.EventPage{}, err
		}
	}
	var all []store.Event
	for k, e := range st.events {
		if k[0] == projectID && keep(e) {
			all = append(all, e)
		}
	}
	slices.SortFunc(all, newestFirst)
	page := store.EventPage{Events: []store.Event{}}
	for _, e := range all {
		if cursor != "" && newestFirst(e, store.Event{Ts: afterTs, ID: afterID}) <= 0 {
			continue
		}
		if len(page.Events) == limit {
			last := page.Events[limit-1]
			page.NextCursor = store.EncodeCursor(strconv.FormatInt(last.Ts, 10), last.ID)
			break
		}
		page.Events = append(page.Events, cloneEvent(e))
	}
	return page, nil
}

func (s *Store) ListTaskEvents(ctx context.Context, projectID, taskID, cursor string, limit int) (store.EventPage, error) {
	limit = clamp(limit, 50, 200)
	return get(ctx, s, func(st *state) (store.EventPage, error) {
		if _, err := st.task(projectID, taskID); err != nil {
			return store.EventPage{}, err
		}
		return st.eventPage(projectID, cursor, limit, func(e store.Event) bool { return e.TaskID != nil && *e.TaskID == taskID })
	})
}

func (s *Store) ListBoardFeed(ctx context.Context, projectID, boardID, cursor string, limit int) (store.EventPage, error) {
	limit = clamp(limit, 50, 200)
	return get(ctx, s, func(st *state) (store.EventPage, error) {
		if _, err := st.boardOf(projectID, boardID); err != nil {
			return store.EventPage{}, err
		}
		return st.eventPage(projectID, cursor, limit, func(e store.Event) bool { return e.BoardID != nil && *e.BoardID == boardID })
	})
}

func (s *Store) AppendChange(ctx context.Context, in store.ChangeInput) (int64, error) {
	if in.Scope == "" || in.Kind == "" || in.Ts <= 0 {
		return 0, domain.Validation("a change needs a scope, a kind and a time", nil)
	}
	payload, err := object("payload", in.Payload)
	if err != nil {
		return 0, err
	}
	return get(ctx, s, func(st *state) (int64, error) {
		st.lastChg++
		c := store.Change{ID: st.lastChg, Scope: in.Scope, Kind: in.Kind, Payload: payload, Ts: in.Ts}
		if in.ProjectID != "" {
			c.ProjectID = &in.ProjectID
		}
		st.changes = append(st.changes, cloneChange(c))
		return c.ID, nil
	})
}

func (st *state) changesAfter(since int64, limit int, keep func(store.Change) bool) []store.Change {
	out := []store.Change{}
	for _, c := range st.changes {
		if len(out) == limit {
			break
		}
		if c.ID > since && keep(c) {
			out = append(out, cloneChange(c))
		}
	}
	return out
}

func (s *Store) ReadChanges(ctx context.Context, since int64, limit int) ([]store.Change, error) {
	limit = clamp(limit, 100, 1000)
	return get(ctx, s, func(st *state) ([]store.Change, error) {
		return st.changesAfter(since, limit, func(store.Change) bool { return true }), nil
	})
}

func (s *Store) ReadChangesForScope(ctx context.Context, scope string, since int64, limit int) ([]store.Change, error) {
	limit = clamp(limit, 100, 1000)
	return get(ctx, s, func(st *state) ([]store.Change, error) {
		return st.changesAfter(since, limit, func(c store.Change) bool { return c.Scope == scope }), nil
	})
}

func (s *Store) DeleteChangesOlderThan(ctx context.Context, before int64, limit int) (int64, error) {
	if limit <= 0 {
		return 0, domain.Validation("a delete needs a positive limit", map[string]any{"limit": limit})
	}
	return get(ctx, s, func(st *state) (int64, error) {
		var n int64
		kept := st.changes[:0:0]
		for _, c := range st.changes {
			if c.Ts < before && n < int64(limit) {
				n++
				continue
			}
			kept = append(kept, c)
		}
		st.changes = kept
		return n, nil
	})
}
