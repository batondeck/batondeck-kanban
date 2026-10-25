package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/bus"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// live is the BFF over the real service and the real bus of a stack (R1.18: every engine
// servicetest selects; the engines wake the bus on commit, memstore's polls every 10 ms).
type live struct {
	*harness
	core *service.Core
	bus  *bus.Bus
}

func newLive(t *testing.T, s *servicetest.Stack, o Options) *live {
	return &live{harness: newStackHarness(t, s, o), core: s.Core, bus: s.Bus}
}

func (h *harness) caller() domain.Caller {
	return domain.Caller{Identity: h.userID, TokenScope: h.userID}
}

// seedBoard is a project and a board the harness's user created.
func seedBoard(t *testing.T, h *harness, core *service.Core, name string) (projectID, boardID string) {
	t.Helper()
	ctx := context.Background()
	p, err := core.Projects().Create(ctx, h.caller(), service.CreateProjectReq{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	b, err := core.Boards().Create(ctx, h.caller(), service.CreateBoardReq{ProjectID: p.ProjectID, Name: name})
	if err != nil {
		t.Fatal(err)
	}
	return p.ProjectID, b.Board.ID
}

// addTask is a task created as caller: one change-log row for the project, through the service.
func (l *live) addTask(t *testing.T, caller domain.Caller, projectID, boardID, title string) {
	t.Helper()
	if _, err := l.core.Tasks().Create(context.Background(), caller, service.CreateTaskReq{ProjectID: projectID, BoardID: boardID, Title: title}); err != nil {
		t.Fatal(err)
	}
}

// setColor is a project-wide change: update_settings names the project and no board.
func (l *live) setColor(t *testing.T, projectID, color string) {
	t.Helper()
	if _, err := l.core.Projects().UpdateSettings(context.Background(), l.caller(), service.UpdateSettingsReq{ProjectID: projectID, Color: service.Value(color)}); err != nil {
		t.Fatal(err)
	}
}

type frame struct {
	event, id, data string
	at              time.Time
}

// sseClient is an open /api/stream: its frames as they arrive, the notifications frames apart (every
// stream carries them, R2.3, so a test of the board or presence frames reads past none).
type sseClient struct {
	status int
	frames chan frame
	notes  chan frame
	cancel func()
	body   string // the error body when status is not 200
}

func (h *harness) stream(t *testing.T, query string, header map[string]string) *sseClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req := h.request("GET", "/api/stream"+query, "", header).WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	c := &sseClient{status: resp.StatusCode, frames: make(chan frame, 64), notes: make(chan frame, 64), cancel: func() { cancel(); resp.Body.Close() }}
	t.Cleanup(c.cancel)
	if resp.StatusCode != http.StatusOK {
		var b strings.Builder
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			b.WriteString(sc.Text())
		}
		c.body = b.String()
		c.cancel()
		return c
	}
	go func() {
		defer close(c.frames)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		var f frame
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				switch {
				case f.event == "notifications":
					f.at = time.Now()
					select {
					case c.notes <- f:
					default: // a test that reads none never stalls the stream
					}
				case f.event != "":
					f.at = time.Now()
					c.frames <- f
				}
				f = frame{}
			case strings.HasPrefix(line, "event: "):
				f.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "id: "):
				f.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "data: "):
				f.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return c
}

// next is the next frame within d, or fails.
func (c *sseClient) next(t *testing.T, d time.Duration) frame {
	t.Helper()
	select {
	case f, ok := <-c.frames:
		if !ok {
			t.Fatal("the stream closed")
		}
		return f
	case <-time.After(d):
		t.Fatalf("no frame within %v", d)
	}
	return frame{}
}

// quiet fails if a frame other than ping arrives within d.
func (c *sseClient) quiet(t *testing.T, d time.Duration) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case f, ok := <-c.frames:
			if ok && f.event != "ping" {
				t.Fatalf("a %s frame arrived (%s), want none", f.event, f.data)
			}
			if !ok {
				return
			}
		case <-deadline:
			return
		}
	}
}

