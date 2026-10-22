package store

import "context"

// WakeSession is a registered doorbell listener: a /wake stream or a session inbox socket. Times
// are Unix milliseconds. SocketPath is "" for a /wake stream; SocketTokenSealed is the socket's
// token sealed with the keyring (nil when there is none), never the token. LastCursor is the last
// change-log id delivered, 0 for none (change-log ids start at 1).
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
}

// WakeSessions is the wake-session area. Key: ID, unique on its own. A session whose ExpiresAt is
// at or before the read's time is expired and reads as missing, before any sweep. Errors: an empty
// id, identity or agent, and an ExpiresAt of 0, are domain.Validation; a missing or expired session
// is domain.NotFound("WakeSession", id).
type WakeSessions interface {
	// PutWakeSession inserts the session or, when the id exists, replaces every field but CreatedAt,
	// which the first put fixed.
	PutWakeSession(ctx context.Context, s WakeSession) error
	// GetWakeSession is the live session.
	GetWakeSession(ctx context.Context, id string) (WakeSession, error)
	// DeleteWakeSession deletes the session, expired or not; a missing one is NOT_FOUND.
	DeleteWakeSession(ctx context.Context, id string) error
}

var (
	_ WakeSessions = (*SQLite)(nil)
	_ WakeSessions = (*Postgres)(nil)
)
