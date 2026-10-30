package store

import (
	"context"
	"errors"
)

// The OAuth 2.1 authorization server's area (R0.13): dynamically registered clients, authorization
// codes, refresh-token families and opaque tokens. Codes and tokens are held by the SHA-256 (hex) of
// their value, never the value. Times are Unix milliseconds; a zero time is "never" (a NULL column).
// A method that finds no row returns ErrNotFound, on both engines; internal/store/conformance holds it.

// ErrOAuthUsed is ConsumeOAuthCode's and ConsumeOAuthToken's answer when the row was consumed
// before: the row comes back beside it, its UsedAt the first use.
var ErrOAuthUsed = errors.New("store: the oauth code or token was used before")

// OAuthClient is a client registered by RFC 7591 dynamic registration: a public client (token
// endpoint auth "none"), whose RedirectURIs are matched exactly. LastUsedAt is the last time an
// authorization was granted to it, 0 until the first.
type OAuthClient struct {
	ClientID     string
	ClientName   string
	RedirectURIs []string
	Scope        string
	CreatedAt    int64
	LastUsedAt   int64
}

// OAuthCode is an authorization code: bound to its client, user, redirect URI, PKCE S256 challenge,
// granted scope and resource (RFC 8707), used once.
type OAuthCode struct {
	CodeHash      string
	ClientID      string
	IdentityID    string
	RedirectURI   string
	CodeChallenge string
	Scope         string
	Resource      string
	CreatedAt     int64
	ExpiresAt     int64
	UsedAt        int64
}

// OAuthFamily is the lineage of the tokens one authorization code started. Revoking it ends every
// token in it: a reused refresh token does that.
type OAuthFamily struct {
	ID            string
	ClientID      string
	IdentityID    string
	Scope         string
	Resource      string
	CreatedAt     int64
	RevokedAt     int64
	RevokedReason string
}

// The kinds an OAuthToken is.
const (
	OAuthAccess  = "access"
	OAuthRefresh = "refresh"
)

// OAuthToken is an access or refresh token of a family. FamilyRevokedAt is read from the family
// (CreateOAuthToken ignores it): a token whose family is revoked is dead whatever its own row says.
type OAuthToken struct {
	TokenHash       string
	Kind            string
	FamilyID        string
	ClientID        string
	IdentityID      string
	Scope           string
	Resource        string
	CreatedAt       int64
	ExpiresAt       int64
	UsedAt          int64
	RevokedAt       int64
	FamilyRevokedAt int64
}

// OAuth is the four oauth_* tables. Expiry is judged by the caller, not here. Deleting a client
// deletes its codes, families and tokens (the foreign keys cascade).
type OAuth interface {
	// CreateOAuthClient inserts c; LastUsedAt is ignored (a client starts unused).
	CreateOAuthClient(ctx context.Context, c OAuthClient) error
	// GetOAuthClient returns the client or ErrNotFound.
	GetOAuthClient(ctx context.Context, clientID string) (OAuthClient, error)
	// TouchOAuthClient sets last_used_at to at; ErrNotFound when there is no such client.
	TouchOAuthClient(ctx context.Context, clientID string, at int64) error
	// DeleteUnusedOAuthClients deletes every client never used and created before createdBefore,
	// and returns how many.
	DeleteUnusedOAuthClients(ctx context.Context, createdBefore int64) (int64, error)
	// DeleteExpiredOAuthCodes removes at most limit codes whose ExpiresAt is before before, used or
	// not, oldest expiry first (the retention sweep), and returns how many; a limit <= 0 is
	// VALIDATION. The client and the family stay.
	DeleteExpiredOAuthCodes(ctx context.Context, before int64, limit int) (int64, error)
	// DeleteExpiredOAuthTokens removes at most limit access and refresh tokens whose ExpiresAt is
	// before before, whatever their UsedAt, RevokedAt or family, oldest expiry first (the retention
	// sweep), and returns how many; a limit <= 0 is VALIDATION. The family stays.
	DeleteExpiredOAuthTokens(ctx context.Context, before int64, limit int) (int64, error)

	// CreateOAuthCode inserts c; UsedAt is ignored. Its ClientID must name a client.
	CreateOAuthCode(ctx context.Context, c OAuthCode) error
	// ConsumeOAuthCode marks the code used at at and returns it as it was (UsedAt 0). A code used
	// before is returned with ErrOAuthUsed and keeps its first use; of two concurrent calls exactly
	// one consumes it. ErrNotFound when there is no such code.
	ConsumeOAuthCode(ctx context.Context, codeHash string, at int64) (OAuthCode, error)

	// CreateOAuthFamily inserts f; RevokedAt and RevokedReason are ignored. Its ClientID must name
	// a client.
	CreateOAuthFamily(ctx context.Context, f OAuthFamily) error
	// GetOAuthFamily returns the family or ErrNotFound.
	GetOAuthFamily(ctx context.Context, id string) (OAuthFamily, error)
	// RevokeOAuthFamily sets revoked_at and the reason unless the family is revoked already (the
	// first revocation stands); ErrNotFound when there is no such family.
	RevokeOAuthFamily(ctx context.Context, id string, at int64, reason string) error

	// CreateOAuthToken inserts t; UsedAt, RevokedAt and FamilyRevokedAt are ignored. Its FamilyID
	// and ClientID must name a family and a client.
	CreateOAuthToken(ctx context.Context, t OAuthToken) error
	// GetOAuthToken returns the token, with its family's revocation time, or ErrNotFound.
	GetOAuthToken(ctx context.Context, tokenHash string) (OAuthToken, error)
	// ConsumeOAuthToken marks the token used at at and returns it as it was (UsedAt 0), as
	// ConsumeOAuthCode does a code: a token used before comes back with ErrOAuthUsed.
	ConsumeOAuthToken(ctx context.Context, tokenHash string, at int64) (OAuthToken, error)
}

var (
	_ OAuth = (*SQLite)(nil)
	_ OAuth = (*Postgres)(nil)
)
