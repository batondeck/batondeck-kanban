package store

import "context"

// WakeSession is one entry of the device session directory (R0.25, ADR-0004): a CLI client process
// on this device that registered for doorbells. Times are Unix milliseconds. SocketPath is the
// client's own inbox socket ("" for a client with no adapter: it is listed, never rung);
// SocketTokenSealed is the socket's token sealed with the keyring (nil when there is none), never
// the token. LastCursor is the last change-log id delivered, 0 for none (change-log ids start at
// 1). PID is the client process (0 for a row written before 0005); Role is "master" or "worker"
// ("" before 0005); ClientSessionID and ProcStart are what the client's registry entry
// (~/.claude/sessions/<pid>.json) said at register; BearerSealed is the bearer it registered
// with, sealed with the keyring, never the bearer. DeviceID is the device whose serve registered it
// ("" before 0019), where its PID names a process (P-167).
type WakeSession struct {
	ID                string
	IdentityID        string
	AgentID           string
	SocketPath        string
	SocketTokenSealed []byte
	LastCursor        int64
	CreatedAt         int64
	LastSeenAt        int64
	ExpiresAt         int64
	PID               int64
	Role              string
	ClientSessionID   string
	ProcStart         string
	BearerSealed      []byte
	DeviceID          string
}

// WakeSessions is the wake-session area. Key: ID, unique on its own; PID, when not 0, is unique
// too among the sessions of its DeviceID. A session whose ExpiresAt is at or before the read's time is expired and reads as missing
// from GetWakeSession, before any sweep; ListWakeSessions lists it. Errors: an empty id, identity
// or agent, an ExpiresAt of 0, a negative PID, a Role other than "", master and worker, and a PID
// another id of the same DeviceID holds, are domain.Validation; a missing or expired session is
// domain.NotFound("WakeSession", id).
type WakeSessions interface {
	// PutWakeSession inserts the session or, when the id exists, replaces every field but CreatedAt,
	// which the first put fixed.
	PutWakeSession(ctx context.Context, s WakeSession) error
	// GetWakeSession is the live session.
	GetWakeSession(ctx context.Context, id string) (WakeSession, error)
	// ListWakeSessions is every session, expired or not, oldest CreatedAt first (then id).
	ListWakeSessions(ctx context.Context) ([]WakeSession, error)
	// SetWakeSessionCursor records the last cursor delivered and when (lastSeenAt); a missing id is
	// NOT_FOUND.
	SetWakeSessionCursor(ctx context.Context, id string, cursor, lastSeenAt int64) error
	// DeleteWakeSession deletes the session, expired or not; a missing one is NOT_FOUND.
	DeleteWakeSession(ctx context.Context, id string) error
}

var (
	_ WakeSessions = (*SQLite)(nil)
	_ WakeSessions = (*Postgres)(nil)
)
