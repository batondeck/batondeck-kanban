package cli

import (
	"context"
	"testing"
	"time"
)

// R2.5 requirement 5 (tested): a device entry's lastDeliveredCursor is 0 until a doorbell is
// written to it, then the change-log id of that doorbell, equal to its wake_sessions row's
// last_cursor; the delivery signals Changed, so the presence frames carry the new cursor.
func TestPresenceEntryCarriesTheDeliveredCursor(t *testing.T) {
	s := startDirectory(t, nil)
	c := s.client(t, "f73a2587-0000-4000-8000-0000000000c5", "peer-token-cursor")
	s.register(t, s.alice, c, "worker")
	if got := s.a.core.DeviceSessions().Entries(); len(got) != 1 || got[0].LastDeliveredCursor != 0 {
		t.Fatalf("entries before any doorbell %+v, want one at cursor 0", got)
	}
	changed := s.a.dir.Changed()
	s.task(t, s.bob, s.alice.id)
	cur := s.lastChange(t)
	c.frame(t, time.Second)
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("the delivery did not signal the presence change")
	}
	deadline := time.Now().Add(time.Second)
	for {
		got := s.a.core.DeviceSessions().Entries()
		rows, _ := s.st.ListWakeSessions(context.Background())
		if len(got) == 1 && got[0].LastDeliveredCursor == cur && len(rows) == 1 && rows[0].LastCursor == cur {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("entries %+v and rows %+v, want lastDeliveredCursor = last_cursor = %d", got, rows, cur)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
