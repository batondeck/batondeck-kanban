package conformance

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("events", events) }

func probeEvents(s store.Store) error {
	_, err := s.ListBoardFeed(context.Background(), "P-probe", "B-probe", "", 1)
	return err
}

func (f taskBoard) event(taskID string, typ domain.EventType, ts int64) store.EventInput {
	return store.EventInput{ProjectID: f.project, TaskID: taskID, Actor: "alice", Type: typ, Ts: ts}
}

func eventTypes(es []store.Event) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = string(e.Type)
	}
	return out
}

// pageAll reads every page of list, limit at a time.
func pageAll(t *testing.T, limit int, list func(cursor string, limit int) (store.EventPage, error)) [][]string {
	t.Helper()
	var pages [][]string
	cursor := ""
	for {
		page, err := list(cursor, limit)
		mustOK(t, err)
		pages = append(pages, eventTypes(page.Events))
		if page.NextCursor == "" {
			return pages
		}
		cursor = page.NextCursor
	}
}

// events is the event log: task and board events appended and read back newest first, by task and
// as the board's feed, paged by cursor, keyed by project, gone with their task; each with its
// refusals.
func events(t *testing.T, newStore Factory) {
	ctx := context.Background()
	requireBuilt(t, newStore, probeTasks, probeEvents)

	t.Run("AppendTaskEventTakesTheTasksBoardAndReadsBack", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		in := f.event(task.ID, domain.EventTaskCreated, taskClock)
		in.Agent = ptr("agent-1")
		in.Data = json.RawMessage(`{"title":"t"}`)
		e, err := f.s.AppendTaskEvent(ctx, in)
		mustOK(t, err)
		if !strings.HasPrefix(e.ID, "E-") {
			t.Fatalf("event id %q, want an E- id", e.ID)
		}
		want := store.Event{
			ProjectID: f.project, ID: e.ID, BoardID: ptr(f.board), TaskID: ptr(task.ID), Actor: "alice",
			Agent: ptr("agent-1"), Type: domain.EventTaskCreated, Data: json.RawMessage(`{"title":"t"}`), Ts: taskClock,
		}
		if !reflect.DeepEqual(e, want) {
			t.Fatalf("appended %+v, want %+v", e, want)
		}
		page, err := f.s.ListTaskEvents(ctx, f.project, task.ID, "", 10)
		mustOK(t, err)
		if len(page.Events) != 1 || !reflect.DeepEqual(page.Events[0], want) || page.NextCursor != "" {
			t.Fatalf("read back %+v", page)
		}
		// Data nil is {}.
		bare, err := f.s.AppendTaskEvent(ctx, f.event(task.ID, domain.EventTaskUpdated, taskClock+1))
		mustOK(t, err)
		if string(bare.Data) != `{}` || bare.Agent != nil {
			t.Fatalf("defaults: %+v", bare)
		}
	})

	t.Run("ListTaskEventsIsNewestFirstAndPages", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		other := f.add(t, "other", nil)
		for i, typ := range []domain.EventType{domain.EventTaskCreated, domain.EventTaskClaimed, domain.EventTaskBlocked, domain.EventTaskMoved, domain.EventTaskCompleted} {
			_, err := f.s.AppendTaskEvent(ctx, f.event(task.ID, typ, taskClock+int64(i)))
			mustOK(t, err)
		}
		_, err := f.s.AppendTaskEvent(ctx, f.event(other.ID, domain.EventTaskCreated, taskClock+10))
		mustOK(t, err)
		pages := pageAll(t, 2, func(cursor string, limit int) (store.EventPage, error) {
			return f.s.ListTaskEvents(ctx, f.project, task.ID, cursor, limit)
		})
		want := [][]string{{"task.completed", "task.moved"}, {"task.blocked", "task.claimed"}, {"task.created"}}
		if !reflect.DeepEqual(pages, want) {
			t.Fatalf("pages %v, want %v", pages, want)
		}
	})

	// One mutation stamps its events with one millisecond: the feeds still read them in the order
	// they were appended (newest first), not in the order of a random id.
	t.Run("EventsOfOneTimestampReadInTheOrderTheyWereAppended", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		const pairs = 40
		for i := range pairs {
			for _, typ := range []domain.EventType{domain.EventTaskCreated, domain.EventTaskClaimed} {
				_, err := f.s.AppendTaskEvent(ctx, f.event(task.ID, typ, taskClock+int64(i)))
				mustOK(t, err)
			}
		}
		var types []string
		for _, page := range pageAll(t, 7, func(cursor string, limit int) (store.EventPage, error) {
			return f.s.ListTaskEvents(ctx, f.project, task.ID, cursor, limit)
		}) {
			types = append(types, page...)
		}
		feed, err := f.s.ListBoardFeed(ctx, f.project, f.board, "", 200)
		mustOK(t, err)
		if len(types) != 2*pairs || !reflect.DeepEqual(eventTypes(feed.Events), types) {
			t.Fatalf("task events %d, feed %v, want %d alike", len(types), eventTypes(feed.Events), 2*pairs)
		}
		flipped := 0
		for i := 0; i < len(types); i += 2 {
			if types[i] != "task.claimed" || types[i+1] != "task.created" {
				flipped++
			}
		}
		if flipped > 0 {
			t.Fatalf("%d of %d same-millisecond pairs read the first-appended event as the newest", flipped, pairs)
		}
	})

	t.Run("AppendTaskEventRefusals", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		second := newBoard(t, f.s, f.project, nil)
		for name, c := range map[string]struct {
			edit   func(*store.EventInput)
			code   domain.Code
			entity string
		}{
			"an unknown type":    {func(in *store.EventInput) { in.Type = "task.dead_lettered" }, domain.CodeValidation, ""},
			"no actor":           {func(in *store.EventInput) { in.Actor = "" }, domain.CodeValidation, ""},
			"no time":            {func(in *store.EventInput) { in.Ts = 0 }, domain.CodeValidation, ""},
			"data not an object": {func(in *store.EventInput) { in.Data = json.RawMessage(`[1]`) }, domain.CodeValidation, ""},
			"data not JSON":      {func(in *store.EventInput) { in.Data = json.RawMessage(`{`) }, domain.CodeValidation, ""},
			"no task":            {func(in *store.EventInput) { in.TaskID = "" }, domain.CodeValidation, ""},
			"another board":      {func(in *store.EventInput) { in.BoardID = second.ID }, domain.CodeValidation, ""},
			"a missing task":     {func(in *store.EventInput) { in.TaskID = "T-9" }, domain.CodeNotFound, "Task"},
			"a missing project":  {func(in *store.EventInput) { in.ProjectID = "P-missing" }, domain.CodeNotFound, "Task"},
		} {
			in := f.event(task.ID, domain.EventTaskUpdated, taskClock)
			c.edit(&in)
			_, err := f.s.AppendTaskEvent(ctx, in)
			if err == nil {
				t.Fatalf("%s: appended", name)
			}
			wantCode(t, err, c.code, c.entity)
		}
		page, err := f.s.ListTaskEvents(ctx, f.project, task.ID, "", 10)
		mustOK(t, err)
		if len(page.Events) != 0 {
			t.Fatalf("a refusal appended %v", eventTypes(page.Events))
		}
		// Positive control: the task's own board named explicitly.
		in := f.event(task.ID, domain.EventTaskUpdated, taskClock)
		in.BoardID = f.board
		_, err = f.s.AppendTaskEvent(ctx, in)
		mustOK(t, err)
	})

	t.Run("AppendBoardEventAndTheFeedHoldsTheBoardsEventsAlone", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		second := newBoard(t, f.s, f.project, nil)
		task := f.add(t, "t", nil)
		elsewhere := f.add(t, "elsewhere", func(in *store.CreateTaskInput) { in.BoardID, in.ColumnID = second.ID, second.Columns[0].ID })
		board := store.EventInput{ProjectID: f.project, BoardID: f.board, Actor: "alice", Type: domain.EventBoardCreated, Ts: taskClock}
		be, err := f.s.AppendBoardEvent(ctx, board)
		mustOK(t, err)
		if be.TaskID != nil || be.BoardID == nil || *be.BoardID != f.board {
			t.Fatalf("board event %+v", be)
		}
		_, err = f.s.AppendTaskEvent(ctx, f.event(task.ID, domain.EventTaskCreated, taskClock+1))
		mustOK(t, err)
		_, err = f.s.AppendTaskEvent(ctx, f.event(task.ID, domain.EventTaskMoved, taskClock+2))
		mustOK(t, err)
		_, err = f.s.AppendTaskEvent(ctx, f.event(elsewhere.ID, domain.EventTaskCreated, taskClock+3))
		mustOK(t, err)
		pages := pageAll(t, 2, func(cursor string, limit int) (store.EventPage, error) {
			return f.s.ListBoardFeed(ctx, f.project, f.board, cursor, limit)
		})
		if want := [][]string{{"task.moved", "task.created"}, {"board.created"}}; !reflect.DeepEqual(pages, want) {
			t.Fatalf("feed %v, want %v", pages, want)
		}
		all, err := f.s.ListBoardFeed(ctx, f.project, second.ID, "", 0)
		mustOK(t, err)
		if got := eventTypes(all.Events); !reflect.DeepEqual(got, []string{"task.created"}) {
			t.Fatalf("the second board's feed %v", got)
		}
	})

	t.Run("AppendBoardEventRefusals", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		ok := store.EventInput{ProjectID: f.project, BoardID: f.board, Actor: "alice", Type: domain.EventBoardCreated, Ts: taskClock}
		for name, c := range map[string]struct {
			edit   func(*store.EventInput)
			code   domain.Code
			entity string
		}{
			"a task":          {func(in *store.EventInput) { in.TaskID = task.ID }, domain.CodeValidation, ""},
			"no board":        {func(in *store.EventInput) { in.BoardID = "" }, domain.CodeValidation, ""},
			"an unknown type": {func(in *store.EventInput) { in.Type = "board.renamed" }, domain.CodeValidation, ""},
			"no actor":        {func(in *store.EventInput) { in.Actor = "" }, domain.CodeValidation, ""},
			"no time":         {func(in *store.EventInput) { in.Ts = -1 }, domain.CodeValidation, ""},
			"a missing board": {func(in *store.EventInput) { in.BoardID = "B-missing" }, domain.CodeNotFound, "Board"},
			"another project": {func(in *store.EventInput) { in.ProjectID = "P-missing" }, domain.CodeNotFound, "Board"},
		} {
			in := ok
			c.edit(&in)
			_, err := f.s.AppendBoardEvent(ctx, in)
			if err == nil {
				t.Fatalf("%s: appended", name)
			}
			wantCode(t, err, c.code, c.entity)
		}
		_, err := f.s.AppendBoardEvent(ctx, ok)
		mustOK(t, err)
	})

	t.Run("ListRefusals", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		_, err := f.s.ListTaskEvents(ctx, f.project, "T-9", "", 10)
		wantCode(t, err, domain.CodeNotFound, "Task")
		_, err = f.s.ListBoardFeed(ctx, f.project, "B-missing", "", 10)
		wantCode(t, err, domain.CodeNotFound, "Board")
		for _, bad := range []string{"%%%", "bnVsbA", store.EncodeCursor("not-a-number", "E-1"), store.EncodeCursor("5", "E-\x001")} {
			_, err = f.s.ListTaskEvents(ctx, f.project, task.ID, bad, 10)
			wantCode(t, err, domain.CodeValidation, "")
			_, err = f.s.ListBoardFeed(ctx, f.project, f.board, bad, 10)
			wantCode(t, err, domain.CodeValidation, "")
		}
		// Positive control: no events is an empty page.
		page, err := f.s.ListBoardFeed(ctx, f.project, f.board, "", 10)
		mustOK(t, err)
		if len(page.Events) != 0 || page.NextCursor != "" {
			t.Fatalf("an empty feed: %+v", page)
		}
	})

	t.Run("EventsAreKeyedByProject", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		g := taskBoardOn(t, f.s)
		mine := f.add(t, "mine", nil)
		theirs := g.add(t, "theirs", nil)
		if mine.ID != theirs.ID {
			t.Fatalf("both projects' first tasks should be T-1: %s %s", mine.ID, theirs.ID)
		}
		_, err := f.s.AppendTaskEvent(ctx, f.event(mine.ID, domain.EventTaskCreated, taskClock))
		mustOK(t, err)
		page, err := f.s.ListTaskEvents(ctx, g.project, theirs.ID, "", 10)
		mustOK(t, err)
		if len(page.Events) != 0 {
			t.Fatalf("project g's T-1 sees %v", eventTypes(page.Events))
		}
	})

	t.Run("DeletingATaskDeletesItsEventsAndKeepsTheBoards", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		_, err := f.s.AppendTaskEvent(ctx, f.event(task.ID, domain.EventTaskCreated, taskClock))
		mustOK(t, err)
		mustOK(t, f.s.DeleteTask(ctx, f.project, task.ID, 1))
		_, err = f.s.AppendBoardEvent(ctx, store.EventInput{ProjectID: f.project, BoardID: f.board, Actor: "alice", Type: domain.EventTaskDeleted, Data: json.RawMessage(`{"taskId":"T-1"}`), Ts: taskClock + 1})
		mustOK(t, err)
		feed, err := f.s.ListBoardFeed(ctx, f.project, f.board, "", 10)
		mustOK(t, err)
		if got := eventTypes(feed.Events); !reflect.DeepEqual(got, []string{"task.deleted"}) {
			t.Fatalf("feed after the delete %v", got)
		}
	})
}
