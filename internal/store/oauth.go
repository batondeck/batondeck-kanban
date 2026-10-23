package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The OAuth area of contract_oauth.go on both engines. The two generated packages have the same
// row and parameter types, so each is built once as sqlitedb's and converted for pgdb.

func atMs(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }

func oauthClientParams(c OAuthClient) (sqlitedb.InsertOauthClientParams, error) {
	uris := c.RedirectURIs
	if uris == nil {
		uris = []string{}
	}
	raw, err := json.Marshal(uris)
	if err != nil {
		return sqlitedb.InsertOauthClientParams{}, err
	}
	return sqlitedb.InsertOauthClientParams{ClientID: c.ClientID, ClientName: c.ClientName,
		RedirectUris: string(raw), Scope: c.Scope, CreatedAt: c.CreatedAt}, nil
}

func oauthClientFromRow(r sqlitedb.GetOauthClientRow) (OAuthClient, error) {
	var uris []string
	if err := json.Unmarshal([]byte(r.RedirectUris), &uris); err != nil {
		return OAuthClient{}, fmt.Errorf("store: oauth client %s: redirect_uris: %w", r.ClientID, err)
	}
	return OAuthClient{ClientID: r.ClientID, ClientName: r.ClientName, RedirectURIs: uris, Scope: r.Scope,
		CreatedAt: r.CreatedAt, LastUsedAt: nullMs(r.LastUsedAt)}, nil
}

func oauthCodeParams(c OAuthCode) sqlitedb.InsertOauthCodeParams {
	return sqlitedb.InsertOauthCodeParams{CodeHash: c.CodeHash, ClientID: c.ClientID, IdentityID: c.IdentityID,
		RedirectUri: c.RedirectURI, CodeChallenge: c.CodeChallenge, Scope: c.Scope, Resource: c.Resource,
		CreatedAt: c.CreatedAt, ExpiresAt: c.ExpiresAt}
}

func oauthCodeFromRow(r sqlitedb.GetOauthCodeRow) OAuthCode {
	return OAuthCode{CodeHash: r.CodeHash, ClientID: r.ClientID, IdentityID: r.IdentityID, RedirectURI: r.RedirectUri,
		CodeChallenge: r.CodeChallenge, Scope: r.Scope, Resource: r.Resource, CreatedAt: r.CreatedAt,
		ExpiresAt: r.ExpiresAt, UsedAt: nullMs(r.UsedAt)}
}

func oauthFamilyParams(f OAuthFamily) sqlitedb.InsertOauthFamilyParams {
	return sqlitedb.InsertOauthFamilyParams{ID: f.ID, ClientID: f.ClientID, IdentityID: f.IdentityID,
		Scope: f.Scope, Resource: f.Resource, CreatedAt: f.CreatedAt}
}

func oauthFamilyFromRow(r sqlitedb.OauthRefreshFamily) OAuthFamily {
	return OAuthFamily{ID: r.ID, ClientID: r.ClientID, IdentityID: r.IdentityID, Scope: r.Scope, Resource: r.Resource,
		CreatedAt: r.CreatedAt, RevokedAt: nullMs(r.RevokedAt), RevokedReason: r.RevokedReason.String}
}

func oauthTokenParams(t OAuthToken) sqlitedb.InsertOauthTokenParams {
	return sqlitedb.InsertOauthTokenParams{TokenHash: t.TokenHash, Kind: t.Kind, FamilyID: t.FamilyID,
		ClientID: t.ClientID, IdentityID: t.IdentityID, Scope: t.Scope, Resource: t.Resource,
		CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt}
}

func oauthTokenFromRow(r sqlitedb.GetOauthTokenRow) OAuthToken {
	return OAuthToken{TokenHash: r.TokenHash, Kind: r.Kind, FamilyID: r.FamilyID, ClientID: r.ClientID,
		IdentityID: r.IdentityID, Scope: r.Scope, Resource: r.Resource, CreatedAt: r.CreatedAt,
		ExpiresAt: r.ExpiresAt, UsedAt: nullMs(r.UsedAt), RevokedAt: nullMs(r.RevokedAt),
		FamilyRevokedAt: nullMs(r.FamilyRevokedAt)}
}

func revokeFamilyParams(id string, at int64, reason string) sqlitedb.RevokeOauthFamilyParams {
	return sqlitedb.RevokeOauthFamilyParams{RevokedAt: atMs(at),
		RevokedReason: sql.NullString{String: reason, Valid: true}, ID: id}
}

// usedCode and usedToken are a Consume's answer once its UPDATE touched n rows: the row as it was
// before this call (UsedAt 0), or the row a first call consumed beside ErrOAuthUsed.
func usedCode(n int64, c OAuthCode) (OAuthCode, error) {
	if n == 0 {
		return c, ErrOAuthUsed
	}
	c.UsedAt = 0
	return c, nil
}

