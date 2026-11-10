package mcp

import (
	"context"
	"crypto/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

// sessionTable is the live MCP sessions, by id: an id per initialize, at most max of them. go-sdk
// collects a session idle past Options.SessionIdle (StreamableHTTPOptions.SessionTimeout) and then
// answers its id with 404 "session not found", the client's cue to initialise again.
type sessionTable struct {
	mu       sync.Mutex
	max      int
	closed   bool                          // Server.Close ran: no session is admitted again
	live     map[string]*mcp.ServerSession // by MCP session id, for the bound and for Close
	agents   map[string]int                // agent-session key -> its live MCP sessions
	keys     map[string]string             // MCP session id -> its agent-session key
	ending   map[string]string             // sessions to close once a request is answered: id -> that request's mark
	inflight map[string]struct{}           // marks of the HTTP requests closeEnding has not seen return
	ends     sync.WaitGroup                // one per admitted session, done when its end is recorded
}

// reserve admits ss, or refuses when the table is full or closed. fresh is false for an id
// already live: a second initialize on its session, which this call did not admit.
func (t *sessionTable) reserve(ss *mcp.ServerSession) (ok, fresh bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, live := t.live[ss.ID()]; live {
		return true, false
	}
	if t.closed || len(t.live) >= t.max {
		return false, false
	}
	t.live[ss.ID()] = ss
	return true, true
}

// closeAll refuses new sessions and returns the live ones.
func (t *sessionTable) closeAll() []*mcp.ServerSession {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	out := make([]*mcp.ServerSession, 0, len(t.live))
	for _, ss := range t.live {
		out = append(out, ss)
	}
	return out
}

func (t *sessionTable) release(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.live, id)
	delete(t.keys, id)
	delete(t.ending, id)
}

// endAfter marks session id to be closed once the HTTP request whose mark is mark has its answer
// (P-450), or, when that request is already over (its client gave up before the tool ran), returns
// the session for the caller to close now: nothing is left to answer, and no later request carries
// the mark. mark is "" only for a request with no HTTP transport in front, a fallback no request
// through Handler takes: then the next request on the session to finish closes it, as before.
func (t *sessionTable) endAfter(id, mark string) *mcp.ServerSession {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, inHand := t.inflight[mark]; mark != "" && !inHand {
		return t.live[id]
	}
	t.ending[id] = mark
	return nil
}

// begin records mark as an HTTP request in hand, until takeEnding sees it return.
func (t *sessionTable) begin(mark string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inflight == nil {
		t.inflight = map[string]struct{}{}
	}
	t.inflight[mark] = struct{}{}
}

// takeEnding is the live session id marked by endAfter, unmarked, or nil. A mark set by one HTTP
// request is taken only by that request (P-450): another request on the same session that finishes
// first (the client's standalone GET, a second call) must not close the transport under the first
// one's answer, which a self-disconnect then lost (it answered 200 with an empty body).
func (t *sessionTable) takeEnding(id, mark string) *mcp.ServerSession {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.inflight, mark)
	want, ok := t.ending[id]
	if !ok || (want != "" && want != mark) {
		return nil
	}
	delete(t.ending, id)
	return t.live[id]
}

// join counts one more live MCP session, id, on an agent session.
func (t *sessionTable) join(key, id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.agents[key]++
	t.keys[id] = key
}

// of is the live MCP sessions of an agent session.
func (t *sessionTable) of(key string) []*mcp.ServerSession {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []*mcp.ServerSession
	for id, k := range t.keys {
		if ss, ok := t.live[id]; ok && k == key {
			out = append(out, ss)
		}
	}
	return out
}

// leave counts one fewer live MCP session on an agent session.
func (t *sessionTable) leave(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.agents[key]--
	if t.agents[key] <= 0 {
		delete(t.agents, key)
	}
}

