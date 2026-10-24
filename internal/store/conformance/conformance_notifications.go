package conformance

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("notifications", notifications) }

// notifications is the in-app feed: a row reads back as written, newest first across projects and
// paged by cursor, the unread count, mark-read counting only the rows it flips (an unknown, a read
// and another recipient's id count 0), the refusals, and the rows leaving with their project.
func notifications(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.ListNotifications(ctx, "U-probe", store.NotificationsQuery{})
		return err
	}
	area(t, "Notifications", newStore, probe, func(t *testing.T) {
		add := func(t *testing.T, s store.Store, who, project string, ts int64) store.Notification {
			t.Helper()
			board, task := "B-x", "T-1"
			n, err := s.AddNotification(ctx, store.AddNotificationInput{IdentityID: who, ProjectID: project, BoardID: &board, TaskID: &task,
				Type: domain.NotificationMention, Actor: "U-actor", Data: json.RawMessage(`{"commentId":"C-1"}`), TS: ts})
			noErr(t, err)
			return n
		}

		t.Run("ARowReadsBackNewestFirstAndPages", func(t *testing.T) {
			s, sd, p, _ := seeded(t, newStore)
			p2, _, err := sd.seedProject(ctx)
			noErr(t, err)
			a := add(t, s, "U-bob", p, 1000)
			b := add(t, s, "U-bob", p2, 3000)
			c := add(t, s, "U-bob", p, 2000)
			add(t, s, "U-carol", p, 4000)
			if a.Read || a.IdentityID != "U-bob" || a.ProjectID != p || *a.BoardID != "B-x" || *a.TaskID != "T-1" || a.Actor != "U-actor" ||
				a.Type != domain.NotificationMention || a.TS != 1000 || string(a.Data) != `{"commentId":"C-1"}` {
				t.Fatalf("row %+v does not read back as written", a)
			}
			page, err := s.ListNotifications(ctx, "U-bob", store.NotificationsQuery{Limit: 2})
			noErr(t, err)
			if len(page.Items) != 2 || page.Items[0].ID != b.ID || page.Items[1].ID != c.ID || page.NextCursor == "" {
				t.Fatalf("page 1 = %+v, want %s %s and a cursor", page, b.ID, c.ID)
			}
			page, err = s.ListNotifications(ctx, "U-bob", store.NotificationsQuery{Limit: 2, Cursor: page.NextCursor})
			noErr(t, err)
			if len(page.Items) != 1 || page.Items[0].ID != a.ID || page.NextCursor != "" {
				t.Fatalf("page 2 = %+v, want %s alone, last", page, a.ID)
			}
			empty, err := s.ListNotifications(ctx, "U-nobody", store.NotificationsQuery{})
			noErr(t, err)
			if empty.Items == nil || len(empty.Items) != 0 {
				t.Fatalf("an empty feed = %#v, want []", empty.Items)
			}
		})

		t.Run("MarkReadCountsOnlyWhatItFlips", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			a, b := add(t, s, "U-bob", p, 1000), add(t, s, "U-bob", p, 2000)
			theirs := add(t, s, "U-carol", p, 3000)
			n, err := s.CountUnreadNotifications(ctx, "U-bob")
			noErr(t, err)
			if n != 2 {
				t.Fatalf("unread = %d, want 2", n)
			}
			flipped, err := s.MarkNotificationsRead(ctx, "U-bob", []string{a.ID, a.ID, "N-unknown", theirs.ID}, false)
			noErr(t, err)
			if flipped != 1 {
				t.Fatalf("mark [a, a, unknown, carol's] flipped %d, want 1", flipped)
			}
			again, err := s.MarkNotificationsRead(ctx, "U-bob", []string{a.ID}, false)
			noErr(t, err)
			none, err := s.MarkNotificationsRead(ctx, "U-bob", nil, false)
			noErr(t, err)
			if again != 0 || none != 0 {
				t.Fatalf("re-marking a read row flipped %d, marking nothing %d; want 0 and 0", again, none)
			}
			all, err := s.MarkNotificationsRead(ctx, "U-bob", nil, true)
			noErr(t, err)
			if all != 1 {
				t.Fatalf("mark all flipped %d, want 1 (%s)", all, b.ID)
			}
			n, err = s.CountUnreadNotifications(ctx, "U-bob")
			noErr(t, err)
			carol, err := s.CountUnreadNotifications(ctx, "U-carol")
			noErr(t, err)
			if n != 0 || carol != 1 {
				t.Fatalf("unread after = bob %d carol %d, want 0 and 1", n, carol)
			}
			page, err := s.ListNotifications(ctx, "U-bob", store.NotificationsQuery{})
			noErr(t, err)
			for _, it := range page.Items {
				if !it.Read {
					t.Fatalf("%s still unread after mark all", it.ID)
				}
			}
		})

		t.Run("Refusals", func(t *testing.T) {
			s, _, p, _ := seeded(t, newStore)
			ok := store.AddNotificationInput{IdentityID: "U-bob", ProjectID: p, Type: domain.NotificationFollowUp, Actor: "U-a", TS: 1}
			for name, mut := range map[string]func(in *store.AddNotificationInput){
				"type":      func(in *store.AddNotificationInput) { in.Type = "assigned" },
				"recipient": func(in *store.AddNotificationInput) { in.IdentityID = "" },
				"actor":     func(in *store.AddNotificationInput) { in.Actor = "" },
				"ts":        func(in *store.AddNotificationInput) { in.TS = 0 },
				"data":      func(in *store.AddNotificationInput) { in.Data = json.RawMessage(`[1]`) },
			} {
				in := ok
				mut(&in)
				_, err := s.AddNotification(ctx, in)
				if e, okk := domain.AsBatonDeckError(err); !okk || e.Code != domain.CodeValidation {
					t.Fatalf("%s: got %v, want VALIDATION", name, err)
				}
			}
			in := ok
			in.ProjectID = "P-missing"
			_, err := s.AddNotification(ctx, in)
			wantErr(t, err, domain.CodeNotFound, "Project")
			_, err = s.ListNotifications(ctx, "U-bob", store.NotificationsQuery{Cursor: "%%%"})
			wantErr(t, err, domain.CodeValidation, "")
			n, err := s.AddNotification(ctx, ok)
			noErr(t, err)
			if string(n.Data) != "{}" {
				t.Fatalf("nil data reads back %s, want {}", n.Data)
			}
		})

		t.Run("RowsLeaveWithTheirProject", func(t *testing.T) {
			s, sd, p, _ := seeded(t, newStore)
			p2, _, err := sd.seedProject(ctx)
			noErr(t, err)
			add(t, s, "U-bob", p, 1000)
			kept := add(t, s, "U-bob", p2, 2000)
			noErr(t, s.DeleteProjectRecursive(ctx, p))
			page, err := s.ListNotifications(ctx, "U-bob", store.NotificationsQuery{})
			noErr(t, err)
			if len(page.Items) != 1 || page.Items[0].ID != kept.ID {
				t.Fatalf("after deleting %s the feed is %+v, want %s alone", p, page.Items, kept.ID)
			}
		})
	})
}
