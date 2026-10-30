package cli

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// lifetimeServe is one serve run over a store kept across runs: its app, its /mcp URL and its stop.
type lifetimeServe struct {
	a    *app
	url  string
	stop func()
}

func startLifetimeServe(t *testing.T, st store.Store) *lifetimeServe {
	t.Helper()
	a := mustCompose(t, st, "", log.New(io.Discard, "", 0))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveUntil(ctx, []net.Listener{ln}, a, "x", log.New(io.Discard, "", 0)) }()
	s := &lifetimeServe{a: a, url: "http://" + ln.Addr().String() + "/mcp"}
	stopped := false
	s.stop = func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serveUntil: %v", err)
		}
	}
	t.Cleanup(s.stop)
	return s
}

// connect initialises an MCP session as the bearer and calls list_projects once.
func (s *lifetimeServe) connect(t *testing.T, bearer string) *sdk.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := sdk.NewClient(&sdk.Implementation{Name: "lifetime", Version: "1"}, nil)
	cs, err := client.Connect(ctx, &sdk.StreamableClientTransport{Endpoint: s.url,
		HTTPClient: &http.Client{Transport: bearerRT{bearer}}, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "list_projects", Arguments: map[string]any{}}); err != nil || res.IsError {
		t.Fatalf("list_projects: %+v %v", res, err)
	}
	return cs
}

// gone waits until no MCP session of this serve run holds an agent session.
func (s *lifetimeServe) gone(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); len(s.a.mcp.LiveAgentSessions()) != 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("live agent sessions %v 5s after the client closed", s.a.mcp.LiveAgentSessions())
		}
	}
}

// sweepAt runs one pass of the serve run's own retention sweeper with its clock at now+ahead.
func (s *lifetimeServe) sweepAt(ahead time.Duration) {
	sw := *s.a.sweep
	sw.Now = func() time.Time { return time.Now().Add(ahead) }
	sw.Sweep(context.Background())
}

// agentRows is the identity's agent_sessions rows.
func agentRows(t *testing.T, st store.Store, identity string) []store.AgentSession {
	t.Helper()
	rows, err := st.ListAgentSessions(context.Background(), identity, 10)
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

// P-264, P-324 (tested on both engines): plan Clarification 7 GCs an agent_sessions row 30 min after
// its last request. The end of its MCP session (DELETE) and a serve stop leave it, a second
// initialise after the restart updates the same row (one more connection, the counts kept), and the
// retention sweep deletes it once it is idle past 30 min with no live session.
func TestAgentSessionOutlivesItsMCPSessionAndARestart(t *testing.T) {
	for _, e := range []struct {
		name string
		open func(t *testing.T) store.Store
	}{
		{"sqlite", func(t *testing.T) store.Store { return storetest.SQLite(t) }},
		{"postgres", func(t *testing.T) store.Store { return storetest.Postgres(t) }},
	} {
		t.Run(e.name, func(t *testing.T) {
			ctx := context.Background()
			st := e.open(t)
			user, err := (&auth.Accounts{Store: st}).Add(ctx, "alice", "Alice", "pw", false)
			if err != nil {
				t.Fatal(err)
			}
			bearer, _, err := (&auth.TokenService{Store: st}).Create(ctx, user.ID, "", "test", time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			first := startLifetimeServe(t, st)
			_ = first.connect(t, bearer).Close() // DELETE: the MCP session ends
			first.gone(t)
			if rows := agentRows(t, st, user.ID); len(rows) != 1 || rows[0].AgentID != "test" || rows[0].SessionCount != 1 || rows[0].RequestCount != 1 {
				t.Fatalf("after the session's DELETE: %+v, want the one row kept (1 connection, 1 request)", rows)
			}
			first.stop()
			if rows := agentRows(t, st, user.ID); len(rows) != 1 {
				t.Fatalf("after the serve stop: %+v, want the row kept", rows)
			}

			second := startLifetimeServe(t, st)
			cs := second.connect(t, bearer)
			rows := agentRows(t, st, user.ID)
			if len(rows) != 1 || rows[0].SessionCount != 2 || rows[0].RequestCount != 2 || rows[0].ToolCounts["list_projects"] != 2 {
				t.Fatalf("after the restart and a second initialise: %+v, want the same row with 2 connections and 2 list_projects", rows)
			}
			_ = cs.Close()
			second.gone(t)
			second.sweepAt(29 * time.Minute)
			if rows := agentRows(t, st, user.ID); len(rows) != 1 {
				t.Fatalf("a sweep 29 min after the last request: %+v, want the row kept", rows)
			}
			second.sweepAt(31 * time.Minute)
			if rows := agentRows(t, st, user.ID); len(rows) != 0 {
				t.Fatalf("a sweep 31 min after the last request with no live session: %+v, want it collected", rows)
			}
		})
	}
}
