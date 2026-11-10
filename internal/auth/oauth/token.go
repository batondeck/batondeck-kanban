package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

const (
	grantCode    = "authorization_code"
	grantRefresh = "refresh_token"
)

// grantError is a /token refusal: the RFC 6749 §5.2 code and description, answered 400.
type grantError struct{ code, desc string }

func (e *grantError) Error() string { return e.code + ": " + e.desc }

func invalidGrant(desc string) *grantError { return &grantError{"invalid_grant", desc} }

// issued is what a successful grant answers.
type issued struct {
	access, refresh, scope string
}

// token is the token endpoint: authorization_code (PKCE S256 verified, the code used once) and
// refresh_token (rotated; a refresh token presented a second time revokes its whole family, so the
// sibling it was rotated into stops working too). A public client names itself with client_id and
// authenticates by PKCE alone.
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "the body is not a form")
		return
	}
	v := r.PostForm
	if k := singleValued(v); k != "" {
		oauthError(w, http.StatusBadRequest, "invalid_request", "parameter given more than once: "+k)
		return
	}
	var out issued
	var err error
	switch v.Get("grant_type") {
	case grantCode:
		out, err = s.exchangeCode(r.Context(), v.Get("code"), v.Get("code_verifier"), v.Get("client_id"), v.Get("redirect_uri"), v.Get("resource"))
	case grantRefresh:
		out, err = s.refresh(r.Context(), v.Get("refresh_token"), v.Get("client_id"), v.Get("resource"), v.Get("scope"))
	default:
		oauthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
		return
	}
	var ge *grantError
	if errors.As(err, &ge) {
		oauthError(w, http.StatusBadRequest, ge.code, ge.desc)
		return
	}
	if err != nil {
		s.o.Logger.Printf("oauth: token: %+v", err)
		oauthError(w, http.StatusInternalServerError, "server_error", "could not issue the token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token":  out.access,
		"token_type":    "Bearer",
		"expires_in":    int64(s.o.AccessTTL / time.Second),
		"refresh_token": out.refresh,
		"scope":         out.scope,
	})
}

// familyOf is the family a code starts: derived from the code's hash, so a code presented a second
// time can revoke the tokens its first use issued (RFC 6749 §4.1.2) with no column linking them.
func familyOf(codeHash string) string { return "fam_" + codeHash[:32] }

