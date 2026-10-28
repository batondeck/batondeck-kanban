package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/store/memstore"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// readBound is the request read bound these tests shrink requestReadTimeout to. They do not run in
// parallel, since the bound is a package variable serveUntil reads.
const readBound = 500 * time.Millisecond

// stall sends a request's headers declaring a 1000-byte body and 10 bytes of it over network to
// addr, then nothing more, and is how long the server took to close the connection after the
// request began, with the status line it answered ("" for none). It gives up after 4s.
func stall(t *testing.T, network, addr, path string) (time.Duration, string) {
	t.Helper()
	conn, err := net.Dial(network, addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	start := time.Now()
	if _, err := fmt.Fprintf(conn, "POST %s HTTP/1.1\r\nHost: stall.test\r\nContent-Type: application/json\r\nContent-Length: 1000\r\n\r\n%s", path, strings.Repeat("x", 10)); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(start.Add(4 * time.Second))
	got, err := io.ReadAll(conn)
	held := time.Since(start)
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return held, "held past 4s"
	}
	status, _, _ := strings.Cut(string(got), "\r\n")
	return held, status
}

// P-232: a request body that stalls is cut at the read bound and its connection closed, on TCP and
// on the Unix socket. Before, only the headers were bounded (ReadHeaderTimeout), so a client could
// hold a handler and its connection for as long as it liked (R2.9: /hooks held 27 s).
func TestServeCutsAStalledBodyAtTheReadBound(t *testing.T) {
	shrink(t, &requestReadTimeout, readBound)
	s := startDirectory(t, nil)
	for name, dial := range map[string][2]string{"tcp": {"tcp", s.tcp}, "unix": {"unix", s.sock}} {
		held, status := stall(t, dial[0], dial[1], "/hooks/I-stall")
		t.Logf("%s: held %s, answered %q", name, held, status)
		if held < readBound || held > 2*time.Second || !strings.HasPrefix(status, "HTTP/1.1 4") {
			t.Errorf("%s: a stalled /hooks body was held %s and answered %q, want it closed between %s and 2s with a 4xx", name, held, status, readBound)
		}
	}
}

// P-232: the read bound covers reading the request, not serving it. /api/stream and the MCP GET
// stream are open past three bounds: the stream still delivers a frame caused after them, and the
// MCP stream's body has not ended. net/http's background read, which cancels a request's context
// on a read error, would cancel it at a read deadline still armed once the request is read; it
// clears the deadline as it starts. A client that closes its end still ends the stream.
func TestServeStreamsOutliveTheReadBound(t *testing.T) {
	shrink(t, &requestReadTimeout, readBound)
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
		defer close(frames)
		sc := bufio.NewScanner(resp.Body)
		event := ""
		for sc.Scan() {
			if v, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
				event = v
			}
			if v, ok := strings.CutPrefix(sc.Text(), "data: "); ok && event == "presence" {
				frames <- v
			}
		}
	}()
	mcpStream := openMCPStream(t, ctx, s.tcp, s.alice.token)
	defer mcpStream.Close()
	// A third stream on a raw connection, which the client half-closes after the bounds: the
	// server's background read then sees EOF and ends the handler, so the response ends.
	raw, err := net.Dial("tcp", s.tcp)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := fmt.Fprintf(raw, "GET /api/stream HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer %s\r\n\r\n", s.tcp, s.alice.token); err != nil {
		t.Fatal(err)
	}

	time.Sleep(3 * readBound)
	c := s.client(t, "S-bound", "")
	s.register(t, s.alice, c, "worker")
	deadline := time.After(3 * time.Second)
	for got := false; !got; {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("/api/stream ended before a frame caused %s after it opened", 3*readBound)
			}
			got = strings.Contains(f, fmt.Sprintf(`"pid":%d`, c.pid))
		case <-deadline:
			t.Fatalf("/api/stream delivered no presence frame for pid %d after %s", c.pid, 3*readBound)
		}
	}
	// The MCP stream writes nothing unprompted: open means a read times out rather than ending.
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, mcpStream)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("the MCP GET stream ended %s+ after it opened: %v", 3*readBound, err)
	case <-time.After(500 * time.Millisecond):
	}

	if err := raw.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	closed := time.Now()
	raw.SetReadDeadline(closed.Add(2 * time.Second))
	got, err := io.ReadAll(raw)
	if err != nil {
		t.Fatalf("/api/stream after its client closed its end (%s after it opened): %v, want the response ended", 4*readBound, err)
	}
	if !strings.HasPrefix(string(got), "HTTP/1.1 200") || !strings.HasSuffix(string(got), "0\r\n\r\n") {
		t.Fatalf("/api/stream after its client closed its end: %q, want a 200 whose chunked body ended", got)
	}
	t.Logf("/api/stream ended %s after its client closed its end", time.Since(closed).Round(time.Millisecond))
}

// openMCPStream initializes an MCP session over raw HTTP at addr with bearer and opens its GET
// stream, which it returns open.
func openMCPStream(t *testing.T, ctx context.Context, addr, bearer string) io.ReadCloser {
	t.Helper()
	post := func(session, body string) *http.Response {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+addr+"/mcp", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+bearer)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if session != "" {
			req.Header.Set("Mcp-Session-Id", session)
			req.Header.Set("MCP-Protocol-Version", "2025-06-18")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	init := post("", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"bound","version":"1"}}}`)
	io.Copy(io.Discard, init.Body)
	init.Body.Close()
	session := init.Header.Get("Mcp-Session-Id")
	if init.StatusCode != http.StatusOK || session == "" {
		t.Fatalf("initialize: %d, session %q", init.StatusCode, session)
	}
	ack := post(session, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	io.Copy(io.Discard, ack.Body)
	ack.Body.Close()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Mcp-Session-Id", session)
	req.Header.Set("MCP-Protocol-Version", "2025-06-18")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("MCP GET stream: %d %s", resp.StatusCode, b)
	}
	return resp.Body
}

// P-232: a request whose body was read in time is not cut while its handler runs past the bound:
// an MCP tool call parked for three bounds returns its result, and a 1 MiB blob PUT trickled over
// 2s on a signed URL (whose expiry is the PUT's own read deadline) is taken.
func TestServeHandlersOutliveTheReadBound(t *testing.T) {
	shrink(t, &requestReadTimeout, readBound)
	cs, stop, done, _ := served(t, memstore.New(), func(ctx context.Context) {
		select {
		case <-ctx.Done():
		case <-time.After(3 * readBound):
		}
	})
	defer func() { stop(); <-done }()
	defer cs.Close()
	callCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := cs.CallTool(callCtx, &sdk.CallToolParams{Name: "probe", Arguments: map[string]any{"projectId": "P-1"}})
	if err != nil || res.IsError {
		t.Fatalf("a tool call parked %s: %v %+v", 3*readBound, err, res)
	}

	b := startBlobs(t, storetest.SQLite(t), 1<<20)
	taskID := b.task(t)
	upload := b.attach(t, taskID, 1<<20)
	pr, pw := io.Pipe()
	go func() {
		body := bytes.Repeat([]byte("y"), 1<<20)
		for i := range 40 {
			time.Sleep(50 * time.Millisecond)
			if _, err := pw.Write(body[i*len(body)/40 : (i+1)*len(body)/40]); err != nil {
				return
			}
		}
		pw.Close()
	}()
	if code, got := do(t, http.MethodPut, upload, pr, 1<<20); code != http.StatusOK {
		t.Fatalf("a 1 MiB PUT over 2s with a %s bound: %d %s", readBound, code, got)
	}
}
