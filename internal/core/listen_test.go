package core

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// syncBuf is a log sink the listener's goroutine and the test may share.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// sockPath is a socket path short enough for macOS's 104-byte limit.
func sockPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "bk")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "s", "bk.sock")
}

// serveOn answers every request with whether it arrived on a Unix socket.
func serveOn(t *testing.T, ln net.Listener) {
	t.Helper()
	srv := &http.Server{ConnContext: ConnContext, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "unix=%v", ViaUnixSocket(r.Context()))
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
}

func unixClient(path string) *http.Client {
	return &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}}
}

func get(c *http.Client, url string) (string, error) {
	resp, err := c.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

func TestParseListen(t *testing.T) {
	for in, want := range map[string]ListenAddr{
		"tcp://:8080":              {NetworkTCP, ":8080"},
		"tcp://127.0.0.1:9":        {NetworkTCP, "127.0.0.1:9"},
		"tcp://[::1]:9":            {NetworkTCP, "[::1]:9"},
		"unix:///run/bk/bk.sock":   {NetworkUnix, "/run/bk/bk.sock"},
		"unix:///run/bk/../x.sock": {NetworkUnix, "/run/x.sock"},
	} {
		got, err := ParseListen(in)
		if err != nil || got != want {
			t.Fatalf("ParseListen(%q) = %+v, %v; want %+v", in, got, err, want)
		}
		if again, _ := ParseListen(got.String()); again != got {
			t.Fatalf("%q does not round-trip: %q", in, got.String())
		}
	}
	for _, bad := range []string{":8080", "127.0.0.1:8080", "tcp://", "tcp://host", "unix://", "unix://rel/x.sock", "unix:///x.sock?q=1", "udp://:53", "http://:80"} {
		if got, err := ParseListen(bad); err == nil {
			t.Fatalf("ParseListen(%q) = %+v, want an error", bad, got)
		}
	}
}

// The same-uid control: the socket is mode 0600, the owning uid is served, and its requests are
// marked as arriving on a Unix socket. A TCP listener's are not.
func TestUnixSocketServesTheOwningUID(t *testing.T) {
	path := sockPath(t)
	ln, err := Listen(ListenAddr{Network: NetworkUnix, Address: path}, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	serveOn(t, ln)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 || fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket mode %v, want a socket with 0600", fi.Mode())
	}
	if di, _ := os.Stat(filepath.Dir(path)); di.Mode().Perm() != 0o700 {
		t.Fatalf("socket directory mode %v, want 0700", di.Mode().Perm())
	}
	if body, err := get(unixClient(path), "http://x/"); err != nil || body != "unix=true" {
		t.Fatalf("same uid over the socket: %q, %v; want unix=true", body, err)
	}
	tcp, err := Listen(ListenAddr{Network: NetworkTCP, Address: "127.0.0.1:0"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	serveOn(t, tcp)
	if body, err := get(&http.Client{Timeout: 3 * time.Second}, "http://"+tcp.Addr().String()+"/"); err != nil || body != "unix=false" {
		t.Fatalf("over TCP: %q, %v; want unix=false", body, err)
	}
}

// The peer-uid refusal: a listener admitting another uid closes this process's connection before
// HTTP reads it, logs the refusal, and goes on accepting.
func TestUnixSocketRefusesAnotherUID(t *testing.T) {
	path := sockPath(t)
	logs := &syncBuf{}
	other := os.Getuid() + 1
	ln, err := listenUnix(path, other, log.New(logs, "", 0))
	if err != nil {
		t.Fatal(err)
	}
	serveOn(t, ln)
	for range 2 {
		if body, err := get(unixClient(path), "http://x/"); err == nil {
			t.Fatalf("a peer of uid %d was served %q by a socket admitting uid %d", os.Getuid(), body, other)
		}
	}
	want := fmt.Sprintf("refused a connection on %s from uid %d (admits uid %d)", path, os.Getuid(), other)
	if n := strings.Count(logs.String(), want); n != 2 {
		t.Fatalf("log %q: want the refusal logged twice (%q)", logs.String(), want)
	}
}

// A socket file nobody answers on is replaced; a live one, or a file that is not a socket, refuses.
func TestUnixSocketStaleLiveAndForeignFiles(t *testing.T) {
	quiet := log.New(io.Discard, "", 0)
	path := sockPath(t)
	ln, err := Listen(ListenAddr{Network: NetworkUnix, Address: path}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(ListenAddr{Network: NetworkUnix, Address: path}, quiet); err == nil || !strings.Contains(err.Error(), "another process is serving it") {
		t.Fatalf("a second listener on a live socket: %v", err)
	}
	// A stale file: the listener's fd closed without unlinking the path, as a killed process leaves it.
	ln.(*peerListener).Listener.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the stale socket file is gone: %v", err)
	}
	ln2, err := Listen(ListenAddr{Network: NetworkUnix, Address: path}, quiet)
	if err != nil {
		t.Fatalf("replacing a stale socket: %v", err)
	}
	ln2.Close()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(ListenAddr{Network: NetworkUnix, Address: path}, quiet); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("a regular file at the path: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != "x" {
		t.Fatal("the regular file was touched")
	}
}