type boardData struct {
	Board struct {
		Board      service.Board      `json:"board"`
		TaskCounts map[string]float64 `json:"taskCounts"`
	} `json:"board"`
	Tasks struct {
		Tasks []service.Task `json:"tasks"`
	} `json:"tasks"`
	Feed struct {
		Events []service.BoardEvent `json:"events"`
	} `json:"feed"`
}

func atoi(t *testing.T, s string) int64 {
	t.Helper()
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		t.Fatalf("id %q: %v", s, err)
	}
	return n
}

func decodeBoard(t *testing.T, f frame) boardData {
	t.Helper()
	var d boardData
	if err := json.Unmarshal([]byte(f.data), &d); err != nil {
		t.Fatalf("board frame %q: %v", f.data, err)
	}
	return d
}

// AC2: a change-log row written by a real mutation, delivered by the real bus on each engine, is one
// board frame within 200 ms, after ready and before the next ping, followed by its updated frame.
func TestStreamBoardFrameOnAChange(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		l := newLive(t, s, Options{Ping: time.Second})
		pid, bid := seedBoard(t, l.harness, l.core, "Live")
		c := l.stream(t, "?projectId="+pid+"&boardId="+bid, nil)
		if c.status != 200 {
			t.Fatalf("stream: %d %s", c.status, c.body)
		}
		opened := time.Now()
		if f := c.next(t, time.Second); f.event != "ready" {
			t.Fatalf("first frame %+v, want ready", f)
		}
		time.Sleep(50 * time.Millisecond) // the tail is reading
		start := time.Now()
		l.addTask(t, l.caller(), pid, bid, "first")
		wrote := time.Since(start)
		f := c.next(t, time.Second)
		if f.event != "board" {
			t.Fatalf("after the change: %+v, want board", f)
		}
		if took := f.at.Sub(start); took > 200*time.Millisecond {
			t.Fatalf("the board frame took %v, want <= 200ms (the write itself took %v)", took, wrote)
		}
		d := decodeBoard(t, f)
		if d.Board.Board.ID != bid || len(d.Tasks.Tasks) != 1 || d.Tasks.Tasks[0].Title != "first" || f.id == "" {
			t.Fatalf("board frame: id %q %s", f.id, f.data)
		}
		t.Logf("board frame %v after the change (the write took %v), id %s", f.at.Sub(start), wrote, f.id)
		u := c.next(t, time.Second)
		if u.event != "updated" || u.data != `{"uri":"batondeck://`+pid+`/board/`+bid+`/feed","count":1}` {
			t.Fatalf("after board: %+v, want updated count 1", u)
		}
		p := c.next(t, 2*time.Second)
		if p.event != "ping" {
			t.Fatalf("then %+v, want ping (one board frame for one change)", p)
		}
		if at := p.at.Sub(opened); at < 900*time.Millisecond || at > 1500*time.Millisecond {
			t.Fatalf("ping at %v after open, want the 1s keepalive", at)
		}
	})
}

