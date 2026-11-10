package oauth

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

const (
	maxRedirectURIs   = 10
	maxRedirectURILen = 2000
	maxClientNameLen  = 200
	maxScopeLen       = 200
)

// registration is the part of an RFC 7591 request this server reads; other members are ignored.
type registration struct {
	RedirectURIs  []string `json:"redirect_uris"`
	ClientName    string   `json:"client_name"`
	GrantTypes    []string `json:"grant_types"`
	ResponseTypes []string `json:"response_types"`
	Scope         string   `json:"scope"`
}

// register is RFC 7591 dynamic registration of a public client. It is unauthenticated, so it is
// bounded where it enters: a per-address rate limit (429 past it), a 64 KiB body, at most 10
// redirect URIs; and each call first deletes the registrations no authorization used within
// UnusedClientTTL. The client authenticates at /token by PKCE alone: whatever
// token_endpoint_auth_method it asks for, it is registered "none", and the answer says so.
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	if ok, retry := s.limiter.allow(s.o.Auth.Proxies.ClientAddr(r), now); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(retry.Seconds()))))
		oauthError(w, http.StatusTooManyRequests, "temporarily_unavailable",
			fmt.Sprintf("too many registrations from this address; retry after %d s", int(math.Ceil(retry.Seconds()))))
		return
	}
	var req registration
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxFormBody))
	if err := dec.Decode(&req); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_client_metadata", "the body is not a JSON registration request")
		return
	}
	if code, desc := checkRegistration(req); code != "" {
		oauthError(w, http.StatusBadRequest, code, desc)
		return
	}
	ctx := r.Context()
	if n, err := s.o.Store.DeleteUnusedOAuthClients(ctx, now.Add(-UnusedClientTTL).UnixMilli()); err != nil {
		s.o.Logger.Printf("oauth: collecting unused registrations: %+v", err)
	} else if n > 0 {
		s.o.Logger.Printf("oauth: deleted %d registrations unused for %s", n, UnusedClientTTL)
	}
	id, err := clientID()
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "could not register the client")
		return
	}
	c := store.OAuthClient{ClientID: id, ClientName: req.ClientName, RedirectURIs: req.RedirectURIs,
		Scope: req.Scope, CreatedAt: now.UnixMilli()}
	if err := s.o.Store.CreateOAuthClient(ctx, c); err != nil {
		s.o.Logger.Printf("oauth: registering a client: %+v", err)
		oauthError(w, http.StatusInternalServerError, "server_error", "could not register the client")
		return
	}
	s.audit(r.Context(), auth.AuditEvent{Action: "oauth.client.registered", Subject: "oauth_client:" + id, Result: "ok",
		Data: map[string]any{"clientName": req.ClientName, "redirectUris": req.RedirectURIs}})
	body := map[string]any{
		"client_id":                  id,
		"client_id_issued_at":        now.Unix(),
		"redirect_uris":              req.RedirectURIs,
		"grant_types":                []string{grantCode, grantRefresh},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}
	if req.ClientName != "" {
		body["client_name"] = req.ClientName
	}
	if req.Scope != "" {
		body["scope"] = req.Scope
	}
	writeJSON(w, http.StatusCreated, body)
}

// checkRegistration is the RFC 7591 §3.2.2 error code and description for a request this server
// will not register, "" for one it will.
func checkRegistration(req registration) (code, desc string) {
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > maxRedirectURIs {
		return "invalid_redirect_uri", fmt.Sprintf("redirect_uris must list 1 to %d URIs", maxRedirectURIs)
	}
	for _, u := range req.RedirectURIs {
		if why := badRedirectURI(u); why != "" {
			return "invalid_redirect_uri", why
		}
	}
	for _, g := range req.GrantTypes {
		if g != grantCode && g != grantRefresh {
			return "invalid_client_metadata", "grant_types may hold only authorization_code and refresh_token"
		}
	}
	if len(req.GrantTypes) > 0 && !slices.Contains(req.GrantTypes, grantCode) {
		return "invalid_client_metadata", "grant_types must include authorization_code"
	}
	for _, t := range req.ResponseTypes {
		if t != "code" {
			return "invalid_client_metadata", "response_types may hold only code"
		}
	}
	// A format character (unicode.Cf: a bidi override, a zero-width joiner) is refused with the
	// control characters: the name is shown on the sign-in and consent pages, where one can make it
	// read as another.
	if len(req.ClientName) > maxClientNameLen || strings.IndexFunc(req.ClientName, hiddenNameRune) >= 0 {
		return "invalid_client_metadata", fmt.Sprintf("client_name is at most %d characters, none of them a control or format character", maxClientNameLen)
	}
	if len(req.Scope) > maxScopeLen {
		return "invalid_client_metadata", fmt.Sprintf("scope is at most %d characters", maxScopeLen)
	}
	return "", ""
}

// hiddenNameRune is a character /register refuses in a client_name: a control character, or a
// format character (unicode.Cf) that can make a shown name read as another.
func hiddenNameRune(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }

// badRedirectURI says why u cannot be a redirect URI, "" when it can: an absolute URI with no
// fragment (RFC 6749 §3.1.2) and no userinfo, https, or http only to a loopback host (RFC 8252 §7.3), or an app's
// private-use scheme (§7.1); never a scheme that runs or reads in the browser.
func badRedirectURI(u string) string {
	if len(u) > maxRedirectURILen {
		return fmt.Sprintf("a redirect URI is at most %d bytes", maxRedirectURILen)
	}
	p, err := url.Parse(u)
	if err != nil || !p.IsAbs() {
		return "a redirect URI must be an absolute URI"
	}
	if p.Fragment != "" || strings.Contains(u, "#") {
		return "a redirect URI must not carry a fragment"
	}
	if p.User != nil {
		// "https://claude.ai@evil.example/cb" goes to evil.example but reads as claude.ai on the
		// consent page's "Returns to".
		return "a redirect URI must not carry a user name or password"
	}
	switch strings.ToLower(p.Scheme) {
	case "https":
		if p.Host == "" {
			return "an https redirect URI needs a host"
		}
	case "http":
		if !loopback(p.Hostname()) {
			return "an http redirect URI must point at a loopback host (localhost, 127.0.0.1, ::1); use https"
		}
	case "javascript", "data", "file", "vbscript", "about", "blob", "ftp", "ws", "wss":
		return "redirect URI scheme " + p.Scheme + " is not allowed"
	}
	return ""
}

func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func clientID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "bdc_" + hex.EncodeToString(b), nil
}

// limiter is a fixed window per address: max calls per window. It holds at most maxTracked
// addresses; past that, the windows that have ended are dropped first.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string]*hitWindow
}

type hitWindow struct {
	start time.Time
	n     int
}

const maxTracked = 10000

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, hits: map[string]*hitWindow{}}
}

// allow counts a call from addr at now: false, and the time until its window ends, past the limit.
func (l *limiter) allow(addr string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	h, ok := l.hits[addr]
	if !ok || now.Sub(h.start) >= l.window {
		if !ok && len(l.hits) >= maxTracked {
			l.prune(now)
		}
		h = &hitWindow{start: now}
		l.hits[addr] = h
	}
	if h.n >= l.max {
		return false, h.start.Add(l.window).Sub(now)
	}
	h.n++
	return true, 0
}

func (l *limiter) prune(now time.Time) {
	for a, h := range l.hits {
		if now.Sub(h.start) >= l.window {
			delete(l.hits, a)
		}
	}
	if len(l.hits) >= maxTracked {
		// Every window is live: forget them all rather than grow without bound.
		clear(l.hits)
	}
}