func s256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (s *Server) exchangeCode(ctx context.Context, code, verifier, clientID, redirectURI, resource string) (issued, error) {
	if code == "" || verifier == "" || clientID == "" || redirectURI == "" {
		return issued{}, &grantError{"invalid_request", "code, code_verifier, client_id and redirect_uri are required"}
	}
	if !strings.HasPrefix(code, auth.OAuthCodePrefix) || len(code) > 256 {
		return issued{}, invalidGrant("invalid or expired authorization code")
	}
	hash := auth.HashOAuthValue(code)
	now := s.now()
	var out issued
	var refused *grantError
	var revoked *auth.AuditEvent // written after the transaction, never inside it
	err := s.o.Store.Atomically(ctx, func(tx store.Store) error {
		c, err := tx.ConsumeOAuthCode(ctx, hash, now.UnixMilli())
		switch {
		case errors.Is(err, store.ErrNotFound):
			refused = invalidGrant("invalid or expired authorization code")
			return nil
		case errors.Is(err, store.ErrOAuthUsed):
			// A second exchange: refuse it and end whatever the first one issued. The revocation
			// commits with this transaction; the refusal is returned after it.
			if err := tx.RevokeOAuthFamily(ctx, familyOf(hash), now.UnixMilli(), "code_reuse"); err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			revoked = &auth.AuditEvent{Action: "oauth.grant.revoked", Actor: c.IdentityID, Subject: "oauth_family:" + familyOf(hash),
				Result: "refused", ErrorCode: "code_reuse", Data: map[string]any{"clientId": c.ClientID}}
			refused = invalidGrant("the authorization code was already used")
			return nil
		case err != nil:
			return err
		}
		// From here the code is spent whatever the outcome: a wrong verifier cannot be retried.
		switch {
		case c.ExpiresAt <= now.UnixMilli():
			refused = invalidGrant("invalid or expired authorization code")
		case c.ClientID != clientID:
			refused = invalidGrant("the code was not issued to this client_id")
		case c.RedirectURI != redirectURI:
			refused = invalidGrant("redirect_uri does not match the authorization request")
		case !pkceValue(verifier) || subtle.ConstantTimeCompare([]byte(s256(verifier)), []byte(c.CodeChallenge)) != 1:
			refused = invalidGrant("PKCE verification failed")
		case resource != "" && strings.TrimRight(resource, "/") != c.Resource:
			refused = &grantError{"invalid_target", "resource does not match the authorization request"}
		}
		if refused != nil {
			return nil
		}
		if refused, err = accountRefusal(ctx, tx, c.IdentityID); refused != nil || err != nil {
			return err
		}
		fam := store.OAuthFamily{ID: familyOf(hash), ClientID: c.ClientID, IdentityID: c.IdentityID,
			Scope: c.Scope, Resource: c.Resource, CreatedAt: now.UnixMilli()}
		if err := tx.CreateOAuthFamily(ctx, fam); err != nil {
			return err
		}
		out, err = s.mint(ctx, tx, fam, c.Scope, now)
		if err != nil {
			return err
		}
		return tx.TouchOAuthClient(ctx, c.ClientID, now.UnixMilli())
	})
	if err != nil {
		return issued{}, err
	}
	if revoked != nil {
		s.o.Logger.Printf("oauth: an authorization code of client %v was exchanged twice; its grant is revoked", revoked.Data["clientId"])
		s.audit(ctx, *revoked)
	}
	if refused != nil {
		return issued{}, refused
	}
	return out, nil
}

func (s *Server) refresh(ctx context.Context, token, clientID, resource, scope string) (issued, error) {
	if token == "" || clientID == "" {
		return issued{}, &grantError{"invalid_request", "refresh_token and client_id are required"}
	}
	if !strings.HasPrefix(token, auth.OAuthRefreshPrefix) || len(token) > 256 {
		return issued{}, invalidGrant("invalid or expired refresh token")
	}
	hash := auth.HashOAuthValue(token)
	now := s.now()
	var out issued
	var refused *grantError
	var revoked *auth.AuditEvent // written after the transaction, never inside it
	err := s.o.Store.Atomically(ctx, func(tx store.Store) error {
		t, err := tx.GetOAuthToken(ctx, hash)
		if errors.Is(err, store.ErrNotFound) {
			refused = invalidGrant("invalid or expired refresh token")
			return nil
		}
		if err != nil {
			return err
		}
		// Checked before the token is consumed, so a stranger presenting it under another client_id
		// cannot burn the holder's token.
		if t.Kind != store.OAuthRefresh || t.ClientID != clientID {
			refused = invalidGrant("invalid or expired refresh token")
			return nil
		}
		if t.FamilyRevokedAt != 0 {
			refused = invalidGrant("the refresh token's grant was revoked; authorize again")
			return nil
		}
		revokeFamily := func() error {
			if err := tx.RevokeOAuthFamily(ctx, t.FamilyID, now.UnixMilli(), "refresh_reuse"); err != nil {
				return err
			}
			revoked = &auth.AuditEvent{Action: "oauth.grant.revoked", Actor: t.IdentityID, Subject: "oauth_family:" + t.FamilyID,
				Result: "refused", ErrorCode: "refresh_reuse", Data: map[string]any{"clientId": t.ClientID}}
			refused = invalidGrant("the refresh token was already used; its grant is revoked")
			return nil
		}
		if t.UsedAt != 0 || t.RevokedAt != 0 {
			return revokeFamily()
		}
		if t.ExpiresAt <= now.UnixMilli() {
			refused = invalidGrant("invalid or expired refresh token")
			return nil
		}
		// A disabled account's grant is not refreshed (nor its token spent), so it cannot slide
		// forward while the account is disabled; once enabled, the unexpired token works again.
		if refused, err = accountRefusal(ctx, tx, t.IdentityID); refused != nil || err != nil {
			return err
		}
		// The request is judged whole before the token is spent: a refusal here leaves it usable.
		fam, err := tx.GetOAuthFamily(ctx, t.FamilyID)
		if err != nil {
			return err
		}
		if resource != "" && strings.TrimRight(resource, "/") != fam.Resource {
			refused = &grantError{"invalid_target", "resource does not match the grant"}
			return nil
		}
		granted := fam.Scope
		if scope != "" {
			// RFC 6749 §6: a narrower scope may be asked for, never a wider one.
			want, ok := narrowest(scope, domain.Role(fam.Scope))
			if !ok {
				refused = &grantError{"invalid_scope", "scope may not exceed the role granted at consent, " + fam.Scope}
				return nil
			}
			granted = string(want)
		}
		if _, err := tx.ConsumeOAuthToken(ctx, hash, now.UnixMilli()); errors.Is(err, store.ErrOAuthUsed) {
			return revokeFamily() // a concurrent rotation won: this is the second presentation
		} else if err != nil {
			return err
		}
		out, err = s.mint(ctx, tx, fam, granted, now)
		return err
	})
	if err != nil {
		return issued{}, err
	}
	if revoked != nil {
		s.o.Logger.Printf("oauth: a refresh token of client %v was presented twice; its grant is revoked", revoked.Data["clientId"])
		s.audit(ctx, *revoked)
	}
	if refused != nil {
		return issued{}, refused
	}
	return out, nil
}