// P-193 (P-295): the first change after a quiet coalesce is a board frame at once, not at the
// coalesce's end; the changes within the coalesce after it gather into one frame at its end. The
// coalesce is 2 s here so the two edges cannot be confused under load.
func TestStreamSendsTheFirstChangeAtOnceAndCoalescesTheRest(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		const coalesce = 2 * time.Second
		l := newLive(t, s, Options{Ping: time.Minute, Coalesce: coalesce})
		pid, bid := seedBoard(t, l.harness, l.core, "Edges")
		c := l.stream(t, "?projectId="+pid+"&boardId="+bid, nil)
		if c.status != 200 {
			t.Fatalf("stream: %d %s", c.status, c.body)
		}
		if f := c.next(t, time.Second); f.event != "ready" {
			t.Fatalf("first frame %+v, want ready", f)
		}
		time.Sleep(50 * time.Millisecond) // the tail is reading
		start := time.Now()
		l.addTask(t, l.caller(), pid, bid, "first")
		f := c.next(t, 3*time.Second)
		lead := f.at.Sub(start)
		if f.event != "board" || lead > coalesce/2 {
			t.Fatalf("the first change: %s after %v, want a board frame well inside the %v coalesce", f.event, lead, coalesce)
		}
		if u := c.next(t, time.Second); u.event != "updated" || !strings.Contains(u.data, `"count":1`) {
			t.Fatalf("%+v, want updated count 1", u)
		}
		l.addTask(t, l.caller(), pid, bid, "second")
		l.addTask(t, l.caller(), pid, bid, "third")
		f = c.next(t, 3*time.Second)
		trail := f.at.Sub(start)
		if f.event != "board" || trail < coalesce-100*time.Millisecond {
			t.Fatalf("the follow-ups: %s after %v, want one board frame at the coalesce's end (%v)", f.event, trail, coalesce)
		}
		if d := decodeBoard(t, f); len(d.Tasks.Tasks) != 3 {
			t.Fatalf("the trailing frame holds %d tasks, want 3", len(d.Tasks.Tasks))
		}
		if u := c.next(t, time.Second); u.event != "updated" || !strings.Contains(u.data, `"count":2`) {
			t.Fatalf("%+v, want updated count 2 (both follow-ups in one frame)", u)
		}
		t.Logf("leading frame %v after the first change; trailing frame %v after it, for 2 follow-ups", lead.Round(time.Millisecond), trail.Round(time.Millisecond))
	})
}

// AC6: a stream for project p carries p's rows and not project q's; a stream for a board of a
// project the caller is not a member of is refused 403 before any frame.
func TestStreamIsScopedToTheProject(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		l := newLive(t, s, Options{Ping: time.Minute})
		pid, bid := seedBoard(t, l.harness, l.core, "P")
		qid, qbid := seedBoard(t, l.harness, l.core, "Q")
		c := l.stream(t, "?projectId="+pid+"&boardId="+bid, nil)
		if c.next(t, time.Second).event != "ready" {
			t.Fatal("no ready")
		}
		time.Sleep(50 * time.Millisecond)
		// q's rows: a task on q's board, and a settings change naming q and no board, which only the
		// project subscription keeps off p's stream.
		l.addTask(t, l.caller(), qid, qbid, "in q")
		l.setColor(t, qid, "#aa0000")
		c.quiet(t, 400*time.Millisecond)
		l.addTask(t, l.caller(), pid, bid, "in p")
		if f := c.next(t, time.Second); f.event != "board" || !strings.Contains(f.data, `"in p"`) || strings.Contains(f.data, `"in q"`) {
			t.Fatalf("after p's change: %+v, want p's board frame", f)
		}
		if u := c.next(t, time.Second); u.event != "updated" || !strings.Contains(u.data, `"count":1`) {
			t.Fatalf("%+v, want updated count 1", u)
		}
		l.setColor(t, pid, "#0000aa") // the positive control: a project-wide row of p is p's board's too
		if f := c.next(t, time.Second); f.event != "board" {
			t.Fatalf("after p's settings change: %+v, want board", f)
		}

		other := domain.Caller{Identity: "U-other", TokenScope: "U-other"}
		op, err := l.core.Projects().Create(context.Background(), other, service.CreateProjectReq{Name: "Theirs"})
		if err != nil {
			t.Fatal(err)
		}
		ob, err := l.core.Boards().Create(context.Background(), other, service.CreateBoardReq{ProjectID: op.ProjectID, Name: "Theirs"})
		if err != nil {
			t.Fatal(err)
		}
		refused := l.stream(t, "?projectId="+op.ProjectID+"&boardId="+ob.Board.ID, nil)
		if refused.status != http.StatusForbidden || !strings.Contains(refused.body, `"FORBIDDEN"`) {
			t.Fatalf("a non-member's board stream: %d %s, want 403 FORBIDDEN", refused.status, refused.body)
		}
		if lone := l.stream(t, "?projectId="+pid, nil); lone.status != http.StatusBadRequest {
			t.Fatalf("projectId without boardId: %d, want 400", lone.status)
		}
	})
}

