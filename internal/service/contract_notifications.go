package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Notifications is the caller's in-app feed (conductor's notifications.ts, T-33): cross-project and
// caller-scoped, so neither tool takes a projectId or checks a membership. The rows are written by
// mutate from the events that materialise them (domain.NotificationsForEvent); delivery is in-app
// and the /api/stream notifications frame only (plan §Target surface: SendGrid and Slack dropped).
type Notifications interface {
	// List is list_notifications.
	// Tier: none (caller-scoped). Writes nothing. Newest first, with the caller's unread count.
	List(ctx context.Context, caller domain.Caller, req ListNotificationsReq) (ListNotificationsResp, error)

	// MarkRead is mark_notifications_read.
	// Tier: none (caller-scoped). Marks the caller's own rows read (ids, or all) and answers how many
	// it flipped from unread. No event, no audit, no change row: a read mark is the reader's own.
	MarkRead(ctx context.Context, caller domain.Caller, req MarkNotificationsReadReq) (MarkNotificationsReadResp, error)
}

// ListNotificationsReq is list_notifications's input.
type ListNotificationsReq struct {
	Cursor string `json:"cursor,omitzero"`
	Limit  int64  `json:"limit,omitzero"`
}

// ListNotificationsResp is list_notifications's output.
type ListNotificationsResp struct {
	NextCursor    string         `json:"nextCursor,omitzero"`
	Notifications []Notification `json:"notifications"`
	Unread        int64          `json:"unread"`
}

// Notification is list_notifications's output notifications[].
type Notification struct {
	Actor      string                  `json:"actor"`
	BoardID    *string                 `json:"boardId"`
	Data       any                     `json:"data"`
	ID         string                  `json:"id"`
	IdentityID string                  `json:"identityId"`
	ProjectID  string                  `json:"projectId"`
	Read       bool                    `json:"read"`
	TaskID     *string                 `json:"taskId"`
	TS         string                  `json:"ts"`
	Type       domain.NotificationType `json:"type"`
}

// MarkNotificationsReadReq is mark_notifications_read's input.
type MarkNotificationsReadReq struct {
	All bool     `json:"all,omitzero"`
	IDs []string `json:"ids,omitzero"`
}

// MarkNotificationsReadResp is mark_notifications_read's output.
type MarkNotificationsReadResp struct {
	Updated int64 `json:"updated"`
}
