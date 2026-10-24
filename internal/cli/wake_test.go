package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
	"github.com/batondeck/batondeck-kanban/internal/wake"
)

// The device session directory and its dispatcher (R0.25) through the real serve composition: a
// migrated SQLite store, the service core, the bus, the directory, and its routes on a real Unix
// listener. A client is a `sleep` this test starts (the process the directory watches), a fake
// Claude Code registry entry naming it, and a fake inbox socket that records every byte written.

// countedSQLite counts the reads a recheck makes: GetToken, GetUser, ListProjects.
type countedSQLite struct {
	*store.SQLite
	rechecks atomic.Int64
}

func (c *countedSQLite) GetToken(ctx context.Context, id string) (store.Token, error) {
	c.rechecks.Add(1)
	return c.SQLite.GetToken(ctx, id)
}

func (c *countedSQLite) GetUser(ctx context.Context, id string) (store.User, error) {
	c.rechecks.Add(1)
	return c.SQLite.GetUser(ctx, id)
}

func (c *countedSQLite) ListProjects(ctx context.Context, identityID string) ([]store.ProjectMembership, error) {
	c.rechecks.Add(1)
	return c.SQLite.ListProjects(ctx, identityID)
}

// person is a user with an API token.
type person struct {
	id, token, tokenID string
}

func (p person) caller() domain.Caller { return domain.Caller{Identity: p.id, TokenScope: p.id} }

// dirServer is one serve run over st.
type dirServer struct {
	st         *countedSQLite
	a          *app
	tcp, sock  string
	reg        string
	keys       *core.Keyring
	logs       *lockedLog
	stop       func()
	alice, bob person // alice is P's admin; bob a master of P
	carol      person // a member of nothing until a test adds her
	project    string
	board      string
	columns    map[domain.Status]string
}

func testKeys(t *testing.T) *core.Keyring {
	t.Helper()
	k, err := core.OpenKeyring(filepath.Join(t.TempDir(), "master.key"), func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// shortDir is a directory short enough for a socket path under macOS's 104-byte limit.
func shortDir(t *testing.T, prefix string) string {
	t.Helper()
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// seedDirectory makes the three people, project P and its board over a fresh store.
func seedDirectory(t *testing.T) (*countedSQLite, person, person, person, string, string) {
	t.Helper()
	ctx := context.Background()
	st := &countedSQLite{SQLite: storetest.SQLite(t)}
	mk := func(login string) person {
		u, err := (&auth.Accounts{Store: st}).Add(ctx, login, login, "pw-"+login, false)
		if err != nil {
			t.Fatal(err)
		}
		tok, info, err := (&auth.TokenService{Store: st}).Create(ctx, u.ID, "", "test", time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return person{id: u.ID, token: tok, tokenID: info.ID}
	}
	alice, bob, carol := mk("alice"), mk("bob"), mk("carol")
	p, err := st.CreateProject(ctx, store.CreateProjectInput{Name: "P", CreatedBy: alice.id}, store.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddMember(ctx, p.ID, bob.id, domain.RoleMaster, alice.id); err != nil {
		t.Fatal(err)
	}
	b, err := st.CreateBoard(ctx, store.CreateBoardInput{ProjectID: p.ID, Name: "B", CreatedBy: alice.id}, store.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return st, alice, bob, carol, p.ID, b.ID
}

// startDirectory serves the app over st (nil: a freshly seeded store) until the test ends or stop.
func startDirectory(t *testing.T, from *dirServer) *dirServer {
	return startDirectoryWith(t, from, false)
}

// startDirectoryWith is startDirectory with the blob store, and with public_url set to the TCP
// listener (the OAuth authorization server on) when oauth.
func startDirectoryWith(t *testing.T, from *dirServer, oauth bool) *dirServer {
	t.Helper()
	s := &dirServer{logs: &lockedLog{}}
	if from == nil {
		s.st, s.alice, s.bob, s.carol, s.project, s.board = seedDirectory(t)
		s.reg, s.keys = shortDir(t, "bkreg"), testKeys(t)
	} else {
		s.st, s.alice, s.bob, s.carol, s.project, s.board, s.reg, s.keys = from.st, from.alice, from.bob, from.carol, from.project, from.board, from.reg, from.keys
	}
	oldReg := wakeRegistry
	wakeRegistry = func() string { return s.reg }
	t.Cleanup(func() { wakeRegistry = oldReg })
	logger := log.New(s.logs, "", 0)
	s.sock = filepath.Join(shortDir(t, "bks"), "bk.sock")
	lns, err := listenAll([]string{"tcp://127.0.0.1:0", "unix://" + s.sock}, logger)
	if err != nil {
		t.Fatal(err)
	}
	s.tcp = lns[0].Addr().String()
	media, err := mediaOptions(&core.Config{Blob: core.Blob{Dir: t.TempDir(), MaxBytes: 1 << 20}}, s.keys, lns, logger)
	if err != nil {
		t.Fatal(err)
	}
	public := ""
	if oauth {
		public = "http://" + s.tcp
	}
	a, err := compose(s.st, "x", public, logger, s.keys, media, core.Retention{})
	if err != nil {
		t.Fatal(err)
	}
	s.a = a
	board, _, err := s.st.GetBoard(context.Background(), s.project, s.board)
	if err != nil {
		t.Fatal(err)
	}
	s.columns = map[domain.Status]string{}
	for _, c := range board.Columns {
		if _, ok := s.columns[c.Status]; !ok {
			s.columns[c.Status] = c.ID
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveUntil(ctx, lns, a, "x", logger) }()
	// The directory starts before HTTP serves: one answered request means it is up.
	if code, _ := httpDo(t, &http.Client{Timeout: 5 * time.Second}, http.MethodGet, "http://"+s.tcp+"/healthz", "", ""); code != http.StatusOK {
		t.Fatalf("healthz %d", code)
	}
	var once sync.Once
	s.stop = func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("serveUntil: %v", err)
			}
		})
	}
	t.Cleanup(s.stop)
	return s
}

// client is a fake Claude Code session: a process, its registry entry and its inbox socket.
type client struct {
	cmd     *exec.Cmd
	pid     int64
	sock    string
	session string
	got     chan []byte // one per connection: every byte written on it
	ln      net.Listener
}

func (s *dirServer) client(t *testing.T, session, peerTok string) *client {
	t.Helper()
	cmd := exec.Command("sleep", "120")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	c := &client{cmd: cmd, pid: int64(cmd.Process.Pid), session: session, got: make(chan []byte, 16)}
	c.sock = filepath.Join(shortDir(t, "bkcc"), strconv.FormatInt(c.pid, 10)+".sock")
	ln, err := net.Listen("unix", c.sock)
	if err != nil {
		t.Fatal(err)
	}
	c.ln = ln
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			b, _ := io.ReadAll(conn)
			conn.Close()
			c.got <- b
		}
	}()
	entry, _ := json.Marshal(map[string]any{"pid": c.pid, "sessionId": session, "cwd": "/x", "status": "idle", "procStart": "Fri Oct 23 12:00:00 2026",
		"messagingSocketPath": c.sock, "peerProtocol": 1})
	if err := os.WriteFile(filepath.Join(s.reg, strconv.FormatInt(c.pid, 10)+".json"), entry, 0o600); err != nil {
		t.Fatal(err)
	}
	if peerTok != "" {
		h := sha256.Sum256([]byte(c.sock))
		key, _ := json.Marshal(map[string]string{"peerToken": peerTok, "procStart": "x", "pidDomain": "darwin"})
		if err := os.WriteFile(filepath.Join(s.reg, fmt.Sprintf("%d.%s.key", c.pid, hex.EncodeToString(h[:]))), key, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

// frame waits for one doorbell write.
func (c *client) frame(t *testing.T, within time.Duration) []byte {
	t.Helper()
	select {
	case b := <-c.got:
		return b
	case <-time.After(within):
		t.Fatalf("pid %d: no doorbell within %v", c.pid, within)
		return nil
	}
}

// none asserts no doorbell is written for d.
func (c *client) none(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case b := <-c.got:
		t.Fatalf("pid %d: an unexpected doorbell %q", c.pid, b)
	default:
	}
	select {
	case b := <-c.got:
		t.Fatalf("pid %d: an unexpected doorbell %q", c.pid, b)
	case <-time.After(d):
	}
}

// content is a doorbell's frame, read from its user frame's content.
func content(t *testing.T, b []byte) wake.Frame {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	var u struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &u); err != nil {
		t.Fatalf("not a user frame %q: %v", b, err)
	}
	parts := strings.Split(u.Message.Content, "\n")
	var f wake.Frame
	if err := json.Unmarshal([]byte(parts[len(parts)-1]), &f); err != nil {
		t.Fatalf("no frame in %q: %v", u.Message.Content, err)
	}
	return f
}

func unixHTTP(path string) *http.Client {
	return &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}}
}

