package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The wake-session area of contract_wakesessions.go, on both engines: one statement per call, and
// expiry judged by the read's statement at its own time. "" and 0 are NULL in the row.

type wakeSessionQueries interface {
	PutWakeSession(ctx context.Context, arg sqlitedb.PutWakeSessionParams) error
	GetWakeSession(ctx context.Context, arg sqlitedb.GetWakeSessionParams) (sqlitedb.WakeSession, error)
	DeleteWakeSession(ctx context.Context, id string) (int64, error)
}

func (s *SQLite) PutWakeSession(ctx context.Context, w WakeSession) error {
	return putWakeSession(ctx, s, w)
}

func (s *Postgres) PutWakeSession(ctx context.Context, w WakeSession) error {
	return putWakeSession(ctx, s, w)
}

func (s *SQLite) GetWakeSession(ctx context.Context, id string) (WakeSession, error) {
	return getWakeSession(ctx, s, id)
}

func (s *Postgres) GetWakeSession(ctx context.Context, id string) (WakeSession, error) {
	return getWakeSession(ctx, s, id)
}

func (s *SQLite) DeleteWakeSession(ctx context.Context, id string) error {
	return deleteWakeSession(ctx, s, id)
}

func (s *Postgres) DeleteWakeSession(ctx context.Context, id string) error {
	return deleteWakeSession(ctx, s, id)
}

func putWakeSession(ctx context.Context, e engine, w WakeSession) error {
	if w.ID == "" || w.IdentityID == "" || w.AgentID == "" || w.ExpiresAt == 0 {
		return domain.Validation("a wake session needs an id, an identity, an agent and an expiry", nil)
	}
	err := e.queries(true).PutWakeSession(ctx, sqlitedb.PutWakeSessionParams{
		ID: w.ID, IdentityID: w.IdentityID, AgentID: w.AgentID, SocketPath: nullText(w.SocketPath),
		SocketTokenSealed: w.SocketTokenSealed, LastCursor: nullMillis(w.LastCursor),
		CreatedAt: w.CreatedAt, LastSeenAt: w.LastSeenAt, ExpiresAt: w.ExpiresAt,
	})
	if err != nil {
		return fmt.Errorf("store: put wake session: %w", err)
	}
	return nil
}

func getWakeSession(ctx context.Context, e engine, id string) (WakeSession, error) {
	r, err := e.queries(false).GetWakeSession(ctx, sqlitedb.GetWakeSessionParams{ID: id, ExpiresAt: nowMillis()})
	if err := found(err, "WakeSession", id); err != nil {
		return WakeSession{}, err
	}
	return WakeSession{
		ID: r.ID, IdentityID: r.IdentityID, AgentID: r.AgentID, SocketPath: r.SocketPath.String,
		SocketTokenSealed: r.SocketTokenSealed, LastCursor: nullOrZero(r.LastCursor),
		CreatedAt: r.CreatedAt, LastSeenAt: r.LastSeenAt, ExpiresAt: r.ExpiresAt,
	}, nil
}

func nullOrZero(n sql.NullInt64) int64 { return n.Int64 }

func deleteWakeSession(ctx context.Context, e engine, id string) error {
	n, err := e.queries(true).DeleteWakeSession(ctx, id)
	if err != nil {
		return fmt.Errorf("store: delete wake session: %w", err)
	}
	if n == 0 {
		return domain.NotFound("WakeSession", id)
	}
	return nil
}

// --- the Postgres queries in sqlitedb's types ---

func (p pgQueries) PutWakeSession(ctx context.Context, arg sqlitedb.PutWakeSessionParams) error {
	return p.q.PutWakeSession(ctx, pgdb.PutWakeSessionParams(arg))
}

func (p pgQueries) GetWakeSession(ctx context.Context, arg sqlitedb.GetWakeSessionParams) (sqlitedb.WakeSession, error) {
	r, err := p.q.GetWakeSession(ctx, pgdb.GetWakeSessionParams(arg))
	return sqlitedb.WakeSession(r), err
}

func (p pgQueries) DeleteWakeSession(ctx context.Context, id string) (int64, error) {
	return p.q.DeleteWakeSession(ctx, id)
}
