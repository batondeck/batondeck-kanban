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
	return c, nil
}
