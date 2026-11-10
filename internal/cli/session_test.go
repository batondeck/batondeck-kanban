package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// `session register|unregister` (R3.3) against the real serve composition over its Unix socket
// (startDirectory): the client is a `sleep` with a fake Claude Code registry entry, found by the
// socket named in CLAUDE_CODE_MESSAGING_SOCKET.

// sessionEnv points the client at s's Unix socket as p, for the session whose socket is socket, and
// at s's registry. It sets the environment and wakeRegistry, so these tests do not run in parallel.
func sessionEnv(t *testing.T, s *dirServer, p person, socket string) {
	t.Helper()
	old := wakeRegistry
	wakeRegistry = func() string { return s.reg }
	t.Cleanup(func() { wakeRegistry = old })
	t.Setenv("BATONDECK_CONFIG", "")
	t.Setenv("BATONDECK_LISTEN", "tcp://"+s.tcp+",unix://"+s.sock)
	t.Setenv("BATONDECK_TOKEN", p.token)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", socket)
	t.Setenv("CLAUDE_CODE_MESSAGING_TOKEN", "child-token-never-sent")
}

func (s *dirServer) rows(t *testing.T) []store.WakeSession {
	t.Helper()
	rows, err := s.st.ListWakeSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// oneLine runs the command and asserts exit 0, no stdout and exactly one stderr line holding want.
func oneLine(t *testing.T, want string, args ...string) string {
	t.Helper()
	code, out, errs := run(args...)
	if code != 0 || out != "" || strings.Count(errs, "\n") != 1 || !strings.Contains(errs, want) {
		t.Fatalf("%v: exit %d, stdout %q, stderr %q; want exit 0, no stdout, one line holding %q", args, code, out, errs, want)
	}
	t.Logf("%v: exit 0, stderr %q", args, errs)
	return errs
}

// AC1/AC3 (tested): register is 201 and a wake_sessions row with the registry's pid, the role, the
// agent label, the socket, the registry's sessionId and procStart, and no socket token (the
// session's CLAUDE_CODE_MESSAGING_TOKEN is its child token, R3.1 §R3.3); unregister removes it.
func TestSessionRegisterAndUnregister(t *testing.T) {
	s := startDirectory(t, nil)
	decoy := s.client(t, "s-decoy", "")
	c := s.client(t, "s-mine", "")
	sessionEnv(t, s, s.alice, c.sock)
	oneLine(t, fmt.Sprintf("pid %d registered as worker", c.pid), "session", "register", "--role", "worker", "--agent", "w1")
	rows := s.rows(t)
	if len(rows) != 1 {
		t.Fatalf("rows %+v", rows)
	}
	r := rows[0]
	t.Logf("wake_sessions row: id=%s pid=%d role=%s identity=%s agent=%s socket=%s session=%s procStart=%q socketToken=%dB",
		r.ID, r.PID, r.Role, r.IdentityID, r.AgentID, r.SocketPath, r.ClientSessionID, r.ProcStart, len(r.SocketTokenSealed))
	if r.PID != c.pid || r.PID == decoy.pid || r.Role != "worker" || r.IdentityID != s.alice.id || r.SocketPath != c.sock ||
		r.ClientSessionID != c.session || r.ProcStart == "" || len(r.SocketTokenSealed) != 0 || !strings.Contains(r.AgentID, "w1") {
		t.Fatalf("the row %+v", r)
	}
	oneLine(t, fmt.Sprintf("pid %d removed", c.pid), "session", "unregister")
	if rows := s.rows(t); len(rows) != 0 || len(s.pids()) != 0 {
		t.Fatalf("after unregister: rows %+v, pids %v", rows, s.pids())
	}
	// A master registers the same way.
	oneLine(t, "registered as master", "session", "register", "--role", "master")
	if rows := s.rows(t); len(rows) != 1 || rows[0].Role != "master" {
		t.Fatalf("master rows %+v", rows)
	}
}

// The client's pid without a messaging socket (tested): the parent process, here `go test`, which
// is this uid's and alive, registered with no socket.
func TestSessionWithoutAMessagingSocketIsTheParent(t *testing.T) {
	if _, name, err := core.ProcessParent(os.Getppid()); err == nil && hookShells[name] {
		t.Skipf("this test's parent is the shell %s, which the client looks past: run it under go test", name)
	}
	s := startDirectory(t, nil)
	sessionEnv(t, s, s.alice, "")
	oneLine(t, fmt.Sprintf("pid %d registered as worker", os.Getppid()), "session", "register", "--role", "worker")
	if rows := s.rows(t); len(rows) != 1 || rows[0].PID != int64(os.Getppid()) || rows[0].SocketPath != "" {
		t.Fatalf("rows %+v, want the parent %d", rows, os.Getppid())
	}
	oneLine(t, "removed", "session", "unregister")
	if rows := s.rows(t); len(rows) != 0 {
		t.Fatalf("rows %+v", rows)
	}
}

// The client under a hook's shells (tested): without a messaging socket it is not the hook's shell.
// The plugin's hook is a /bin/sh script that Claude Code runs through `sh -c`, so the command's
// parent is that script's shell, which exits the moment the command returns: it was registered,
// reported as registered, and dropped in the same second. Here this test process plays the client
// and runs a script of the hook's shape the same way; the registered pid is this process, and it
// stays.
func TestSessionWithoutAMessagingSocketUnderAHookShellIsTheClient(t *testing.T) {
	s := startDirectory(t, nil)
	sessionEnv(t, s, s.alice, "")
	script := filepath.Join(t.TempDir(), "session.sh")
	body := fmt.Sprintf("#!/bin/sh\n%q -test.run='^TestSessionHookChild$' >&2\nexit 0\n", os.Args[0])
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	hook := exec.Command("sh", "-c", fmt.Sprintf("%q start", script))
	hook.Env = append(os.Environ(), "BK_SESSION_HOOK_CHILD=1")
	var stderr strings.Builder
	hook.Stderr = &stderr
	if out, err := hook.Output(); err != nil || len(out) != 0 {
		t.Fatalf("the hook: %v, stdout %q, stderr %q", err, out, stderr.String())
	}
	t.Logf("the hook's stderr: %q", stderr.String())
	want := fmt.Sprintf("pid %d registered as worker", os.Getpid())
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("the hook's stderr %q lacks %q: the client is this process, the shells' parent", stderr.String(), want)
	}
	time.Sleep(200 * time.Millisecond) // the shells have exited; an entry for one of them would be gone
	if rows := s.rows(t); len(rows) != 1 || rows[0].PID != int64(os.Getpid()) || !slices.Contains(s.pids(), int64(os.Getpid())) {
		t.Fatalf("rows %+v, pids %v; want this process, %d, registered and kept", rows, s.pids(), os.Getpid())
	}
}

