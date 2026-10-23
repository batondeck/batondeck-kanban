package cli

import (
	"bytes"
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/memstore"
)

type bearerRT struct{ token string }

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// /mcp is mounted over the store's API tokens: a live token initialises a session, a revoked one
// and none are 401 with a Bearer challenge.
func TestServeMountsMCPOverTokens(t *testing.T) {
	path := migratedDB(t)
	withStdin(t, "pw\n")
	if code, _, errs := run("user", "add", "alice"); code != 0 {
		t.Fatalf("user add: %s", errs)
	}
	_, live, _ := run("token", "create", "--user", "alice")
	_, revoked, _ := run("token", "create", "--user", "alice")
	live, revoked = strings.TrimSpace(live), strings.TrimSpace(revoked)
	if code, _, errs := run("token", "revoke", revoked[:3+16]); code != 0 {
		t.Fatalf("revoke: %s", errs)
	}
	st, err := store.OpenSQLite(path, "FULL")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a := mustCompose(t, st, "", log.New(io.Discard, "", 0))
	mcpSrv, core := a.mcp, a.core
	defer core.Close(context.Background())
	srv := httptest.NewServer(handler("x", mcpSrv.Handler()))
	defer srv.Close()

	post := func(bearer string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	for name, bearer := range map[string]string{"none": "", "revoked": revoked} {
		if r := post(bearer); r.StatusCode != http.StatusUnauthorized || r.Header.Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("%s: %d %q, want 401 Bearer", name, r.StatusCode, r.Header.Get("WWW-Authenticate"))
		}
	}

	if r := post(live); r.StatusCode != http.StatusOK || r.Header.Get("Mcp-Session-Id") == "" {
		t.Errorf("live: %d, Mcp-Session-Id %q", r.StatusCode, r.Header.Get("Mcp-Session-Id"))
	}
}

// capturing is a memstore that keeps every tool_calls row it is handed. The engines' tool-call
// statements are unbuilt until R0.11, so the service core is proved over memstore.
type capturing struct {
	*memstore.Store
	rows []store.ToolCall
}

func (c *capturing) InsertToolCalls(ctx context.Context, batch []store.ToolCall) error {
	c.rows = append(c.rows, batch...)
	return c.Store.InsertToolCalls(ctx, batch)
}

// The serve composition writes one tool_calls row per tool call and touches the caller's agent
// session, through the service core.
func TestServeRecordsToolCallsAndAgentSessions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st := &capturing{Store: memstore.New()}
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := (&auth.Accounts{Store: st}).Add(ctx, "alice", "Alice", "pw", false)
	if err != nil {
		t.Fatal(err)
	}
	live, _, err := (&auth.TokenService{Store: st}).Create(ctx, user.ID, "", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	a := mustCompose(t, st, "", log.New(io.Discard, "", 0))
	mcpSrv, core := a.mcp, a.core
	defer core.Close(context.Background())
	type in struct {
		ProjectID string `json:"projectId"`
	}
	type out struct {
		OK bool `json:"ok"`
	}
	mcp.AddTool(mcpSrv, &sdk.Tool{Name: "probe"}, nil, func(context.Context, *sdk.CallToolRequest, in) (*sdk.CallToolResult, out, error) {
		return nil, out{OK: true}, nil
	})
	srv := httptest.NewServer(handler("x", mcpSrv.Handler()))
	defer srv.Close()

	client := sdk.NewClient(&sdk.Implementation{Name: "serve-test", Version: "1"}, nil)
	cs, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: srv.URL + "/mcp",
		HTTPClient: &http.Client{Transport: bearerRT{live}}, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "probe", Arguments: map[string]any{"projectId": "P-1"}}); err != nil || res.IsError {
		t.Fatalf("probe: %+v %v", res, err)
	}
	if err := core.FlushToolCalls(ctx); err != nil {
		t.Fatal(err)
	}

	var rows []string
	for _, r := range st.rows {
		rows = append(rows, r.Tool+" "+r.Identity+" "+string(r.Outcome)+" "+r.Code)
	}
	if want := "probe " + user.ID + " ok OK"; len(rows) != 1 || rows[0] != want {
		t.Errorf("tool_calls = %q, want [%q]", rows, want)
	}

	sessions, err := st.ListAgentSessions(ctx, user.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].AgentID != "id:"+user.ID || sessions[0].ClientName != "serve-test" ||
		sessions[0].SessionCount != 1 || sessions[0].RequestCount != 1 || sessions[0].ToolCounts["probe"] != 1 || sessions[0].Kind != "service_account" {
		t.Errorf("agent sessions = %+v, want one: client serve-test, one connection, one probe call", sessions)
	}
}

// A stop with a client attached (initialised, its GET stream open) returns nil well inside the
// shutdown budget, and the session's agent row is removed on the way out.
func TestRunServerStopsWithAnAttachedSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st := memstore.New()
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := (&auth.Accounts{Store: st}).Add(ctx, "alice", "Alice", "pw", false)
	if err != nil {
		t.Fatal(err)
	}
	live, _, err := (&auth.TokenService{Store: st}).Create(ctx, user.ID, "", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- runServer(serveCtx, []net.Listener{ln}, st, "x", "", log.New(io.Discard, "", 0), nil, service.MediaOptions{})
	}()

	client := sdk.NewClient(&sdk.Implementation{Name: "attached", Version: "1"}, nil)
	cs, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: "http://" + ln.Addr().String() + "/mcp",
		HTTPClient: &http.Client{Transport: bearerRT{live}}, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if rows, err := st.ListAgentSessions(ctx, user.ID, 10); err != nil || len(rows) != 1 {
		t.Fatalf("agent sessions while attached = %+v, %v; want one", rows, err)
	}
	time.Sleep(200 * time.Millisecond) // the client's standalone GET stream is open by now

	start := time.Now()
	stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runServer returned %v after %s, want nil", err, time.Since(start))
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("runServer still stopping after 5s (the budget is %s)", shutdownBudget)
	}
	t.Logf("stopped in %s", time.Since(start))
	if rows, err := st.ListAgentSessions(ctx, user.ID, 10); err != nil || len(rows) != 0 {
		t.Errorf("agent sessions after the stop = %+v, %v; want none", rows, err)
	}
}

