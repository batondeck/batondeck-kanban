package auth

// API tokens (conductor's CliToken, hdtp's TokenService): named, revocable, hashed at rest. A token
// is "bd_" + a 16-hex-character public id + "_" + 43 characters of base64url secret. The id is the
// lookup key and is safe to show; only the SHA-256 of the secret is stored, and the plaintext exists
// once, in Create's return. scripts/check-secrets.py refuses the format in a commit.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// TokenPrefix starts every API token.
const TokenPrefix = "bd_"

const (
	tokenIDBytes     = 8
	tokenSecretBytes = 32
	maxBearerLen     = 256
	maxLabelLen      = 100
)

// DefaultTokenTTL and MaxTokenTTL bound a token's life. The TokenService is the one machine path
// (agents' .mcp.json, scripts), so these are longer than conductor's 12 h / 7 d, whose machines
// used WorkOS API keys instead.
const (
	DefaultTokenTTL = 30 * 24 * time.Hour
	MaxTokenTTL     = 365 * 24 * time.Hour
)

// TokenService mints, validates, revokes and lists API tokens.
type TokenService struct {
	Store store.Tokens
	// Audit receives Mint's cli_token.minted and RevokeOwned's cli_token.revoked rows; nil
	// refuses both (INTERNAL), since an unrecorded mint is the thing they exist to prevent.
	Audit store.Audit
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

func (t *TokenService) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// TokenInfo is a token as List and Create answer it: metadata only, never the hash or the secret.
type TokenInfo struct {
	ID         string
	IdentityID string
	Subject    string
	Label      string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	RevokedAt  time.Time // zero while live
	Status     string    // live, expired or revoked
}

// Display is the token's public prefix, "bd_<id>": what a list shows and a revoke may name.
func (i TokenInfo) Display() string { return TokenPrefix + i.ID }

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func tokenStatus(r store.Token, now time.Time) string {
	switch {
	case r.RevokedAt != 0:
		return "revoked"
	case r.ExpiresAt <= now.UnixMilli():
		return "expired"
	}
	return "live"
}

func tokenInfo(r store.Token, now time.Time) TokenInfo {
	i := TokenInfo{ID: r.ID, IdentityID: r.IdentityID, Subject: r.Subject, Label: r.Label,
		CreatedAt: time.UnixMilli(r.CreatedAt), ExpiresAt: time.UnixMilli(r.ExpiresAt), Status: tokenStatus(r, now)}
	if r.RevokedAt != 0 {
		i.RevokedAt = time.UnixMilli(r.RevokedAt)
	}
	return i
}

// Create mints a token acting for identityID (a user's id), named label, living ttl (0 means
// DefaultTokenTTL; above MaxTokenTTL is VALIDATION). A label whose agent any token of another
// identity has carried, live, revoked or expired, is VALIDATION (P-257 B1, review round 2): the
// label is that agent, the first identity to mint it keeps it, and a lapse does not hand it on. A
// label whose agent starts "id:" is VALIDATION for everyone, the minter's own id included: that is
// the form AgentID derives for a caller that names no agent, so as a label it would present the
// token's caller as that identity's agentless self (P-352).
// The returned plaintext is the ONLY time the secret exists outside a hash.
func (t *TokenService) Create(ctx context.Context, identityID, subject, label string, ttl time.Duration) (plaintext string, info TokenInfo, err error) {
	label = strings.TrimSpace(label)
	if label == "" || len(label) > maxLabelLen {
		return "", TokenInfo{}, domain.Validation("a token label is 1 to 100 characters", map[string]any{"field": "label"})
	}
	if ttl == 0 {
		ttl = DefaultTokenTTL
	}
	if ttl < time.Minute || ttl > MaxTokenTTL {
		return "", TokenInfo{}, domain.Validation("a token lives from 1 minute to 365 days", map[string]any{"field": "ttl"})
	}
	if agent := domain.TokenAgent(label); strings.HasPrefix(agent, "id:") {
		return "", TokenInfo{}, domain.Validation("a token label cannot start with \"id:\": that form is the id the server derives for a caller that names no agent",
			map[string]any{"field": "label", "label": agent})
	}
	if err := t.labelFree(ctx, identityID, label); err != nil {
		return "", TokenInfo{}, err
	}
	idb := make([]byte, tokenIDBytes)
	sec := make([]byte, tokenSecretBytes)
	if _, err := rand.Read(idb); err != nil {
		return "", TokenInfo{}, err
	}
	if _, err := rand.Read(sec); err != nil {
		return "", TokenInfo{}, err
	}
	id := hex.EncodeToString(idb)
	secret := base64.RawURLEncoding.EncodeToString(sec)
	now := t.now()
	row := store.Token{ID: id, IdentityID: identityID, Subject: subject, SecretHash: hashSecret(secret),
		Label: label, Permissions: "[]", CreatedAt: now.UnixMilli(), ExpiresAt: now.Add(ttl).UnixMilli()}
	if err := t.Store.CreateToken(ctx, row); err != nil {
		return "", TokenInfo{}, err
	}
	return TokenPrefix + id + "_" + secret, tokenInfo(row, now), nil
}

// labelFree refuses a label whose agent (domain.TokenAgent) any token row of another identity
// carries, whatever its status, so the first identity to mint a label owns it, and work handed to
// the label is never claimable by whoever mints it after a lapse. The retention sweep deletes lapsed
// token rows but keeps each identity's oldest and newest row of each label (retention.LapsedGrace),
// so every label an identity carried still has a row here, and so does its earliest. The default
// label names no agent and is everyone's. It is a read before the insert, not a unique index (no
// migration): two identities minting one new label at the same instant can both land, and the
// service's label check (lifecycle ownLabel) then refuses both below master, for good.
func (t *TokenService) labelFree(ctx context.Context, identityID, label string) error {
	agent := domain.TokenAgent(label)
	if agent == "" {
		return nil
	}
	rows, err := t.Store.ListTokens(ctx, "")
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.IdentityID != identityID && domain.TokenAgent(r.Label) == agent {
			return domain.Validation("label "+strconv.Quote(agent)+" is another account's agent: choose another label",
				map[string]any{"field": "label", "label": agent})
		}
	}
	return nil
}

