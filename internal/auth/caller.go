package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// DefaultAccountCacheTTL is how long an account's enabled state is trusted before it is read
// again: the plan's membership/grant cache window, and so the longest a disabled user's sessions
// and tokens keep working.
const DefaultAccountCacheTTL = 60 * time.Second

// AnonymousActor is the audit actor of an event no identity made: a refused request that carried
// no credential, a dynamic client registration.
const AnonymousActor = "anonymous"

// AuditEvent is one audit row as the audit_log table holds it; the writer stamps the time.
type AuditEvent struct {
	Action    string // dotted, e.g. "request.refused"
	Actor     string // the identity, "" when the request had none
	Subject   string // e.g. "path:/api/x"
	Result    string // "refused"
	ErrorCode string // e.g. "FORBIDDEN"
	Data      map[string]any
}

// Service is the auth surface the server mounts: ResolveCaller for bearers, the session login and
// middleware, and the CSRF check.
type Service struct {
	Accounts *Accounts
	Tokens   *TokenService
	Sessions store.Sessions
	// Audit receives the CSRF check's refusals and the OAuth server's events (client registered,
	// consent granted or denied, grant revoked); serve sets StoreAudit. nil records nothing.
	Audit func(ctx context.Context, e AuditEvent)
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// AccountCacheTTL: 0 means DefaultAccountCacheTTL.
	AccountCacheTTL time.Duration
	// SessionTTL: 0 means DefaultSessionTTL.
	SessionTTL time.Duration
	// OAuth is the OAuth server's tokens (internal/auth/oauth issues them); nil resolves API tokens
	// only.
	OAuth store.OAuth
	// OAuthResource is the resource (RFC 8707) this service's bearers are presented at: an OAuth
	// access token issued for any other is refused.
	OAuthResource string
	// PublicURL is the server's public_url ("" when unset). When it is https the session cookies are
	// Secure whatever the request says (a TLS proxy may forward plain http and no
	// X-Forwarded-Proto), and the CSRF check takes a browser's Origin that is its origin as this
	// server's own (a proxy may forward another Host).
	PublicURL string
	// Proxies are the trusted reverse proxies: a sign-in through one counts against the client its
	// X-Forwarded-For names (TrustedProxies.ClientAddr); nil reads no header.
	Proxies TrustedProxies

	mu          sync.Mutex
	cache       map[string]accountEntry
	clientNames map[string]string // OAuth client id -> its agent name (oauthClientName)
	refusals    *buckets          // the audit budget of refusals, per actor (csrf.go)
}

type accountEntry struct {
	usable bool // exists and is not disabled
	admin  bool
	at     time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

type freshAccountKey struct{}

// FreshAccount marks ctx so that a ResolveCaller under it reads the account from the store rather
// than the cache (and refreshes the cache): /wake's periodic recheck, so a disable ends an open
// stream within one recheck whatever other traffic keeps the cache warm.
func FreshAccount(ctx context.Context) context.Context {
	return context.WithValue(ctx, freshAccountKey{}, true)
}

// account answers whether the user may act, from the cache while its entry is younger than
// AccountCacheTTL (unless ctx is FreshAccount's), else from the store.
func (s *Service) account(ctx context.Context, userID string) (accountEntry, error) {
	ttl := s.AccountCacheTTL
	if ttl == 0 {
		ttl = DefaultAccountCacheTTL
	}
	now := s.now()
	s.mu.Lock()
	e, ok := s.cache[userID]
	s.mu.Unlock()
	if fresh, _ := ctx.Value(freshAccountKey{}).(bool); ok && !fresh && now.Sub(e.at) < ttl {
		return e, nil
	}
	u, err := s.Accounts.Store.GetUser(ctx, userID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		e = accountEntry{at: now}
	case err != nil:
		return accountEntry{}, err
	default:
		e = accountEntry{usable: u.DisabledAt == 0, admin: u.WorkspaceAdmin, at: now}
	}
	s.mu.Lock()
	if s.cache == nil {
		s.cache = map[string]accountEntry{}
	}
	s.cache[userID] = e
	s.mu.Unlock()
	return e, nil
}

// callerFor is the principal a user acts as. A token or session carries no role: the project
// membership decides it per call. The doorbell scope is the identity.
func callerFor(userID string, e accountEntry) domain.Caller {
	return domain.Caller{Identity: userID, WorkspaceAdmin: e.admin, TokenScope: userID}
}

// ResolveCaller resolves an API token or an OAuth access token (with or without a leading
// "Bearer ") to its principal.
func (s *Service) ResolveCaller(ctx context.Context, bearer string) (domain.Caller, error) {
	bearer = strings.TrimSpace(bearer)
	if len(bearer) > 7 && strings.EqualFold(bearer[:7], "bearer ") {
		bearer = strings.TrimSpace(bearer[7:])
	}
	if strings.HasPrefix(bearer, OAuthAccessPrefix) {
		return s.resolveOAuth(ctx, bearer)
	}
	row, err := s.Tokens.Validate(ctx, bearer)
	if err != nil {
		return domain.Caller{}, err
	}
	e, err := s.account(ctx, row.IdentityID)
	if err != nil {
		return domain.Caller{}, err
	}
	if !e.usable {
		return domain.Caller{}, errUnauthenticated()
	}
	c := callerFor(row.IdentityID, e)
	c.Via, c.Agent = domain.ViaAPIToken, domain.TokenAgent(row.Label) // audit B1
	if strings.HasPrefix(c.Agent, "id:") {
		// A label minted before Create refused "id:" (P-352) names no agent, as the default label
		// does: "id:<identity>" is the id AgentID derives, never a name a token carries.
		c.Agent = ""
	}
	c.Labelled = c.Agent != ""
	return c, nil
}