func httpDo(t *testing.T, c *http.Client, method, url, bearer, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// register registers c as role for p over the Unix socket.
func (s *dirServer) register(t *testing.T, p person, c *client, role string) {
	t.Helper()
	body := fmt.Sprintf(`{"pid":%d,"role":%q,"socket":%q,"sessionId":%q}`, c.pid, role, c.sock, c.session)
	if code, got := httpDo(t, unixHTTP(s.sock), http.MethodPost, "http://x/internal/wake-session", p.token, body); code != http.StatusCreated {
		t.Fatalf("register pid %d: %d %s", c.pid, code, got)
	}
}

// pids is the directory's live pids.
func (s *dirServer) pids() []int64 {
	var out []int64
	for _, e := range s.a.dir.Entries() {
		out = append(out, e.PID)
	}
	slices.Sort(out)
	return out
}

// task creates a task as p in TODO, assigned to assignee ("" for none).
func (s *dirServer) task(t *testing.T, p person, assignee string) service.Task {
	t.Helper()
	ctx := context.Background()
	got, err := s.a.core.Tasks().Create(ctx, p.caller(), service.CreateTaskReq{ProjectID: s.project, BoardID: s.board, Title: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if assignee == "" {
		return got.Task
	}
	return s.assign(t, p, got.Task, assignee)
}

func (s *dirServer) assign(t *testing.T, p person, task service.Task, assignee string) service.Task {
	t.Helper()
	got, err := s.a.core.Tasks().Update(context.Background(), p.caller(), service.UpdateTaskReq{ProjectID: s.project, TaskID: task.ID, Version: int64(task.Version),
		Patch: service.TaskPatch{Assignee: service.Value(assignee)}})
	if err != nil {
		t.Fatal(err)
	}
	return got.Task
}

func (s *dirServer) move(t *testing.T, p person, task service.Task, to domain.Status) service.Task {
	t.Helper()
	got, err := s.a.core.Tasks().Move(context.Background(), p.caller(), service.MoveTaskReq{ProjectID: s.project, TaskID: task.ID, Version: int64(task.Version), ToStatus: to})
	if err != nil {
		t.Fatal(err)
	}
	return got.Task
}

// waitLog waits until the log holds want at least n times.
func (s *dirServer) waitLog(t *testing.T, want string, n int) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); strings.Count(s.logs.String(), want) < n; {
		if time.Now().After(deadline) {
			t.Fatalf("the log never held %q %d times:\n%s", want, n, s.logs.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// lastChange is the change log's highest id.
func (s *dirServer) lastChange(t *testing.T) int64 {
	t.Helper()
	rows, err := s.st.ReadChanges(context.Background(), 0, 10000)
	if err != nil || len(rows) == 0 {
		t.Fatalf("the change log: %d rows, %v", len(rows), err)
	}
	return rows[len(rows)-1].ID
}

// AC1/AC2 (tested): a worker is in the directory once registered, its presence signal fires, and an
// assignment by someone else writes EXACTLY the R3.1 frame into its socket within 1 s of the commit:
// the auth line from the key file, then one user frame naming the registry's sessionId, whose content
// is one plain line and the contentless {kind, cursor, projectId, boardId} (boardId since P-191).
func TestDirectoryRingsAWorkerWithTheExactFrame(t *testing.T) {
	s := startDirectory(t, nil)
	c := s.client(t, "f73a2587-0000-4000-8000-000000000001", "peer-token-xyz")
	changed := s.a.dir.Changed()
	s.register(t, s.alice, c, "worker")
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("register did not signal the presence change")
	}
	if got := s.a.core.DeviceSessions().Entries(); len(got) != 1 || got[0].PID != c.pid || got[0].Role != "worker" || got[0].IdentityID != s.alice.id || got[0].Adapter != wake.AdapterClaudeCode {
		t.Fatalf("entries %+v", got)
	}
	rows, _ := s.st.ListWakeSessions(context.Background())
	if len(rows) != 1 || rows[0].PID != c.pid || rows[0].Role != "worker" || rows[0].ClientSessionID != c.session || len(rows[0].BearerSealed) == 0 || bytes.Contains(rows[0].BearerSealed, []byte(s.alice.token)) {
		t.Fatalf("the row %+v", rows)
	}
	t.Logf("row: id=%s pid=%d role=%s identity=%s agent=%s socket=%s session=%s", rows[0].ID, rows[0].PID, rows[0].Role, rows[0].IdentityID, rows[0].AgentID, rows[0].SocketPath, rows[0].ClientSessionID)
	committed := time.Now()
	s.task(t, s.bob, s.alice.id)
	cur := s.lastChange(t)
	b := c.frame(t, time.Second)
	t.Logf("written %v after the commit: %q", time.Since(committed), b)
	if d := time.Since(committed); d > time.Second {
		t.Fatalf("the doorbell came %v after the commit, want under 1s", d)
	}
	want := fmt.Sprintf(`{"type":"auth","token":"peer-token-xyz"}`+"\n"+
		`{"type":"user","session_id":%q,"message":{"role":"user","content":"batondeck: work was assigned to you on %s (cursor %d); call claim_next {projectId: %s, boardId: %s}, then list_notifications.\n{\"kind\":\"assigned\",\"cursor\":%d,\"projectId\":\"%s\",\"boardId\":\"%s\"}"}}`+"\n",
		c.session, s.project, cur, s.project, s.board, cur, s.project, s.board)
	if string(b) != want {
		t.Fatalf("the doorbell bytes\n got %q\nwant %q", b, want)
	}
	c.none(t, 500*time.Millisecond)
	if rows, _ = s.st.ListWakeSessions(context.Background()); rows[0].LastCursor != cur {
		t.Fatalf("last_cursor %d, want %d", rows[0].LastCursor, cur)
	}
}

// AC6 and the master skips (tested): a claim and a master's self-assignment write no doorbell at
// all, to the worker or the master; a worker's self-assignment rings no worker. The positive
// controls: bob assigning alice rings her, alice's move rings the master.
func TestDirectoryNoSelfRing(t *testing.T) {
	s := startDirectory(t, nil)
	w, m := s.client(t, "s-worker", ""), s.client(t, "s-master", "")
	s.register(t, s.alice, w, "worker")
	s.register(t, s.bob, m, "master")
	ctx := context.Background()
	open, mine, hers := s.task(t, s.bob, ""), s.task(t, s.bob, ""), s.task(t, s.bob, "")
	if f := content(t, w.frame(t, wake.SlowWindow+time.Second)); f.Kind != wake.KindBroadcast {
		t.Fatalf("bob's creates rang the worker %+v, want one broadcast", f)
	}
	m.none(t, 0) // bob's own creates ring bob's master nothing (it has had the 5 s window above)
	before := s.a.dir.Writes()
	if _, err := s.a.core.Lifecycle().Claim(ctx, s.alice.caller(), service.ClaimTaskReq{ProjectID: s.project, TaskID: open.ID}); err != nil {
		t.Fatal(err)
	}
	s.assign(t, s.bob, mine, s.bob.id) // the master assigns itself
	w.none(t, wake.SlowWindow+500*time.Millisecond)
	m.none(t, 0)
	if n := s.a.dir.Writes() - before; n != 0 {
		t.Fatalf("a claim and a self-assignment wrote %d doorbells, want 0", n)
	}
	t.Logf("dispatcher writes over claim_task and the master's self-assignment: 0 (total before %d)", before)
	s.assign(t, s.alice, hers, s.alice.id) // the worker's identity assigns itself: no worker ring
	if f := content(t, m.frame(t, wake.SlowWindow+time.Second)); f.Kind != wake.KindChanged {
		t.Fatalf("alice's self-assignment rang the master %+v", f)
	}
	w.none(t, 0)
	other := s.task(t, s.bob, s.alice.id) // the positive controls
	if f := content(t, w.frame(t, time.Second)); f.Kind != wake.KindAssigned {
		t.Fatalf("bob's assignment rang %+v, want assigned", f)
	}
	s.move(t, s.alice, other, domain.InProgress)
	if f := content(t, m.frame(t, wake.SlowWindow+time.Second)); f.Kind != wake.KindChanged || f.ProjectID != s.project {
		t.Fatalf("alice's move rang the master %+v, want changed on %s", f, s.project)
	}
}

// AC3 (tested): three state changes inside 2 s ring a master once in its 5 s window, at the last
// change's cursor.
func TestDirectoryMasterCoalescesAt5s(t *testing.T) {
	s := startDirectory(t, nil)
	m := s.client(t, "s-master", "")
	s.register(t, s.bob, m, "master")
	task := s.task(t, s.alice, "")
	start := time.Now()
	task = s.move(t, s.alice, task, domain.InProgress)
	task = s.move(t, s.alice, task, domain.Blocked)
	s.move(t, s.alice, task, domain.InProgress)
	cur := s.lastChange(t)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("the three changes took %v", d)
	}
	f := content(t, m.frame(t, wake.SlowWindow+time.Second))
	t.Logf("one frame %v after the first change: %+v", time.Since(start), f)
	if f.Kind != wake.KindChanged || f.Cursor != cur {
		t.Fatalf("frame %+v, want changed at cursor %d", f, cur)
	}
	m.none(t, wake.SlowWindow)
}

// AC4 (tested): kill -9 of a registered process drops its entry and its row within 1 s, with no
// unregister call.
func TestDirectoryDropsAKilledProcessWithin1s(t *testing.T) {
	s := startDirectory(t, nil)
	c := s.client(t, "s-1", "")
	s.register(t, s.alice, c, "worker")
	changed := s.a.dir.Changed()
	killed := time.Now()
	if err := syscall.Kill(int(c.pid), syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatalf("the entry was still there 1s after kill -9: %v", s.pids())
	}
	t.Logf("entry gone %v after kill -9 (pids now %v)", time.Since(killed), s.pids())
	if len(s.pids()) != 0 {
		t.Fatalf("pids %v", s.pids())
	}
	deadline := time.Now().Add(time.Second)
	for rows, _ := s.st.ListWakeSessions(context.Background()); len(rows) != 0; rows, _ = s.st.ListWakeSessions(context.Background()) {
		if time.Now().After(deadline) {
			t.Fatalf("rows %+v 1s after the exit", rows)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(s.logs.String(), "the process exited") {
		t.Fatalf("the log does not name the exit:\n%s", s.logs.String())
	}
}

// AC5 (tested), at delivery: a revoked token's entry is dropped by the next delivery, which writes
// nothing; the recheck is 3 statements.
func TestDirectoryDeliveryRechecksTheBearer(t *testing.T) {
	s := startDirectory(t, nil)
	c := s.client(t, "s-1", "")
	s.register(t, s.alice, c, "worker")
	s.task(t, s.bob, s.alice.id)
	_ = c.frame(t, time.Second) // the positive control: the bearer holds
	if _, err := (&auth.TokenService{Store: s.st}).Revoke(context.Background(), s.alice.tokenID); err != nil {
		t.Fatal(err)
	}
	s.task(t, s.bob, s.alice.id)
	c.none(t, time.Second)
	if len(s.pids()) != 0 {
		t.Fatalf("the revoked entry is still live: %v", s.pids())
	}
	if !strings.Contains(s.logs.String(), "bearer no longer resolves") {
		t.Fatalf("the drop's reason is not logged:\n%s", s.logs.String())
	}
}

// AC5 (tested), by the sweep: with nothing to deliver, a disabled user's entry is dropped within
// one sweep; an idle sweep costs 3 statements per entry.
func TestDirectorySweepDropsADisabledUser(t *testing.T) {
	old := wakeSweep
	wakeSweep = 300 * time.Millisecond
	t.Cleanup(func() { wakeSweep = old })
	s := startDirectory(t, nil)
	c := s.client(t, "s-1", "")
	s.register(t, s.alice, c, "worker")
	n0 := s.st.rechecks.Load()
	for deadline := time.Now().Add(5 * wakeSweep); s.st.rechecks.Load()-n0 < 3 && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond) // the sweep's reads are done; the next is wakeSweep away
	per := s.st.rechecks.Load() - n0
	t.Logf("one idle sweep of one entry: %d statements", per)
	if per != 3 {
		t.Fatalf("one idle sweep: %d statements, want 3", per)
	}
	if _, err := (&auth.Accounts{Store: s.st}).Disable(context.Background(), "alice"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * wakeSweep)
	for len(s.pids()) != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if len(s.pids()) != 0 {
		t.Fatalf("a disabled user's entry outlived two sweeps: %v", s.pids())
	}
}

// Owner ruling 2026-10-23 (tested): a scope change is applied in place at the sweep. Carol, added to
// P, hears P's broadcasts without registering again; removed, she stops.
func TestDirectoryScopesChangeInPlace(t *testing.T) {
	old := wakeSweep
	wakeSweep = 300 * time.Millisecond
	t.Cleanup(func() { wakeSweep = old })
	s := startDirectory(t, nil)
	c := s.client(t, "s-carol", "")
	s.register(t, s.carol, c, "worker")
	s.task(t, s.bob, "")
	c.none(t, wake.SlowWindow+500*time.Millisecond) // not a member yet: P's broadcast is not hers
	ctx := context.Background()
	if _, err := s.a.core.Projects().AddMember(ctx, s.alice.caller(), service.AddMemberReq{ProjectID: s.project, IdentityID: s.carol.id, Role: domain.RoleWorker}); err != nil {
		t.Fatal(err)
	}
	s.waitLog(t, fmt.Sprintf("the scopes of pid %d changed", c.pid), 1)
	s.task(t, s.bob, "")
	if f := content(t, c.frame(t, wake.SlowWindow+time.Second)); f.Kind != wake.KindBroadcast || f.ProjectID != s.project {
		t.Fatalf("after the add %+v", f)
	}
	if _, err := s.a.core.Projects().RemoveMember(ctx, s.alice.caller(), service.RemoveMemberReq{ProjectID: s.project, IdentityID: s.carol.id}); err != nil {
		t.Fatal(err)
	}
	s.waitLog(t, fmt.Sprintf("the scopes of pid %d changed", c.pid), 2)
	s.task(t, s.bob, "")
	c.none(t, wake.SlowWindow+500*time.Millisecond)
	if got := s.pids(); len(got) != 1 || got[0] != c.pid {
		t.Fatalf("the scope change dropped the entry: %v", got)
	}
}

// AC8's pid ownership refusal (tested): a pid another uid owns is 403 and a dead one 400, each
// leaving no entry; the caller's own child is 201 (the positive control). A socket the registry
// does not name is 400. Over TCP both routes are 404.
func TestDirectoryRegisterRefusals(t *testing.T) {
	s := startDirectory(t, nil)
	c := s.client(t, "s-1", "")
	u := unixHTTP(s.sock)
	reg := func(body string) (int, string) {
		return httpDo(t, u, http.MethodPost, "http://x/internal/wake-session", s.alice.token, body)
	}
	if uid, err := core.ProcessUID(1); err != nil || uid == os.Getuid() {
		t.Skipf("pid 1 is uid %d (%v): no process of another uid to register", uid, err)
	}
	if code, got := reg(`{"pid":1,"role":"worker"}`); code != http.StatusForbidden || !strings.Contains(got, "not owned by the caller's uid") {
		t.Fatalf("pid 1 (root's): %d %s, want 403", code, got)
	}
	dead := exec.Command("true")
	_ = dead.Run()
	if code, got := reg(fmt.Sprintf(`{"pid":%d,"role":"worker"}`, dead.Process.Pid)); code != http.StatusBadRequest || !strings.Contains(got, "not a live process") {
		t.Fatalf("a dead pid: %d %s, want 400", code, got)
	}
	if code, got := reg(fmt.Sprintf(`{"pid":%d,"role":"worker","socket":"/tmp/elsewhere.sock"}`, c.pid)); code != http.StatusBadRequest {
		t.Fatalf("a socket the registry does not name: %d %s, want 400", code, got)
	}
	if code, got := reg(fmt.Sprintf(`{"pid":%d,"role":"admin"}`, c.pid)); code != http.StatusBadRequest {
		t.Fatalf("role admin: %d %s, want 400", code, got)
	}
	if code, _ := httpDo(t, u, http.MethodPost, "http://x/internal/wake-session", "", `{}`); code != http.StatusUnauthorized {
		t.Fatalf("no bearer: %d, want 401", code)
	}
	if len(s.pids()) != 0 {
		t.Fatalf("a refusal left entries %v", s.pids())
	}
	if code, got := reg(fmt.Sprintf(`{"pid":%d,"role":"worker","socket":%q}`, c.pid, c.sock)); code != http.StatusCreated {
		t.Fatalf("the caller's own child: %d %s, want 201", code, got)
	}
	tcp := &http.Client{Timeout: 3 * time.Second}
	if code, _ := httpDo(t, tcp, http.MethodPost, "http://"+s.tcp+"/internal/wake-session", s.alice.token, `{}`); code != http.StatusNotFound {
		t.Fatalf("register over TCP: %d, want 404", code)
	}
	if code, _ := httpDo(t, tcp, http.MethodDelete, fmt.Sprintf("http://%s/internal/wake-session/%d", s.tcp, c.pid), s.alice.token, ""); code != http.StatusNotFound {
		t.Fatalf("unregister over TCP: %d, want 404", code)
	}
	// Unregister: another identity's pid is 404, the owner's is 204 and drops it.
	if code, _ := httpDo(t, u, http.MethodDelete, fmt.Sprintf("http://x/internal/wake-session/%d", c.pid), s.bob.token, ""); code != http.StatusNotFound {
		t.Fatalf("bob unregistering alice's pid: %d, want 404", code)
	}
	if code, _ := httpDo(t, u, http.MethodDelete, fmt.Sprintf("http://x/internal/wake-session/%d", c.pid), s.alice.token, ""); code != http.StatusNoContent || len(s.pids()) != 0 {
		t.Fatalf("unregister: %d, pids %v", code, s.pids())
	}
}

// AC7 (tested): GET /wake is gone, 404 over TCP and over the Unix socket.
func TestWakeRouteIsGone(t *testing.T) {
	s := startDirectory(t, nil)
	for name, c := range map[string]*http.Client{"tcp": {Timeout: 3 * time.Second}, "unix": unixHTTP(s.sock)} {
		host := s.tcp
		if name == "unix" {
			host = "x"
		}
		if code, _ := httpDo(t, c, http.MethodGet, "http://"+host+"/wake", s.alice.token, ""); code != http.StatusNotFound {
			t.Fatalf("GET /wake over %s: %d, want 404", name, code)
		}
	}
}

// A delivery whose socket nobody answers (ECONNREFUSED, as a SIGKILL leaves it) or that is gone
// (ENOENT) drops the entry.
func TestDirectoryDropsOnADeadSocket(t *testing.T) {
	s := startDirectory(t, nil)
	c := s.client(t, "s-1", "")
	s.register(t, s.alice, c, "worker")
	c.ln.Close() // the file goes too: ENOENT
	s.task(t, s.bob, s.alice.id)
	deadline := time.Now().Add(2 * time.Second)
	for len(s.pids()) != 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if len(s.pids()) != 0 || !strings.Contains(s.logs.String(), "its socket is gone") {
		t.Fatalf("pids %v after a delivery to a closed socket; log:\n%s", s.pids(), s.logs.String())
	}
}

// The start-up rebuild: a restarted server restores a live entry from its row, drops a dead one's,
// and catches the live one up from its last delivered cursor (assignments made while it was down, on
// two boards: one frame naming both).
func TestDirectoryRebuildsAndCatchesUp(t *testing.T) {
	s := startDirectory(t, nil)
	live, dead := s.client(t, "s-live", ""), s.client(t, "s-dead", "")
	s.register(t, s.alice, live, "worker")
	s.register(t, s.alice, dead, "worker")
	s.task(t, s.bob, s.alice.id)
	_ = live.frame(t, time.Second)
	_ = dead.frame(t, time.Second)
	// The cursor is recorded just after the write the client saw: wait for it before stopping.
	deadline := time.Now().Add(2 * time.Second)
	for {
		rows, _ := s.st.ListWakeSessions(context.Background())
		if len(rows) == 2 && rows[0].LastCursor > 0 && rows[1].LastCursor > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the cursors were never recorded: %+v", rows)
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.stop()
	_ = dead.cmd.Process.Kill()
	_ = dead.cmd.Wait()
	// While the server is down, bob assigns alice a task on each of two boards through the store's
	// service on the same store.
	down := service.New(s.st, service.Options{Logger: log.New(io.Discard, "", 0), ToolCallFlush: -1})
	other, err := down.Boards().Create(context.Background(), s.alice.caller(), service.CreateBoardReq{ProjectID: s.project, Name: "other"})
	if err != nil {
		t.Fatal(err)
	}
	for _, board := range []string{s.board, other.Board.ID} {
		got, err := down.Tasks().Create(context.Background(), s.bob.caller(), service.CreateTaskReq{ProjectID: s.project, BoardID: board, Title: "while down"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := down.Tasks().Update(context.Background(), s.bob.caller(), service.UpdateTaskReq{ProjectID: s.project, TaskID: got.Task.ID, Version: int64(got.Task.Version), Patch: service.TaskPatch{Assignee: service.Value(s.alice.id)}}); err != nil {
			t.Fatal(err)
		}
	}
	_ = down.Close(context.Background())
	cur := s.lastChange(t)
	again := startDirectory(t, s)
	if got := again.pids(); len(got) != 1 || got[0] != live.pid {
		t.Fatalf("restored pids %v, want only %d", got, live.pid)
	}
	f := content(t, live.frame(t, 2*time.Second))
	boards := []string{s.board, other.Board.ID}
	slices.Sort(boards)
	if f.Kind != wake.KindAssigned || f.Cursor != cur || f.BoardID != "" || !slices.Equal(f.BoardIDs, boards) {
		t.Fatalf("the catch-up rang %+v, want assigned at %d on boards %v (P-191)", f, cur, boards)
	}
	if !strings.Contains(again.logs.String(), fmt.Sprintf("dropped the row of pid %d at start-up", dead.pid)) {
		t.Fatalf("the dead row's drop is not logged:\n%s", again.logs.String())
	}
}

// setProcStart rewrites c's registry entry with another procStart, as a reused pid's new process
// would have.
func (s *dirServer) setProcStart(t *testing.T, c *client, procStart string) {
	t.Helper()
	entry, _ := json.Marshal(map[string]any{"pid": c.pid, "sessionId": c.session, "cwd": "/x", "status": "idle", "procStart": procStart,
		"messagingSocketPath": c.sock, "peerProtocol": 1})
	if err := os.WriteFile(filepath.Join(s.reg, strconv.FormatInt(c.pid, 10)+".json"), entry, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Review B1 (tested): another identity re-registering a live, registered pid is 403 and changes
// nothing: alice keeps the entry and is still rung. A forged sessionId or procStart against the
// registry is 400 and registers nothing. No bearer reaches the log.
func TestDirectoryRefusesAnotherIdentityTakingAPid(t *testing.T) {
	s := startDirectory(t, nil)
	c := s.client(t, "s-1", "")
	u := unixHTTP(s.sock)
	post := func(tok, body string) (int, string) {
		return httpDo(t, u, http.MethodPost, "http://x/internal/wake-session", tok, body)
	}
	if code, got := post(s.alice.token, fmt.Sprintf(`{"pid":%d,"role":"worker","socket":%q,"sessionId":"forged"}`, c.pid, c.sock)); code != http.StatusBadRequest || !strings.Contains(got, "sessionId") {
		t.Fatalf("a forged sessionId: %d %s, want 400", code, got)
	}
	if code, got := post(s.alice.token, fmt.Sprintf(`{"pid":%d,"role":"worker","socket":%q,"procStart":"Thu Jan  1 00:00:00 1970"}`, c.pid, c.sock)); code != http.StatusBadRequest || !strings.Contains(got, "procStart") {
		t.Fatalf("a forged procStart: %d %s, want 400", code, got)
	}
	if len(s.pids()) != 0 {
		t.Fatalf("the refusals left %v", s.pids())
	}
	s.register(t, s.alice, c, "worker")
	code, got := post(s.bob.token, fmt.Sprintf(`{"pid":%d,"role":"master","socket":%q,"sessionId":%q}`, c.pid, c.sock, c.session))
	t.Logf("bob re-registering alice's pid: %d %s", code, got)
	if code != http.StatusForbidden || !strings.Contains(got, "registered by another identity") {
		t.Fatalf("bob takes over alice's pid: %d %s, want 403", code, got)
	}
	if e := s.a.dir.Entries(); len(e) != 1 || e[0].IdentityID != s.alice.id || e[0].Role != "worker" {
		t.Fatalf("after the refusal the entry is %+v, want alice's worker", e)
	}
	if strings.Contains(s.logs.String(), s.alice.token) || strings.Contains(s.logs.String(), s.bob.token) {
		t.Fatal("a bearer is in the log")
	}
	s.task(t, s.bob, s.alice.id)
	if f := content(t, c.frame(t, 2*time.Second)); f.Kind != wake.KindAssigned {
		t.Fatalf("alice after the refusal: %+v, want assigned", f)
	}
}

// Review B2 (tested): a delivery whose registry entry names the pid with another procStart (the pid
// now belongs to another process) drops the entry and writes nothing; the unchanged registry is
// rung (the positive control).
func TestDirectoryDropsAReusedPidAtDelivery(t *testing.T) {
	s := startDirectory(t, nil)
	c := s.client(t, "s-1", "")
	s.register(t, s.alice, c, "worker")
	s.task(t, s.bob, s.alice.id)
	_ = c.frame(t, 2*time.Second)
	before := s.a.dir.Writes()
	s.setProcStart(t, c, "Sat Oct 24 09:00:00 2026")
	s.task(t, s.bob, s.alice.id)
	s.waitLog(t, fmt.Sprintf("dropped pid %d (worker, %s): its pid now names another process", c.pid, s.alice.id), 1)
	c.none(t, 500*time.Millisecond)
	if n := s.a.dir.Writes() - before; n != 0 || len(s.pids()) != 0 {
		t.Fatalf("after the procStart moved: %d writes, pids %v; want 0 and none", n, s.pids())
	}
}

// Review A41-9 (tested): RingAssigned's self-skip. A handoff to oneself rings no one; a handoff to
// bob rings bob's worker (the positive control).
func TestDirectoryHandoffToSelfRingsNobody(t *testing.T) {
	s := startDirectory(t, nil)
	a, b := s.client(t, "s-alice", ""), s.client(t, "s-bob", "")
	s.register(t, s.alice, a, "worker")
	s.register(t, s.bob, b, "worker")
	ctx := context.Background()
	mine, theirs := s.task(t, s.alice, ""), s.task(t, s.alice, "")
	_ = b.frame(t, wake.SlowWindow+time.Second) // the creates' broadcast to bob; alice's own is not rung
	_ = a.frame(t, time.Second)
	handoff := func(task service.Task, to string) {
		t.Helper()
		cl, err := s.a.core.Lifecycle().Claim(ctx, s.alice.caller(), service.ClaimTaskReq{ProjectID: s.project, TaskID: task.ID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.a.core.Lifecycle().Handoff(ctx, s.alice.caller(), service.HandoffTaskReq{ProjectID: s.project, TaskID: task.ID, LeaseID: cl.LeaseID, ToAgent: to, MemoryNote: "n"}); err != nil {
			t.Fatal(err)
		}
	}
	before := s.a.dir.Writes()
	handoff(mine, s.alice.id)
	a.none(t, wake.SlowWindow+500*time.Millisecond)
	if n := s.a.dir.Writes() - before; n != 0 {
		t.Fatalf("a handoff to oneself wrote %d doorbells, want 0", n)
	}
	handoff(theirs, s.bob.id)
	if f := content(t, b.frame(t, 2*time.Second)); f.Kind != wake.KindAssigned {
		t.Fatalf("the handoff to bob rang %+v, want assigned", f)
	}
}

// Review A41-3 (tested): each master-silent extra rings no master. A worker claims a task, then
// heartbeats, adds a context item, sets the summary, writes memory, attaches a file and comments
// (R2.1, ADR-0004 "comments ring nobody"): the master hears nothing; the worker's move rings it (the positive control).
func TestDirectoryMasterSilentTools(t *testing.T) {
	s := startDirectory(t, nil)
	w, m := s.client(t, "s-worker", ""), s.client(t, "s-master", "")
	s.register(t, s.alice, w, "worker")
	s.register(t, s.bob, m, "master")
	ctx := context.Background()
	al := s.alice.caller()
	task := s.task(t, s.bob, "")
	_ = w.frame(t, wake.SlowWindow+time.Second)
	cl, err := s.a.core.Lifecycle().Claim(ctx, al, service.ClaimTaskReq{ProjectID: s.project, TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	m.none(t, wake.SlowWindow+500*time.Millisecond) // the claim (ruled) is quiet before the extras start
	before := s.a.dir.Writes()
	steps := []struct {
		tool string
		run  func() error
	}{
		{"heartbeat_task", func() error {
			_, err := s.a.core.Lifecycle().Heartbeat(ctx, al, service.HeartbeatTaskReq{ProjectID: s.project, TaskID: task.ID, LeaseID: cl.LeaseID})
			return err
		}},
		{"add_context_item", func() error {
			_, err := s.a.core.Context().AddItem(ctx, al, service.AddContextItemReq{ProjectID: s.project, TaskID: task.ID, Kind: "note", Body: "b"})
			return err
		}},
		{"set_summary", func() error {
			got, err := s.a.core.Tasks().Get(ctx, al, service.GetTaskReq{ProjectID: s.project, TaskID: task.ID})
			if err != nil {
				return err
			}
			_, err = s.a.core.Context().SetSummary(ctx, al, service.SetSummaryReq{ProjectID: s.project, TaskID: task.ID, Summary: "s", Version: int64(got.Task.Version)})
			return err
		}},
		{"write_memory", func() error {
			_, err := s.a.core.Context().WriteMemory(ctx, al, service.WriteMemoryReq{ProjectID: s.project, TaskID: task.ID, Scope: domain.MemoryScopeTask, Key: "k", Value: "v"})
			return err
		}},
		{"attach_file", func() error {
			_, err := s.a.core.Media().AttachFile(ctx, al, service.AttachFileReq{ProjectID: s.project, TaskID: task.ID, FileName: "a.txt", Kind: "file", MimeType: "text/plain", Bytes: 3})
			return err
		}},
		{"add_comment", func() error {
			_, err := s.a.core.Comments().AddComment(ctx, al, service.AddCommentReq{ProjectID: s.project, TaskID: task.ID, Body: "a comment"})
			return err
		}},
	}
	for _, st := range steps {
		if err := st.run(); err != nil {
			t.Fatalf("%s: %v", st.tool, err)
		}
	}
	for _, st := range steps { // each wrote its row: the silence is the dispatcher's, not a missing row
		if !s.changeOf(t, st.tool) {
			t.Fatalf("no change row from %s", st.tool)
		}
	}
	m.none(t, wake.SlowWindow+500*time.Millisecond)
	if n := s.a.dir.Writes() - before; n != 0 {
		t.Fatalf("the master-silent tools wrote %d doorbells, want 0", n)
	}
	got, err := s.a.core.Tasks().Get(ctx, al, service.GetTaskReq{ProjectID: s.project, TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	s.move(t, s.alice, got.Task, domain.Blocked)
	if f := content(t, m.frame(t, wake.SlowWindow+time.Second)); f.Kind != wake.KindChanged {
		t.Fatalf("the move rang the master %+v, want changed", f)
	}
}

// changeOf reports whether the change log holds a row written by tool.
func (s *dirServer) changeOf(t *testing.T, tool string) bool {
	t.Helper()
	rows, err := s.st.ReadChanges(context.Background(), 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if strings.Contains(string(r.Payload), `"tool":"`+tool+`"`) {
			return true
		}
	}
	return false
}

// Review A41-5 (tested), replacing the refresh-reuse test /wake took with it: an entry registered
// with an OAuth access token is dropped by the sweep once a reused refresh token revokes the family.
func TestDirectoryDropsAnOAuthEntryAfterRefreshReuse(t *testing.T) {
	old := wakeSweep
	wakeSweep = 300 * time.Millisecond
	t.Cleanup(func() { wakeSweep = old })
	s := startDirectoryWith(t, nil, true)
	public := "http://" + s.tcp
	clientID, access, refresh := oauthGrant(t, public, "alice", "pw-alice")
	c := s.client(t, "s-oauth", "")
	s.register(t, person{id: s.alice.id, token: access}, c, "worker")
	s.waitLog(t, fmt.Sprintf("registered pid %d", c.pid), 1)
	time.Sleep(2 * wakeSweep)
	if len(s.pids()) != 1 {
		t.Fatalf("the OAuth entry did not hold before the reuse: %v", s.pids())
	}
	if m := tokenCall(t, public, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}}); m["_status"] != http.StatusOK {
		t.Fatalf("first refresh: %v", m)
	}
	if m := tokenCall(t, public, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}}); m["error"] != "invalid_grant" {
		t.Fatalf("refresh reuse: %v, want invalid_grant", m)
	}
	s.waitLog(t, fmt.Sprintf("dropped pid %d (worker, %s): its bearer no longer resolves", c.pid, s.alice.id), 1)
	if len(s.pids()) != 0 {
		t.Fatalf("pids %v after the family was revoked", s.pids())
	}
}