// The walk past the hook's shells (tested on a process table): it stops at the nearest ancestor that
// is not a shell, which may be pid 1 (a client that is a container's first process; the walk
// stopped short of pid 1 and registered the hook's shell), and never at another uid's process or one
// it cannot read.
func TestClientProcessWalksPastShells(t *testing.T) {
	type proc struct {
		ppid int
		name string
		uid  int
	}
	me := os.Getuid()
	for _, c := range []struct {
		what  string
		table map[int]proc
		from  int
		want  int
	}{
		{"a client as pid 1", map[int]proc{40: {12, "sh", me}, 12: {1, "dash", me}, 1: {0, "claude", me}}, 40, 1},
		{"a client under init", map[int]proc{40: {12, "sh", me}, 12: {30, "dash", me}, 30: {1, "node", me}, 1: {0, "systemd", 0}}, 40, 30},
		{"a client that is no shell", map[int]proc{40: {30, "node", me}}, 40, 40},
		{"another uid's parent", map[int]proc{40: {12, "sh", me}, 12: {1, "sshd", me + 1}}, 40, 40},
		{"an unreadable parent", map[int]proc{40: {12, "sh", me}}, 40, 40},
	} {
		parent := func(pid int) (int, string, error) {
			if p, ok := c.table[pid]; ok {
				return p.ppid, p.name, nil
			}
			return 0, "", core.ErrNoProcess
		}
		uid := func(pid int) (int, error) {
			if p, ok := c.table[pid]; ok {
				return p.uid, nil
			}
			return 0, core.ErrNoProcess
		}
		if got := walkPastShells(c.from, parent, uid); got != c.want {
			t.Errorf("%s: the client of %d is %d, want %d", c.what, c.from, got, c.want)
		}
	}
}

// TestSessionHookChild is no test of its own: it is the command the hook script of
// TestSessionWithoutAMessagingSocketUnderAHookShellIsTheClient runs.
func TestSessionHookChild(t *testing.T) {
	if os.Getenv("BK_SESSION_HOOK_CHILD") != "1" {
		t.Skip("the hook's command, run by TestSessionWithoutAMessagingSocketUnderAHookShellIsTheClient")
	}
	os.Exit(session([]string{"register", "--role", "worker"}, os.Stdout, os.Stderr))
}