// parseToken splits a presented bearer into its id and secret, or fails. It never reaches the store
// with anything but the exact id shape Create makes.
func parseToken(presented string) (id, secret string, ok bool) {
	if len(presented) > maxBearerLen || !strings.HasPrefix(presented, TokenPrefix) {
		return "", "", false
	}
	id, secret, found := strings.Cut(presented[len(TokenPrefix):], "_")
	if !found || !validTokenID(id) || len(secret) < 16 {
		return "", "", false
	}
	return id, secret, true
}

// validTokenID reports whether id has the exact shape Create makes: 16 lowercase hex characters.
func validTokenID(id string) bool {
	if len(id) != 2*tokenIDBytes {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && strings.ToLower(id) == id
}

// Validate resolves a presented bearer to its live row. A malformed, unknown, wrong-secret,
// expired or revoked token is one refusal: UNAUTHENTICATED. Revocation takes effect on the next
// call: the row is read every time.
func (t *TokenService) Validate(ctx context.Context, presented string) (store.Token, error) {
	id, secret, ok := parseToken(presented)
	if !ok {
		return store.Token{}, errUnauthenticated()
	}
	row, err := t.Store.GetToken(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.Token{}, errUnauthenticated()
	}
	if err != nil {
		return store.Token{}, err
	}
	if subtle.ConstantTimeCompare([]byte(hashSecret(secret)), []byte(row.SecretHash)) != 1 {
		return store.Token{}, errUnauthenticated()
	}
	if tokenStatus(row, t.now()) != "live" {
		return store.Token{}, errUnauthenticated()
	}
	return row, nil
}

// Revoke revokes the token with this id ("bd_<id>" is accepted too); NOT_FOUND when there is none.
// Anything but the id's exact shape is VALIDATION, and the message never repeats the input: a whole
// token pasted here must not reach a terminal or a log a second time. Revoking a revoked token keeps
// its first revocation time.
func (t *TokenService) Revoke(ctx context.Context, id string) (TokenInfo, error) {
	id = strings.TrimPrefix(id, TokenPrefix)
	if !validTokenID(id) {
		return TokenInfo{}, domain.Validation("a token id is bd_ followed by 16 lowercase hex characters (the TOKEN column of `token list`), not the whole token", map[string]any{"field": "id"})
	}
	if err := t.Store.RevokeToken(ctx, id, t.now().UnixMilli()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return TokenInfo{}, domain.NotFound("token", id)
		}
		return TokenInfo{}, err
	}
	row, err := t.Store.GetToken(ctx, id)
	if err != nil {
		return TokenInfo{}, err
	}
	return tokenInfo(row, t.now()), nil
}

