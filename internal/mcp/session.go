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
	mu   sync.Mutex
	max  int
	live map[string]struct{}
}

// reserve admits id, or refuses when the table is full.
func (t *sessionTable) reserve(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.live[id]; ok {
		return true
	}
	if len(t.live) >= t.max {
		return false
	}
	t.live[id] = struct{}{}
	return true
}

// has reports whether id is an initialised session.
func (t *sessionTable) has(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.live[id]
	return ok
}

func (t *sessionTable) release(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.live, id)
}

// sessionKind is how a session's caller authenticated. R0's only bearer is a CLI API token.
// TODO(R0.13): oauth_user for an OAuth access token.
const sessionKind service.SessionKind = "service_account"

// sessionMiddleware keeps the agent_sessions row of each MCP session: created at initialize (a
// session over the bound is refused there, and go-sdk drops a session whose initialize failed),
// touched by every later request of an initialised session, removed when the session ends, by idle collection, DELETE or
// close. A bookkeeping failure is logged and never fails the request.
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
		case isNotification(method) || !s.sessions.has(ss.ID()):
			// A notification is no request; a session that never initialised (a client probing
			// with server/discover first) has no agent session to touch.
		default:
			touch := service.TouchAgentSessionReq{SessionKey: ss.ID(), AgentName: agentName(caller, ss.InitializeParams()), Kind: sessionKind}
			if method == "tools/call" {
				touch.Tool = toolName(req)
			}
			if _, err := s.services.Sessions.TouchAgentSession(ctx, caller, touch); err != nil {
				s.log.Printf("mcp: touch agent session %s failed: %+v", ss.ID(), err)
			}
		}
		return next(ctx, method, req)
	}
}

func (s *Server) initialize(ctx context.Context, ss *mcp.ServerSession, caller domain.Caller, method string, req mcp.Request, next mcp.MethodHandler) (mcp.Result, error) {
	id := ss.ID()
	if !s.sessions.reserve(id) {
		s.log.Printf("mcp: session limit %d reached; initialize refused", s.sessions.max)
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "too many MCP sessions; retry later"}
	}
	res, err := next(ctx, method, req)
	if err != nil {
		s.sessions.release(id)
		return res, err
	}
	params, _ := req.GetParams().(*mcp.InitializeParams)
	touch := service.TouchAgentSessionReq{SessionKey: id, AgentName: agentName(caller, params), Kind: sessionKind}
	if params != nil && params.ClientInfo != nil {
		touch.ClientName, touch.ClientVersion = params.ClientInfo.Name, params.ClientInfo.Version
	}
	if _, err := s.services.Sessions.TouchAgentSession(ctx, caller, touch); err != nil {
		s.log.Printf("mcp: create agent session %s failed: %+v", id, err)
	}
	go func() {
		_ = ss.Wait()
		s.sessions.release(id)
		// The request's context is gone with its request; the removal outlives it.
		if _, err := s.services.Sessions.RemoveAgentSession(context.WithoutCancel(ctx), caller, service.RemoveAgentSessionReq{SessionKey: id}); err != nil {
			s.log.Printf("mcp: remove agent session %s failed: %+v", id, err)
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

func isNotification(method string) bool {
	return len(method) > len("notifications/") && method[:len("notifications/")] == "notifications/"
}

// subscribe refuses every URI: R0 has no resource, and a subscription that can never deliver is
// refused rather than accepted silently. R1.7 registers the resources and replaces this.
func subscribe(_ context.Context, req *mcp.SubscribeRequest) error {
	return mcp.ResourceNotFoundError(req.Params.URI)
}

func unsubscribe(context.Context, *mcp.UnsubscribeRequest) error { return nil }
