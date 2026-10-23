// Package mcp is the MCP transport (ADR-0003, layer 3): a go-sdk Server behind the stateful
// Streamable HTTP handler at /mcp. Every request's bearer resolves to a domain.Caller through
// auth.Resolver (auth.go); a tool error is the one BatonDeckError shape (errors.go); each MCP session
// is bounded, collected when idle and mirrored as an agent session through service.Sessions
// (session.go). It calls internal/service and nothing below it.
package mcp

import (
	"context"
	"io"
	"log"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
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
	// ResourceMetadataURL is the RFC 9728 protected-resource metadata a 401 names in its
	// WWW-Authenticate challenge; "" names none (no OAuth server).
	ResourceMetadataURL string
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
	manifest *manifest
	halted   context.Context // done once Close has given up waiting for the handlers
	halt     context.CancelFunc
}

// New builds the server: the instructions, the four prompts and the resource templates, tools
// declared (each area adds its own), stateful sessions. Resources are readable and never pushed:
// no subscribe capability is advertised and no SubscribeHandler is set, so go-sdk answers
// resources/subscribe with method not found (ADR-0004).
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
	s.halted, s.halt = context.WithCancel(context.Background())
	s.sessions = &sessionTable{max: o.MaxSessions, live: map[string]*mcp.ServerSession{}, agents: map[string]int{}}
	s.manifest = &manifest{entries: map[string]ManifestEntry{}}
	s.tools = &toolList{memo: map[audience]*mcp.ListToolsResult{}, visible: map[string]func(audience) bool{}, seen: map[string]audience{}, log: s.log}
	s.sdk = mcp.NewServer(&mcp.Implementation{Name: "batondeck-kanban", Version: o.Version}, &mcp.ServerOptions{
		Instructions: Instructions,
		Capabilities: &mcp.ServerCapabilities{
			Tools:     &mcp.ToolCapabilities{ListChanged: true},
			Prompts:   &mcp.PromptCapabilities{ListChanged: true},
			Resources: &mcp.ResourceCapabilities{ListChanged: true},
		},
	})
	// The sending handler, kept to notify one session (go-sdk exports no per-session list_changed).
	s.sdk.AddSendingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler { s.tools.send = next; return next })
	registerPrompts(s.sdk)
	registerResources(s)
	// The first is outermost: the handler's context is cancelled at Close's deadline, and the caller
	// rides it into the session bookkeeping, the tools/list filter and the error shape, which sits
	// next to the tool handler.
	s.sdk.AddReceivingMiddleware(s.haltable, withCaller, s.sessionMiddleware, s.tools.middleware, s.errorMiddleware)
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.sdk }, &mcp.StreamableHTTPOptions{
		Stateless:      false,
		SessionTimeout: o.SessionIdle,
	})
	s.handler = requireBearer(o.Resolver, o.ResourceMetadataURL, s.log)(streamable)
	return s
}

// Close refuses new sessions, ends every live one, each as any other end does (its agent session
// removed with the last of its agent's), and waits for that bookkeeping or ctx. A session's end
// waits for its in-flight handlers, so at ctx's end Close cancels their contexts and returns
// ctx.Err(). Call it before the HTTP server's Shutdown: a session's open GET stream would
// otherwise hold Shutdown until its deadline.
func (s *Server) Close(ctx context.Context) error {
	live := s.sessions.closeAll()
	done := make(chan struct{})
	go func() {
		var closing sync.WaitGroup
		for _, ss := range live {
			closing.Go(func() { _ = ss.Close() })
		}
		closing.Wait()
		s.sessions.ends.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		s.halt()
		return ctx.Err()
	}
}

// haltable derives every handler's context from the server's, which Close cancels at its deadline.
func (s *Server) haltable(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		defer context.AfterFunc(s.halted, cancel)()
		return next(ctx, method, req)
	}
}

// Handler is what /mcp mounts.
func (s *Server) Handler() http.Handler { return s.handler }

// SDK is the go-sdk server the tool areas register on, through AddTool.
func (s *Server) SDK() *mcp.Server { return s.sdk }

// audience is what the tool list is filtered and memoised by: the part of a caller that decides
// which tools it may see. A token carries no project role (membership decides it per call), so the
// workspace-admin flag is all of it.
type audience struct{ admin bool }

// toolList filters tools/list per audience and memoises the answer. gen counts registrations, so
// a list read before one is never memoised after it. seen is each live session's audience as of its
// last request: one that changes (the caller's workspace-admin flag, re-read through the 60 s
// account cache) sends that session notifications/tools/list_changed through send.
type toolList struct {
	mu      sync.Mutex
	gen     int
	memo    map[audience]*mcp.ListToolsResult
	visible map[string]func(audience) bool // tool name -> who sees it; absent: everyone
	seen    map[string]audience            // MCP session id -> its audience
	send    mcp.MethodHandler              // go-sdk's sending handler
	log     *log.Logger
}

// AddTool registers a tool on s. visible says which callers see it in tools/list (nil: every
// caller); it decides visibility only, never authority, which service.authorize holds. A handler
// error that is neither a BatonDeckError nor a protocol error is logged and becomes INTERNAL, so
// the error middleware reads any other failure as go-sdk refusing the arguments.
func AddTool[In, Out any](s *Server, t *mcp.Tool, visible func(admin bool) bool, h mcp.ToolHandlerFor[In, Out]) {
	wrapped := func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		res, out, err := h(ctx, req, in)
		if err == nil {
			return res, out, nil
		}
		if _, ok := domain.AsBatonDeckError(err); ok {
			return res, out, err
		}
		if _, ok := err.(*jsonrpc.Error); ok {
			return res, out, err
		}
		s.log.Printf("mcp: tool %s failed: %+v\n%s", t.Name, err, debug.Stack())
		return res, out, domain.NewError(domain.CodeInternal, "internal error", nil)
	}
	s.tools.mu.Lock()
	defer s.tools.mu.Unlock()
	mcp.AddTool(s.sdk, t, wrapped)
	if visible != nil {
		s.tools.visible[t.Name] = func(a audience) bool { return visible(a.admin) }
	} else {
		delete(s.tools.visible, t.Name)
	}
	s.tools.gen++
	clear(s.tools.memo)
}
