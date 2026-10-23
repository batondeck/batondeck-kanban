package wake

import (
	"testing"
	"time"
)

// Five doorbells of one kind inside 100 ms are one frame carrying the last cursor, released Quiet
// after the last of them; another kind is held on its own.
func TestCoalescerFiveInABurstIsOneFrame(t *testing.T) {
	c := newCoalescer(300*time.Millisecond, time.Second)
	start := time.Now()
	for i := range 5 {
		c.add(Frame{Kind: KindAssigned, TaskID: "T-1", Cursor: int64(10 + i)}, start.Add(time.Duration(i)*20*time.Millisecond))
	}
	c.add(Frame{Kind: KindBroadcast, TaskID: "T-9", Cursor: 13}, start.Add(100*time.Millisecond))
	if got := c.release(start.Add(379 * time.Millisecond)); len(got) != 0 {
		t.Fatalf("released before the kind was quiet: %+v", got)
	}
	got := c.release(start.Add(380 * time.Millisecond))
	if len(got) != 1 || got[0].Kind != KindAssigned || got[0].Cursor != 14 {
		t.Fatalf("once assigned was quiet: %+v, want one assigned frame at cursor 14", got)
	}
	got = c.release(start.Add(400 * time.Millisecond))
	if len(got) != 1 || got[0].Kind != KindBroadcast || got[0].Cursor != 13 || !c.empty() || c.C() != nil {
		t.Fatalf("once broadcast was quiet: %+v (empty %v)", got, c.empty())
	}
}

// The bus delivers a burst in two reads up to its poll apart: still one frame. A kind that never
// pauses is released MaxHold after its first doorbell.
func TestCoalescerSplitReadsAndMaxHold(t *testing.T) {
	c := newCoalescer(300*time.Millisecond, time.Second)
	start := time.Now()
	c.add(Frame{Kind: KindAssigned, Cursor: 1}, start)
	c.add(Frame{Kind: KindAssigned, Cursor: 5}, start.Add(250*time.Millisecond))
	if got := c.release(start.Add(549 * time.Millisecond)); len(got) != 0 {
		t.Fatalf("released between the two reads of one burst: %+v", got)
	}
	if got := c.release(start.Add(550 * time.Millisecond)); len(got) != 1 || got[0].Cursor != 5 {
		t.Fatalf("after the burst: %+v, want one frame at cursor 5", got)
	}
	for i := range 5 { // every 200 ms, never quiet for 300
		c.add(Frame{Kind: KindBroadcast, Cursor: int64(100 + i)}, start.Add(time.Duration(i)*200*time.Millisecond))
	}
	if got := c.release(start.Add(999 * time.Millisecond)); len(got) != 0 {
		t.Fatalf("released before MaxHold: %+v", got)
	}
	if got := c.release(start.Add(time.Second)); len(got) != 1 || got[0].Cursor != 104 {
		t.Fatalf("a kind that never paused, at MaxHold: %+v, want the frame at cursor 104", got)
	}
}

// The held frame is the highest cursor, whatever order they arrive in (a Postgres commit can land
// below one already delivered); the timer fires when the frame is due.
func TestCoalescerKeepsTheHighestCursorAndFires(t *testing.T) {
	c := newCoalescer(30*time.Millisecond, time.Second)
	now := time.Now()
	c.add(Frame{Kind: KindAssigned, Cursor: 7}, now)
	c.add(Frame{Kind: KindAssigned, Cursor: 5}, now)
	select {
	case <-c.C():
	case <-time.After(time.Second):
		t.Fatal("the timer never fired")
	}
	if got := c.release(time.Now()); len(got) != 1 || got[0].Cursor != 7 {
		t.Fatalf("released %+v, want the frame at cursor 7", got)
	}
}

// Only assigned and broadcast rows ring; the payload's fields are the frame's, the row id its cursor.
func TestFrameOf(t *testing.T) {
	p := []byte(`{"kind":"assigned","agent":"U-1","taskId":"T-3","projectId":"P-1","boardId":"B-1","tool":"claim_task","title":"never read"}`)
	f, ok := frameOf(42, KindAssigned, p)
	if want := (Frame{Agent: "U-1", Kind: KindAssigned, TaskID: "T-3", ProjectID: "P-1", BoardID: "B-1", Cursor: 42}); !ok || f != want {
		t.Fatalf("frameOf = %+v %v, want %+v", f, ok, want)
	}
	for _, kind := range []string{"changed", "", "assigned "} {
		if _, ok := frameOf(1, kind, p); ok {
			t.Fatalf("a %q row rang", kind)
		}
	}
	if _, ok := frameOf(1, KindBroadcast, []byte("not json")); ok {
		t.Fatal("an unreadable payload rang")
	}
}
