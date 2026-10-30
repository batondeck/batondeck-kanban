package store

import (
	"context"
	"errors"
)

// The auth areas' contract (ADR-0003): local users, browser sessions and API tokens. Every time is
// Unix milliseconds, as everywhere in the schema; a zero time is "never" (a NULL column). A method
// that finds no row returns ErrNotFound, on both engines; internal/store/conformance holds it.

// ErrEmailTaken is CreateUser's answer when another user already holds the email.
var ErrEmailTaken = errors.New("store: a user with that email exists")

// User is a local account. Email is the sign-in name, unique, stored as given (the auth service
// lowercases it). PasswordHash is argon2id in its encoded form. WorkspaceAdmin is the account's
// workspace-admin flag, set at creation (migrations/*/0002_users_workspace_admin.sql).
type User struct {
	ID             string
	Email          string
	DisplayName    string
	PasswordHash   string
	CreatedAt      int64
	DisabledAt     int64
	WorkspaceAdmin bool
}

// Session is a browser session. IDHash is the SHA-256 (hex) of the cookie's secret, never the
// secret; CSRFToken is the synchroniser token the session's unsafe requests must echo.
type Session struct {
	IDHash     string
	UserID     string
	CSRFToken  string
	CreatedAt  int64
	LastSeenAt int64
	ExpiresAt  int64
}

// Token is an API token's row. ID is public (the lookup key a bearer carries); SecretHash is the
// SHA-256 (hex) of the secret half, never the secret. Permissions is a JSON array.
type Token struct {
	ID          string
	IdentityID  string
	Subject     string
	SecretHash  string
	Label       string
	Permissions string
	CreatedAt   int64
	ExpiresAt   int64
	LastUsedAt  int64
	RevokedAt   int64
}

// Users is the users table. Keys: ID, and Email (unique).
type Users interface {
	// CreateUser inserts u; ErrEmailTaken when its Email is held. DisabledAt is ignored: a user is
	// created enabled.
	CreateUser(ctx context.Context, u User) error
	// GetUser and GetUserByEmail return the user or ErrNotFound.
	GetUser(ctx context.Context, id string) (User, error)
	GetUserByEmail(ctx context.Context, email string) (User, error)
	// ListUsers returns every user, oldest first (created_at, then id).
	ListUsers(ctx context.Context) ([]User, error)
	// DisableUser sets disabled_at to at unless it is already set (the first time stands);
	// ErrNotFound when no user has id.
	DisableUser(ctx context.Context, id string, at int64) error
	// EnableUser clears disabled_at, the way back from DisableUser; an enabled user stays as it is.
	// ErrNotFound when no user has id.
	EnableUser(ctx context.Context, id string) error
	// SetUserPassword replaces the password hash; ErrNotFound when no user has id.
	SetUserPassword(ctx context.Context, id, passwordHash string) error
}

// Sessions is the sessions table. Key: IDHash. Expiry is judged by the caller, not here.
type Sessions interface {
	// CreateSession inserts s; its UserID must name a user (the foreign key refuses otherwise).
	CreateSession(ctx context.Context, s Session) error
	// GetSession returns the session or ErrNotFound, expired or not.
	GetSession(ctx context.Context, idHash string) (Session, error)
	// DeleteSession removes the session (sign-out); a hash with no row is not an error.
	DeleteSession(ctx context.Context, idHash string) error
	// DeleteUserSessions removes every session of the user (a password change) and returns how many
	// it removed; a user with none, or no such user, is 0 and no error.
	DeleteUserSessions(ctx context.Context, userID string) (int64, error)
}

// Tokens is the tokens table. Key: ID. Expiry and revocation are judged by the caller.
type Tokens interface {
	// CreateToken inserts t; LastUsedAt and RevokedAt are ignored (a token starts unused, live).
	CreateToken(ctx context.Context, t Token) error
	// GetToken returns the token or ErrNotFound, revoked or not.
	GetToken(ctx context.Context, id string) (Token, error)
	// ListTokens returns identityID's tokens, or every token when identityID is "", oldest first
	// (created_at, then id), revoked ones included.
	ListTokens(ctx context.Context, identityID string) ([]Token, error)
	// RevokeToken sets revoked_at to at unless it is already set (the first time stands);
	// ErrNotFound when no token has id.
	RevokeToken(ctx context.Context, id string, at int64) error
}

var (
	_ Users    = (*SQLite)(nil)
	_ Sessions = (*SQLite)(nil)
	_ Tokens   = (*SQLite)(nil)
	_ Users    = (*Postgres)(nil)
	_ Sessions = (*Postgres)(nil)
	_ Tokens   = (*Postgres)(nil)
)
