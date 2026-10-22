package store

import "context"

// The engines' methods for the four areas of store contract I (contract_{tasks,claims,events,
// changelog}.go), unbuilt: each returns unbuilt's INTERNAL naming R0.9, which writes their
// statements, deletes them from this file as it does, and the file with its last method.

func (s *SQLite) CreateTask(context.Context, CreateTaskInput) (Task, error) {
	return Task{}, unbuilt("CreateTask", "R0.9")
}

func (s *SQLite) GetTask(context.Context, string, string) (Task, error) {
	return Task{}, unbuilt("GetTask", "R0.9")
}

func (s *SQLite) UpdateTask(context.Context, string, string, int64, TaskPatch) (Task, error) {
	return Task{}, unbuilt("UpdateTask", "R0.9")
}

func (s *SQLite) SetStatus(context.Context, string, string, int64, StatusChange) (Task, error) {
	return Task{}, unbuilt("SetStatus", "R0.9")
}

func (s *SQLite) SetOrderKey(context.Context, string, string, int64, string, string, string, int64) (Task, error) {
	return Task{}, unbuilt("SetOrderKey", "R0.9")
}

func (s *SQLite) ListTasks(context.Context, string, TaskFilter, string, int) (TaskPage, error) {
	return TaskPage{}, unbuilt("ListTasks", "R0.9")
}

func (s *SQLite) CountTasks(context.Context, string, TaskFilter) (int64, error) {
	return 0, unbuilt("CountTasks", "R0.9")
}

func (s *SQLite) ListTasksByIDs(context.Context, string, []string) ([]Task, error) {
	return nil, unbuilt("ListTasksByIDs", "R0.9")
}

func (s *SQLite) DeleteTask(context.Context, string, string, int64) error {
	return unbuilt("DeleteTask", "R0.9")
}

func (s *SQLite) ClaimNext(context.Context, FrontierQuery, ClaimInput) (Task, bool, error) {
	return Task{}, false, unbuilt("ClaimNext", "R0.9")
}

func (s *SQLite) ClaimTask(context.Context, string, string, int64, ClaimInput) (Task, error) {
	return Task{}, unbuilt("ClaimTask", "R0.9")
}

func (s *SQLite) Heartbeat(context.Context, string, string, string, string, int64, int64) (Task, error) {
	return Task{}, unbuilt("Heartbeat", "R0.9")
}

func (s *SQLite) ReleaseLease(context.Context, string, string, string, string, int64) (Task, error) {
	return Task{}, unbuilt("ReleaseLease", "R0.9")
}

func (s *SQLite) ListExpiredLeases(context.Context, string, string, int64, int) ([]Task, error) {
	return nil, unbuilt("ListExpiredLeases", "R0.9")
}

func (s *SQLite) PreviewFrontier(context.Context, FrontierQuery, int) ([]Task, error) {
	return nil, unbuilt("PreviewFrontier", "R0.9")
}

func (s *SQLite) AppendTaskEvent(context.Context, EventInput) (Event, error) {
	return Event{}, unbuilt("AppendTaskEvent", "R0.9")
}

func (s *SQLite) AppendBoardEvent(context.Context, EventInput) (Event, error) {
	return Event{}, unbuilt("AppendBoardEvent", "R0.9")
}

func (s *SQLite) ListTaskEvents(context.Context, string, string, string, int) (EventPage, error) {
	return EventPage{}, unbuilt("ListTaskEvents", "R0.9")
}

func (s *SQLite) ListBoardFeed(context.Context, string, string, string, int) (EventPage, error) {
	return EventPage{}, unbuilt("ListBoardFeed", "R0.9")
}

func (s *SQLite) AppendChange(context.Context, ChangeInput) (int64, error) {
	return 0, unbuilt("AppendChange", "R0.9")
}

func (s *SQLite) ReadChanges(context.Context, int64, int) ([]Change, error) {
	return nil, unbuilt("ReadChanges", "R0.9")
}

func (s *SQLite) ReadChangesForScope(context.Context, string, int64, int) ([]Change, error) {
	return nil, unbuilt("ReadChangesForScope", "R0.9")
}