// The server's uid (tested): the client sends the bearer only to a server of its own uid, as the
// server admits only clients of its own uid. Another local user who binds the configured socket
// path first (a /tmp directory after a reboot, say) got every session's BATONDECK_TOKEN from the
// SessionStart hook, which fails quietly by design. A server of another uid is played here by a
// listener of this uid with the client's expected uid pointed elsewhere: it is refused in one
// stderr line, exit 0, and the listener reads no request.
func TestSessionSendsTheBearerOnlyToAServerOfItsUID(t *testing.T) {
	sock := filepath.Join(shortDir(t, "bkimp"), "bk.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 4)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	t.Setenv("BATONDECK_CONFIG", "")
	t.Setenv("BATONDECK_LISTEN", "unix://"+sock)
	const bearer = "a-bearer-the-impostor-must-not-read" // the client sends it as it is
	t.Setenv("BATONDECK_TOKEN", bearer)
	t.Setenv("CLAUDE_CODE_MESSAGING_SOCKET", "")
	old := sessionServerUID
	sessionServerUID = func() int { return os.Getuid() + 1 }
	t.Cleanup(func() { sessionServerUID = old })
	for _, sub := range [][]string{{"session", "register", "--role", "worker"}, {"session", "unregister"}} {
		oneLine(t, fmt.Sprintf("is served by uid %d, not uid %d", os.Getuid(), os.Getuid()+1), sub...)
	}
	select {
	case auth := <-got:
		t.Fatalf("a server of another uid read a request carrying %q", auth)
	default:
	}
	sessionServerUID = old // the positive control: the same listener, as this uid's, is sent the request
	oneLine(t, "registered as", "session", "register", "--role", "worker")
	if auth := <-got; auth != "Bearer "+bearer {
		t.Fatalf("the positive control's request carried %q", auth)
	}
}

// AC2 (tested): the routes are 404 over TCP for the client's exact body, and a client whose listen
// config names only TCP does not fall back to it: one line, exit 0, no row. The positive control is
// TestSessionRegisterAndUnregister, whose listen has the TCP entry first.
func TestSessionIsUnixSocketOnly(t *testing.T) {
	s := startDirectory(t, nil)
	c := s.client(t, "s-1", "")
	body := fmt.Sprintf(`{"pid":%d,"role":"worker","socket":%q,"sessionId":%q}`, c.pid, c.sock, c.session)
	tcp := &http.Client{Timeout: 3 * time.Second}
	code, got := httpDo(t, tcp, http.MethodPost, "http://"+s.tcp+"/internal/wake-session", s.alice.token, body)
	t.Logf("POST over TCP: %d %q", code, got)
	if code != http.StatusNotFound {
		t.Fatalf("register over TCP: %d, want 404", code)
	}
	sessionEnv(t, s, s.alice, c.sock)
	t.Setenv("BATONDECK_LISTEN", "tcp://"+s.tcp)
	oneLine(t, "no unix:// entry in listen", "session", "register", "--role", "worker")
	oneLine(t, "no unix:// entry in listen", "session", "unregister")
	if rows := s.rows(t); len(rows) != 0 {
		t.Fatalf("a TCP-only client left rows %+v", rows)
	}
}

// AC2's refusals (tested): every refusal is one stderr line naming the status and the server's
// message, exit 0, no row: a pid another uid owns, a pid no one registered, no bearer, a socket no
// live registry entry names.
func TestSessionRefusalsAreOneLineExitZero(t *testing.T) {
	s := startDirectory(t, nil)
	if uid, err := core.ProcessUID(1); err != nil || uid == os.Getuid() {
		t.Skipf("pid 1 is uid %d (%v): no process of another uid to register", uid, err)
	}
	other := filepath.Join(shortDir(t, "bkoth"), "1.sock")
	entry, _ := json.Marshal(map[string]any{"pid": 1, "sessionId": "s-root", "procStart": "x", "messagingSocketPath": other, "peerProtocol": 1})
	if err := os.WriteFile(filepath.Join(s.reg, "1.json"), entry, 0o600); err != nil {
		t.Fatal(err)
	}
	sessionEnv(t, s, s.alice, other)
	oneLine(t, "pid 1: refused, 403 Forbidden FORBIDDEN: pid 1 is not owned by the caller's uid", "session", "register", "--role", "worker")
	c := s.client(t, "s-1", "")
	sessionEnv(t, s, s.alice, c.sock)
	oneLine(t, fmt.Sprintf("pid %d: refused, 404 Not Found NOT_FOUND", c.pid), "session", "unregister")
	t.Setenv("BATONDECK_TOKEN", "")
	oneLine(t, "BATONDECK_TOKEN is not set", "session", "register", "--role", "worker")
	sessionEnv(t, s, s.alice, "/tmp/no-such-session.sock")
	oneLine(t, "no live session in "+s.reg+" names the socket /tmp/no-such-session.sock", "session", "register", "--role", "worker")
	if rows := s.rows(t); len(rows) != 0 {
		t.Fatalf("a refusal left rows %+v", rows)
	}
}

// AC4 (tested): with the server stopped, register and unregister exit 0 with one stderr line.
func TestSessionServerDownExitsZero(t *testing.T) {
	s := startDirectory(t, nil)
	c := s.client(t, "s-1", "")
	sessionEnv(t, s, s.alice, c.sock)
	s.stop()
	oneLine(t, fmt.Sprintf("pid %d: the server on %s:", c.pid, s.sock), "session", "register", "--role", "worker")
	oneLine(t, fmt.Sprintf("pid %d: the server on %s:", c.pid, s.sock), "session", "unregister")
}

func TestSessionUsageExitsTwo(t *testing.T) {
	for _, args := range [][]string{{"session"}, {"session", "nope"}, {"session", "register"}, {"session", "register", "--role", "admin"}, {"session", "unregister", "x"}} {
		if code, out, errs := run(args...); code != 2 || out != "" || !strings.Contains(errs, "usage: batondeck-kanban session") {
			t.Fatalf("%v: %d %q %q", args, code, out, errs)
		}
	}
}
