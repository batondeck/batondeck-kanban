// Package oauth is the OAuth 2.1 authorization server MCP remote connectors (claude.ai, ChatGPT,
// mcp-remote, the MCP Inspector) sign in through, ported from conductor's
// gateway/mcp-gateway/src/oauth/as.ts with one stated departure: its tokens are opaque values held
// hashed in the store (internal/auth resolves them at /mcp), not RS256 JWTs, so there is no JWKS.
// The identity provider is the local session login of internal/auth plus a consent page, in place of
// WorkOS.
//
//	GET  /.well-known/oauth-authorization-server   RFC 8414 metadata (metadata.go)
//	GET  /.well-known/oauth-protected-resource[/mcp]  RFC 9728 metadata of /mcp (resource.go)
//	POST /register                                 RFC 7591 dynamic registration (register.go)
//	GET  /authorize                                the sign-in page, then the consent page (authorize.go)
//	POST /authorize                                the consent decision, CSRF-checked (consent.go)
//	POST /login                                    the sign-in the page posts (internal/auth's LoginHandler)
//	POST /token                                    authorization_code and refresh_token (token.go)
//
// Every URL is built from the public_url setting, as conductor builds them from config.publicUrl.
package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// Lifetimes: conductor's access (OAUTH_ACCESS_TTL_SEC 3600) and refresh (OAUTH_REFRESH_TTL_SEC
// 1209600) defaults; a code lives as long as conductor's in-flight authorization (600 s).
const (
	DefaultAccessTTL  = time.Hour
	DefaultRefreshTTL = 14 * 24 * time.Hour
	DefaultCodeTTL    = 10 * time.Minute
)

// Registration bounds: DefaultRegisterLimit registrations per client address per
// DefaultRegisterWindow, and a registration no authorization used within UnusedClientTTL is
// deleted at the next registration.
const (
	DefaultRegisterLimit  = 20
	DefaultRegisterWindow = time.Hour
	UnusedClientTTL       = 24 * time.Hour
)

// MCPPath is the protected resource's path: the resource is PublicURL + MCPPath.
const MCPPath = "/mcp"

const maxFormBody = 64 << 10

// Options builds a Server.
type Options struct {
	// PublicURL is the issuer and the base of every URL: scheme://host[:port], no path.
	PublicURL string
	// Resources are the resources (RFC 8707) tokens may be issued for; nil means the one,
	// PublicURL + MCPPath. The first is the one a request naming none gets.
	Resources []string
	// Store holds the clients, codes, families and tokens.
	Store store.Store
	// Auth is the session login, the CSRF check and the audit hook.
	Auth *auth.Service
	// Logger receives the refusals worth an operator's eye; nil discards them.
	Logger *log.Logger
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// AccessTTL, RefreshTTL, CodeTTL: 0 means the Default.
	AccessTTL, RefreshTTL, CodeTTL time.Duration
	// RegisterLimit per RegisterWindow per client address: 0 means the Default.
	RegisterLimit  int
	RegisterWindow time.Duration
}

// Server is the authorization server.
type Server struct {
	o       Options
	issuer  string
	limiter *limiter
}

// New checks the options and builds the server. A PublicURL that is not scheme://host[:port] is an
// error naming it.
func New(o Options) (*Server, error) {
	issuer, err := issuerOf(o.PublicURL)
	if err != nil {
		return nil, err
	}
	if o.Store == nil || o.Auth == nil {
		return nil, errors.New("oauth: Store and Auth are required")
	}
	if o.Resources == nil {
		o.Resources = []string{issuer + MCPPath}
	}
	o.Resources = slices.Clone(o.Resources)
	for i, r := range o.Resources {
		o.Resources[i] = strings.TrimRight(r, "/")
	}
	if o.Logger == nil {
		o.Logger = log.New(io.Discard, "", 0)
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.AccessTTL == 0 {
		o.AccessTTL = DefaultAccessTTL
	}
	if o.RefreshTTL == 0 {
		o.RefreshTTL = DefaultRefreshTTL
	}
	if o.CodeTTL == 0 {
		o.CodeTTL = DefaultCodeTTL
	}
	if o.RegisterLimit == 0 {
		o.RegisterLimit = DefaultRegisterLimit
	}
	if o.RegisterWindow == 0 {
		o.RegisterWindow = DefaultRegisterWindow
	}
	return &Server{o: o, issuer: issuer, limiter: newLimiter(o.RegisterLimit, o.RegisterWindow)}, nil
}

// issuerOf normalises public_url: an http(s) URL with a host and no path, query or fragment, without
// a trailing slash.
func issuerOf(publicURL string) (string, error) {
	u, err := url.Parse(strings.TrimRight(publicURL, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" ||
		u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return "", fmt.Errorf("oauth: public_url %q is not scheme://host[:port] (http or https, no path)", publicURL)
	}
	return u.Scheme + "://" + u.Host, nil
}

// Issuer is the issuer identifier, the normalised public_url.
func (s *Server) Issuer() string { return s.issuer }

// Resource is the resource a request naming none is issued for: the /mcp endpoint's URL.
func (s *Server) Resource() string { return s.o.Resources[0] }

// ResourceMetadataURL is the RFC 9728 document /mcp's 401 names.
func (s *Server) ResourceMetadataURL() string { return s.issuer + prmPath }

// Mount puts the server's routes on mux.
func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET "+asMetadataPath, cors(s.metadata))
	mux.HandleFunc("OPTIONS "+asMetadataPath, preflight)
	for path, resource := range s.prmPaths() {
		h := s.protectedResource(resource)
		mux.HandleFunc("GET "+path, cors(h))
		mux.HandleFunc("OPTIONS "+path, preflight)
	}
	mux.HandleFunc("POST /register", cors(s.register))
	mux.HandleFunc("OPTIONS /register", preflight)
	mux.HandleFunc("GET /authorize", s.authorizePage)
	mux.Handle("POST /authorize", s.o.Auth.Authenticate(s.o.Auth.CSRF(http.HandlerFunc(s.consent))))
	mux.Handle("POST /login", s.o.Auth.CSRF(s.o.Auth.LoginHandler()))
	mux.HandleFunc("POST /token", cors(s.token))
	mux.HandleFunc("OPTIONS /token", preflight)
}

// cors lets a browser-based client (the MCP Inspector) read the metadata and call /register and
// /token: none of them reads a cookie, so any origin may.
func cors(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		h(w, r)
	}
}

func preflight(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, MCP-Protocol-Version")
	w.Header().Set("Access-Control-Max-Age", "86400")
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// oauthError is the RFC 6749 §5.2 error body.
func oauthError(w http.ResponseWriter, status int, code, description string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": description})
}

// singleValued names a parameter given more than once (RFC 6749 §3.1 forbids it), "" for none.
func singleValued(v url.Values) string {
	for k, vs := range v {
		if len(vs) > 1 {
			return k
		}
	}
	return ""
}

func (s *Server) now() time.Time { return s.o.Now() }

func (s *Server) audit(ctx context.Context, e auth.AuditEvent) {
	if s.o.Auth.Audit != nil {
		s.o.Auth.Audit(ctx, e)
	}
}

// roles is the scope vocabulary: exactly the domain roles, in rank order.
func roles() []string {
	out := []string{}
	for _, r := range domain.AllRoles() {
		out = append(out, string(r))
	}
	return out
}
