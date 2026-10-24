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

// The notification area of contract_notifications.go, on both engines: each call is one statement.

type notificationQueries interface {
	InsertNotification(ctx context.Context, arg sqlitedb.InsertNotificationParams) (sqlitedb.Notification, error)
	ListNotificationsNewest(ctx context.Context, arg sqlitedb.ListNotificationsNewestParams) ([]sqlitedb.Notification, error)
	CountUnreadNotifications(ctx context.Context, identityID string) (int64, error)
	MarkNotificationsReadByID(ctx context.Context, arg sqlitedb.MarkNotificationsReadByIDParams) (int64, error)
	MarkAllNotificationsRead(ctx context.Context, identityID string) (int64, error)
}

func (s *SQLite) AddNotification(ctx context.Context, in AddNotificationInput) (Notification, error) {
	return addNotification(ctx, s, in)
}

func (s *Postgres) AddNotification(ctx context.Context, in AddNotificationInput) (Notification, error) {
	return addNotification(ctx, s, in)
}

func (s *SQLite) ListNotifications(ctx context.Context, identityID string, q NotificationsQuery) (NotificationsPage, error) {
	return listNotifications(ctx, s, identityID, q)
}

func (s *Postgres) ListNotifications(ctx context.Context, identityID string, q NotificationsQuery) (NotificationsPage, error) {
	return listNotifications(ctx, s, identityID, q)
}

func (s *SQLite) CountUnreadNotifications(ctx context.Context, identityID string) (int64, error) {
	return s.queries(false).CountUnreadNotifications(ctx, identityID)
}

func (s *Postgres) CountUnreadNotifications(ctx context.Context, identityID string) (int64, error) {
	return s.queries(false).CountUnreadNotifications(ctx, identityID)
}

func (s *SQLite) MarkNotificationsRead(ctx context.Context, identityID string, ids []string, all bool) (int64, error) {
	return markNotificationsRead(ctx, s, identityID, ids, all)
}

func (s *Postgres) MarkNotificationsRead(ctx context.Context, identityID string, ids []string, all bool) (int64, error) {
	return markNotificationsRead(ctx, s, identityID, ids, all)
}

func notificationOf(r sqlitedb.Notification) Notification {
	return Notification{IdentityID: r.IdentityID, ID: r.ID, ProjectID: r.ProjectID, BoardID: strPtr(r.BoardID), TaskID: strPtr(r.TaskID),
		Type: domain.NotificationType(r.Type), Actor: r.Actor, Data: json.RawMessage(r.Data), Read: r.Read == 1, TS: r.Ts}
}

func addNotification(ctx context.Context, e engine, in AddNotificationInput) (Notification, error) {
	if err := CheckNotification(in); err != nil {
		return Notification{}, err
	}
	data := "{}"
	if len(in.Data) > 0 {
		data = string(in.Data)
	}
	row, err := e.queries(true).InsertNotification(ctx, sqlitedb.InsertNotificationParams{
		IdentityID: in.IdentityID, ID: newID("N-", 16), BoardID: nullStr(in.BoardID), Type: string(in.Type), TaskID: nullStr(in.TaskID),
		Actor: in.Actor, Data: data, Ts: in.TS, ProjectID: in.ProjectID,
	})
	if err := found(err, "Project", in.ProjectID); err != nil {
		return Notification{}, err
	}
	return notificationOf(row), nil
}

func listNotifications(ctx context.Context, e engine, identityID string, q NotificationsQuery) (NotificationsPage, error) {
	afterTS, afterID, err := tsCursor(q.Cursor)
	if err != nil {
		return NotificationsPage{}, err
	}
	limit := clamp(q.Limit, 50, 200)
	rows, err := e.queries(false).ListNotificationsNewest(ctx, sqlitedb.ListNotificationsNewestParams{
		IdentityID: identityID, AfterID: afterID, AfterTs: afterTS, PageLimit: int64(limit + 1)})
	if err != nil {
		return NotificationsPage{}, fmt.Errorf("store: list notifications: %w", err)
	}
	page := NotificationsPage{Items: []Notification{}}
	for _, r := range rows {
		if len(page.Items) == limit {
			last := page.Items[limit-1]
			page.NextCursor = EncodeCursor(strconv.FormatInt(last.TS, 10), last.ID)
			break
		}
		page.Items = append(page.Items, notificationOf(r))
	}
	return page, nil
}

func markNotificationsRead(ctx context.Context, e engine, identityID string, ids []string, all bool) (int64, error) {
	switch {
	case all:
		return e.queries(true).MarkAllNotificationsRead(ctx, identityID)
	case len(ids) == 0:
		return 0, nil
	}
	return e.queries(true).MarkNotificationsReadByID(ctx, sqlitedb.MarkNotificationsReadByIDParams{IdentityID: identityID, Ids: ids})
}

// --- the Postgres queries in sqlitedb's types ---

func (p pgQueries) InsertNotification(ctx context.Context, arg sqlitedb.InsertNotificationParams) (sqlitedb.Notification, error) {
	r, err := p.q.InsertNotification(ctx, pgdb.InsertNotificationParams(arg))
	return sqlitedb.Notification(r), err
}

func (p pgQueries) ListNotificationsNewest(ctx context.Context, arg sqlitedb.ListNotificationsNewestParams) ([]sqlitedb.Notification, error) {
	rows, err := p.q.ListNotificationsNewest(ctx, pgdb.ListNotificationsNewestParams(arg))
	return convert(rows, func(r pgdb.Notification) sqlitedb.Notification { return sqlitedb.Notification(r) }), err
}

func (p pgQueries) CountUnreadNotifications(ctx context.Context, identityID string) (int64, error) {
	return p.q.CountUnreadNotifications(ctx, identityID)
}

func (p pgQueries) MarkNotificationsReadByID(ctx context.Context, arg sqlitedb.MarkNotificationsReadByIDParams) (int64, error) {
	return p.q.MarkNotificationsReadByID(ctx, pgdb.MarkNotificationsReadByIDParams(arg))
}

func (p pgQueries) MarkAllNotificationsRead(ctx context.Context, identityID string) (int64, error) {
	return p.q.MarkAllNotificationsRead(ctx, identityID)
}
