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

// sessionTable is the live MCP sessions, by id: an id per initialize, at most max of them and at most
// perIdentity of one identity's. go-sdk collects a session idle past Options.SessionIdle
// (StreamableHTTPOptions.SessionTimeout) and then answers its id with 404 "session not found", the
// client's cue to initialise again.
type sessionTable struct {
	mu          sync.Mutex
	max         int
	perIdentity int
	closed      bool                          // Server.Close ran: no session is admitted again
	live        map[string]*mcp.ServerSession // by MCP session id, for the bound and for Close
	owner       map[string]string             // MCP session id -> the identity that initialised it
	seen        map[string]time.Time          // MCP session id -> when its last request arrived or was answered
	calls       map[string]int                // MCP session id -> its requests in hand (never closed to make room)
	evicting    map[string]struct{}           // sessions closed to make room for their identity's newer one
	agents      map[string]int                // agent-session key -> its live MCP sessions
	keys        map[string]string             // MCP session id -> its agent-session key
	ending      map[string]string             // sessions to close once a request is answered: id -> that request's mark
	closing     map[string]struct{}           // sessions EndAgentSessions is ending: their calls touch no row
	inflight    map[string]struct{}           // marks of the HTTP requests closeEnding has not seen return
	ends        sync.WaitGroup                // one per admitted session, done when its end is recorded
}

// The answers to an initialize reserve refuses.
const (
	refusedTable    = "too many MCP sessions; retry later"
	refusedIdentity = "too many MCP sessions for this identity, each with a request in hand; end one or retry later"
)

// reserve admits ss, initialised by identity at now, or refuses it with the answer to send:
// refusedTable when the table is full or closed. fresh is false for an id already live: a second
// initialize on its session, which this call did not admit. An identity at its own bound is
// admitted all the same, and evict is its longest-idle session with no request in hand, which the
// caller closes: from then on it counts against neither bound, so one identity can never hold more
// than perIdentity of the table, nor lock another identity out of it. A call in progress is never
// cut to make room: when every one of the identity's sessions has a request in hand, its own
// initialize is refused (refusedIdentity).
func (t *sessionTable) reserve(ss *mcp.ServerSession, identity string, now time.Time) (fresh bool, evict *mcp.ServerSession, refused string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, live := t.live[ss.ID()]; live {
		return false, nil, ""
	}
	if t.closed {
		return false, nil, refusedTable
	}
	mine, oldest := 0, ""
	for id, who := range t.owner {
		if _, going := t.evicting[id]; going || who != identity {
			continue
		}
		mine++
		if t.calls[id] == 0 && (oldest == "" || t.seen[id].Before(t.seen[oldest])) {
			oldest = id
		}
	}
	switch {
	case mine >= t.perIdentity && t.live[oldest] != nil:
		evict = t.live[oldest]
		t.evicting[oldest] = struct{}{}
	case mine >= t.perIdentity:
		return false, nil, refusedIdentity
	case len(t.live)-len(t.evicting) >= t.max:
		return false, nil, refusedTable
	}
	t.live[ss.ID()], t.owner[ss.ID()], t.seen[ss.ID()] = ss, identity, now
	return true, evict, ""
}

// saw records a request on live session id at now, for the longest-idle choice, and holds it in
// hand until done is called with its answer: a session with a request in hand is never the one
// reserve closes to make room. done also records the answer's time as the session's last activity.
func (t *sessionTable) saw(id string, now time.Time) (done func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, live := t.live[id]; !live {
		return func() {}
	}
	t.seen[id] = now
	t.calls[id]++
	return func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if _, live := t.live[id]; live {
			t.seen[id] = time.Now()
			if t.calls[id]--; t.calls[id] <= 0 {
				delete(t.calls, id)
			}
		}
	}
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
	delete(t.owner, id)
	delete(t.seen, id)
	delete(t.calls, id)
	delete(t.evicting, id)
	delete(t.keys, id)
	delete(t.ending, id)
	delete(t.closing, id)
}