// accountRefusal is the grant refusal for an identity that cannot act: a disabled or missing
// account (read through tx, not the resolver's 60 s cache). nil, nil when it can.
func accountRefusal(ctx context.Context, tx store.Store, identityID string) (*grantError, error) {
	u, err := tx.GetUser(ctx, identityID)
	if errors.Is(err, store.ErrNotFound) {
		return invalidGrant("the grant's account no longer exists"), nil
	}
	if err != nil {
		return nil, err
	}
	if u.DisabledAt != 0 {
		return invalidGrant("the grant's account is disabled"), nil
	}
	return nil, nil
}

// narrowest is the highest role a refresh's scope names, if it is at most the granted one.
func narrowest(scope string, granted domain.Role) (domain.Role, bool) {
	top := domain.Role("")
	for _, f := range strings.Fields(scope) {
		if r := domain.Role(f); domain.RoleRank(r) > domain.RoleRank(top) {
			top = r
		}
	}
	if top == "" || domain.RoleRank(top) > domain.RoleRank(granted) {
		return "", false
	}
	return top, true
}

// mint issues an access and a refresh token of fam with scope.
func (s *Server) mint(ctx context.Context, tx store.Store, fam store.OAuthFamily, scope string, now time.Time) (issued, error) {
	access, accessHash, err := auth.MintOAuthValue(auth.OAuthAccessPrefix)
	if err != nil {
		return issued{}, err
	}
	refresh, refreshHash, err := auth.MintOAuthValue(auth.OAuthRefreshPrefix)
	if err != nil {
		return issued{}, err
	}
	for _, t := range []store.OAuthToken{
		{TokenHash: accessHash, Kind: store.OAuthAccess, ExpiresAt: now.Add(s.o.AccessTTL).UnixMilli()},
		{TokenHash: refreshHash, Kind: store.OAuthRefresh, ExpiresAt: now.Add(s.o.RefreshTTL).UnixMilli()},
	} {
		t.FamilyID, t.ClientID, t.IdentityID, t.Scope, t.Resource, t.CreatedAt = fam.ID, fam.ClientID, fam.IdentityID, scope, fam.Resource, now.UnixMilli()
		if err := tx.CreateOAuthToken(ctx, t); err != nil {
			return issued{}, err
		}
	}
	return issued{access: access, refresh: refresh, scope: scope}, nil
}
