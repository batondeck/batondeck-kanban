package cli

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// R1.11: serve wires the device session directory into /api/stream, so a process registering over
// the Unix socket reaches its user's stream as a presence frame, and its unregister as another.
func TestServeStreamsPresenceFromTheDirectory(t *testing.T) {
	t.Parallel()
	s := startDirectory(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+s.tcp+"/api/stream", nil)
	req.Header.Set("Authorization", "Bearer "+s.alice.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream: %d", resp.StatusCode)
	}
	frames := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		event := ""
		for sc.Scan() {
			line := sc.Text()
			if v, ok := strings.CutPrefix(line, "event: "); ok {
				event = v
			}
			if v, ok := strings.CutPrefix(line, "data: "); ok && event == "presence" {
				frames <- v
			}
		}
		close(frames)
	}()
	next := func(what string) string {
		t.Helper()
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("%s: the stream closed", what)
			}
			return f
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: no presence frame within 3s", what)
		}
		return ""
	}
	if f := next("on connect"); f != `{"sessions":[]}` {
		t.Fatalf("on connect: %s", f)
	}
	c := s.client(t, "S-presence", "")
	s.register(t, s.alice, c, "worker")
	if f := next("after register"); !strings.Contains(f, fmt.Sprintf(`"pid":%d`, c.pid)) || !strings.Contains(f, `"role":"worker"`) {
		t.Fatalf("after register: %s, want pid %d as a worker", f, c.pid)
	}
	if code, got := httpDo(t, unixHTTP(s.sock), http.MethodDelete, fmt.Sprintf("http://x/internal/wake-session/%d", c.pid), s.alice.token, ""); code != http.StatusNoContent {
		t.Fatalf("unregister: %d %s", code, got)
	}
	if f := next("after unregister"); f != `{"sessions":[]}` {
		t.Fatalf("after unregister: %s", f)
	}
}
