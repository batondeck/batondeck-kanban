package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/auth/authfake"
	"github.com/batondeck/batondeck-kanban/internal/bus"
	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
	"github.com/batondeck/batondeck-kanban/internal/wake"
)

// countedSQLite counts the change-log reads the bus and /wake make: each is one statement.
type countedSQLite struct {
	*store.SQLite
	reads atomic.Int64
	// beforeReplay, when set, runs once before the first ReadChangesForScope (bus.Replay's read).
	beforeReplay atomic.Pointer[func()]
	// rechecks counts the reads a stream's recheck makes: GetToken, GetUser, ListProjects.
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

func (c *countedSQLite) ReadChanges(ctx context.Context, since int64, limit int) ([]store.Change, error) {
	c.reads.Add(1)
	return c.SQLite.ReadChanges(ctx, since, limit)
}

func (c *countedSQLite) ReadChangesForScope(ctx context.Context, scope string, since int64, limit int) ([]store.Change, error) {
	c.reads.Add(1)
	if f := c.beforeReplay.Swap(nil); f != nil {
		(*f)()
	}
	return c.SQLite.ReadChangesForScope(ctx, scope, since, limit)
}

// wakeServer is the real serve composition on a TCP and a Unix listener over a migrated SQLite
// store, with alice (a member of project P) holding a token.
type wakeServer struct {
	st      *countedSQLite
	tcp     string // host:port
	sock    string // socket path
	token   string
	alice   string // her identity, her token scope
	project string
	logs    *lockedLog
}

func startWake(t *testing.T, sealer service.Sealer) *wakeServer {
	t.Helper()
	ctx := context.Background()
	st := &countedSQLite{SQLite: storetest.SQLite(t)}
	user, err := (&auth.Accounts{Store: st}).Add(ctx, "alice", "Alice", "pw", false)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := (&auth.TokenService{Store: st}).Create(ctx, user.ID, "", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(ctx, store.CreateProjectInput{Name: "P", CreatedBy: user.ID}, store.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "bkw")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	logs := &lockedLog{}
	logger := log.New(logs, "", 0)
	lns, err := listenAll([]string{"tcp://127.0.0.1:0", "unix://" + filepath.Join(dir, "bk.sock")}, logger)
	if err != nil {
		t.Fatal(err)
	}
	serveCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- runServer(serveCtx, lns, st, "x", "", logger, sealer) }()
	t.Cleanup(func() {
		stop()
		if err := <-done; err != nil {
			t.Errorf("runServer: %v", err)
		}
	})
	return &wakeServer{st: st, tcp: lns[0].Addr().String(), sock: filepath.Join(dir, "bk.sock"), token: token, alice: user.ID, project: p.ID, logs: logs}
}

// ring appends a doorbell row as the service writes one (service.writeChanges) and returns its id.
func (s *wakeServer) ring(t *testing.T, scope, kind, agent, taskID string) int64 {
	t.Helper()
	payload, _ := json.Marshal(map[string]string{"kind": kind, "agent": agent, "taskId": taskID, "projectId": s.project, "boardId": "B-1", "tool": "test"})
	id, err := s.st.AppendChange(context.Background(), store.ChangeInput{Scope: scope, ProjectID: s.project, Kind: kind, Payload: payload, Ts: time.Now().UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// event is one SSE event: a frame, or a comment.
type event struct {
	frame   *wake.Frame
	id      string
	comment string
	at      time.Time
}

// sse is an open /wake stream.
type sse struct {
	resp   *http.Response
	events chan event
}

func (s *wakeServer) open(t *testing.T, query string) *sse {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://"+s.tcp+"/wake"+query, nil)
	req.Header.Set("Authorization", "Bearer "+s.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "text/event-stream" {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /wake%s: %d %q", query, resp.StatusCode, b)
	}
	st := &sse{resp: resp, events: make(chan event, 64)}
	go func() {
		defer close(st.events)
		r := bufio.NewReader(resp.Body)
		var ev event
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\n")
			switch {
			case line == "":
				ev.at = time.Now()
				st.events <- ev
				ev = event{}
			case strings.HasPrefix(line, ": "):
				ev.comment = strings.TrimPrefix(line, ": ")
			case strings.HasPrefix(line, "id: "):
				ev.id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "data: "):
				var f wake.Frame
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &f); err != nil {
					panic(err)
				}
				ev.frame = &f
			}
		}
	}()
	t.Cleanup(func() { resp.Body.Close() })
	if ev := st.next(t, time.Second); ev.comment != "ok" {
		t.Fatalf("first event %+v, want the ok comment", ev)
	}
	return st
}

func (s *sse) next(t *testing.T, within time.Duration) event {
	t.Helper()
	select {
	case ev, ok := <-s.events:
		if !ok {
			t.Fatal("the stream ended")
		}
		return ev
	case <-time.After(within):
		t.Fatalf("no event within %v", within)
	}
	return event{}
}

// none asserts no event arrives for d.
func (s *sse) none(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case ev := <-s.events:
		t.Fatalf("an event arrived: %+v (frame %+v)", ev, ev.frame)
	case <-time.After(d):
	}
}

func (s *sse) frame(t *testing.T, within time.Duration) wake.Frame {
	t.Helper()
	ev := s.next(t, within)
	if ev.frame == nil {
		t.Fatalf("event %+v, want a frame", ev)
	}
	if ev.id != fmt.Sprint(ev.frame.Cursor) {
		t.Fatalf("id line %q, frame cursor %d", ev.id, ev.frame.Cursor)
	}
	return *ev.frame
}

// AC1: a doorbell row for the caller's scope is one frame within 500 ms, carrying exactly the
// zero-authority fields; an idle stream issues no statement.
func TestWakeLiveFrameAndIdleCost(t *testing.T) {
	s := startWake(t, nil)
	w := s.open(t, "")
	before := s.st.reads.Load()
	w.none(t, 2*time.Second)
	if idle := s.st.reads.Load() - before; idle != 0 {
		t.Fatalf("an idle /wake stream cost %d change-log statements in 2 s, want 0", idle)
	}
	at := time.Now()
	id := s.ring(t, s.alice, "assigned", s.alice, "T-7")
	f := w.frame(t, time.Second)
	took := time.Since(at)
	t.Logf("row %d -> frame in %v", id, took)
	if took > 500*time.Millisecond {
		t.Fatalf("frame after %v, want within 500 ms", took)
	}
	want := wake.Frame{Agent: s.alice, Kind: "assigned", TaskID: "T-7", ProjectID: s.project, BoardID: "B-1", Cursor: id}
	if f != want {
		t.Fatalf("frame %+v, want %+v", f, want)
	}
	// broadcast to the project the caller is a member of rings; a changed row does not.
	s.ring(t, service.BroadcastScope(s.project), "changed", "", "T-8")
	id = s.ring(t, service.BroadcastScope(s.project), "broadcast", "", "T-8")
	if f = w.frame(t, time.Second); f.Kind != "broadcast" || f.Cursor != id || f.Agent != "" {
		t.Fatalf("broadcast frame %+v, want cursor %d", f, id)
	}
}

// Another identity's doorbells and another project's broadcasts never reach the stream; the
// caller's own, written after them, does (the positive control).
func TestWakeScopesIsolate(t *testing.T) {
	s := startWake(t, nil)
	w := s.open(t, "")
	s.ring(t, "U-someone-else", "assigned", "U-someone-else", "T-1")
	s.ring(t, service.BroadcastScope("P-not-hers"), "broadcast", "", "T-2")
	w.none(t, 700*time.Millisecond)
	id := s.ring(t, s.alice, "assigned", s.alice, "T-3")
	if f := w.frame(t, time.Second); f.Cursor != id {
		t.Fatalf("frame %+v, want her own at cursor %d", f, id)
	}
}

// AC2: close the stream, write two doorbells, reopen with ?since=<last cursor>: both replay in order,
// uncoalesced, before a new live one.
func TestWakeCatchUpReplaysThenGoesLive(t *testing.T) {
	s := startWake(t, nil)
	w := s.open(t, "")
	c0 := s.ring(t, s.alice, "assigned", s.alice, "T-1")
	if f := w.frame(t, time.Second); f.Cursor != c0 {
		t.Fatalf("live frame %+v, want cursor %d", f, c0)
	}
	w.resp.Body.Close()
	c1 := s.ring(t, s.alice, "assigned", s.alice, "T-2")
	s.ring(t, s.alice, "changed", "", "T-2")
	c2 := s.ring(t, s.alice, "assigned", s.alice, "T-3")
	w = s.open(t, fmt.Sprintf("?since=%d", c0))
	for _, want := range []struct {
		cursor int64
		task   string
	}{{c1, "T-2"}, {c2, "T-3"}} {
		if f := w.frame(t, time.Second); f.Cursor != want.cursor || f.TaskID != want.task {
			t.Fatalf("replayed %+v, want cursor %d %s", f, want.cursor, want.task)
		}
	}
	c3 := s.ring(t, s.alice, "assigned", s.alice, "T-4")
	if f := w.frame(t, time.Second); f.Cursor != c3 {
		t.Fatalf("live after the replay %+v, want cursor %d", f, c3)
	}
	w.none(t, 500*time.Millisecond)
}

// AC3: five doorbells of one kind inside 100 ms are exactly one frame, carrying the last cursor.
func TestWakeCoalescesABurst(t *testing.T) {
	s := startWake(t, nil)
	w := s.open(t, "")
	start := time.Now()
	var last int64
	for i := range 5 {
		last = s.ring(t, s.alice, "assigned", s.alice, fmt.Sprintf("T-%d", i))
		time.Sleep(15 * time.Millisecond)
	}
	if took := time.Since(start); took > 100*time.Millisecond {
		t.Fatalf("the five writes took %v, over the 100 ms the control needs", took)
	}
	f := w.frame(t, time.Second)
	if f.Cursor != last || f.TaskID != "T-4" {
		t.Fatalf("frame %+v, want one carrying the last cursor %d", f, last)
	}
	w.none(t, 700*time.Millisecond)
}

// The keepalive comment arrives every Keepalive on a stream with nothing to say, at no statement.
func TestWakeKeepalive(t *testing.T) {
	st := &countedSQLite{SQLite: storetest.SQLite(t)}
	b := bus.New(st, bus.Options{Watcher: st})
	t.Cleanup(b.Close)
	h := wake.Handler(wake.Options{Bus: b, Resolver: authfake.Resolver{Callers: map[string]domain.Caller{"t": {Identity: "U-1", TokenScope: "U-1"}}}, Logger: log.New(io.Discard, "", 0), Keepalive: 100 * time.Millisecond,
		Scopes: func(context.Context, domain.Caller) ([]string, error) { return []string{"U-1"}, nil }})
	srv := &http.Server{Handler: h}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	s := &wakeServer{tcp: ln.Addr().String(), token: "t"}
	w := s.open(t, "")
	before := st.reads.Load()
	var prev time.Time
	for i := range 3 {
		ev := w.next(t, time.Second)
		if ev.comment != "keepalive" {
			t.Fatalf("event %+v, want a keepalive", ev)
		}
		if i > 0 && ev.at.Sub(prev) < 80*time.Millisecond {
			t.Fatalf("keepalives %v apart, want about 100 ms", ev.at.Sub(prev))
		}
		prev = ev.at
	}
	if n := st.reads.Load() - before; n != 0 {
		t.Fatalf("%d statements while keeping alive, want 0", n)
	}
}

// /wake refuses a missing or unknown bearer (401 with the challenge) and a malformed cursor (400).
func TestWakeRefusals(t *testing.T) {
	s := startWake(t, nil)
	for _, tc := range []struct {
		bearer, query string
		status        int
	}{
		{"", "", http.StatusUnauthorized},
		{"bd_nope", "", http.StatusUnauthorized},
		{s.token, "?since=-1", http.StatusBadRequest},
		{s.token, "?since=abc", http.StatusBadRequest},
		{s.token, "?since=1&since=2", http.StatusBadRequest},
	} {
		req, _ := http.NewRequest(http.MethodGet, "http://"+s.tcp+"/wake"+tc.query, nil)
		if tc.bearer != "" {
			req.Header.Set("Authorization", "Bearer "+tc.bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatalf("%q %q: %d, want %d", tc.bearer, tc.query, resp.StatusCode, tc.status)
		}
		if tc.status == http.StatusUnauthorized && resp.Header.Get("WWW-Authenticate") != "Bearer" {
			t.Fatalf("401 without the Bearer challenge: %v", resp.Header)
		}
	}
}

func unixHTTP(path string) *http.Client {
	return &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}}
}

