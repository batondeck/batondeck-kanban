package domain

import "encoding/json"

// NotificationType is an in-app notification's type: the three conductor materialises (notify.ts).
type NotificationType string

const (
	NotificationMention       NotificationType = "mention"
	NotificationFollowUp      NotificationType = "follow_up"
	NotificationReviewRequest NotificationType = "review_request"
)

// MaxNotificationsPerEvent caps the rows one event writes (conductor's MAX_NOTIFICATIONS_PER_EVENT):
// mentions past it notify nobody.
const MaxNotificationsPerEvent = 50

// NotificationDraft is one row an event materialises: its recipient, type and data.
type NotificationDraft struct {
	Recipient string
	Type      NotificationType
	Data      map[string]any
}

// NotificationsForEvent is conductor's notificationsForEvent: the in-app rows an event of type typ
// written by actor materialises, from its own data and nothing else. comment.added notifies each
// resolved mention; task.follow_up and task.review_requested notify their targetIdentity. Every
// recipient field read here was resolved against the project's members before the event existed
// (the mentions by ResolveMentions, the targets by the follow-up and review paths), so no
// caller-asserted string reaches a feed. Nobody is notified of their own act.
func NotificationsForEvent(typ EventType, actor string, data []byte) []NotificationDraft {
	var d struct {
		Mentions       []any `json:"mentions"`
		CommentID      any   `json:"commentId"`
		FollowUpID     any   `json:"followUpId"`
		TargetIdentity any   `json:"targetIdentity"`
	}
	if len(data) > 0 {
		_ = json.Unmarshal(data, &d)
	}
	str := func(v any) any {
		if s, ok := v.(string); ok {
			return s
		}
		return nil
	}
	switch typ {
	case EventCommentAdded:
		var out []NotificationDraft
		seen := map[string]bool{}
		for _, m := range d.Mentions {
			id, ok := m.(string)
			if !ok || id == "" || id == actor || seen[id] {
				continue
			}
			seen[id] = true
			if len(out) == MaxNotificationsPerEvent {
				break
			}
			out = append(out, NotificationDraft{Recipient: id, Type: NotificationMention, Data: map[string]any{"commentId": str(d.CommentID)}})
		}
		return out
	case EventTaskReviewRequested, EventTaskFollowUp:
		target, ok := d.TargetIdentity.(string)
		if !ok || target == "" || target == actor {
			return nil
		}
		if typ == EventTaskReviewRequested {
			return []NotificationDraft{{Recipient: target, Type: NotificationReviewRequest, Data: map[string]any{}}}
		}
		return []NotificationDraft{{Recipient: target, Type: NotificationFollowUp, Data: map[string]any{"followUpId": str(d.FollowUpID)}}}
	}
	return nil
}
