package store

import "context"

// ContextKind is a context item's kind.
type ContextKind string

const (
	ContextNote     ContextKind = "note"
	ContextDecision ContextKind = "decision"
	ContextField    ContextKind = "field"
	ContextLink     ContextKind = "link"
)

// ContextItem is one note, decision, field or link on a task. TS is Unix milliseconds.
type ContextItem struct {
	ProjectID string
	TaskID    string
	ID        string
	Kind      ContextKind
	Body      string
	Author    string
	TS        int64
}

// AddContextItemInput is a context item to append to a task.
type AddContextItemInput struct {
	ProjectID string
	TaskID    string
	Kind      ContextKind
	Body      string
	Author    string
}

// ContextItemsQuery pages a task's context items. Limit <= 0 is 50 and a limit above 200 is 200.
// Ascending reads oldest first; the default is newest first. Cursor is a page's NextCursor.
type ContextItemsQuery struct {
	Limit     int
	Cursor    string
	Ascending bool
}

// ContextItemsPage is one page of context items; NextCursor is "" on the last page.
type ContextItemsPage struct {
	Items      []ContextItem
	NextCursor string
}

// Context is the context-item area. Keys: an item is (project_id, task_id, id); a task is
// (project_id, task_id), never a task id alone (conductor rule 6, T-111). Errors: a missing task is
// domain.NotFound("Task", taskID); an unknown kind, an empty body or author and a malformed cursor
// are domain.Validation. The area is versionless: items are appended, never edited. Idempotent
// replay of an add is the caller's (Idempotency.WithIdempotency), not this area's. Order is (ts,
// id), newest first unless Ascending; a cursor is base64url JSON {order, id} with order the ts.
type Context interface {
	// AddContextItem appends the item, stamped with a new id and the current time, or one millisecond
	// past the task's newest item when that is not earlier: a task's items are strictly increasing in
	// ts in the order they were added, so the (ts, id) order is the insertion order even within one
	// millisecond, where the random id alone would order them arbitrarily (resume's last checkpoint,
	// P-117). add_context_item and cancel_task hold the task's lock (mutate's LockTask); on-call's
	// annotate is followed by a versioned task write, so a racing add fails it STALE; the incident
	// note writes to a task its own transaction created. No two adds on one task race the read.
	AddContextItem(ctx context.Context, in AddContextItemInput) (ContextItem, error)
	// ListContextItems is one page of the task's items in the query's order.
	ListContextItems(ctx context.Context, projectID, taskID string, q ContextItemsQuery) (ContextItemsPage, error)
	// DeleteContextItemsByTask deletes every item of the task and returns how many it deleted; a
	// task with none, or none at all, is 0 and no error (delete_task's cascade, plan §Fold 2).
	DeleteContextItemsByTask(ctx context.Context, projectID, taskID string) (int64, error)
}

var (
	_ Context = (*SQLite)(nil)
	_ Context = (*Postgres)(nil)
)
