package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

type feedFrame struct {
	Notifications []service.Notification `json:"notifications"`
	Unread        int64                  `json:"unread"`
}

// nextNote is the next notifications frame within d.
func (c *sseClient) nextNote(t *testing.T, d time.Duration) feedFrame {
	t.Helper()
	select {
	case f := <-c.notes:
		var out feedFrame
		if err := json.Unmarshal([]byte(f.data), &out); err != nil {
			t.Fatalf("notifications frame %q: %v", f.data, err)
		}
		return out
	case <-time.After(d):
		t.Fatalf("no notifications frame within %v", d)
	}
	return feedFrame{}
}

// AC1's stream half: every /api/stream (here the identity-keyed one the nav badge opens, with no
// board) sends the caller's feed on open, and again when a notification row for the caller
// commits; another user's stream hears nothing of it. Then the two routes read and clear the row.
func TestStreamCarriesTheCallersNotifications(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		l := newLive(t, s, Options{Ping: time.Minute})
		pid, bid := seedBoard(t, l.harness, l.core, "P")
		bob := l.as("bob")
		ctx := context.Background()
		if _, err := l.core.Projects().AddMember(ctx, l.caller(), service.AddMemberReq{ProjectID: pid, IdentityID: bob.userID, Role: domain.Role("member")}); err != nil {
			t.Fatal(err)
		}
		task, err := l.core.Tasks().Create(ctx, l.caller(), service.CreateTaskReq{ProjectID: pid, BoardID: bid, Title: "for bob"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := l.core.Tasks().Update(ctx, l.caller(), service.UpdateTaskReq{ProjectID: pid, TaskID: task.Task.ID, Version: int64(task.Task.Version),
			Patch: service.TaskPatch{Assignee: service.Value(bob.userID)}}); err != nil {
			t.Fatal(err)
		}

		mine, theirs := bob.stream(t, "", nil), l.stream(t, "", nil)
		if mine.status != http.StatusOK || theirs.status != http.StatusOK {
			t.Fatalf("streams: %d %d", mine.status, theirs.status)
		}
		if f := mine.nextNote(t, time.Second); f.Unread != 0 || len(f.Notifications) != 0 {
			t.Fatalf("bob's feed on open %+v, want empty", f)
		}
		theirs.nextNote(t, time.Second)

		fu, err := l.core.FollowUps().AddFollowUp(ctx, l.caller(), service.AddFollowUpReq{ProjectID: pid, TaskID: task.Task.ID, Body: "your turn"})
		if err != nil {
			t.Fatal(err)
		}
		f := mine.nextNote(t, 2*time.Second)
		if f.Unread != 1 || len(f.Notifications) != 1 || f.Notifications[0].Type != domain.NotificationFollowUp || *f.Notifications[0].TaskID != task.Task.ID {
			t.Fatalf("bob's frame after the follow-up %+v", f)
		}
		t.Logf("bob's notifications frame: unread %d, %s from %s on %s (follow-up %s)", f.Unread, f.Notifications[0].Type, f.Notifications[0].Actor, *f.Notifications[0].TaskID, fu.FollowUp.ID)
		select {
		case n := <-theirs.notes:
			t.Fatalf("the actor's stream heard bob's notification: %s", n.data)
		case <-time.After(300 * time.Millisecond):
		}

		status, body := bob.do("GET", "/api/notifications?limit=10", "", nil)
		var got feedFrame
		if status != http.StatusOK || json.Unmarshal([]byte(body), &got) != nil || got.Unread != 1 {
			t.Fatalf("GET /api/notifications: %d %s", status, body)
		}
		if status, body := bob.do("POST", "/api/notifications/read", `{"ids":["`+got.Notifications[0].ID+`"]}`, nil); status != http.StatusOK || body != `{"updated":1}`+"\n" && body != `{"updated":1}` {
			t.Fatalf("POST /api/notifications/read: %d %q", status, body)
		}
		if status, body := bob.do("POST", "/api/notifications/read", `{"all":true}`, nil); status != http.StatusOK || (body != `{"updated":0}`+"\n" && body != `{"updated":0}`) {
			t.Fatalf("mark all after: %d %q", status, body)
		}
		if status, _ := bob.do("GET", "/api/notifications?limit=500", "", nil); status != http.StatusBadRequest {
			t.Fatalf("limit 500: %d, want 400", status)
		}
	})
}
