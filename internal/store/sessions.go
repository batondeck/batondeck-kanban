package store

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

func sessionFromRow(r sqlitedb.Session) Session {
	return Session{IDHash: r.IDHash, UserID: r.UserID, CSRFToken: r.CsrfToken,
		CreatedAt: r.CreatedAt, LastSeenAt: r.LastSeenAt, ExpiresAt: r.ExpiresAt}
}

func insertSessionParams(s Session) sqlitedb.InsertSessionParams {
	return sqlitedb.InsertSessionParams{IDHash: s.IDHash, UserID: s.UserID, CsrfToken: s.CSRFToken,
		CreatedAt: s.CreatedAt, LastSeenAt: s.LastSeenAt, ExpiresAt: s.ExpiresAt}
}

// --- SQLite ---

func (s *SQLite) CreateSession(ctx context.Context, sess Session) error {
	if err := s.authWrite().InsertSession(ctx, insertSessionParams(sess)); err != nil {
		return authErr("create session", err)
	}
	return nil
}

func (s *SQLite) GetSession(ctx context.Context, idHash string) (Session, error) {
	r, err := s.authRead().GetSession(ctx, idHash)
	if err != nil {
		return Session{}, authErr("get session", err)
	}
	return sessionFromRow(r), nil
}

func (s *SQLite) DeleteSession(ctx context.Context, idHash string) error {
	if err := s.authWrite().DeleteSession(ctx, idHash); err != nil {
		return authErr("delete session", err)
	}
	return nil
}

// --- Postgres ---

func (s *Postgres) CreateSession(ctx context.Context, sess Session) error {
	if err := s.authQ().InsertSession(ctx, pgdb.InsertSessionParams(insertSessionParams(sess))); err != nil {
		return authErr("create session", err)
	}
	return nil
}

func (s *Postgres) GetSession(ctx context.Context, idHash string) (Session, error) {
	r, err := s.authQ().GetSession(ctx, idHash)
	if err != nil {
		return Session{}, authErr("get session", err)
	}
	return sessionFromRow(sqlitedb.Session(r)), nil
}

func (s *Postgres) DeleteSession(ctx context.Context, idHash string) error {
	if err := s.authQ().DeleteSession(ctx, idHash); err != nil {
		return authErr("delete session", err)
	}
	return nil
}
