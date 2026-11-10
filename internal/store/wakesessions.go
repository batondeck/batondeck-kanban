package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The wake-session area of contract_wakesessions.go, on both engines: one statement per call, and
// expiry judged by the read's statement at its own time. "" and 0 are NULL in the row.

type wakeSessionQueries interface {
	PutWakeSession(ctx context.Context, arg sqlitedb.PutWakeSessionParams) error
	GetWakeSession(ctx context.Context, arg sqlitedb.GetWakeSessionParams) (sqlitedb.WakeSession, error)
	ListWakeSessions(ctx context.Context) ([]sqlitedb.WakeSession, error)
	SetWakeSessionCursor(ctx context.Context, arg sqlitedb.SetWakeSessionCursorParams) (int64, error)
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

func (s *SQLite) ListWakeSessions(ctx context.Context) ([]WakeSession, error) {
	return listWakeSessions(ctx, s)
}

func (s *Postgres) ListWakeSessions(ctx context.Context) ([]WakeSession, error) {
	return listWakeSessions(ctx, s)
}

func (s *SQLite) SetWakeSessionCursor(ctx context.Context, id string, cursor, lastSeenAt int64) error {
	return setWakeSessionCursor(ctx, s, id, cursor, lastSeenAt)
}

func (s *Postgres) SetWakeSessionCursor(ctx context.Context, id string, cursor, lastSeenAt int64) error {
	return setWakeSessionCursor(ctx, s, id, cursor, lastSeenAt)
}

func (s *SQLite) DeleteWakeSession(ctx context.Context, id string) error {
	return deleteWakeSession(ctx, s, id)
}

func (s *Postgres) DeleteWakeSession(ctx context.Context, id string) error {
	return deleteWakeSession(ctx, s, id)
}

// ValidWakeSession is the contract's check of a session before it is written, shared by every
// backend so that they refuse alike.
func ValidWakeSession(w WakeSession) error {
	if w.ID == "" || w.IdentityID == "" || w.AgentID == "" || w.ExpiresAt == 0 {
		return domain.Validation("a wake session needs an id, an identity, an agent and an expiry", nil)
	}
	if w.PID < 0 {
		return domain.Validation("a wake session's pid cannot be negative", map[string]any{"field": "pid"})
	}
	if w.Role != "" && w.Role != "master" && w.Role != "worker" {
		return domain.Validation("a wake session's role is master or worker", map[string]any{"field": "role"})
	}
	return nil
}

// ErrWakePIDTaken is the refusal of a pid another session of the same device holds.
func ErrWakePIDTaken(pid int64) error {
	return domain.Validation(fmt.Sprintf("pid %d is registered by another wake session", pid), map[string]any{"field": "pid"})
}

func putWakeSession(ctx context.Context, e engine, w WakeSession) error {
	if err := ValidWakeSession(w); err != nil {
		return err
	}
	err := e.queries(true).PutWakeSession(ctx, sqlitedb.PutWakeSessionParams{
		ID: w.ID, IdentityID: w.IdentityID, AgentID: w.AgentID, SocketPath: nullText(w.SocketPath),
		SocketTokenSealed: w.SocketTokenSealed, LastCursor: nullMillis(w.LastCursor),
		CreatedAt: w.CreatedAt, LastSeenAt: w.LastSeenAt, ExpiresAt: w.ExpiresAt,
		Pid: nullMillis(w.PID), Role: nullText(w.Role), ClientSessionID: nullText(w.ClientSessionID),
		ProcStart: nullText(w.ProcStart), BearerSealed: w.BearerSealed, DeviceID: w.DeviceID,
	})
	if err != nil {
		// The partial unique index wake_sessions_pid (device_id, pid), as each engine names it in its
		// error: SQLite by its columns, Postgres by its name.
		if m := err.Error(); strings.Contains(m, "wake_sessions_pid") || strings.Contains(m, "wake_sessions.pid") {
			return ErrWakePIDTaken(w.PID)
		}
		return fmt.Errorf("store: put wake session: %w", err)
	}
	return nil
}

func wakeSessionOf(r sqlitedb.WakeSession) WakeSession {
	return WakeSession{
		ID: r.ID, IdentityID: r.IdentityID, AgentID: r.AgentID, SocketPath: r.SocketPath.String,
		SocketTokenSealed: r.SocketTokenSealed, LastCursor: nullOrZero(r.LastCursor),
		CreatedAt: r.CreatedAt, LastSeenAt: r.LastSeenAt, ExpiresAt: r.ExpiresAt,
		PID: nullOrZero(r.Pid), Role: r.Role.String, ClientSessionID: r.ClientSessionID.String,
		ProcStart: r.ProcStart.String, BearerSealed: r.BearerSealed, DeviceID: r.DeviceID,
	}
}

func getWakeSession(ctx context.Context, e engine, id string) (WakeSession, error) {
	r, err := e.queries(false).GetWakeSession(ctx, sqlitedb.GetWakeSessionParams{ID: id, ExpiresAt: nowMillis()})
	if err := found(err, "WakeSession", id); err != nil {
		return WakeSession{}, err
	}
	return wakeSessionOf(r), nil
}

func listWakeSessions(ctx context.Context, e engine) ([]WakeSession, error) {
	rows, err := e.queries(false).ListWakeSessions(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: list wake sessions: %w", err)
	}
	out := make([]WakeSession, 0, len(rows))
	for _, r := range rows {
		out = append(out, wakeSessionOf(r))
	}
	return out, nil
}

func setWakeSessionCursor(ctx context.Context, e engine, id string, cursor, lastSeenAt int64) error {
	n, err := e.queries(true).SetWakeSessionCursor(ctx, sqlitedb.SetWakeSessionCursorParams{LastCursor: nullMillis(cursor), LastSeenAt: lastSeenAt, ID: id})
	if err != nil {
		return fmt.Errorf("store: set wake session cursor: %w", err)
	}
	if n == 0 {
		return domain.NotFound("WakeSession", id)
	}
	return nil
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

func (p pgQueries) ListWakeSessions(ctx context.Context) ([]sqlitedb.WakeSession, error) {
	rows, err := p.q.ListWakeSessions(ctx)
	out := make([]sqlitedb.WakeSession, len(rows))
	for i, r := range rows {
		out[i] = sqlitedb.WakeSession(r)
	}
	return out, err
}

func (p pgQueries) SetWakeSessionCursor(ctx context.Context, arg sqlitedb.SetWakeSessionCursorParams) (int64, error) {
	return p.q.SetWakeSessionCursor(ctx, pgdb.SetWakeSessionCursorParams(arg))
}

func (p pgQueries) DeleteWakeSession(ctx context.Context, id string) (int64, error) {
	return p.q.DeleteWakeSession(ctx, id)
}
