// Package mcp is the MCP transport (ADR-0003, layer 3): a go-sdk Server behind the stateful
// Streamable HTTP handler at /mcp. Every request's bearer resolves to a domain.Caller through
// auth.Resolver (auth.go); a tool error is the one BatonDeckError shape (errors.go); each MCP session
// is bounded, collected when idle and mirrored as an agent session through service.Sessions
// (session.go). It calls internal/service and nothing below it.
package mcp

import (
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

// DefaultSessionIdle is how long a session lives without a request before it is collected
// (plan §Performance "MCP sessions GC after 30 min idle").
const DefaultSessionIdle = 30 * time.Minute

// DefaultMaxSessions bounds the live sessions; an initialize past it is refused.
const DefaultMaxSessions = 1000

// Options builds a Server.
type Options struct {
	// Version is the binary's version, reported as serverInfo.version.
	Version string
	// Resolver turns each request's bearer into its caller.
	Resolver auth.Resolver
	// Services is what the tools and the session bookkeeping call.
	Services service.Services
	// SessionIdle: 0 means DefaultSessionIdle.
	SessionIdle time.Duration
	// MaxSessions: 0 means DefaultMaxSessions.
	MaxSessions int
	// Logger receives refusals, bookkeeping failures and recovered panics; nil discards them.
	Logger *log.Logger
}

// Server is the MCP server and its HTTP handler.
type Server struct {
	sdk      *mcp.Server
	services service.Services
	log      *log.Logger
	handler  http.Handler
	sessions *sessionTable
	tools    *toolList
}

// New builds the server: tools, prompts and resources declared (all empty in R0), both subscribe
// handlers set (go-sdk panics with one), stateful sessions.
func New(o Options) *Server {
	if o.SessionIdle == 0 {
		o.SessionIdle = DefaultSessionIdle
	}
	if o.MaxSessions == 0 {
		o.MaxSessions = DefaultMaxSessions
	}
	if o.Logger == nil {
		o.Logger = log.New(io.Discard, "", 0)
	}
	s := &Server{services: o.Services, log: o.Logger}
	s.sessions = &sessionTable{max: o.MaxSessions, live: map[string]struct{}{}}
	s.tools = &toolList{memo: map[audience]*mcp.ListToolsResult{}, visible: map[string]func(audience) bool{}}
	s.sdk = mcp.NewServer(&mcp.Implementation{Name: "batondeck-kanban", Version: o.Version}, &mcp.ServerOptions{
		Capabilities: &mcp.ServerCapabilities{
			Tools:     &mcp.ToolCapabilities{ListChanged: true},
			Prompts:   &mcp.PromptCapabilities{ListChanged: true},
			Resources: &mcp.ResourceCapabilities{ListChanged: true, Subscribe: true},
		},
		SubscribeHandler:   subscribe,
		UnsubscribeHandler: unsubscribe,
	})
	// The first is outermost: the caller rides the context into the session bookkeeping, the
	// tools/list filter and the error shape, which sits next to the tool handler.
	s.sdk.AddReceivingMiddleware(withCaller, s.sessionMiddleware, s.tools.middleware, s.errorMiddleware)
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.sdk }, &mcp.StreamableHTTPOptions{
		Stateless:      false,
		SessionTimeout: o.SessionIdle,
	})
	s.handler = requireBearer(o.Resolver, s.log)(streamable)
	return s
}

// Handler is what /mcp mounts.
func (s *Server) Handler() http.Handler { return s.handler }

// SDK is the go-sdk server the tool areas register on, through AddTool.
func (s *Server) SDK() *mcp.Server { return s.sdk }

// audience is what the tool list is filtered and memoised by: the part of a caller that decides
// which tools it may see. A token carries no project role (membership decides it per call), so the
// workspace-admin flag is all of it.
type audience struct{ admin bool }

// toolList filters tools/list per audience and memoises the answer.
type toolList struct {
	mu      sync.Mutex
	memo    map[audience]*mcp.ListToolsResult
	visible map[string]func(audience) bool // tool name -> who sees it; absent: everyone
}

// AddTool registers a tool on s. visible says which callers see it in tools/list (nil: every
// caller); it decides visibility only, never authority, which service.authorize holds.
func AddTool[In, Out any](s *Server, t *mcp.Tool, visible func(admin bool) bool, h mcp.ToolHandlerFor[In, Out]) {
	s.tools.mu.Lock()
	if visible != nil {
		s.tools.visible[t.Name] = func(a audience) bool { return visible(a.admin) }
	} else {
		delete(s.tools.visible, t.Name)
	}
	clear(s.tools.memo)
	s.tools.mu.Unlock()
	mcp.AddTool(s.sdk, t, h)
}