// markClosing records sessions EndAgentSessions is ending, until each is released.
func (t *sessionTable) markClosing(ss []*mcp.ServerSession) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closing == nil {
		t.closing = map[string]struct{}{}
	}
	for _, s := range ss {
		t.closing[s.ID()] = struct{}{}
	}
}

// isClosing is whether EndAgentSessions is ending session id.
func (t *sessionTable) isClosing(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.closing[id]
	return ok
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
// touched by every tool call once it has its outcome (touchCall). The end of a session (idle collection, DELETE, Close, a restart)
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
		defer s.sessions.saw(ss.ID(), time.Now())()
		ctx = context.WithValue(ctx, sessionIDKey{}, ss.ID())
		if ex := req.GetExtra(); ex != nil && ex.Header != nil {
			ctx = context.WithValue(ctx, requestMarkKey{}, ex.Header.Get(requestMarkHeader))
		}
		switch {
		case method == "initialize":
			return s.initialize(ctx, ss, caller, method, req, next)
		case method == "tools/call":
			res, err := next(ctx, method, req)
			s.touchCall(ctx, ss, caller, req, res, err)
			return res, err
		}
		return next(ctx, method, req)
	}
}

// touchWait bounds a tools/call's session touch, which runs on the call's values without its
// cancellation: a call its client abandoned is still a request.
const touchWait = 5 * time.Second

// touchCall records a finished tools/call on its agent session. It runs after the handler so it
// knows the outcome: a call that succeeded names a project that is the caller's (every tool naming
// one authorizes on it), which becomes the agent's lastProjectId; a refused or failed call's
// projectId may be any project's, a non-member's included, so it names none. A session
// EndAgentSessions is ending is not touched: disconnect_agent deletes the row once the sessions
// are ended, and a touch after that would write the row back.
func (s *Server) touchCall(ctx context.Context, ss *mcp.ServerSession, caller domain.Caller, req mcp.Request, res mcp.Result, err error) {
	if s.sessions.isClosing(ss.ID()) {
		return
	}
	touch := service.TouchAgentSessionReq{SessionKey: service.SessionKey(caller.Identity, service.AgentID(caller)), AgentName: agentName(caller, ss.InitializeParams()),
		Kind: sessionKind(caller), Tool: s.registeredName(req)}
	if r, _ := res.(*mcp.CallToolResult); err == nil && r != nil && !r.IsError && touch.Tool != unknownTool {
		touch.ProjectID = projectOf(req)
	}
	tctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), touchWait)
	defer cancel()
	if _, err := s.services.Sessions.TouchAgentSession(tctx, caller, touch); err != nil {
		s.log.Printf("mcp: touch agent session %s failed: %+v", touch.SessionKey, err)
	}
}

func (s *Server) initialize(ctx context.Context, ss *mcp.ServerSession, caller domain.Caller, method string, req mcp.Request, next mcp.MethodHandler) (mcp.Result, error) {
	id := ss.ID()
	fresh, evict, refused := s.sessions.reserve(ss, caller.Identity, time.Now())
	switch refused {
	case refusedTable:
		s.log.Printf("mcp: session limit %d reached; initialize refused", s.sessions.max)
	case refusedIdentity:
		s.log.Printf("mcp: %s holds %d MCP sessions, its bound, each with a request in hand; initialize refused", caller.Identity, s.sessions.perIdentity)
	}
	if refused != "" {
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: refused}
	}
	if evict != nil {
		// Its end runs the usual bookkeeping (the goroutine below, started by its own initialize);
		// its client's next request is answered 404 session not found, the cue to initialise again.
		s.log.Printf("mcp: %s holds %d MCP sessions, its bound; closing its longest-idle one", caller.Identity, s.sessions.perIdentity)
		go func() { _ = evict.Close() }()
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
	s.sessions.markClosing(live) // a call still in hand on any of them must not touch the row back after the delete
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
