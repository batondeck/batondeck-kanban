package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Comments is a task's threaded discussion.
type Comments interface {
	// AddComment is add_comment.
	// Tier: write. Resolves the body's @mentions against the project's members. Event:
	// comment.added. Audit: none. Doorbell: none, and it rings no master either (ADR-0004 "Comments
	// ring nobody"). Replays on idempotencyKey.
	AddComment(ctx context.Context, caller domain.Caller, req AddCommentReq) (AddCommentResp, error)

	// ListComments is list_comments.
	// Tier: read. Writes nothing. Oldest first.
	ListComments(ctx context.Context, caller domain.Caller, req ListCommentsReq) (ListCommentsResp, error)
}

// AddCommentReq is add_comment's input.
type AddCommentReq struct {
	Body            string `json:"body"`
	ProjectID       string `json:"projectId"`
	TaskID          string `json:"taskId"`
	IdempotencyKey  string `json:"idempotencyKey,omitzero"`
	ParentCommentID string `json:"parentCommentId,omitzero"`
}

// AddCommentResp is add_comment's output.
type AddCommentResp struct {
	Comment  Comment  `json:"comment"`
	Warnings []string `json:"warnings,omitzero"`
}

// ListCommentsReq is list_comments' input.
type ListCommentsReq struct {
	ProjectID string `json:"projectId"`
	TaskID    string `json:"taskId"`
	Cursor    string `json:"cursor,omitzero"`
	Limit     int64  `json:"limit,omitzero"`
}

// ListCommentsResp is list_comments' output.
type ListCommentsResp struct {
	Comments   []Comment `json:"comments"`
	NextCursor string    `json:"nextCursor,omitzero"`
}

// Comment is one comment on a task.
type Comment struct {
	Author          string   `json:"author"`
	Body            string   `json:"body"`
	ID              string   `json:"id"`
	Mentions        []string `json:"mentions"`
	ParentCommentID *string  `json:"parentCommentId"`
	TaskID          string   `json:"taskId"`
	Ts              string   `json:"ts"`
}