// LiveAgentSessions is the (identity, agent) pair of every agent session holding at least one live
// MCP session in this process, unordered: the rows the retention sweep keeps however idle (P-279).
func (s *Server) LiveAgentSessions() [][2]string {
	s.sessions.mu.Lock()
	defer s.sessions.mu.Unlock()
	out := make([][2]string, 0, len(s.sessions.agents))
	for key := range s.sessions.agents {
		if identity, agent, ok := strings.Cut(key, "::"); ok {
			out = append(out, [2]string{identity, agent})
		}
	}
	return out
}

// sessionKind is how a session's caller authenticated (D6): an OAuth access token, else an API
// token, the only other bearer /mcp takes.
func sessionKind(c domain.Caller) service.SessionKind {
	if c.Via == domain.ViaOAuth {
		return service.SessionKindOAuth
	}
	return service.SessionKindAPIToken
}

// sessionMiddleware keeps the agent_sessions row of each MCP session: written at initialize (a
// session over the bound is refused there, and go-sdk drops a session whose initialize failed) and
// touched by every tool call. The end of a session (idle collection, DELETE, Close, a restart)
// leaves the row: plan Clarification 7 GCs it 30 min after its last request, which the retention
// sweep does (AgentSessionIdle), skipping an agent LiveAgentSessions still names (P-279);
// disconnect_agent deletes it at once. A bookkeeping failure is logged and never fails the request.
//
// The row is keyed by (identity, agent), not by MCP session: the agent is Caller.Agent (the token's
// label, the OAuth client's name, else the session's clientInfo.name), so two clients on one token
// with different names are two rows, and one agent may hold several sessions,
// and service.Sessions reads a touch with no tool as a connection (its session count grows), so
// only initialize sends one.
func (s *Server) sessionMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		ss, ok := req.GetSession().(*mcp.ServerSession)
		if !ok || ss.ID() == "" {
			return next(ctx, method, req)
		}
		caller, _ := CallerFrom(ctx)
		ctx = context.WithValue(ctx, sessionIDKey{}, ss.ID())
		if ex := req.GetExtra(); ex != nil && ex.Header != nil {
			ctx = context.WithValue(ctx, requestMarkKey{}, ex.Header.Get(requestMarkHeader))
		}
		switch {
		case method == "initialize":
			return s.initialize(ctx, ss, caller, method, req, next)
		case method == "tools/call":
			touch := service.TouchAgentSessionReq{SessionKey: service.SessionKey(caller.Identity, service.AgentID(caller)), AgentName: agentName(caller, ss.InitializeParams()),
				Kind: sessionKind(caller), Tool: s.registeredName(req)}
			if _, err := s.services.Sessions.TouchAgentSession(ctx, caller, touch); err != nil {
				s.log.Printf("mcp: touch agent session %s failed: %+v", touch.SessionKey, err)
			}
		}
		return next(ctx, method, req)
	}
}

func (s *Server) initialize(ctx context.Context, ss *mcp.ServerSession, caller domain.Caller, method string, req mcp.Request, next mcp.MethodHandler) (mcp.Result, error) {
	id := ss.ID()
	ok, fresh := s.sessions.reserve(ss)
	if !ok {
		s.log.Printf("mcp: session limit %d reached; initialize refused", s.sessions.max)
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "too many MCP sessions; retry later"}
	}
	if !fresh {
		// Another initialize admitted this session and keeps its bookkeeping; go-sdk answers this one.
		return next(ctx, method, req)
	}
	res, err := next(ctx, method, req)
	if err != nil {
		s.sessions.release(id)
		s.tools.forget(id)
		return res, err
	}
	params, _ := req.GetParams().(*mcp.InitializeParams)
	key := service.SessionKey(caller.Identity, service.AgentID(caller))
	touch := service.TouchAgentSessionReq{SessionKey: key, AgentName: agentName(caller, params), Kind: sessionKind(caller), OAuthClientID: caller.OAuthClientID}
	if params != nil && params.ClientInfo != nil {
		// The client's own words, cleaned and bounded as an agent name is: up to the 4 MiB body
		// otherwise, a NUL included (which Postgres refuses, leaving a live session with no row).
		touch.ClientName, touch.ClientVersion = domain.AgentLabel(params.ClientInfo.Name), domain.AgentLabel(params.ClientInfo.Version)
	}
	if _, err := s.services.Sessions.TouchAgentSession(ctx, caller, touch); err != nil {
		s.log.Printf("mcp: create agent session %s failed: %+v", key, err)
	}
	s.sessions.join(key, id)
	s.sessions.ends.Add(1)
	go func() {
		defer s.sessions.ends.Done()
		_ = ss.Wait()
		s.sessions.release(id)
		s.tools.forget(id)
		s.sessions.leave(key) // the row stays: the retention sweep collects it once idle (P-264)
	}()
	return res, nil
}

