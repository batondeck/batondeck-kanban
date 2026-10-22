package store

import (
	"context"
	"database/sql"

	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

func tokenFromRow(r sqlitedb.Token) Token {
	return Token{ID: r.ID, IdentityID: r.IdentityID, Subject: r.Subject, SecretHash: r.SecretHash,
		Label: r.Label, Permissions: r.Permissions, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt,
		LastUsedAt: nullMs(r.LastUsedAt), RevokedAt: nullMs(r.RevokedAt)}
}

func tokensFromRows(rows []sqlitedb.Token) []Token {
	out := make([]Token, 0, len(rows))
	for _, r := range rows {
		out = append(out, tokenFromRow(r))
	}
	return out
}

func insertTokenParams(t Token) sqlitedb.InsertTokenParams {
	return sqlitedb.InsertTokenParams{ID: t.ID, IdentityID: t.IdentityID, Subject: t.Subject,
		SecretHash: t.SecretHash, Label: t.Label, Permissions: t.Permissions,
		CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt}
}

func revokeParams(id string, at int64) sqlitedb.RevokeTokenParams {
	return sqlitedb.RevokeTokenParams{RevokedAt: sql.NullInt64{Int64: at, Valid: true}, ID: id}
}

// --- SQLite ---

func (s *SQLite) CreateToken(ctx context.Context, t Token) error {
	if err := s.authWrite().InsertToken(ctx, insertTokenParams(t)); err != nil {
		return authErr("create token", err)
	}
	return nil
}

func (s *SQLite) GetToken(ctx context.Context, id string) (Token, error) {
	r, err := s.authRead().GetToken(ctx, id)
	if err != nil {
		return Token{}, authErr("get token", err)
	}
	return tokenFromRow(r), nil
}

func (s *SQLite) ListTokens(ctx context.Context, identityID string) ([]Token, error) {
	var rows []sqlitedb.Token
	var err error
	if identityID == "" {
		rows, err = s.authRead().ListTokens(ctx)
	} else {
		rows, err = s.authRead().ListTokensByIdentity(ctx, identityID)
	}
	if err != nil {
		return nil, authErr("list tokens", err)
	}
	return tokensFromRows(rows), nil
}

func (s *SQLite) RevokeToken(ctx context.Context, id string, at int64) error {
	n, err := s.authWrite().RevokeToken(ctx, revokeParams(id, at))
	if err != nil {
		return authErr("revoke token", err)
	}
	if n == 0 {
		// Already revoked, or no such token: only the second is an error.
		_, err := s.GetToken(ctx, id)
		return err
	}
	return nil
}

// --- Postgres ---

func (s *Postgres) CreateToken(ctx context.Context, t Token) error {
	if err := s.authQ().InsertToken(ctx, pgdb.InsertTokenParams(insertTokenParams(t))); err != nil {
		return authErr("create token", err)
	}
	return nil
}

func (s *Postgres) GetToken(ctx context.Context, id string) (Token, error) {
	r, err := s.authQ().GetToken(ctx, id)
	if err != nil {
		return Token{}, authErr("get token", err)
	}
	return tokenFromRow(sqlitedb.Token(r)), nil
}

func (s *Postgres) ListTokens(ctx context.Context, identityID string) ([]Token, error) {
	var rows []pgdb.Token
	var err error
	if identityID == "" {
		rows, err = s.authQ().ListTokens(ctx)
	} else {
		rows, err = s.authQ().ListTokensByIdentity(ctx, identityID)
	}
	if err != nil {
		return nil, authErr("list tokens", err)
	}
	out := make([]Token, 0, len(rows))
	for _, r := range rows {
		out = append(out, tokenFromRow(sqlitedb.Token(r)))
	}
	return out, nil
}

func (s *Postgres) RevokeToken(ctx context.Context, id string, at int64) error {
	n, err := s.authQ().RevokeToken(ctx, pgdb.RevokeTokenParams(revokeParams(id, at)))
	if err != nil {
		return authErr("revoke token", err)
	}
	if n == 0 {
		_, err := s.GetToken(ctx, id)
		return err
	}
	return nil
}
