package service

import (
	"context"
	"encoding/json"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// Notifications is the in-app feed service.
func (c *Core) Notifications() Notifications { return notificationsSvc{c} }

type notificationsSvc struct{ c *Core }

// maxMarkIDs is mark_notifications_read's most ids (conductor's z.array(IdArg).max(500)).
const maxMarkIDs = 500

// FeedSnapshotLimit is the page the /api/stream notifications frame carries (conductor's
// notificationsFetch, list_notifications {limit: 50}).
const FeedSnapshotLimit = 50

// List is list_notifications: two statements, the page and the unread count.
func (s notificationsSvc) List(ctx context.Context, caller domain.Caller, req ListNotificationsReq) (ListNotificationsResp, error) {
	if _, err := s.c.authorize(ctx, caller, "", "list_notifications", domain.GrantSubject{}, false); err != nil {
		return ListNotificationsResp{}, err
	}
	if err := listLimit(req.Limit); err != nil {
		return ListNotificationsResp{}, err
	}
	page, err := s.c.store.ListNotifications(ctx, caller.Identity, store.NotificationsQuery{Limit: int(req.Limit), Cursor: req.Cursor})
	if err != nil {
		return ListNotificationsResp{}, s.c.mapErr("list_notifications", err)
	}
	unread, err := s.c.store.CountUnreadNotifications(ctx, caller.Identity)
	if err != nil {
		return ListNotificationsResp{}, s.c.mapErr("list_notifications", err)
	}
	out := ListNotificationsResp{Notifications: make([]Notification, 0, len(page.Items)), Unread: unread, NextCursor: page.NextCursor}
	for _, n := range page.Items {
		var data any = map[string]any{}
		if len(n.Data) > 0 {
			if err := json.Unmarshal(n.Data, &data); err != nil {
				return ListNotificationsResp{}, s.c.mapErr("list_notifications", err)
			}
		}
		out.Notifications = append(out.Notifications, Notification{Actor: n.Actor, BoardID: n.BoardID, Data: data, ID: n.ID,
			IdentityID: n.IdentityID, ProjectID: n.ProjectID, Read: n.Read, TaskID: n.TaskID, TS: isoMs(n.TS), Type: n.Type})
	}
	return out, nil
}

// MarkRead is mark_notifications_read: one statement. Neither ids nor all: true (`{}`, `{all: false}`,
// `{ids: []}`) names nothing to mark, and is VALIDATION rather than updated 0 (P-439).
func (s notificationsSvc) MarkRead(ctx context.Context, caller domain.Caller, req MarkNotificationsReadReq) (MarkNotificationsReadResp, error) {
	if _, err := s.c.authorize(ctx, caller, "", "mark_notifications_read", domain.GrantSubject{}, false); err != nil {
		return MarkNotificationsReadResp{}, err
	}
	if len(req.IDs) == 0 && !req.All {
		return MarkNotificationsReadResp{}, domain.Validation("mark_notifications_read needs ids or all: true", map[string]any{"field": "ids"})
	}
	if len(req.IDs) > maxMarkIDs {
		return MarkNotificationsReadResp{}, domain.Validation("ids has more than 500 entries", map[string]any{"field": "ids", "max": maxMarkIDs})
	}
	for _, id := range req.IDs {
		if err := required("ids", id); err != nil {
			return MarkNotificationsReadResp{}, err
		}
	}
	n, err := s.c.store.MarkNotificationsRead(ctx, caller.Identity, req.IDs, req.All)
	if err != nil {
		return MarkNotificationsReadResp{}, s.c.mapErr("mark_notifications_read", err)
	}
	return MarkNotificationsReadResp{Updated: n}, nil
}
