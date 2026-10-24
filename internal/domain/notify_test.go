package domain

import (
	"fmt"
	"slices"
	"testing"
)

// The mapper reads only the event's own data: the resolved mentions and targetIdentity, never the
// routing handle (conductor's T-212); nobody is notified of their own act; mentions are capped.
func TestNotificationsForEvent(t *testing.T) {
	got := NotificationsForEvent(EventCommentAdded, "u-ada", []byte(`{"commentId":"C-1","mentions":["u-grace","u-ada","u-grace",7,"u-bob"]}`))
	var to []string
	for _, d := range got {
		to = append(to, d.Recipient)
		if d.Type != NotificationMention || d.Data["commentId"] != "C-1" {
			t.Fatalf("mention draft %+v", d)
		}
	}
	if !slices.Equal(to, []string{"u-grace", "u-bob"}) {
		t.Fatalf("mentions notified %v, want grace and bob once each, never the author", to)
	}
	if d := NotificationsForEvent(EventTaskFollowUp, "u-ada", []byte(`{"followUpId":"FU-1","targetIdentity":"u-grace","target":"agent-x"}`)); len(d) != 1 ||
		d[0].Recipient != "u-grace" || d[0].Type != NotificationFollowUp || d[0].Data["followUpId"] != "FU-1" {
		t.Fatalf("follow-up %+v", d)
	}
	if d := NotificationsForEvent(EventTaskReviewRequested, "u-ada", []byte(`{"targetIdentity":"u-grace","reviewer":"u-grace"}`)); len(d) != 1 || d[0].Type != NotificationReviewRequest {
		t.Fatalf("review request %+v", d)
	}
	for name, c := range map[string]struct {
		typ  EventType
		data string
	}{
		"a follow-up with no verified target": {EventTaskFollowUp, `{"followUpId":"FU-1","targetIdentity":null,"target":"u-grace"}`},
		"a review of nobody":                  {EventTaskReviewRequested, `{"targetIdentity":null,"reviewer":"agent-x"}`},
		"a self-review":                       {EventTaskReviewRequested, `{"targetIdentity":"u-ada"}`},
		"a handoff":                           {EventTaskHandoff, `{"toAgent":"u-grace","targetIdentity":"u-grace"}`},
		"an assignment":                       {EventTaskUpdated, `{"assignee":"u-grace"}`},
		"malformed data":                      {EventCommentAdded, `[1]`},
	} {
		if d := NotificationsForEvent(c.typ, "u-ada", []byte(c.data)); len(d) != 0 {
			t.Fatalf("%s notified %+v", name, d)
		}
	}
	var many []string
	for i := range 60 {
		many = append(many, fmt.Sprintf(`"u-%d"`, i))
	}
	if d := NotificationsForEvent(EventCommentAdded, "u-ada", []byte(`{"mentions":[`+joinComma(many)+`]}`)); len(d) != MaxNotificationsPerEvent {
		t.Fatalf("60 mentions wrote %d rows, want the cap %d", len(d), MaxNotificationsPerEvent)
	}
}

func joinComma(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}
