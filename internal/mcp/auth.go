package mcp

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

// callerKey carries the caller in a TokenInfo's Extra, which is how go-sdk hands a per-request
// value to a method handler on a stateful session (RequestExtra.TokenInfo, set from each POST).
const callerKey = "batondeck.caller"

// requestBaseKey carries the POST's service.RequestBase the same way: a tool call's ctx on a
// stateful session holds the initialize POST's values, not its own POST's, so the blob URLs it
// signs would otherwise keep the host the session was opened at.
const requestBaseKey = "batondeck.requestBase"

// requireBearer resolves every request's bearer through r. No bearer, or one the resolver refuses
// as UNAUTHENTICATED, is 401 with `WWW-Authenticate: Bearer`, naming resourceMetadata (RFC 9728)
// when it is set; any other resolver error is 500. The TokenInfo's UserID is sessionBinding, so
// go-sdk refuses (403) a session presented with another identity's bearer, or with a bearer of the
// same identity that authenticated another way or carries another agent (P-323).
func requireBearer(r auth.Resolver, resourceMetadata string, logger *log.Logger) func(http.Handler) http.Handler {
	verify := func(ctx context.Context, token string, req *http.Request) (*sdkauth.TokenInfo, error) {
		c, err := r.ResolveCaller(ctx, token)
		if err != nil {
			if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeUnauthenticated {
				return nil, fmt.Errorf("%w: %s", sdkauth.ErrInvalidToken, e.Message)
			}
			logger.Printf("mcp: resolve bearer failed: %+v", err)
			return nil, fmt.Errorf("resolving the bearer failed")
		}
		extra := map[string]any{callerKey: c}
		if base := service.RequestBase(req.Context()); base != "" {
			extra[requestBaseKey] = base
		}
		return &sdkauth.TokenInfo{UserID: sessionBinding(c), Extra: extra}, nil
	}
	// Tokens are checked for expiry by the resolver, so the TokenInfo carries none.
	bearer := sdkauth.RequireBearerToken(verify, &sdkauth.RequireBearerTokenOptions{AllowMissingExpiration: true, ResourceMetadataURL: resourceMetadata})
	return func(next http.Handler) http.Handler {
		h := bearer(next)
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			h.ServeHTTP(&challengeWriter{ResponseWriter: w}, req)
		})
	}
}

// sessionBinding is what a stateful session is bound to: the bearer's identity, how it
// authenticated, and the agent it carries (its token's label, or its OAuth client's name; "" for a
// default-label token, whose agent is the session's own clientInfo.name). The session's
// agent_sessions row is keyed at initialize, so a later bearer that would act as another agent is
// refused rather than working under that row (P-323).
func sessionBinding(c domain.Caller) string {
	return c.Identity + "\x00" + c.Via + "\x00" + c.Agent
}

// challengeWriter adds `WWW-Authenticate: Bearer` to a 401 that carries no challenge. go-sdk writes
// one only when a resource-metadata URL or scopes are configured, so this covers a server with no
// OAuth server (public_url unset).
type challengeWriter struct {
	http.ResponseWriter
}

func (w *challengeWriter) WriteHeader(code int) {
	if code == http.StatusUnauthorized && w.Header().Get("WWW-Authenticate") == "" {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	w.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the writer's Flush, which the event stream needs.
func (w *challengeWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush passes through, for the streamable transport's SSE responses.
func (w *challengeWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

type callerCtxKey struct{}

// CallerFrom is the caller of the MCP request ctx belongs to.
func CallerFrom(ctx context.Context) (domain.Caller, bool) {
	c, ok := ctx.Value(callerCtxKey{}).(domain.Caller)
	return c, ok
}

// withCaller puts the request's caller on its context, for every method handler after it. A caller
// whose bearer names no agent (an API token with the default label) is told apart by the MCP
// initialize clientInfo.name of its session (audit B1), the initialize's own on that request. The
// POST's request base (service.RequestBase) goes on the context too, for the blob URLs it signs.
func withCaller(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if c, ok := callerOf(req); ok {
			if c.Agent == "" {
				c.Agent = clientAgent(req)
			}
			ctx = context.WithValue(ctx, callerCtxKey{}, c)
		}
		if base := requestBaseOf(req); base != "" {
			ctx = service.WithRequestBase(ctx, base)
		}
		return next(ctx, method, req)
	}
}

// clientAgent is the clientInfo.name of req's initialize, or of its session's, as an agent name. A
// name starting "id:" names no agent, as a token label of that shape does (auth.ResolveCaller):
// "id:<identity>" is the id service.AgentID derives, never a name a client declares, so a client
// cannot stamp another identity's derived id on its own work.
func clientAgent(req mcp.Request) string {
	p, _ := req.GetParams().(*mcp.InitializeParams)
	if p == nil {
		if ss, ok := req.GetSession().(*mcp.ServerSession); ok {
			p = ss.InitializeParams()
		}
	}
	if p == nil || p.ClientInfo == nil {
		return ""
	}
	if name := domain.AgentLabel(p.ClientInfo.Name); !strings.HasPrefix(name, "id:") {
		return name
	}
	return ""
}

func callerOf(req mcp.Request) (domain.Caller, bool) {
	extra := req.GetExtra()
	if extra == nil || extra.TokenInfo == nil {
		return domain.Caller{}, false
	}
	c, ok := extra.TokenInfo.Extra[callerKey].(domain.Caller)
	return c, ok
}

// requestBaseOf is the service.RequestBase of the POST that carried req, "" for none.
func requestBaseOf(req mcp.Request) string {
	extra := req.GetExtra()
	if extra == nil || extra.TokenInfo == nil {
		return ""
	}
	b, _ := extra.TokenInfo.Extra[requestBaseKey].(string)
	return b
}
