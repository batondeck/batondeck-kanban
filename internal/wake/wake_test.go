package wake

import (
	"encoding/json"
	"errors"
	"fmt"
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
// string content holding one plain line and the contentless frame; no priority, no from, no task.
// The frame names its board since P-191 (owner 2026-10-24: "add boardId to the doorbell").
func TestFrameBytes(t *testing.T) {
	f := Frame{Kind: KindAssigned, Cursor: 42, ProjectID: "P-1", BoardID: "B-1"}
	b := string(frameBytes("S-1", "", f, ""))
	want := `{"type":"user","session_id":"S-1","message":{"role":"user","content":"batondeck: work was assigned to you on P-1 (cursor 42); call claim_next {projectId: P-1, boardId: B-1}, then list_notifications.\n{\"kind\":\"assigned\",\"cursor\":42,\"projectId\":\"P-1\",\"boardId\":\"B-1\"}"}}` + "\n"
	if b != want {
		t.Fatalf("frame\n got %q\nwant %q", b, want)
	}
	if a := string(frameBytes("S-1", "tok", f, "")); a != `{"type":"auth","token":"tok"}`+"\n"+want {
		t.Fatalf("with a token %q", a)
	}
	for _, bad := range []string{"priority", "from", "taskId", "title"} {
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

// P-191 AC1: rows read from the change log carry their board into the frame, and the coalescer
// merges a key's frames into the union of one project's boards: one board is boardId, several are
// boardIds (sorted, distinct, at most MaxBoards), and a frame of another project brings only its own
// boards. A frame naming no board (a project-level row, ringAll's after dropped rows, more than
// MaxBoards boards) means every board, so it absorbs a board frame rather than being narrowed to it
// (review of #64, B1), and the line says list_boards first. A master's changed line names next_task.
func TestFrameBoards(t *testing.T) {
	frameOf := func(id int64, project, board string) Frame {
		payload, _ := json.Marshal(map[string]string{"projectId": project, "boardId": board})
		r := rowOf(id, "U-1", &project, KindAssigned, payload)
		return Frame{Kind: KindAssigned, Cursor: r.ID, ProjectID: r.ProjectID, BoardID: r.BoardID}
	}
	coalesce := func(fs ...Frame) Frame {
		c := newCoalescer()
		start := time.Now()
		for i, f := range fs {
			c.add(1, f, start.Add(time.Duration(i)*time.Millisecond), AssignedQuiet, AssignedMaxHold)
		}
		got := c.release(start.Add(AssignedMaxHold))
		if len(got) != 1 {
			t.Fatalf("released %+v, want one frame", got)
		}
		return got[0].frame
	}
	js := func(f Frame) string { b, _ := json.Marshal(f); return string(b) }

	one := coalesce(frameOf(1, "P-1", "B-1"), frameOf(2, "P-1", "B-1"))
	if got := js(one); got != `{"kind":"assigned","cursor":2,"projectId":"P-1","boardId":"B-1"}` {
		t.Fatalf("one board's rows: %s", got)
	}
	two := coalesce(frameOf(3, "P-1", "B-b"), frameOf(4, "P-1", "B-a"), frameOf(5, "P-1", "B-b"))
	if got := js(two); got != `{"kind":"assigned","cursor":5,"projectId":"P-1","boardIds":["B-a","B-b"]}` {
		t.Fatalf("two boards' rows: %s", got)
	}
	project := coalesce(frameOf(6, "P-1", ""))
	if got := js(project); got != `{"kind":"assigned","cursor":6,"projectId":"P-1"}` {
		t.Fatalf("a project-level row: %s", got)
	}
	if got := js(coalesce(frameOf(7, "P-1", ""), frameOf(8, "P-1", "B-1"))); got != `{"kind":"assigned","cursor":8,"projectId":"P-1"}` {
		t.Fatalf("a project-level row then a board's: %s", got)
	}
	// ringAll's frame after dropped rows, the shape directory.go builds: it must not be narrowed to B-1.
	all := coalesce(frameOf(9, "P-1", "B-1"), Frame{Kind: KindAssigned, Cursor: 9, ProjectID: "P-1"})
	if got := js(all); got != `{"kind":"assigned","cursor":9,"projectId":"P-1"}` || strings.Contains(all.Line(), "boardId: B-1") {
		t.Fatalf("a board's row then ringAll's: %s\n%s", got, all.Line())
	}
	if got := js(coalesce(frameOf(9, "P-1", "B-1"), frameOf(10, "P-2", "B-2"))); got != `{"kind":"assigned","cursor":10,"projectId":"P-2","boardId":"B-2"}` {
		t.Fatalf("another project's frame: %s", got)
	}
	var many []Frame
	for i := MaxBoards + 1; i > 0; i-- {
		many = append(many, frameOf(int64(100+i), "P-1", fmt.Sprintf("B-%02d", i)))
	}
	if full := coalesce(many[1:]...); len(full.BoardIDs) != MaxBoards || full.BoardIDs[0] != "B-01" || full.BoardIDs[MaxBoards-1] != "B-20" {
		t.Fatalf("%d boards: %+v, want B-01..B-20", MaxBoards, full)
	}
	over := coalesce(many...)
	if got := js(over); got != `{"kind":"assigned","cursor":121,"projectId":"P-1"}` {
		t.Fatalf("%d boards: %s, want no partial list", MaxBoards+1, got)
	}

	for _, c := range []struct {
		f    Frame
		want string
	}{
		{one, "; call claim_next {projectId: P-1, boardId: B-1}, then list_notifications."},
		{two, "; call claim_next {projectId: P-1, boardId} for each boardId of B-a, B-b, then list_notifications."},
		{project, "; call list_boards {projectId: P-1}, then claim_next {projectId, boardId} on each board, then list_notifications."},
		{over, "; call list_boards {projectId: P-1}, then claim_next {projectId, boardId} on each board, then list_notifications."},
		{Frame{Kind: KindChanged, ProjectID: "P-1", BoardID: "B-1"}, "; call next_task {projectId: P-1, boardId: B-1, includeInbox: true}, then list_notifications."},
		{Frame{Kind: KindChanged, ProjectID: "P-1", BoardIDs: []string{"B-a", "B-b"}}, "; call next_task {projectId: P-1, boardId, includeInbox: true} for each boardId of B-a, B-b, then list_notifications."},
		{Frame{Kind: KindChanged, ProjectID: "P-1"}, "; call list_boards {projectId: P-1}, then next_task {projectId, boardId, includeInbox: true} on each board, then list_notifications."},
	} {
		if line := c.f.Line(); !strings.Contains(line, c.want) {
			t.Fatalf("line %q\nwant it to hold %q", line, c.want)
		}
	}
}

// P-276: a worker registered under an agent label hears its assigned doorbell as claim_next with
// assignee: <label>, then without it (work routed to its account); its broadcast and a master's
// changed are Line's. The positive control: label "" is Line, word for word.
func TestLineForALabelledWorker(t *testing.T) {
	one := Frame{Kind: KindAssigned, Cursor: 7, ProjectID: "P-1", BoardID: "B-1"}
	for _, c := range []struct {
		f     Frame
		label string
		want  string
	}{
		{one, "agent-a", `; call claim_next {projectId: P-1, boardId: B-1, assignee: "agent-a"}, then claim_next without assignee, then list_notifications.`},
		{Frame{Kind: KindAssigned, ProjectID: "P-1", BoardIDs: []string{"B-a", "B-b"}}, "my agent", `; call claim_next {projectId: P-1, boardId, assignee: "my agent"} for each boardId of B-a, B-b, then claim_next without assignee, then list_notifications.`},
		{Frame{Kind: KindAssigned, ProjectID: "P-1"}, "agent-a", `; call list_boards {projectId: P-1}, then claim_next {projectId, boardId, assignee: "agent-a"} on each board, then claim_next without assignee, then list_notifications.`},
		{Frame{Kind: KindBroadcast, ProjectID: "P-1", BoardID: "B-1"}, "agent-a", "; call claim_next {projectId: P-1, boardId: B-1}, then list_notifications."},
		{Frame{Kind: KindChanged, ProjectID: "P-1", BoardID: "B-1"}, "agent-a", "; call next_task {projectId: P-1, boardId: B-1, includeInbox: true}, then list_notifications."},
	} {
		if line := c.f.LineFor(c.label); !strings.Contains(line, c.want) {
			t.Fatalf("line %q\nwant it to hold %q", line, c.want)
		}
	}
	if one.LineFor("") != one.Line() || strings.Contains(one.Line(), "assignee") {
		t.Fatalf("label \"\" is not Line: %q vs %q", one.LineFor(""), one.Line())
	}
	if got := string(frameBytes("S-1", "", one, "agent-a")); !strings.Contains(got, `assignee: \"agent-a\"`) {
		t.Fatalf("frameBytes does not carry the label's line: %s", got)
	}
	// The entry's label: a worker's agent, never a master's or a derived id:<identity>.
	for _, c := range []struct {
		e    entry
		want string
	}{
		{entry{role: "worker", agent: "agent-a"}, "agent-a"},
		{entry{role: "worker", agent: "id:U-1"}, ""},
		{entry{role: "master", agent: "agent-a"}, ""},
	} {
		if got := c.e.label(); got != c.want {
			t.Fatalf("label of %+v = %q, want %q", c.e, got, c.want)
		}
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