// List returns identityID's tokens ("" for every identity's), oldest first, metadata only.
func (t *TokenService) List(ctx context.Context, identityID string) ([]TokenInfo, error) {
	rows, err := t.Store.ListTokens(ctx, identityID)
	if err != nil {
		return nil, err
	}
	now := t.now()
	out := make([]TokenInfo, 0, len(rows))
	for _, r := range rows {
		out = append(out, tokenInfo(r, now))
	}
	return out, nil
}

// The audit actions of a token minted or revoked by a person or the host (P-159): the dropped
// create_cli_token and revoke_cli_token tools recorded them, and these paths replace those tools.
const (
	AuditTokenMinted  = "cli_token.minted"
	AuditTokenRevoked = "cli_token.revoked"
)

// HostActor is the audit actor of the host's `token` subcommand: whoever runs the binary on the
// host, who holds no identity of their own.
const HostActor = "host"

// Mint is Create, recorded: a cli_token.minted row (actor, subject the identity the token acts for,
// data {tokenId, label, via}) follows the token, and when that row cannot be written the token is
// revoked before the error returns, so no token exists unrecorded.
func (t *TokenService) Mint(ctx context.Context, actor, via, identityID, subject, label string, ttl time.Duration) (string, TokenInfo, error) {
	if t.Audit == nil {
		return "", TokenInfo{}, domain.NewError(domain.CodeInternal, "token mint: no audit log to record it in", nil)
	}
	plain, info, err := t.Create(ctx, identityID, subject, label, ttl)
	if err != nil {
		return "", TokenInfo{}, err
	}
	if err := t.audit(ctx, AuditTokenMinted, actor, info, via); err != nil {
		_ = t.Store.RevokeToken(context.WithoutCancel(ctx), info.ID, t.now().UnixMilli())
		return "", TokenInfo{}, err
	}
	return plain, info, nil
}

// RevokeOwned is Revoke of a token owner holds ("" for any owner: the host's `token revoke`); a
// token of another identity is NOT_FOUND, as one that does not exist, so a session cannot learn
// which ids are live. It records cli_token.revoked.
func (t *TokenService) RevokeOwned(ctx context.Context, actor, via, owner, id string) (TokenInfo, error) {
	if t.Audit == nil {
		return TokenInfo{}, domain.NewError(domain.CodeInternal, "token revoke: no audit log to record it in", nil)
	}
	bare := strings.TrimPrefix(id, TokenPrefix)
	if owner != "" && validTokenID(bare) {
		row, err := t.Store.GetToken(ctx, bare)
		if errors.Is(err, store.ErrNotFound) || (err == nil && row.IdentityID != owner) {
			return TokenInfo{}, domain.NotFound("token", bare)
		}
		if err != nil {
			return TokenInfo{}, err
		}
	}
	info, err := t.Revoke(ctx, id)
	if err != nil {
		return TokenInfo{}, err
	}
	return info, t.audit(ctx, AuditTokenRevoked, actor, info, via)
}

// audit records a token row. Its subject is the identity in the form every identity-valued audit
// subject is stored in (domain.CanonIdentity), so list_audit_log finds token rows like the rest (P-441).
func (t *TokenService) audit(ctx context.Context, action, actor string, info TokenInfo, via string) error {
	data, err := json.Marshal(map[string]string{"tokenId": info.ID, "label": info.Label, "via": via})
	if err != nil {
		return err
	}
	_, err = t.Audit.AppendAudit(ctx, store.AuditRow{TS: t.now().UnixMilli(), Action: action, Actor: actor, Subject: domain.CanonIdentity(info.IdentityID),
		Result: store.AuditOK, Data: data})
	return err
}