func post(t *testing.T, c *http.Client, url, bearer, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
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

// AC5: POST /internal/wake-session over the socket is 201 and writes the row, its token sealed;
// over TCP it is 404, as a route that does not exist is.
func TestInternalWakeSessionOnlyOverTheSocket(t *testing.T) {
	key, err := core.OpenKeyring(filepath.Join(t.TempDir(), "master.key"), func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	s := startWake(t, key)
	body := `{"socket":"/tmp/claude-inbox.sock","token":"inbox-secret-1"}`
	tcp := &http.Client{Timeout: 3 * time.Second}
	code, got := post(t, tcp, "http://"+s.tcp+"/internal/wake-session", s.token, body)
	if code != http.StatusNotFound {
		t.Fatalf("over TCP: %d %q, want 404", code, got)
	}
	// Over TCP the path answers every method exactly as a path that does not exist.
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodOptions, http.MethodPatch, http.MethodGet} {
		answer := func(path string) string {
			req, _ := http.NewRequest(m, "http://"+s.tcp+path, strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+s.token)
			resp, err := tcp.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			b, _ := io.ReadAll(resp.Body)
			return fmt.Sprintf("%d Allow=%q %q", resp.StatusCode, resp.Header.Get("Allow"), b)
		}
		route, missing := answer("/internal/wake-session"), answer("/internal/no-such-route")
		if route != missing {
			t.Fatalf("%s over TCP: /internal/wake-session answers %s, a missing path %s", m, route, missing)
		}
	}
	code, got = post(t, unixHTTP(s.sock), "http://x/internal/wake-session", s.token, body)
	if code != http.StatusCreated {
		t.Fatalf("over the socket: %d %q, want 201", code, got)
	}
	var resp service.RegisterWakeSessionResp
	if err := json.Unmarshal([]byte(got), &resp); err != nil || !strings.HasPrefix(resp.ID, "W-") || resp.ExpiresAt == "" {
		t.Fatalf("body %q: %+v %v", got, resp, err)
	}
	row, err := s.st.GetWakeSession(context.Background(), resp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.IdentityID != s.alice || row.SocketPath != "/tmp/claude-inbox.sock" || bytes.Contains(row.SocketTokenSealed, []byte("inbox-secret-1")) {
		t.Fatalf("row %+v", row)
	}
	if pt, err := key.Decrypt(row.SocketTokenSealed, service.SocketTokenAAD(row.ID)); err != nil || string(pt) != "inbox-secret-1" {
		t.Fatalf("the sealed token opens to %q, %v", pt, err)
	}
	// Refusals over the socket.
	for _, tc := range []struct {
		bearer, body string
		status       int
	}{
		{"", body, http.StatusUnauthorized},
		{"bd_nope", body, http.StatusUnauthorized},
		{s.token, `{"socket":"rel/inbox.sock"}`, http.StatusBadRequest},
		{s.token, `{"socket":"/a","extra":1}`, http.StatusBadRequest},
		{s.token, `{"socket":"/a"} {}`, http.StatusBadRequest},
		{s.token, `not json`, http.StatusBadRequest},
		{s.token, `{"socket":"/a","token":"` + strings.Repeat("x", 17<<10) + `"}`, http.StatusBadRequest},
	} {
		if code, got := post(t, unixHTTP(s.sock), "http://x/internal/wake-session", tc.bearer, tc.body); code != tc.status {
			t.Fatalf("%q %.40q: %d %q, want %d", tc.bearer, tc.body, code, got, tc.status)
		}
	}
}

// With no keyring a token is refused 500 INTERNAL naming the setting, and a session without one
// still registers.
func TestInternalWakeSessionWithoutAKeyring(t *testing.T) {
	s := startWake(t, nil)
	code, got := post(t, unixHTTP(s.sock), "http://x/internal/wake-session", s.token, `{"socket":"/tmp/i.sock","token":"t"}`)
	if code != http.StatusInternalServerError || !strings.Contains(got, core.MasterKeyEnv) || !strings.Contains(got, `"INTERNAL"`) {
		t.Fatalf("a token with no keyring: %d %q", code, got)
	}
	if code, got = post(t, unixHTTP(s.sock), "http://x/internal/wake-session", s.token, `{"socket":"/tmp/i.sock"}`); code != http.StatusCreated {
		t.Fatalf("no token, no keyring: %d %q, want 201", code, got)
	}
}

// AC4 at the server: the same handler answers on the Unix socket as on TCP.
func TestServeAnswersOnTheUnixSocket(t *testing.T) {
	s := startWake(t, nil)
	resp, err := unixHTTP(s.sock).Get("http://x/healthz")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(b) != healthBody("x") {
		t.Fatalf("healthz over the socket: %d %q", resp.StatusCode, b)
	}
	if fi, err := os.Stat(s.sock); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket %v %v, want mode 0600", fi, err)
	}
}

// The keyring chain: the environment's key, else master.key beside the SQLite file, else (Postgres)
// beside the config file, else none and one WARN line.
func TestOpenKeyringChain(t *testing.T) {
	none := func(string) (string, bool) { return "", false }
	dir := t.TempDir()
	logs := &lockedLog{}
	logger := log.New(logs, "", 0)
	sqlite := &core.Config{Database: core.Database{URL: filepath.Join(dir, "db", "bk.db")}}
	if err := os.MkdirAll(filepath.Join(dir, "db"), 0o700); err != nil {
		t.Fatal(err)
	}
	if k, err := openKeyring(sqlite, "", none, logger); err != nil || k == nil {
		t.Fatalf("sqlite: %v %v", k, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "db", "master.key")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("sqlite master.key: %v %v", fi, err)
	}
	pg := &core.Config{Database: core.Database{URL: "postgres://u@h/db"}}
	cfgPath := filepath.Join(dir, "etc", "bk.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if k, err := openKeyring(pg, cfgPath, none, logger); err != nil || k == nil {
		t.Fatalf("postgres with a config file: %v %v", k, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "etc", "master.key")); err != nil {
		t.Fatalf("postgres master.key beside the config: %v", err)
	}
	k, err := openKeyring(pg, "", none, logger)
	if err != nil || k != nil {
		t.Fatalf("postgres with neither: %v %v, want no sealer", k, err)
	}
	if !strings.Contains(logs.String(), "WARN no keyring: set "+core.MasterKeyEnv) {
		t.Fatalf("log %q: want the WARN line", logs.String())
	}
	key := strings.Repeat("A", 43) + "="
	env := func(n string) (string, bool) { return key, n == core.MasterKeyEnv }
	if k, err := openKeyring(pg, "", env, logger); err != nil || k == nil {
		t.Fatalf("postgres with %s: %v %v", core.MasterKeyEnv, k, err)
	}
}

// serveHandler serves h on a fresh TCP listener and returns its host:port.
func serveHandler(t *testing.T, h http.Handler) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().String()
}

