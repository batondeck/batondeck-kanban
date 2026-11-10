package auth

// OAuth access tokens at the resource: the opaque values internal/auth/oauth issues, resolved here
// beside API tokens so that every bearer a transport sees goes through one ResolveCaller. A value is
// a prefix plus 43 characters of base64url (32 random bytes); only its SHA-256 is stored. Every
// prefix starts "bd_", so scripts/check-secrets.py refuses one in a commit as it does an API token,
// and none parses as an API token (whose "bd_" is followed by 16 hex characters and "_").

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The prefixes of the OAuth server's three opaque values.
const (
	OAuthAccessPrefix  = "bd_at_"
	OAuthRefreshPrefix = "bd_rt_"
	OAuthCodePrefix    = "bd_ac_"
)

const oauthValueBytes = 32

// MintOAuthValue returns a new opaque value with prefix, and the hash it is stored under.
func MintOAuthValue(prefix string) (value, hash string, err error) {
	b := make([]byte, oauthValueBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	value = prefix + base64.RawURLEncoding.EncodeToString(b)
	return value, HashOAuthValue(value), nil
}

// HashOAuthValue is the hash an OAuth code or token is stored and looked up under.
func HashOAuthValue(value string) string { return hashSecret(value) }

// resolveOAuth resolves an OAuth access token: live (not expired, not revoked, its family not
// revoked), issued for s.OAuthResource, and acting for a usable account. Its scope is the role the
// user granted at consent, a ceiling on their project role; only the uncapped grant (admin) keeps
// the account's workspace-admin flag. Every refusal is one UNAUTHENTICATED.
func (s *Service) resolveOAuth(ctx context.Context, bearer string) (domain.Caller, error) {
	if s.OAuth == nil || len(bearer) > maxBearerLen {
		return domain.Caller{}, errUnauthenticated()
	}
	t, err := s.OAuth.GetOAuthToken(ctx, HashOAuthValue(bearer))
	if errors.Is(err, store.ErrNotFound) {
		return domain.Caller{}, errUnauthenticated()
	}
	if err != nil {
		return domain.Caller{}, err
	}
	if t.Kind != store.OAuthAccess || t.RevokedAt != 0 || t.FamilyRevokedAt != 0 ||
		t.ExpiresAt <= s.now().UnixMilli() || t.Resource != s.OAuthResource {
		return domain.Caller{}, errUnauthenticated()
	}
	role, err := domain.ParseRole(t.Scope)
	if err != nil {
		return domain.Caller{}, errUnauthenticated()
	}
	e, err := s.account(ctx, t.IdentityID)
	if err != nil {
		return domain.Caller{}, err
	}
	if !e.usable {
		return domain.Caller{}, errUnauthenticated()
	}
	c := callerFor(t.IdentityID, e)
	c.Role = string(role)
	c.WorkspaceAdmin = e.admin && role == domain.RoleAdmin
	c.Via, c.OAuthClientID = domain.ViaOAuth, t.ClientID
	if c.Agent, err = s.oauthClientName(ctx, t.ClientID); err != nil {
		return domain.Caller{}, err
	}
	return c, nil
}

// maxClientNames bounds the client-name cache; a full cache is emptied and fills again.
const maxClientNames = 1024

// oauthClientName is the agent an OAuth client's registered client_name names (audit B1), through
// domain.AgentLabel; a name starting "id:" names none, as a token label of that shape does. A client's name never changes after registration, so it is read once per
// client and kept: the per-request resolution, and the wake Sweep's 3 statements per entry, do not
// grow. A client that is gone names no agent (its tokens went with it).
func (s *Service) oauthClientName(ctx context.Context, clientID string) (string, error) {
	s.mu.Lock()
	name, ok := s.clientNames[clientID]
	s.mu.Unlock()
	if ok {
		return name, nil
	}
	cl, err := s.OAuth.GetOAuthClient(ctx, clientID)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	name = domain.AgentLabel(cl.ClientName)
	if strings.HasPrefix(name, "id:") {
		name = "" // "id:<identity>" is the id AgentID derives, never a name a client registers
	}
	s.mu.Lock()
	if s.clientNames == nil || len(s.clientNames) >= maxClientNames {
		s.clientNames = map[string]string{}
	}
	s.clientNames[clientID] = name
	s.mu.Unlock()
	return name, nil
}