// A reconnect with the last board frame's id (Last-Event-ID, or ?cursor=) replays what it missed as
// one board frame counting the project's board rows; a cursor more than ReplayCap rows old gets
// resync and no board frame.
func TestStreamReplaysAMissedCursorAndCapsIt(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		l := newLive(t, s, Options{Ping: time.Minute, ReplayCap: 6})
		pid, bid := seedBoard(t, l.harness, l.core, "R")
		qid, qbid := seedBoard(t, l.harness, l.core, "Q")
		c := l.stream(t, "?projectId="+pid+"&boardId="+bid, nil)
		c.next(t, time.Second)
		time.Sleep(50 * time.Millisecond)
		l.addTask(t, l.caller(), pid, bid, "seen")
		seen := c.next(t, time.Second)
		if seen.event != "board" || seen.id == "" {
			t.Fatalf("%+v, want a board frame with an id", seen)
		}
		c.cancel()
		for _, title := range []string{"m1", "m2", "m3"} {
			l.addTask(t, l.caller(), pid, bid, title)
		}
		l.addTask(t, l.caller(), qid, qbid, "q1")
		l.setColor(t, qid, "#aa0000") // q's, naming no board: only the replay's project filter drops it

		for _, via := range []struct {
			query  string
			header map[string]string
		}{{"", map[string]string{"Last-Event-ID": seen.id}}, {"&cursor=" + seen.id, nil}} {
			r := l.stream(t, "?projectId="+pid+"&boardId="+bid+via.query, via.header)
			if f := r.next(t, time.Second); f.event != "ready" {
				t.Fatalf("%+v, want ready", f)
			}
			f := r.next(t, time.Second)
			d := decodeBoard(t, f)
			if f.event != "board" || len(d.Tasks.Tasks) != 4 || atoi(t, f.id) <= atoi(t, seen.id) {
				t.Fatalf("replay (%v): %+v, want a board frame of 4 tasks past id %s", via, f, seen.id)
			}
			if u := r.next(t, time.Second); u.event != "updated" || !strings.Contains(u.data, `"count":3`) {
				t.Fatalf("replay (%v): %+v, want updated count 3 (q's rows are not p's)", via, u)
			}
			r.cancel()
		}

		l.addTask(t, l.caller(), pid, bid, "m4") // 6 rows after the cursor: still within the cap of 6
		within := l.stream(t, "?projectId="+pid+"&boardId="+bid+"&cursor="+seen.id, nil)
		within.next(t, time.Second)
		if f := within.next(t, time.Second); f.event != "board" {
			t.Fatalf("6 rows after the cursor, cap 6: %+v, want board", f)
		}
		within.cancel()
		l.addTask(t, l.caller(), qid, qbid, "q3") // 7 rows: past the cap
		old := l.stream(t, "?projectId="+pid+"&boardId="+bid+"&cursor="+seen.id, nil)
		old.next(t, time.Second)
		if f := old.next(t, time.Second); f.event != "resync" {
			t.Fatalf("7 rows after the cursor, cap 6: %+v, want resync", f)
		}
		old.quiet(t, 300*time.Millisecond)
	})
}

// fakeDirectory is the device session directory's double: Register and Unregister change its
// entries and close Changed's channel, as R0.25's directory does.
type fakeDirectory struct {
	mu      sync.Mutex
	entries []PresenceEntry
	changed chan struct{}
}

func newFakeDirectory() *fakeDirectory { return &fakeDirectory{changed: make(chan struct{})} }

func (d *fakeDirectory) Entries() []PresenceEntry {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]PresenceEntry(nil), d.entries...)
}

func (d *fakeDirectory) Changed() <-chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.changed
}

func (d *fakeDirectory) signal() {
	close(d.changed)
	d.changed = make(chan struct{})
}

