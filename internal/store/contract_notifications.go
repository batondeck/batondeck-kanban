package store

import (
	"context"
	"encoding/json"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Notification is one row of a recipient's in-app feed (conductor's Notification, T-33). Data is a
// JSON object; TS is Unix milliseconds.
type Notification struct {
	IdentityID string
	ID         string
	ProjectID  string
	BoardID    *string
	TaskID     *string
	Type       domain.NotificationType
	Actor      string
	Data       json.RawMessage
	Read       bool
	TS         int64
}

// AddNotificationInput is a row to append to IdentityID's feed, born unread. Data nil is {}.
type AddNotificationInput struct {
	IdentityID string
	ProjectID  string
	BoardID    *string
	TaskID     *string
	Type       domain.NotificationType
	Actor      string
	Data       json.RawMessage
	TS         int64
}

// NotificationsQuery pages a feed newest first. Limit <= 0 is 50 and above 200 is 200 (conductor's
// listNotifications). Cursor is a page's NextCursor.
type NotificationsQuery struct {
	Limit  int
	Cursor string
}

// NotificationsPage is one page of a feed; NextCursor is "" on the last page.
type NotificationsPage struct {
	Items      []Notification
	NextCursor string
}

// Notifications is the in-app feed area. Keys: a row is (identity_id, id), its id minted here
// ("N-" and random hex); the feed is the recipient's, across projects, so no read takes a project.
// Errors: a missing project is domain.NotFound("Project", id); an empty recipient or actor, a type
// that is not a domain.NotificationType, Data that is not a JSON object, TS <= 0 and a malformed
// cursor are domain.Validation. Order is (ts, id) descending; a cursor is base64url JSON {order, id}
// with order the ts. A project's rows leave with it, by its cascade.
type Notifications interface {
	// AddNotification appends the row, unread.
	AddNotification(ctx context.Context, in AddNotificationInput) (Notification, error)
	// ListNotifications is one page of identityID's feed, newest first.
	ListNotifications(ctx context.Context, identityID string, q NotificationsQuery) (NotificationsPage, error)
	// CountUnreadNotifications is how many of identityID's rows are unread.
	CountUnreadNotifications(ctx context.Context, identityID string) (int64, error)
	// MarkNotificationsRead marks identityID's rows read, every unread one when all, else those of
	// ids, and returns how many it flipped from unread: an id already read, unknown or another
	// recipient's counts 0. Neither ids nor all is 0.
	MarkNotificationsRead(ctx context.Context, identityID string, ids []string, all bool) (int64, error)
}

var (
	_ Notifications = (*SQLite)(nil)
	_ Notifications = (*Postgres)(nil)
)

// CheckNotification is AddNotification's validation, shared with memstore.
func CheckNotification(in AddNotificationInput) error {
	switch in.Type {
	case domain.NotificationMention, domain.NotificationFollowUp, domain.NotificationReviewRequest, domain.NotificationHandoff:
	default:
		return domain.Validation("unknown notification type: "+string(in.Type), map[string]any{"type": in.Type})
	}
	switch {
	case in.IdentityID == "":
		return domain.Validation("a notification needs a recipient", nil)
	case in.Actor == "":
		return domain.Validation("a notification needs an actor", nil)
	case in.TS <= 0:
		return domain.Validation("a notification needs a time", nil)
	}
	if len(in.Data) > 0 {
		var obj map[string]json.RawMessage
		if json.Unmarshal(in.Data, &obj) != nil || obj == nil {
			return domain.Validation("a notification's data is a JSON object", nil)
		}
	}
	return nil
}
