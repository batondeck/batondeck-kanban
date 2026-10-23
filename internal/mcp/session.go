package mcp

import (
	"context"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

// sessionTable is the live MCP sessions, by id: an id per initialize, at most max of them. go-sdk
// collects a session idle past Options.SessionIdle (StreamableHTTPOptions.SessionTimeout) and then
// answers its id with 404 "session not found", the client's cue to initialise again.
type sessionTable struct {
	mu     sync.Mutex
	max    int
	closed bool                          // Server.Close ran: no session is admitted again
	live   map[string]*mcp.ServerSession // by MCP session id, for the bound and for Close
	agents map[string]int                // agent-session key -> its live MCP sessions
	ends   sync.WaitGroup                // one per admitted session, done when its end is recorded
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
}

// join counts one more live MCP session on an agent session.
func (t *sessionTable) join(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.agents[key]++
}

// leave counts one fewer and reports whether it was the agent session's last.
func (t *sessionTable) leave(key string) (last bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.agents[key]--
	if t.agents[key] > 0 {
		return false
	}
	delete(t.agents, key)
	return true
}

// sessionKind is how a session's caller authenticated. R0's only bearer is a CLI API token.
// TODO(R0.13): oauth_user for an OAuth access token.
const sessionKind service.SessionKind = "service_account"

// sessionMiddleware keeps the agent_sessions row of each MCP session: written at initialize (a
// session over the bound is refused there, and go-sdk drops a session whose initialize failed),
// touched by every tool call, removed when the session ends, by idle
// collection, DELETE or close, when that was the last live MCP session of its agent. A bookkeeping
// failure is logged and never fails the request.
//
// The row is keyed by (identity, agent), not by MCP session: one agent may hold several sessions,
// and service.Sessions reads a touch with no tool as a connection (its session count grows), so
// only initialize sends one.
func (s *Server) sessionMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		ss, ok := req.GetSession().(*mcp.ServerSession)
		if !ok || ss.ID() == "" {
			return next(ctx, method, req)
		}
		caller, _ := CallerFrom(ctx)
		switch {
		case method == "initialize":
			return s.initialize(ctx, ss, caller, method, req, next)
		case method == "tools/call":
			touch := service.TouchAgentSessionReq{SessionKey: service.SessionKey(caller.Identity, service.AgentID(caller)), AgentName: agentName(caller, ss.InitializeParams()),
				Kind: sessionKind, Tool: toolName(req)}
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
		return res, err
	}
	params, _ := req.GetParams().(*mcp.InitializeParams)
	key := service.SessionKey(caller.Identity, service.AgentID(caller))
	touch := service.TouchAgentSessionReq{SessionKey: key, AgentName: agentName(caller, params), Kind: sessionKind}
	if params != nil && params.ClientInfo != nil {
		touch.ClientName, touch.ClientVersion = params.ClientInfo.Name, params.ClientInfo.Version
	}
	if _, err := s.services.Sessions.TouchAgentSession(ctx, caller, touch); err != nil {
		s.log.Printf("mcp: create agent session %s failed: %+v", key, err)
	}
	s.sessions.join(key)
	s.sessions.ends.Add(1)
	go func() {
		defer s.sessions.ends.Done()
		_ = ss.Wait()
		s.sessions.release(id)
		if !s.sessions.leave(key) {
			return
		}
		// The request's context is gone with its request; the removal outlives it.
		_, err := s.services.Sessions.RemoveAgentSession(context.WithoutCancel(ctx), caller, service.RemoveAgentSessionReq{SessionKey: key})
		if err != nil {
			s.log.Printf("mcp: remove agent session %s failed: %+v", key, err)
		}
	}()
	return res, nil
}

// agentName is the session's display label: the caller's agent, else the client's name.
func agentName(c domain.Caller, p *mcp.InitializeParams) string {
	if c.Agent != "" {
		return c.Agent
	}
	if p != nil && p.ClientInfo != nil && p.ClientInfo.Name != "" {
		return p.ClientInfo.Name
	}
	return "mcp"
}

// subscribe refuses every URI: R0 has no resource, and a subscription that can never deliver is
// refused rather than accepted silently. R1.7 registers the resources and replaces this.
func subscribe(_ context.Context, req *mcp.SubscribeRequest) error {
	return mcp.ResourceNotFoundError(req.Params.URI)
}

func unsubscribe(context.Context, *mcp.UnsubscribeRequest) error { return nil }
