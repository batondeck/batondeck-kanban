package store

import "context"

// FollowUpStatus is a follow-up's state.
type FollowUpStatus string

const (
	FollowUpOpen  FollowUpStatus = "open"
	FollowUpAcked FollowUpStatus = "acked"
)

// FollowUp is one directive on a task. Target is its routing handle (an agent id, a label or an
// identity) and TargetIdentity the verified member identity behind it, or nil when none is bound;
// TS is Unix milliseconds.
type FollowUp struct {
	ProjectID      string
	TaskID         string
	ID             string
	Body           string
	Author         string
	Target         *string
	TargetIdentity *string
	Status         FollowUpStatus
	Reopened       bool
	TS             int64
}

// AddFollowUpInput is a follow-up to append to a task, born open.
type AddFollowUpInput struct {
	ProjectID      string
	TaskID         string
	Body           string
	Author         string
	Target         *string
	TargetIdentity *string
	Reopened       bool
}

// FollowUpsQuery pages a task's follow-ups oldest first, the open ones only when OpenOnly. Limit
// <= 0 is 100 and a limit above 200 is 200 (conductor's listFollowUps). Cursor is a page's
// NextCursor.
type FollowUpsQuery struct {
	Limit    int
	Cursor   string
	OpenOnly bool
}

// FollowUpsPage is one page of follow-ups; NextCursor is "" on the last page.
type FollowUpsPage struct {
	Items      []FollowUp
	NextCursor string
}

// FollowUps is the follow-up area. Keys: a follow-up is (project_id, task_id, id); a task is
// (project_id, task_id), never a task id alone. Errors: a missing task is domain.NotFound("Task",
// taskID); a missing follow-up is domain.NotFound("FollowUp", id); an empty body or author and a
// malformed cursor are domain.Validation. Order is (ts, id) ascending; a cursor is base64url JSON
// {order, id} with order the ts. Follow-ups leave with their task, by its cascade.
type FollowUps interface {
	// AddFollowUp appends the follow-up, open, stamped with a new id and the current time.
	AddFollowUp(ctx context.Context, in AddFollowUpInput) (FollowUp, error)
	// GetFollowUp is one follow-up of the task.
	GetFollowUp(ctx context.Context, projectID, taskID, id string) (FollowUp, error)
	// ListFollowUps is one page of the task's follow-ups, oldest first.
	ListFollowUps(ctx context.Context, projectID, taskID string, q FollowUpsQuery) (FollowUpsPage, error)
	// AckFollowUp sets the follow-up acked and returns it; an acked one stays acked.
	AckFollowUp(ctx context.Context, projectID, taskID, id string) (FollowUp, error)
}

var (
	_ FollowUps = (*SQLite)(nil)
	_ FollowUps = (*Postgres)(nil)
)