// served starts serveUntil over st with a probe tool and a client attached as a fresh account;
// park, when non-nil, is the tool's body. It returns the client, the stop, runServer's result and
// the server's log.
func served(t *testing.T, st store.Store, park func(context.Context)) (*sdk.ClientSession, context.CancelFunc, <-chan error, *lockedLog) {
	t.Helper()
	ctx := context.Background()
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := (&auth.Accounts{Store: st}).Add(ctx, "alice", "Alice", "pw", false)
	if err != nil {
		t.Fatal(err)
	}
	live, _, err := (&auth.TokenService{Store: st}).Create(ctx, user.ID, "", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	logs := &lockedLog{}
	logger := log.New(logs, "", 0)
	a := mustCompose(t, st, "", logger)
	mcpSrv := a.mcp
	type in struct {
		ProjectID string `json:"projectId"`
	}
	type out struct {
		OK bool `json:"ok"`
	}
	mcp.AddTool(mcpSrv, &sdk.Tool{Name: "probe"}, nil, func(ctx context.Context, _ *sdk.CallToolRequest, _ in) (*sdk.CallToolResult, out, error) {
		if park != nil {
			park(ctx)
		}
		return nil, out{OK: true}, nil
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- serveUntil(serveCtx, []net.Listener{ln}, a, "x", logger) }()
	client := sdk.NewClient(&sdk.Implementation{Name: "attached", Version: "1"}, nil)
	cs, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: "http://" + ln.Addr().String() + "/mcp",
		HTTPClient: &http.Client{Transport: bearerRT{live}}, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cs, stop, done, logs
}

// shrink sets *v to d for the test.
func shrink(t *testing.T, v *time.Duration, d time.Duration) {
	old := *v
	*v = d
	t.Cleanup(func() { *v = old })
}

type lockedLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// A tool still running at the stop, and ignoring its context, holds the stop no longer than the
// shutdown budget, and runServer names the step that ran out.
func TestRunServerBoundedByAParkedTool(t *testing.T) {
	shrink(t, &shutdownBudget, 300*time.Millisecond)
	entered, release := make(chan struct{}), make(chan struct{})
	cs, stop, done, _ := served(t, memstore.New(), func(context.Context) { close(entered); <-release })
	var once sync.Once
	free := func() { once.Do(func() { close(release) }) }
	defer free()
	go func() {
		_, _ = cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "probe", Arguments: map[string]any{"projectId": "P-1"}})
	}()
	<-entered
	start := time.Now()
	stop()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "closing the MCP sessions: context deadline exceeded") {
			t.Errorf("runServer = %v, want the MCP close named as the failure", err)
		}
		if took := time.Since(start); took > 2*time.Second {
			t.Errorf("runServer took %s with a %s budget", took, shutdownBudget)
		}
	case <-time.After(5 * time.Second):
		free()
		<-done // before the budget is restored under it
		t.Fatalf("runServer still stopping 5s after the stop, budget %s, behind a parked tool", shutdownBudget)
	}
}

// slowFlush is a store whose InsertToolCalls signals and takes 600ms before writing.
type slowFlush struct {
	*memstore.Store
	started chan struct{}
	once    sync.Once
	mu      sync.Mutex
	written int
}

func (s *slowFlush) InsertToolCalls(ctx context.Context, batch []store.ToolCall) error {
	s.once.Do(func() { close(s.started) })
	time.Sleep(600 * time.Millisecond)
	if err := s.Store.InsertToolCalls(context.WithoutCancel(ctx), batch); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.written += len(batch)
	return nil
}

// The flush after the stop runs on its own budget: with the shutdown budget already spent, the
// background flush in progress is still waited for, so its rows are written before runServer
// returns.
func TestFlushOutlivesASpentShutdown(t *testing.T) {
	st := &slowFlush{Store: memstore.New(), started: make(chan struct{})}
	cs, stop, done, logs := served(t, st, nil)
	if _, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: "probe", Arguments: map[string]any{"projectId": "P-1"}}); err != nil {
		t.Fatal(err)
	}
	_ = cs.Close()
	select {
	case <-st.started: // the background flush holds the row
	case <-time.After(5 * time.Second):
		t.Fatal("no background flush began in 5s")
	}
	shrink(t, &shutdownBudget, time.Nanosecond)
	stop()
	<-done
	st.mu.Lock()
	written := st.written
	st.mu.Unlock()
	if written != 1 {
		t.Errorf("%d tool_calls rows written when runServer returned, want 1: the stop did not wait for the flush in progress", written)
	}
	if strings.Contains(logs.String(), "flushing the buffered tool calls") {
		t.Errorf("the flush failed:\n%s", logs.String())
	}
}

// mustCompose is compose with no keyring, failing the test on an error.
func mustCompose(t *testing.T, st store.Store, publicURL string, logger *log.Logger) *app {
	t.Helper()
	a, err := compose(st, "x", publicURL, logger, nil, service.MediaOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return a
}
