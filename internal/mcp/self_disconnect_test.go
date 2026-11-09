package mcp_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// gatedSessions holds disconnect_agent's session touch (it runs before the tool) until released, so
// a test can drop the HTTP request before EndAgentSessions marks the session.
type gatedSessions struct {
	service.Sessions
	armed   chan struct{}
	entered chan struct{}
	release chan struct{}
}

func (g *gatedSessions) TouchAgentSession(ctx context.Context, c domain.Caller, req service.TouchAgentSessionReq) (service.TouchAgentSessionResp, error) {
	if req.Tool == "disconnect_agent" {
		select {
		case <-g.armed:
			close(g.entered)
			<-g.release
		default:
		}
	}
	return g.Sessions.TouchAgentSession(ctx, c, req)
}

// A self-disconnect whose HTTP request its client abandons before the tool reaches
// EndAgentSessions still ends the session (P-450, reviewer-P450's finding 1): the marking request is
// no longer in hand, so the end is not parked for a request that will never come.
func TestSelfDisconnectAbandonedBeforeTheMark(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		g := &gatedSessions{Sessions: s.Services.Sessions, armed: make(chan struct{}), entered: make(chan struct{}), release: make(chan struct{})}
		s.Services.Sessions = g
		h := bootStack(t, s)
		root := &caller{t: t, cs: h.Connect(t, stackRoot), called: map[string]int{}}
		again := &caller{t: t, cs: h.Connect(t, stackGrace), called: map[string]int{}}
		again.call("list_agent_sessions", map[string]any{})
		id := again.cs.ID()

		close(g.armed)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, h.URL(), strings.NewReader(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"disconnect_agent","arguments":{"identityId":"u-grace","agentId":"harness"}}}`))
			req.Header.Set("Authorization", "Bearer "+stackGrace)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")
			req.Header.Set("Mcp-Session-Id", id)
			req.Header.Set("Mcp-Protocol-Version", "2025-06-18")
			res, err := http.DefaultClient.Do(req)
			if err == nil {
				res.Body.Close()
			}
			done <- err
		}()
		<-g.entered
		cancel() // the client gives up on the request
		<-done
		time.Sleep(500 * time.Millisecond) // the server sees the dropped connection; the POST's handler returns
		close(g.release)                   // the tool now runs: EndAgentSessions marks the calling session

		// The disconnect ran: its audit row is written after EndAgentSessions.
		deadline := time.Now().Add(5 * time.Second)
		for {
			if n := len(root.call("list_audit_log", map[string]any{"action": "agent.disconnected"})["entries"].([]any)); n > 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the disconnect never ran")
			}
			time.Sleep(20 * time.Millisecond)
		}
		var codes []int
		for i := 0; i < 10; i++ {
			code, _ := postOnSession(t, h.URL(), stackGrace, id)
			codes = append(codes, code)
			time.Sleep(200 * time.Millisecond)
		}
		t.Logf("%s: codes of 10 requests on the self-disconnected session over 2 s: %v", s.Engine, codes)
		if codes[len(codes)-1] != http.StatusNotFound {
			t.Errorf("the self-disconnected session is still live after its disconnect ran and 10 more requests finished")
		}
	})
}