// agentName is the session's display label: the caller's agent, else the client's name as an agent
// name (domain.AgentLabel), else "mcp".
func agentName(c domain.Caller, p *mcp.InitializeParams) string {
	if c.Agent != "" {
		return c.Agent
	}
	if p != nil && p.ClientInfo != nil {
		if name := domain.AgentLabel(p.ClientInfo.Name); name != "" {
			return name
		}
	}
	return "mcp"
}

// sessionIDKey carries the MCP session id of the request in hand to its tool handler, so
// EndAgentSessions can tell the calling session from the others.
type sessionIDKey struct{}

// requestMarkHeader carries a mark closeEnding gives each HTTP request, server-side, so a
// self-disconnect closes its session after its own answer and no other's (P-450). go-sdk hands the
// request's headers to the handler (RequestExtra.Header is the request's own header map, which
// closeEnding writes before calling it); sessionMiddleware moves the mark into the context under
// requestMarkKey. A client's value for it is replaced, never trusted, and it is never written to a
// response or logged. TestSelfDisconnectUnderOtherRequests guards the whole path.
const requestMarkHeader = "X-Batondeck-Request-Mark"

type requestMarkKey struct{}

// endWait bounds how long EndAgentSessions waits for another session to close: a close waits for
// that session's in-flight handlers to return.
const endWait = 5 * time.Second

// EndAgentSessions ends every live MCP session of (identityID, agentID) and returns how many
// (service.AgentSessionEnder, disconnect_agent). Each other session is closed before it returns
// (up to endWait), so its next request is answered 404 session not found. The calling session, when
// it is one of them, is closed by closeEnding once this HTTP request has its answer: closed from
// its own handler, the close would wait on that handler, and closed beside it, the answer would be
// lost with the transport. The end of each runs the usual bookkeeping (initialize's goroutine).
func (s *Server) EndAgentSessions(ctx context.Context, identityID, agentID string) int {
	self, _ := ctx.Value(sessionIDKey{}).(string)
	mark, _ := ctx.Value(requestMarkKey{}).(string)
	live := s.sessions.of(service.SessionKey(identityID, agentID))
	var others sync.WaitGroup
	for _, ss := range live {
		if ss.ID() == self {
			if gone := s.sessions.endAfter(ss.ID(), mark); gone != nil {
				go func() { _ = gone.Close() }()
			}
			continue
		}
		others.Go(func() { _ = ss.Close() })
	}
	done := make(chan struct{})
	go func() { others.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(endWait):
		s.log.Printf("mcp: ending %s's sessions: still closing after %s", service.SessionKey(identityID, agentID), endWait)
	case <-ctx.Done():
	}
	return len(live)
}

var _ service.AgentSessionEnder = (*Server)(nil)

// closeEnding closes a session EndAgentSessions marked from inside its own request, once that
// request's HTTP answer is written (next has returned). Each request gets its own mark first, so only
// the request that made the mark closes the session (P-450).
func (s *Server) closeEnding(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mark := rand.Text()
		r.Header.Set(requestMarkHeader, mark)
		s.sessions.begin(mark)
		// takeEnding also drops the mark from the requests in hand, so it runs for every request,
		// a panicking one included.
		defer func() {
			if ss := s.sessions.takeEnding(r.Header.Get("Mcp-Session-Id"), mark); ss != nil {
				go func() { _ = ss.Close() }()
			}
		}()
		next.ServeHTTP(w, r)
	})
}
