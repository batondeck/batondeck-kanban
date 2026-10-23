package mcp

import (
	"context"
	"fmt"
	"log"
	"net/http"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// callerKey carries the caller in a TokenInfo's Extra, which is how go-sdk hands a per-request
// value to a method handler on a stateful session (RequestExtra.TokenInfo, set from each POST).
const callerKey = "batondeck.caller"

// requireBearer resolves every request's bearer through r. No bearer, or one the resolver refuses
// as UNAUTHENTICATED, is 401 with `WWW-Authenticate: Bearer`; any other resolver error is 500. The
// caller's identity is the TokenInfo's UserID, so go-sdk refuses (403) a session presented with
// another identity's bearer.
func requireBearer(r auth.Resolver, logger *log.Logger) func(http.Handler) http.Handler {
	verify := func(ctx context.Context, token string, _ *http.Request) (*sdkauth.TokenInfo, error) {
		c, err := r.ResolveCaller(ctx, token)
		if err != nil {
			if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeUnauthenticated {
				return nil, fmt.Errorf("%w: %s", sdkauth.ErrInvalidToken, e.Message)
			}
			logger.Printf("mcp: resolve bearer failed: %+v", err)
			return nil, fmt.Errorf("resolving the bearer failed")
		}
		return &sdkauth.TokenInfo{UserID: c.Identity, Extra: map[string]any{callerKey: c}}, nil
	}
	// Tokens are checked for expiry by the resolver, so the TokenInfo carries none.
	bearer := sdkauth.RequireBearerToken(verify, &sdkauth.RequireBearerTokenOptions{AllowMissingExpiration: true})
	return func(next http.Handler) http.Handler {
		h := bearer(next)
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			h.ServeHTTP(&challengeWriter{ResponseWriter: w}, req)
		})
	}
}

// challengeWriter adds `WWW-Authenticate: Bearer` to a 401 that carries no challenge. go-sdk writes
// one only when a resource-metadata URL or scopes are configured; R0.13 sets the URL (RFC 9728).
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

// withCaller puts the request's caller on its context, for every method handler after it.
func withCaller(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if c, ok := callerOf(req); ok {
			ctx = context.WithValue(ctx, callerCtxKey{}, c)
		}
		return next(ctx, method, req)
	}
}

func callerOf(req mcp.Request) (domain.Caller, bool) {
	extra := req.GetExtra()
	if extra == nil || extra.TokenInfo == nil {
		return domain.Caller{}, false
	}
	c, ok := extra.TokenInfo.Extra[callerKey].(domain.Caller)
	return c, ok
}