func usedToken(n int64, t OAuthToken) (OAuthToken, error) {
	if n == 0 {
		return t, ErrOAuthUsed
	}
	t.UsedAt = 0
	return t, nil
}

// --- SQLite ---

func (s *SQLite) CreateOAuthClient(ctx context.Context, c OAuthClient) error {
	p, err := oauthClientParams(c)
	if err != nil {
		return err
	}
	if err := s.authWrite().InsertOauthClient(ctx, p); err != nil {
		return authErr("create oauth client", err)
	}
	return nil
}

func (s *SQLite) GetOAuthClient(ctx context.Context, clientID string) (OAuthClient, error) {
	r, err := s.authRead().GetOauthClient(ctx, clientID)
	if err != nil {
		return OAuthClient{}, authErr("get oauth client", err)
	}
	return oauthClientFromRow(r)
}

func (s *SQLite) TouchOAuthClient(ctx context.Context, clientID string, at int64) error {
	n, err := s.authWrite().TouchOauthClient(ctx, sqlitedb.TouchOauthClientParams{LastUsedAt: atMs(at), ClientID: clientID})
	if err != nil {
		return authErr("touch oauth client", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLite) DeleteUnusedOAuthClients(ctx context.Context, createdBefore int64) (int64, error) {
	n, err := s.authWrite().DeleteUnusedOauthClients(ctx, createdBefore)
	if err != nil {
		return 0, authErr("delete unused oauth clients", err)
	}
	return n, nil
}

func (s *SQLite) CreateOAuthCode(ctx context.Context, c OAuthCode) error {
	if err := s.authWrite().InsertOauthCode(ctx, oauthCodeParams(c)); err != nil {
		return authErr("create oauth code", err)
	}
	return nil
}

// ConsumeOAuthCode updates on the write connection and reads back on it too, so the read sees the
// write whether or not the call is inside Atomically.
func (s *SQLite) ConsumeOAuthCode(ctx context.Context, codeHash string, at int64) (OAuthCode, error) {
	q := s.authWrite()
	n, err := q.UseOauthCode(ctx, sqlitedb.UseOauthCodeParams{UsedAt: atMs(at), CodeHash: codeHash})
	if err != nil {
		return OAuthCode{}, authErr("consume oauth code", err)
	}
	r, err := q.GetOauthCode(ctx, codeHash)
	if err != nil {
		return OAuthCode{}, authErr("consume oauth code", err)
	}
	return usedCode(n, oauthCodeFromRow(r))
}

func (s *SQLite) CreateOAuthFamily(ctx context.Context, f OAuthFamily) error {
	if err := s.authWrite().InsertOauthFamily(ctx, oauthFamilyParams(f)); err != nil {
		return authErr("create oauth family", err)
	}
	return nil
}

func (s *SQLite) GetOAuthFamily(ctx context.Context, id string) (OAuthFamily, error) {
	r, err := s.authRead().GetOauthFamily(ctx, id)
	if err != nil {
		return OAuthFamily{}, authErr("get oauth family", err)
	}
	return oauthFamilyFromRow(r), nil
}

func (s *SQLite) RevokeOAuthFamily(ctx context.Context, id string, at int64, reason string) error {
	q := s.authWrite()
	n, err := q.RevokeOauthFamily(ctx, revokeFamilyParams(id, at, reason))
	if err != nil {
		return authErr("revoke oauth family", err)
	}
	if n == 0 {
		// Revoked already, or no such family: only the second is an error.
		_, err := q.GetOauthFamily(ctx, id)
		if err != nil {
			return authErr("revoke oauth family", err)
		}
	}
	return nil
}

func (s *SQLite) CreateOAuthToken(ctx context.Context, t OAuthToken) error {
	if err := s.authWrite().InsertOauthToken(ctx, oauthTokenParams(t)); err != nil {
		return authErr("create oauth token", err)
	}
	return nil
}

func (s *SQLite) GetOAuthToken(ctx context.Context, tokenHash string) (OAuthToken, error) {
	r, err := s.authRead().GetOauthToken(ctx, tokenHash)
	if err != nil {
		return OAuthToken{}, authErr("get oauth token", err)
	}
	return oauthTokenFromRow(r), nil
}

func (s *SQLite) ConsumeOAuthToken(ctx context.Context, tokenHash string, at int64) (OAuthToken, error) {
	q := s.authWrite()
	n, err := q.UseOauthToken(ctx, sqlitedb.UseOauthTokenParams{UsedAt: atMs(at), TokenHash: tokenHash})
	if err != nil {
		return OAuthToken{}, authErr("consume oauth token", err)
	}
	r, err := q.GetOauthToken(ctx, tokenHash)
	if err != nil {
		return OAuthToken{}, authErr("consume oauth token", err)
	}
	return usedToken(n, oauthTokenFromRow(r))
}

// --- Postgres ---

func (s *Postgres) CreateOAuthClient(ctx context.Context, c OAuthClient) error {
	p, err := oauthClientParams(c)
	if err != nil {
		return err
	}
	if err := s.authQ().InsertOauthClient(ctx, pgdb.InsertOauthClientParams(p)); err != nil {
		return authErr("create oauth client", err)
	}
	return nil
}

func (s *Postgres) GetOAuthClient(ctx context.Context, clientID string) (OAuthClient, error) {
	r, err := s.authQ().GetOauthClient(ctx, clientID)
	if err != nil {
		return OAuthClient{}, authErr("get oauth client", err)
	}
	return oauthClientFromRow(sqlitedb.GetOauthClientRow(r))
}

func (s *Postgres) TouchOAuthClient(ctx context.Context, clientID string, at int64) error {
	n, err := s.authQ().TouchOauthClient(ctx, pgdb.TouchOauthClientParams{LastUsedAt: atMs(at), ClientID: clientID})
	if err != nil {
		return authErr("touch oauth client", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Postgres) DeleteUnusedOAuthClients(ctx context.Context, createdBefore int64) (int64, error) {
	n, err := s.authQ().DeleteUnusedOauthClients(ctx, createdBefore)
	if err != nil {
		return 0, authErr("delete unused oauth clients", err)
	}
	return n, nil
}

func (s *Postgres) CreateOAuthCode(ctx context.Context, c OAuthCode) error {
	if err := s.authQ().InsertOauthCode(ctx, pgdb.InsertOauthCodeParams(oauthCodeParams(c))); err != nil {
		return authErr("create oauth code", err)
	}
	return nil
}

func (s *Postgres) ConsumeOAuthCode(ctx context.Context, codeHash string, at int64) (OAuthCode, error) {
	q := s.authQ()
	n, err := q.UseOauthCode(ctx, pgdb.UseOauthCodeParams{UsedAt: atMs(at), CodeHash: codeHash})
	if err != nil {
		return OAuthCode{}, authErr("consume oauth code", err)
	}
	r, err := q.GetOauthCode(ctx, codeHash)
	if err != nil {
		return OAuthCode{}, authErr("consume oauth code", err)
	}
	return usedCode(n, oauthCodeFromRow(sqlitedb.GetOauthCodeRow(r)))
}

func (s *Postgres) CreateOAuthFamily(ctx context.Context, f OAuthFamily) error {
	if err := s.authQ().InsertOauthFamily(ctx, pgdb.InsertOauthFamilyParams(oauthFamilyParams(f))); err != nil {
		return authErr("create oauth family", err)
	}
	return nil
}

func (s *Postgres) GetOAuthFamily(ctx context.Context, id string) (OAuthFamily, error) {
	r, err := s.authQ().GetOauthFamily(ctx, id)
	if err != nil {
		return OAuthFamily{}, authErr("get oauth family", err)
	}
	return oauthFamilyFromRow(sqlitedb.OauthRefreshFamily(r)), nil
}

func (s *Postgres) RevokeOAuthFamily(ctx context.Context, id string, at int64, reason string) error {
	n, err := s.authQ().RevokeOauthFamily(ctx, pgdb.RevokeOauthFamilyParams(revokeFamilyParams(id, at, reason)))
	if err != nil {
		return authErr("revoke oauth family", err)
	}
	if n == 0 {
		_, err := s.GetOAuthFamily(ctx, id)
		return err
	}
	return nil
}

func (s *Postgres) CreateOAuthToken(ctx context.Context, t OAuthToken) error {
	if err := s.authQ().InsertOauthToken(ctx, pgdb.InsertOauthTokenParams(oauthTokenParams(t))); err != nil {
		return authErr("create oauth token", err)
	}
	return nil
}

func (s *Postgres) GetOAuthToken(ctx context.Context, tokenHash string) (OAuthToken, error) {
	r, err := s.authQ().GetOauthToken(ctx, tokenHash)
	if err != nil {
		return OAuthToken{}, authErr("get oauth token", err)
	}
	return oauthTokenFromRow(sqlitedb.GetOauthTokenRow(r)), nil
}

func (s *Postgres) ConsumeOAuthToken(ctx context.Context, tokenHash string, at int64) (OAuthToken, error) {
	q := s.authQ()
	n, err := q.UseOauthToken(ctx, pgdb.UseOauthTokenParams{UsedAt: atMs(at), TokenHash: tokenHash})
	if err != nil {
		return OAuthToken{}, authErr("consume oauth token", err)
	}
	r, err := q.GetOauthToken(ctx, tokenHash)
	if err != nil {
		return OAuthToken{}, authErr("consume oauth token", err)
	}
	return usedToken(n, oauthTokenFromRow(sqlitedb.GetOauthTokenRow(r)))
}