func (d *fakeDirectory) register(e PresenceEntry) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.entries = append(d.entries, e)
	d.signal()
}

func (d *fakeDirectory) unregister(pid int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for i, e := range d.entries {
		if e.PID == pid {
			d.entries = append(d.entries[:i], d.entries[i+1:]...)
			break
		}
	}
	d.signal()
}

// AC6: the stream's presence frames are the caller's live processes on the device: one on connect,
// one per register and unregister that changes them; another user's process is not shown.
func TestStreamPresenceFollowsTheDirectory(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		dir := newFakeDirectory()
		l := newLive(t, s, Options{Ping: time.Minute, Presence: dir})
		c := l.stream(t, "", nil)
		if f := c.next(t, time.Second); f.event != "ready" {
			t.Fatalf("%+v, want ready", f)
		}
		if f := c.next(t, time.Second); f.event != "presence" || f.data != `{"sessions":[]}` {
			t.Fatalf("on connect: %+v, want an empty presence frame", f)
		}
		dir.register(PresenceEntry{PID: 4242, Role: "worker", IdentityID: l.userID, AgentID: "builder-1", Adapter: "claude-code", RegisteredAt: "2026-10-23T00:00:00Z"})
		f := c.next(t, time.Second)
		if f.event != "presence" || !strings.Contains(f.data, `"pid":4242`) || !strings.Contains(f.data, `"role":"worker"`) {
			t.Fatalf("after register: %+v, want a presence frame naming pid 4242", f)
		}
		t.Logf("register: event: %s data: %s", f.event, f.data)
		dir.register(PresenceEntry{PID: 7, Role: "master", IdentityID: "U-someone-else", AgentID: "m", RegisteredAt: "2026-10-23T00:00:01Z"})
		c.quiet(t, 200*time.Millisecond)
		dir.unregister(4242)
		f = c.next(t, time.Second)
		if f.event != "presence" || f.data != `{"sessions":[]}` {
			t.Fatalf("after unregister: %+v, want an empty presence frame", f)
		}
		t.Logf("unregister: event: %s data: %s", f.event, f.data)
		c.quiet(t, 200*time.Millisecond)
	})
}

// A member removed while their stream is open has it closed at the next board frame, which
// re-authorizes as them; nothing of the board reaches them after the removal (the reviewer's probe
// of PR #43). remove_member writes a change-log row of the project, so that frame is due at once.
func TestStreamClosesWhenTheMemberIsRemoved(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		l := newLive(t, s, Options{Ping: 200 * time.Millisecond})
		pid, bid := seedBoard(t, l.harness, l.core, "P")
		bob := l.as("bob")
		if _, err := l.core.Projects().AddMember(context.Background(), l.caller(), service.AddMemberReq{ProjectID: pid, IdentityID: bob.userID, Role: domain.Role("member")}); err != nil {
			t.Fatal(err)
		}
		c := bob.stream(t, "?projectId="+pid+"&boardId="+bid, nil)
		if c.status != http.StatusOK {
			t.Fatalf("bob's stream: %d %s", c.status, c.body)
		}
		if f := c.next(t, time.Second); f.event != "ready" {
			t.Fatalf("%+v, want ready", f)
		}
		time.Sleep(50 * time.Millisecond)
		if _, err := l.core.Projects().RemoveMember(context.Background(), l.caller(), service.RemoveMemberReq{ProjectID: pid, IdentityID: bob.userID}); err != nil {
			t.Fatal(err)
		}
		l.addTask(t, l.caller(), pid, bid, "after the removal")
		deadline := time.After(time.Second)
		for {
			select {
			case f, ok := <-c.frames:
				if !ok {
					return
				}
				if f.event != "ping" {
					t.Fatalf("a %s frame after the removal: %s", f.event, f.data)
				}
			case <-deadline:
				t.Fatal("the stream is still open 1s after the member was removed")
			}
		}
	})
}