// A stream whose subscription overflows ends with ": dropped", naming a cursor from which a replay
// recovers every doorbell it had not written; a stream with nothing written yet past its since names
// that since.
func TestWakeDroppedStreamNamesASafeCursor(t *testing.T) {
	ctx := context.Background()
	st := &countedSQLite{SQLite: storetest.SQLite(t)}
	b := bus.New(st, bus.Options{Watcher: st})
	t.Cleanup(b.Close)
	h := wake.Handler(wake.Options{Bus: b, Resolver: authfake.Resolver{Callers: map[string]domain.Caller{"t": {Identity: "U-1", TokenScope: "U-1"}}},
		Logger: log.New(io.Discard, "", 0), Buffer: 1, Quiet: 20 * time.Millisecond,
		Scopes: func(context.Context, domain.Caller) ([]string, error) { return []string{"U-1"}, nil }})
	s := &wakeServer{st: st, tcp: serveHandler(t, h), token: "t", project: "P-1"}
	w := s.open(t, "?since=0")
	first := s.ring(t, "U-1", "assigned", "U-1", "T-0")
	if f := w.frame(t, time.Second); f.Cursor != first {
		t.Fatalf("frame %+v, want cursor %d", f, first)
	}
	time.Sleep(50 * time.Millisecond) // the stream's next turn records first as written
	var ids []int64
	if err := st.Atomically(ctx, func(tx store.Store) error {
		for i := range 50 {
			payload, _ := json.Marshal(map[string]string{"kind": "assigned", "agent": "U-1", "taskId": fmt.Sprintf("T-%d", i+1), "projectId": "P-1", "boardId": "B-1"})
			id, err := tx.AppendChange(ctx, store.ChangeInput{Scope: "U-1", ProjectID: "P-1", Kind: "assigned", Payload: payload, Ts: time.Now().UnixMilli()})
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var ev event
	for ev = w.next(t, 2*time.Second); ev.frame != nil; ev = w.next(t, 2*time.Second) {
	}
	want := fmt.Sprintf("dropped, reconnect with since=%d", first)
	if ev.comment != want {
		t.Fatalf("last event %+v, want the comment %q", ev, want)
	}
	if _, open := <-w.events; open {
		t.Fatal("the stream went on after the dropped comment")
	}
	w = s.open(t, fmt.Sprintf("?since=%d", first))
	for _, id := range ids {
		if f := w.frame(t, time.Second); f.Cursor != id {
			t.Fatalf("replay after the drop: %+v, want cursor %d", f, id)
		}
	}
}

// A change committed between the stream's Subscribe and its Replay arrives both ways (the live
// subscription and the replay) and is written once.
func TestWakeReplayAndLiveOverlapWritesOnce(t *testing.T) {
	st := &countedSQLite{SQLite: storetest.SQLite(t)}
	b := bus.New(st, bus.Options{Watcher: st})
	t.Cleanup(b.Close)
	h := wake.Handler(wake.Options{Bus: b, Resolver: authfake.Resolver{Callers: map[string]domain.Caller{"t": {Identity: "U-1", TokenScope: "U-1"}}},
		Logger: log.New(io.Discard, "", 0), Quiet: 20 * time.Millisecond,
		Scopes: func(context.Context, domain.Caller) ([]string, error) { return []string{"U-1"}, nil }})
	s := &wakeServer{st: st, tcp: serveHandler(t, h), token: "t", project: "P-1"}
	before := s.ring(t, "U-1", "assigned", "U-1", "T-before")
	var overlap atomic.Int64
	hook := func() {
		// Subscribed already: this commit reaches the subscription, and the replay reads it.
		overlap.Store(s.ring(t, "U-1", "assigned", "U-1", "T-overlap"))
		time.Sleep(100 * time.Millisecond) // the bus tail delivers it to the subscription meanwhile
	}
	st.beforeReplay.Store(&hook)
	w := s.open(t, fmt.Sprintf("?since=%d", before-1))
	got := []int64{w.frame(t, time.Second).Cursor, w.frame(t, time.Second).Cursor}
	if got[0] != before || got[1] != overlap.Load() {
		t.Fatalf("frames %v, want [%d %d]", got, before, overlap.Load())
	}
	w.none(t, 300*time.Millisecond)
}

// /wake answers 503 while the bus is closed and 500 when the caller's scopes cannot be read.
func TestWakeRefusesWhenItCannotSubscribe(t *testing.T) {
	st := storetest.SQLite(t)
	resolver := authfake.Resolver{Callers: map[string]domain.Caller{"t": {Identity: "U-1", TokenScope: "U-1"}}}
	closed := bus.New(st, bus.Options{})
	closed.Close()
	scopes := func(context.Context, domain.Caller) ([]string, error) { return []string{"U-1"}, nil }
	failing := func(context.Context, domain.Caller) ([]string, error) { return nil, fmt.Errorf("store down") }
	open := bus.New(st, bus.Options{})
	t.Cleanup(open.Close)
	for _, tc := range []struct {
		name   string
		o      wake.Options
		status int
	}{
		{"closed bus", wake.Options{Bus: closed, Scopes: scopes}, http.StatusServiceUnavailable},
		{"scopes fail", wake.Options{Bus: open, Scopes: failing}, http.StatusInternalServerError},
	} {
		tc.o.Resolver, tc.o.Logger = resolver, log.New(io.Discard, "", 0)
		addr := serveHandler(t, wake.Handler(tc.o))
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/wake", nil)
		req.Header.Set("Authorization", "Bearer t")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatalf("%s: %d, want %d", tc.name, resp.StatusCode, tc.status)
		}
	}
}

// recheckServer is /wake over the real resolver and service core, with a short Recheck and account
// cache, for the tests of what an open stream does when its bearer or memberships change.
type recheckServer struct {
	*wakeServer
	accounts *auth.Accounts
	tokens   *auth.TokenService
	tokenID  string
	resolver *auth.Service
	bus      *bus.Bus
}

func startRecheck(t *testing.T, recheck, accountTTL time.Duration) *recheckServer {
	t.Helper()
	return startRecheckWith(t, wake.Options{Recheck: recheck}, accountTTL)
}

// startRecheckWith is startRecheck with the handler's tunables in o (Bus, Resolver, Scopes and
// Logger are filled in here).
func startRecheckWith(t *testing.T, o wake.Options, accountTTL time.Duration) *recheckServer {
	t.Helper()
	ctx := context.Background()
	st := &countedSQLite{SQLite: storetest.SQLite(t)}
	accounts, tokens := &auth.Accounts{Store: st}, &auth.TokenService{Store: st}
	user, err := accounts.Add(ctx, "alice", "Alice", "pw", false)
	if err != nil {
		t.Fatal(err)
	}
	token, info, err := tokens.Create(ctx, user.ID, "", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	p, err := st.CreateProject(ctx, store.CreateProjectInput{Name: "P", CreatedBy: user.ID}, store.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	quiet := log.New(io.Discard, "", 0)
	resolver := &auth.Service{Accounts: accounts, Tokens: tokens, Sessions: st, AccountCacheTTL: accountTTL}
	sc := service.New(st, service.Options{Logger: quiet})
	t.Cleanup(func() { sc.Close(context.Background()) })
	b := bus.New(st, bus.Options{Watcher: st})
	t.Cleanup(b.Close)
	o.Bus, o.Resolver, o.Scopes, o.Logger = b, resolver, sc.WakeScopes, quiet
	if o.Quiet == 0 {
		o.Quiet = 20 * time.Millisecond
	}
	ws := &wakeServer{st: st, tcp: serveHandler(t, wake.Handler(o)), token: token, alice: user.ID, project: p.ID}
	return &recheckServer{wakeServer: ws, accounts: accounts, tokens: tokens, tokenID: info.ID, resolver: resolver, bus: b}
}

// last waits for the stream's final comment and asserts the stream then ends.
func (s *sse) last(t *testing.T, within time.Duration) string {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case ev, ok := <-s.events:
			if !ok {
				t.Fatal("the stream ended with no comment")
			}
			if ev.comment == "keepalive" || ev.frame != nil {
				continue
			}
			if _, open := <-s.events; open {
				t.Fatalf("the stream went on after %q", ev.comment)
			}
			return ev.comment
		case <-deadline:
			t.Fatalf("the stream did not end within %v", within)
		}
	}
}

// A revoked token ends its open stream within one recheck.
func TestWakeRecheckEndsARevokedToken(t *testing.T) {
	s := startRecheck(t, 200*time.Millisecond, time.Minute)
	w := s.open(t, "")
	if _, err := s.tokens.Revoke(context.Background(), s.tokenID); err != nil {
		t.Fatal(err)
	}
	if got := w.last(t, 500*time.Millisecond); got != "reauthenticate, reconnect" {
		t.Fatalf("last comment %q, want reauthenticate", got)
	}
}

// A disabled user's open stream ends within one recheck once the account cache has expired.
func TestWakeRecheckEndsADisabledUser(t *testing.T) {
	s := startRecheck(t, 200*time.Millisecond, 100*time.Millisecond)
	w := s.open(t, "")
	if _, err := s.accounts.Disable(context.Background(), "alice"); err != nil {
		t.Fatal(err)
	}
	if got := w.last(t, 500*time.Millisecond); got != "reauthenticate, reconnect" {
		t.Fatalf("last comment %q, want reauthenticate", got)
	}
}

// Removed from her project, alice's stream ends naming the cursor to reconnect from, and the
// reconnected stream no longer hears the project's broadcasts while it hears her own doorbells.
func TestWakeRecheckEndsAChangedMembership(t *testing.T) {
	s := startRecheck(t, 200*time.Millisecond, time.Minute)
	w := s.open(t, "?since=0")
	first := s.ring(t, s.alice, "assigned", s.alice, "T-1")
	if f := w.frame(t, time.Second); f.Cursor != first {
		t.Fatalf("frame %+v", f)
	}
	if err := s.st.RemoveMember(context.Background(), s.project, s.alice); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("scopes changed, reconnect with since=%d", first)
	if got := w.last(t, 500*time.Millisecond); got != want {
		t.Fatalf("last comment %q, want %q", got, want)
	}
	w = s.open(t, fmt.Sprintf("?since=%d", first))
	s.ring(t, service.BroadcastScope(s.project), "broadcast", "", "T-2")
	own := s.ring(t, s.alice, "assigned", s.alice, "T-3")
	if f := w.frame(t, time.Second); f.Cursor != own {
		t.Fatalf("after the removal: frame %+v, want only her own at cursor %d", f, own)
	}
}

// The positive control and the statement pin: a stream whose bearer and memberships hold stays open
// across rechecks, still delivers, costs 3 statements per recheck (GetToken, GetUser, ListProjects;
// GetUser is skipped while the account cache is fresher than the recheck, so 3 is the ceiling), and
// none in between.
func TestWakeRecheckKeepsAValidStreamAtThreeStatements(t *testing.T) {
	const every = 300 * time.Millisecond
	s := startRecheck(t, every, every/2) // the account cache always stale at a recheck
	w := s.open(t, "")
	reads := s.st.reads.Load() // after the bus tail's start
	time.Sleep(every / 2)      // between the connect and the first recheck
	base := s.st.rechecks.Load()
	var deltas []int64
	for range 3 {
		time.Sleep(every)
		n := s.st.rechecks.Load()
		deltas = append(deltas, n-base)
		base = n
	}
	for i, d := range deltas {
		if d != 3 {
			t.Fatalf("recheck %d cost %d statements, want 3 (all: %v)", i+1, d, deltas)
		}
	}
	if n := s.st.reads.Load() - reads; n != 0 {
		t.Fatalf("%d change-log reads while idle, want 0", n)
	}
	id := s.ring(t, s.alice, "assigned", s.alice, "T-9")
	if f := w.frame(t, time.Second); f.Cursor != id {
		t.Fatalf("after %d rechecks: frame %+v, want cursor %d", len(deltas), f, id)
	}
}

// A catch-up longer than one Replay page reads the next page from where the last one ended:
// 1,001 doorbells replay once each, in order.
func TestWakeCatchUpPagesPastOneReplay(t *testing.T) {
	ctx := context.Background()
	st := &countedSQLite{SQLite: storetest.SQLite(t)}
	b := bus.New(st, bus.Options{Watcher: st})
	t.Cleanup(b.Close)
	h := wake.Handler(wake.Options{Bus: b, Resolver: authfake.Resolver{Callers: map[string]domain.Caller{"t": {Identity: "U-1", TokenScope: "U-1"}}},
		Logger: log.New(io.Discard, "", 0), Scopes: func(context.Context, domain.Caller) ([]string, error) { return []string{"U-1"}, nil }})
	s := &wakeServer{st: st, tcp: serveHandler(t, h), token: "t", project: "P-1"}
	const n = 1001
	var ids []int64
	if err := st.Atomically(ctx, func(tx store.Store) error {
		for i := range n {
			payload, _ := json.Marshal(map[string]string{"kind": "assigned", "agent": "U-1", "taskId": fmt.Sprintf("T-%d", i), "projectId": "P-1", "boardId": "B-1"})
			id, err := tx.AppendChange(ctx, store.ChangeInput{Scope: "U-1", ProjectID: "P-1", Kind: "assigned", Payload: payload, Ts: time.Now().UnixMilli()})
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	w := s.open(t, "?since=0")
	for i, id := range ids {
		if f := w.frame(t, time.Second); f.Cursor != id {
			t.Fatalf("replayed frame %d: cursor %d, want %d", i, f.Cursor, id)
		}
	}
	w.none(t, 300*time.Millisecond)
}

// A recheck that cannot read the scopes ends the stream rather than trusting them, naming its cursor.
func TestWakeRecheckEndsWhenItCannotConfirm(t *testing.T) {
	st := &countedSQLite{SQLite: storetest.SQLite(t)}
	b := bus.New(st, bus.Options{Watcher: st})
	t.Cleanup(b.Close)
	var calls atomic.Int64
	h := wake.Handler(wake.Options{Bus: b, Resolver: authfake.Resolver{Callers: map[string]domain.Caller{"t": {Identity: "U-1", TokenScope: "U-1"}}},
		Logger: log.New(io.Discard, "", 0), Recheck: 200 * time.Millisecond,
		Scopes: func(context.Context, domain.Caller) ([]string, error) {
			if calls.Add(1) > 1 {
				return nil, fmt.Errorf("store down")
			}
			return []string{"U-1"}, nil
		}})
	s := &wakeServer{st: st, tcp: serveHandler(t, h), token: "t"}
	w := s.open(t, "?since=0")
	if got := w.last(t, 500*time.Millisecond); got != "recheck failed, reconnect with since=0" {
		t.Fatalf("last comment %q", got)
	}
}

// backlog commits n doorbells for alice in one transaction and returns the last id.
func (s *recheckServer) backlog(t *testing.T, n int) int64 {
	t.Helper()
	ctx := context.Background()
	var last int64
	if err := s.st.Atomically(ctx, func(tx store.Store) error {
		for i := range n {
			payload, _ := json.Marshal(map[string]string{"kind": "assigned", "agent": s.alice, "taskId": fmt.Sprintf("T-%d", i), "projectId": s.project, "boardId": "B-1"})
			id, err := tx.AppendChange(ctx, store.ChangeInput{Scope: s.alice, ProjectID: s.project, Kind: "assigned", Payload: payload, Ts: time.Now().UnixMilli()})
			if err != nil {
				return err
			}
			last = id
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return last
}

// rawWake opens GET path on a raw TCP connection with a 4 KiB receive buffer, so the server's writes
// block on the reader's pace.
func rawWake(t *testing.T, addr, token, path string) (net.Conn, *bufio.Reader) {
	t.Helper()
	d := net.Dialer{Control: func(_, _ string, c syscall.RawConn) error {
		return c.Control(func(fd uintptr) { _ = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_RCVBUF, 4096) })
	}}
	c, err := d.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: x\r\nAuthorization: Bearer %s\r\n\r\n", path, token)
	return c, bufio.NewReaderSize(c, 512)
}

// A stream still in catch-up is rechecked like a live one: revoked while it replays a backlog to a
// slow reader, the server ends it within one Recheck of the revocation (its subscription closes),
// no doorbell committed after the revocation reaches the reader later than that, and its last
// comment names its cursor. The fast reader is the positive control (the reviewer's round-2 repro,
// made a test). The slow reader still drains, after the end, frames written before it.
func TestWakeCatchUpIsRechecked(t *testing.T) {
	for _, slow := range []bool{true, false} {
		t.Run(fmt.Sprintf("slow=%v", slow), func(t *testing.T) {
			const every = 200 * time.Millisecond
			s := startRecheck(t, every, every)
			s.backlog(t, 8000)
			_, r := rawWake(t, s.tcp, s.token, "/wake?since=0")
			start := time.Now()
			var revokedAt time.Time
			var lastBefore int64 = -1
			serverEnd := make(chan time.Duration, 1)
			var end string
			var late []string
			for time.Since(start) < 30*time.Second {
				line, err := r.ReadString('\n')
				if err != nil {
					t.Fatalf("the stream ended without a comment: %v", err)
				}
				if slow {
					time.Sleep(150 * time.Microsecond)
				}
				if revokedAt.IsZero() && time.Since(start) > 500*time.Millisecond {
					if _, err := s.tokens.Revoke(context.Background(), s.tokenID); err != nil {
						t.Fatal(err)
					}
					revokedAt = time.Now()
					lastBefore = s.backlog(t, 1) // every id above it commits after the revocation
					s.backlog(t, 20)
					go func() {
						for s.bus.Running() && time.Since(revokedAt) < 10*time.Second {
							time.Sleep(5 * time.Millisecond)
						}
						serverEnd <- time.Since(revokedAt)
					}()
				}
				line = strings.TrimSpace(line)
				if id, ok := strings.CutPrefix(line, "id: "); ok && lastBefore > 0 {
					if n, _ := strconv.ParseInt(id, 10, 64); n > lastBefore && time.Since(revokedAt) > every {
						late = append(late, fmt.Sprintf("id %d at +%v", n, time.Since(revokedAt).Round(time.Millisecond)))
					}
				}
				if strings.HasPrefix(line, ": ") && line != ": ok" && line != ": keepalive" {
					end = strings.TrimPrefix(line, ": ")
					break
				}
			}
			ended := <-serverEnd
			t.Logf("slow=%v: the server ended the stream %v after the revocation (Recheck %v); the reader saw %q %v after it", slow, ended.Round(time.Millisecond), every, end, time.Since(revokedAt).Round(time.Millisecond))
			if ended > every+100*time.Millisecond {
				t.Fatalf("the server ended the stream %v after the revocation, want within one Recheck (%v)", ended, every)
			}
			if len(late) > 0 {
				t.Fatalf("%d doorbell(s) committed after the revocation reached the reader more than one Recheck after it: %v", len(late), late)
			}
			if !strings.HasPrefix(end, "reauthenticate, reconnect with since=") {
				t.Fatalf("last comment %q, want reauthenticate with a cursor", end)
			}
		})
	}
}

// A peer that stops reading is cut off by the write deadline even when no recheck is due; a peer
// that reads (the positive control) keeps its stream.
func TestWakeWriteDeadlineCutsAStalledReader(t *testing.T) {
	for _, stall := range []bool{true, false} {
		t.Run(fmt.Sprintf("stall=%v", stall), func(t *testing.T) {
			s := startRecheckWith(t, wake.Options{Recheck: time.Hour, WriteTimeout: 300 * time.Millisecond}, time.Hour)
			s.backlog(t, 8000)
			_, r := rawWake(t, s.tcp, s.token, "/wake?since=0")
			for { // through the response head and ": ok": the stream is subscribed
				line, err := r.ReadString('\n')
				if err != nil {
					t.Fatal(err)
				}
				if line == ": ok\n" {
					break
				}
			}
			if !s.bus.Running() {
				t.Fatal("the stream is not subscribed after its ok comment")
			}
			if !stall {
				go func() { _, _ = io.Copy(io.Discard, r) }()
			}
			deadline := time.Now().Add(3 * time.Second)
			for s.bus.Running() && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if stall && s.bus.Running() {
				t.Fatal("a stalled reader's stream still holds its subscription 3 s after a 300 ms write deadline")
			}
			if !stall && !s.bus.Running() {
				t.Fatal("a reading peer's stream was cut")
			}
		})
	}
}

// A disable ends an open stream within one recheck even while other traffic keeps the resolver's
// account cache warm: the recheck reads the account from the store.
func TestWakeRecheckEndsADisabledUserWithAWarmCache(t *testing.T) {
	const every = 200 * time.Millisecond
	s := startRecheck(t, every, time.Minute)
	w := s.open(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { // another of alice's clients, keeping the cache entry fresh
		for ctx.Err() == nil {
			_, _ = s.resolver.ResolveCaller(ctx, s.token)
			time.Sleep(5 * time.Millisecond)
		}
	}()
	time.Sleep(50 * time.Millisecond)
	if _, err := s.accounts.Disable(context.Background(), "alice"); err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	if _, err := s.resolver.ResolveCaller(context.Background(), s.token); err != nil {
		t.Fatalf("the cache is not warm (the control): %v", err)
	}
	if got := w.last(t, every+150*time.Millisecond); got != "reauthenticate, reconnect" {
		t.Fatalf("last comment %q", got)
	}
	t.Logf("disable -> end %v with a warm cache (Recheck %v)", time.Since(at).Round(time.Millisecond), every)
}

// Added to a project, alice's stream ends so that she reconnects into its broadcasts.
func TestWakeRecheckEndsAnAddedMembership(t *testing.T) {
	s := startRecheck(t, 200*time.Millisecond, time.Minute)
	ctx := context.Background()
	other, err := s.st.CreateProject(ctx, store.CreateProjectInput{Name: "Q", CreatedBy: "U-bob"}, store.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	w := s.open(t, "?since=0")
	if _, err := s.st.AddMember(ctx, other.ID, s.alice, domain.RoleWorker, "U-bob"); err != nil {
		t.Fatal(err)
	}
	if got := w.last(t, 500*time.Millisecond); got != "scopes changed, reconnect with since=0" {
		t.Fatalf("last comment %q", got)
	}
	w = s.open(t, "?since=0")
	id := s.ring(t, service.BroadcastScope(other.ID), "broadcast", "", "T-1")
	if f := w.frame(t, time.Second); f.Cursor != id {
		t.Fatalf("after reconnecting: %+v, want the new project's broadcast %d", f, id)
	}
}

// ": reauthenticate" names the cursor to resume from, as every other ending does.
func TestWakeReauthenticateNamesTheCursor(t *testing.T) {
	s := startRecheck(t, 200*time.Millisecond, time.Minute)
	w := s.open(t, "?since=0")
	id := s.ring(t, s.alice, "assigned", s.alice, "T-1")
	if f := w.frame(t, time.Second); f.Cursor != id {
		t.Fatalf("frame %+v", f)
	}
	if _, err := s.tokens.Revoke(context.Background(), s.tokenID); err != nil {
		t.Fatal(err)
	}
	if got, want := w.last(t, 500*time.Millisecond), fmt.Sprintf("reauthenticate, reconnect with since=%d", id); got != want {
		t.Fatalf("last comment %q, want %q", got, want)
	}
}
