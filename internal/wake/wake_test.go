package wake

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Five assigned doorbells of one entry inside 100 ms are one frame at the last cursor, released
// AssignedQuiet after the last; the same entry's broadcast, and another entry's assigned, are held
// on their own keys.
func TestCoalescerPerEntryAndKind(t *testing.T) {
	c := newCoalescer()
	start := time.Now()
	for i := range 5 {
		q, m := window(KindAssigned)
		c.add(1, Frame{Kind: KindAssigned, Cursor: int64(10 + i)}, start.Add(time.Duration(i)*20*time.Millisecond), q, m)
	}
	q, m := window(KindBroadcast)
	c.add(1, Frame{Kind: KindBroadcast, Cursor: 13}, start, q, m)
	q, m = window(KindAssigned)
	c.add(2, Frame{Kind: KindAssigned, Cursor: 9}, start, q, m)
	if got := c.release(start.Add(299 * time.Millisecond)); len(got) != 0 {
		t.Fatalf("released before quiet: %+v", got)
	}
	got := c.release(start.Add(380 * time.Millisecond))
	if len(got) != 2 || got[0].pid != 2 || got[0].frame.Cursor != 9 || got[1].pid != 1 || got[1].frame.Cursor != 14 {
		t.Fatalf("at 380 ms: %+v, want entry 2 at 9 then entry 1 at 14", got)
	}
	if got := c.release(start.Add(SlowWindow - time.Millisecond)); len(got) != 0 {
		t.Fatalf("the broadcast before its 5 s window: %+v", got)
	}
	if got := c.release(start.Add(SlowWindow)); len(got) != 1 || got[0].frame.Kind != KindBroadcast || !c.empty() || c.C() != nil {
		t.Fatalf("at 5 s: %+v", got)
	}
}

// A kind that never pauses is released at its cap; the held cursor is the highest whatever the
// arrival order; forget drops an entry's frames.
func TestCoalescerCapHighestAndForget(t *testing.T) {
	c := newCoalescer()
	start := time.Now()
	q, m := window(KindAssigned)
	for i := range 6 { // every 200 ms, never quiet for 300
		c.add(1, Frame{Kind: KindAssigned, Cursor: int64(100 + i)}, start.Add(time.Duration(i)*200*time.Millisecond), q, m)
	}
	c.add(1, Frame{Kind: KindAssigned, Cursor: 50}, start.Add(time.Second), q, m)
	if got := c.release(start.Add(AssignedMaxHold)); len(got) != 1 || got[0].frame.Cursor != 105 {
		t.Fatalf("at the cap: %+v, want cursor 105", got)
	}
	c.add(3, Frame{Kind: KindChanged, Cursor: 1}, start, SlowWindow, SlowWindow)
	c.forget(3)
	if !c.empty() {
		t.Fatal("forget left a frame")
	}
}

// The frame bytes are R3.1's: an optional auth line, then one user frame with session_id and a
// string content holding one plain line and the contentless frame; no priority, no from.
func TestFrameBytes(t *testing.T) {
	f := Frame{Kind: KindAssigned, Cursor: 42, ProjectID: "P-1"}
	b := string(frameBytes("S-1", "", f))
	want := `{"type":"user","session_id":"S-1","message":{"role":"user","content":"batondeck: work was assigned to you on P-1 (cursor 42); call claim_next, then list_notifications.\n{\"kind\":\"assigned\",\"cursor\":42,\"projectId\":\"P-1\"}"}}` + "\n"
	if b != want {
		t.Fatalf("frame\n got %q\nwant %q", b, want)
	}
	if a := string(frameBytes("S-1", "tok", f)); a != `{"type":"auth","token":"tok"}`+"\n"+want {
		t.Fatalf("with a token %q", a)
	}
	for _, bad := range []string{"priority", "from", "taskId", "boardId"} {
		if strings.Contains(b, bad) {
			t.Fatalf("the frame carries %q: %s", bad, b)
		}
	}
	var line struct {
		Message struct{ Content any } `json:"message"`
	}
	_ = json.Unmarshal([]byte(b), &line)
	if _, ok := line.Message.Content.(string); !ok {
		t.Fatal("content is not a string (Claude Code ignores any other)")
	}
}

// A registry entry is read for its pid only; a mismatch or a missing file is refused.
func TestReadRegistry(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("10.json", `{"pid":10,"sessionId":"s","procStart":"p","messagingSocketPath":"/x.sock","peerProtocol":1}`)
	write("11.json", `{"pid":12,"sessionId":"s","messagingSocketPath":"/x.sock"}`)
	if r, err := readRegistry(dir, 10); err != nil || r.SessionID != "s" || r.PeerProtocol != 1 {
		t.Fatalf("readRegistry(10) = %+v, %v", r, err)
	}
	if _, err := readRegistry(dir, 11); err == nil {
		t.Fatal("an entry naming another pid was read")
	}
	if _, err := readRegistry(dir, 13); !errors.Is(err, errNoRegistry) {
		t.Fatalf("a missing entry: %v", err)
	}
}

// ENOENT (no socket file) and ECONNREFUSED (a file nobody listens on, as a SIGKILL leaves it) are
// "gone"; a socket that answers takes the write.
func TestWriteDoorbellAndGone(t *testing.T) {
	dir, err := os.MkdirTemp("", "bkw")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	missing := filepath.Join(dir, "none.sock")
	if err := writeDoorbell(missing, []byte("x\n"), time.Second); !gone(err) || !errors.Is(err, syscall.ENOENT) {
		t.Fatalf("a missing socket: %v", err)
	}
	stale := filepath.Join(dir, "stale.sock")
	ln, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	if err := writeDoorbell(stale, []byte("x\n"), time.Second); !gone(err) || !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("a stale socket: %v", err)
	}
	live := filepath.Join(dir, "live.sock")
	ln, err = net.Listen("unix", live)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go func() {
		c, _ := ln.Accept()
		b := make([]byte, 16)
		n, _ := c.Read(b)
		got <- string(b[:n])
		c.Close()
	}()
	if err := writeDoorbell(live, []byte("x\n"), time.Second); err != nil {
		t.Fatal(err)
	}
	if s := <-got; s != "x\n" {
		t.Fatalf("read %q", s)
	}
}
