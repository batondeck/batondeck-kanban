package store

import "context"

// Comment is one comment on a task. Mentions are the member identities its body @mentions; TS is
// Unix milliseconds.
type Comment struct {
	ProjectID       string
	TaskID          string
	ID              string
	Author          string
	Body            string
	ParentCommentID *string
	Mentions        []string
	TS              int64
}

// AddCommentInput is a comment to append to a task.
type AddCommentInput struct {
	ProjectID       string
	TaskID          string
	Author          string
	Body            string
	ParentCommentID *string
	Mentions        []string
}

// CommentsQuery pages a task's comments oldest first. Limit <= 0 is 100 and a limit above 200 is
// 200 (conductor's listComments). Cursor is a page's NextCursor.
type CommentsQuery struct {
	Limit  int
	Cursor string
}

// CommentsPage is one page of comments; NextCursor is "" on the last page.
type CommentsPage struct {
	Items      []Comment
	NextCursor string
}

// Comments is the comment area. Keys: a comment is (project_id, task_id, id); a task is
// (project_id, task_id), never a task id alone. Errors: a missing task is domain.NotFound("Task",
// taskID); an empty body or author and a malformed cursor are domain.Validation. Comments are
// appended, never edited; they leave with their task, by its cascade. Order is (ts, id) ascending; a
// cursor is base64url JSON {order, id} with order the ts.
type Comments interface {
	// AddComment appends the comment, stamped with a new id and the current time.
	AddComment(ctx context.Context, in AddCommentInput) (Comment, error)
	// ListComments is one page of the task's comments, oldest first.
	ListComments(ctx context.Context, projectID, taskID string, q CommentsQuery) (CommentsPage, error)
}

var (
	_ Comments = (*SQLite)(nil)
	_ Comments = (*Postgres)(nil)
)
