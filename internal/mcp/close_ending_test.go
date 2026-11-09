package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// liveServerSession is a real go-sdk ServerSession over in-memory transports, and how to tell it
// closed: Wait returns.
func liveServerSession(t *testing.T) (*mcp.ServerSession, <-chan struct{}) {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := mcp.NewServer(&mcp.Implementation{Name: "s", Version: "1"}, nil).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close(); _ = ss.Close() })
	closed := make(chan struct{})
	go func() { _ = ss.Wait(); close(closed) }()
	return ss, closed
}

// A session that disconnects itself is closed after ITS request's answer, not after whichever
// request on the session finishes first (P-450). The disconnect's request marks the session; a
// second request on the same session (the client's standalone GET, another call) finishes while the
// first is still writing its answer. Before, that second request took the mark and closed the
// transport, and the self-disconnect answered 200 with an empty body
// (TestDisconnectAgentEndsTheSession on PR 98's first ci run).
func TestCloseEndingWaitsForTheMarkingRequest(t *testing.T) {
	ss, closed := liveServerSession(t)
	// In-memory sessions have no id; the table keys this one as an HTTP session would be.
	const id = "S-test"
	s := &Server{sessions: &sessionTable{max: 10, live: map[string]*mcp.ServerSession{id: ss}, agents: map[string]int{}, keys: map[string]string{}, ending: map[string]string{}}}

	other := s.closeEnding(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	marking := s.closeEnding(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// What disconnect_agent does from inside its own request: EndAgentSessions marks the
		// calling session with this request's mark (which sessionMiddleware reads from the header).
		s.sessions.endAfter(id, r.Header.Get(requestMarkHeader))
		// Another request on the same session finishes before this one has its answer.
		req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
		req.Header.Set("Mcp-Session-Id", id)
		other.ServeHTTP(httptest.NewRecorder(), req)
		select {
		case <-closed:
			t.Fatal("the session closed when ANOTHER request on it finished, before the marking request's answer")
		case <-time.After(200 * time.Millisecond):
		}
		_, _ = w.Write([]byte(`{"disconnected":true}`))
	}))

	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Mcp-Session-Id", id)
	req.Header.Set(requestMarkHeader, "a client's own value") // replaced, never trusted
	rec := httptest.NewRecorder()
	marking.ServeHTTP(rec, req)
	if rec.Body.String() != `{"disconnected":true}` {
		t.Fatalf("the marking request answered %q", rec.Body.String())
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the session did not close after the marking request's answer")
	}
}

// A request that carries no mark (no HTTP transport in front) keeps the old rule: the next request
// on the session to finish closes it.
func TestTakeEndingWithoutAMark(t *testing.T) {
	ss, _ := liveServerSession(t)
	const id = "S-test"
	tb := &sessionTable{live: map[string]*mcp.ServerSession{id: ss}, ending: map[string]string{}}
	tb.endAfter(id, "")
	if got := tb.takeEnding(id, "any"); got != ss {
		t.Fatalf("an unmarked end was not taken: %v", got)
	}
	if got := tb.takeEnding(id, "any"); got != nil {
		t.Fatal("an end was taken twice")
	}
	tb.endAfter(id, "m1")
	if got := tb.takeEnding(id, "m2"); got != nil {
		t.Fatal("another request's mark took the end")
	}
	if got := tb.takeEnding(id, "m1"); got != ss {
		t.Fatal("the marking request did not take its end")
	}
}