func (s *SQLite) DeleteChangesOlderThan(context.Context, int64, int) (int64, error) {
	return 0, unbuilt("DeleteChangesOlderThan", "R0.9")
}

func (s *Postgres) CreateTask(context.Context, CreateTaskInput) (Task, error) {
	return Task{}, unbuilt("CreateTask", "R0.9")
}

func (s *Postgres) GetTask(context.Context, string, string) (Task, error) {
	return Task{}, unbuilt("GetTask", "R0.9")
}

func (s *Postgres) UpdateTask(context.Context, string, string, int64, TaskPatch) (Task, error) {
	return Task{}, unbuilt("UpdateTask", "R0.9")
}

func (s *Postgres) SetStatus(context.Context, string, string, int64, StatusChange) (Task, error) {
	return Task{}, unbuilt("SetStatus", "R0.9")
}

func (s *Postgres) SetOrderKey(context.Context, string, string, int64, string, string, string, int64) (Task, error) {
	return Task{}, unbuilt("SetOrderKey", "R0.9")
}

func (s *Postgres) ListTasks(context.Context, string, TaskFilter, string, int) (TaskPage, error) {
	return TaskPage{}, unbuilt("ListTasks", "R0.9")
}

func (s *Postgres) CountTasks(context.Context, string, TaskFilter) (int64, error) {
	return 0, unbuilt("CountTasks", "R0.9")
}

func (s *Postgres) ListTasksByIDs(context.Context, string, []string) ([]Task, error) {
	return nil, unbuilt("ListTasksByIDs", "R0.9")
}

func (s *Postgres) DeleteTask(context.Context, string, string, int64) error {
	return unbuilt("DeleteTask", "R0.9")
}

func (s *Postgres) ClaimNext(context.Context, FrontierQuery, ClaimInput) (Task, bool, error) {
	return Task{}, false, unbuilt("ClaimNext", "R0.9")
}

func (s *Postgres) ClaimTask(context.Context, string, string, int64, ClaimInput) (Task, error) {
	return Task{}, unbuilt("ClaimTask", "R0.9")
}

func (s *Postgres) Heartbeat(context.Context, string, string, string, string, int64, int64) (Task, error) {
	return Task{}, unbuilt("Heartbeat", "R0.9")
}

func (s *Postgres) ReleaseLease(context.Context, string, string, string, string, int64) (Task, error) {
	return Task{}, unbuilt("ReleaseLease", "R0.9")
}

func (s *Postgres) ListExpiredLeases(context.Context, string, string, int64, int) ([]Task, error) {
	return nil, unbuilt("ListExpiredLeases", "R0.9")
}

func (s *Postgres) PreviewFrontier(context.Context, FrontierQuery, int) ([]Task, error) {
	return nil, unbuilt("PreviewFrontier", "R0.9")
}

func (s *Postgres) AppendTaskEvent(context.Context, EventInput) (Event, error) {
	return Event{}, unbuilt("AppendTaskEvent", "R0.9")
}

func (s *Postgres) AppendBoardEvent(context.Context, EventInput) (Event, error) {
	return Event{}, unbuilt("AppendBoardEvent", "R0.9")
}

func (s *Postgres) ListTaskEvents(context.Context, string, string, string, int) (EventPage, error) {
	return EventPage{}, unbuilt("ListTaskEvents", "R0.9")
}

func (s *Postgres) ListBoardFeed(context.Context, string, string, string, int) (EventPage, error) {
	return EventPage{}, unbuilt("ListBoardFeed", "R0.9")
}

func (s *Postgres) AppendChange(context.Context, ChangeInput) (int64, error) {
	return 0, unbuilt("AppendChange", "R0.9")
}

func (s *Postgres) ReadChanges(context.Context, int64, int) ([]Change, error) {
	return nil, unbuilt("ReadChanges", "R0.9")
}

func (s *Postgres) ReadChangesForScope(context.Context, string, int64, int) ([]Change, error) {
	return nil, unbuilt("ReadChangesForScope", "R0.9")
}

func (s *Postgres) DeleteChangesOlderThan(context.Context, int64, int) (int64, error) {
	return 0, unbuilt("DeleteChangesOlderThan", "R0.9")
}