// closes reads c until it ends within d, failing on any frame but ping: nothing reaches a caller
// after the refusal (P-219). It answers the pings seen.
func (c *sseClient) closes(t *testing.T, d time.Duration) (pings int) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case f, ok := <-c.frames:
			if !ok {
				return pings
			}
			if f.event != "ping" {
				t.Fatalf("a %s frame after the refusal: %s", f.event, f.data)
			}
			pings++
		case <-deadline:
			t.Fatalf("the stream is still open %v after the refusal", d)
		}
	}
}

// pings reads n pings within d, failing on a close or a frame but ping: the stream is healthy.
func (c *sseClient) pings(t *testing.T, n int, d time.Duration) {
	t.Helper()
	deadline := time.After(d)
	for seen := 0; seen < n; {
		select {
		case f, ok := <-c.frames:
			if !ok {
				t.Fatalf("the stream closed after %d of %d pings", seen, n)
			}
			if f.event != "ping" {
				t.Fatalf("a %s frame (%s), want ping", f.event, f.data)
			}
			seen++
		case <-deadline:
			t.Fatalf("%d of %d pings within %v", seen, n, d)
		}
	}
}

// openBoardStream is h's board stream, read past ready and its presence frame.
func openBoardStream(t *testing.T, h *harness, pid, bid string, header map[string]string) *sseClient {
	t.Helper()
	c := h.stream(t, "?projectId="+pid+"&boardId="+bid, header)
	if c.status != http.StatusOK {
		t.Fatalf("stream: %d %s", c.status, c.body)
	}
	if f := c.next(t, time.Second); f.event != "ready" {
		t.Fatalf("%+v, want ready", f)
	}
	return c
}

// The control: a stream whose caller still resolves stays open across pings (P-219).
func TestStreamStaysOpenAcrossPings(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		l := newLive(t, s, Options{Ping: 100 * time.Millisecond})
		pid, bid := seedBoard(t, l.harness, l.core, "P")
		c := openBoardStream(t, l.harness, pid, bid, nil)
		c.pings(t, 5, 2*time.Second)
	})
}

// A logout deletes the session row, so the stream it opened closes at the next ping and the
// reconnect is 401 (P-219; rev54 finding 1).
func TestStreamClosesAfterLogout(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		l := newLive(t, s, Options{Ping: 200 * time.Millisecond})
		pid, bid := seedBoard(t, l.harness, l.core, "P")
		c := openBoardStream(t, l.harness, pid, bid, nil)
		c.pings(t, 1, time.Second)
		if st, body := l.do("POST", "/api/logout", "", nil); st != http.StatusNoContent {
			t.Fatalf("logout: %d %s", st, body)
		}
		at := time.Now()
		c.closes(t, time.Second)
		t.Logf("closed %v after the logout", time.Since(at).Round(time.Millisecond))
		if again := l.stream(t, "?projectId="+pid+"&boardId="+bid, nil); again.status != http.StatusUnauthorized {
			t.Fatalf("reconnect: %d %s, want 401", again.status, again.body)
		}
	})
}

// A disable reaches an open stream within the account cache's bound: the cached entry keeps it
// open until DefaultAccountCacheTTL has passed on the auth clock, then the next ping closes it.
func TestStreamClosesWhenTheUserIsDisabled(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		var skew atomic.Int64
		now := func() time.Time { return time.Now().Add(time.Duration(skew.Load())) }
		l := &live{harness: newStackHarnessAt(t, s, Options{Ping: 100 * time.Millisecond}, now), core: s.Core, bus: s.Bus}
		pid, bid := seedBoard(t, l.harness, l.core, "P")
		c := openBoardStream(t, l.harness, pid, bid, nil)
		c.pings(t, 1, time.Second)
		if _, err := l.auth.Accounts.Disable(context.Background(), "alice"); err != nil {
			t.Fatal(err)
		}
		c.pings(t, 3, 2*time.Second) // within the bound: the cached entry still holds
		skew.Store(int64(auth.DefaultAccountCacheTTL + time.Second))
		at := time.Now()
		c.closes(t, time.Second)
		t.Logf("closed %v after the cache bound passed", time.Since(at).Round(time.Millisecond))
		if again := l.stream(t, "", nil); again.status != http.StatusUnauthorized {
			t.Fatalf("reconnect: %d %s, want 401", again.status, again.body)
		}
	})
}

