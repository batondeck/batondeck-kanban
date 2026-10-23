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

func (t *sessionTable) release(id string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.live, id)
}

// sessionKind is how a session's caller authenticated. R0's only bearer is a CLI API token.
// TODO(R0.13): oauth_user for an OAuth access token.
const sessionKind service.SessionKind = "service_account"

// sessionMiddleware keeps the agent_sessions row of each MCP session: written at initialize (a
// session over the bound is refused there, and go-sdk drops a session whose initialize failed),
// touched by every tool call, removed when the session ends, by idle
// collection, DELETE or close. A bookkeeping failure is logged and never fails the request.
//
// The row is keyed by (identity, agent), not by MCP session: service.Sessions reads a touch with
// no tool as a connection (its session count grows), so only initialize sends one.
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
			touch := service.TouchAgentSessionReq{SessionKey: agentSessionKey(caller), AgentName: agentName(caller, ss.InitializeParams()),
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
	key := agentSessionKey(caller)
	touch := service.TouchAgentSessionReq{SessionKey: key, AgentName: agentName(caller, params), Kind: sessionKind}
	if params != nil && params.ClientInfo != nil {
		touch.ClientName, touch.ClientVersion = params.ClientInfo.Name, params.ClientInfo.Version
	}
	if _, err := s.services.Sessions.TouchAgentSession(ctx, caller, touch); err != nil {
		s.log.Printf("mcp: create agent session %s failed: %+v", key, err)
	}
	go func() {
		_ = ss.Wait()
		s.sessions.release(id)
		// The request's context is gone with its request; the removal outlives it. NOT_FOUND is
		// another session of the same agent having removed the row first.
		_, err := s.services.Sessions.RemoveAgentSession(context.WithoutCancel(ctx), caller, service.RemoveAgentSessionReq{SessionKey: key})
		if e, ok := domain.AsBatonDeckError(err); err != nil && !(ok && e.Code == domain.CodeNotFound) {
			s.log.Printf("mcp: remove agent session %s failed: %+v", key, err)
		}
	}()
	return res, nil
}

// agentSessionKey is the caller's agent-session key, service core's "<identity>::<agent id>" with
// the agent id the caller's agent, else "id:<identity>".
// TODO(R0.21): service.SessionKey(caller.Identity, service.AgentID(caller)) once service core lands.
func agentSessionKey(c domain.Caller) string {
	agent := c.Agent
	if agent == "" {
		agent = "id:" + c.Identity
	}
	return c.Identity + "::" + agent
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