// A revoked API token ends the bearer stream it opened before its next frame: here a board frame,
// no ping being due, so a board change after the revoke reaches nobody (P-219).
func TestStreamClosesWhenTheTokenIsRevoked(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		l := newLive(t, s, Options{Ping: time.Minute})
		pid, bid := seedBoard(t, l.harness, l.core, "P")
		token, info, err := l.auth.Tokens.Create(context.Background(), l.userID, l.userID, "stream", time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		bearer := &harness{t: t, auth: l.auth, srv: l.srv, userID: l.userID} // no cookie
		header := map[string]string{"Authorization": "Bearer " + token}
		c := openBoardStream(t, bearer, pid, bid, header)
		l.addTask(t, l.caller(), pid, bid, "before the revoke")
		for _, want := range []string{"board", "updated"} {
			if f := c.next(t, time.Second); f.event != want {
				t.Fatalf("%+v, want the bearer's %s frame", f, want)
			}
		}
		if _, err := l.auth.Tokens.Revoke(context.Background(), info.ID); err != nil {
			t.Fatal(err)
		}
		at := time.Now()
		l.addTask(t, l.caller(), pid, bid, "after the revoke")
		c.closes(t, time.Second)
		t.Logf("closed %v after the revoke", time.Since(at).Round(time.Millisecond))
		if again := bearer.stream(t, "", header); again.status != http.StatusUnauthorized {
			t.Fatalf("reconnect: %d %s, want 401", again.status, again.body)
		}
	})
}

// A presence change after logout, no ping being due, closes the stream before its presence frame
// (P-219; the rev6667 review's probe).
func TestStreamClosesOnAPresenceChangeAfterLogout(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		dir := newFakeDirectory()
		l := newLive(t, s, Options{Ping: time.Minute, Presence: dir})
		c := l.stream(t, "", nil)
		if f := c.next(t, time.Second); f.event != "ready" {
			t.Fatalf("%+v, want ready", f)
		}
		if f := c.next(t, time.Second); f.event != "presence" {
			t.Fatalf("%+v, want presence", f)
		}
		if st, body := l.do("POST", "/api/logout", "", nil); st != http.StatusNoContent {
			t.Fatalf("logout: %d %s", st, body)
		}
		dir.register(PresenceEntry{PID: 4242, Role: "worker", IdentityID: l.userID, AgentID: "b", RegisteredAt: "2026-10-23T00:00:00Z"})
		c.closes(t, time.Second)
	})
}

// A notification row after logout, no ping being due, closes the stream before its notifications
// frame (P-219; the rev6667 review's probe).
func TestStreamClosesOnANotificationAfterLogout(t *testing.T) {
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
		mine := bob.stream(t, "", nil)
		if mine.status != http.StatusOK {
			t.Fatalf("stream: %d", mine.status)
		}
		if f := mine.next(t, time.Second); f.event != "ready" {
			t.Fatalf("%+v, want ready", f)
		}
		mine.nextNote(t, time.Second)
		if st, body := bob.do("POST", "/api/logout", "", nil); st != http.StatusNoContent {
			t.Fatalf("logout: %d %s", st, body)
		}
		if _, err := l.core.FollowUps().AddFollowUp(ctx, l.caller(), service.AddFollowUpReq{ProjectID: pid, TaskID: task.Task.ID, Body: "your turn"}); err != nil {
			t.Fatal(err)
		}
		mine.closes(t, 2*time.Second)
		select {
		case n := <-mine.notes:
			t.Fatalf("a notifications frame after the logout: %s", n.data)
		default:
		}
	})
}
