// Package auth is who a request is: local users with argon2id passwords, browser sessions with
// CSRF, API tokens (the TokenService), the OAuth server's access tokens (oauthtokens.go; the server is
// internal/auth/oauth) and ResolveCaller, which turns either bearer into a domain.Caller.
// It reaches the database only through internal/store's Users, Sessions, Tokens and OAuth.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/crypto/argon2"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// Argon2Params are argon2id's cost parameters. They are written into every hash, so a hash made
// under other parameters still verifies.
type Argon2Params struct {
	Time    uint32
	Memory  uint32 // KiB
	Threads uint8
}

// DefaultArgon2 is RFC 9106's second recommended option: t=3, 64 MiB, p=4.
var DefaultArgon2 = Argon2Params{Time: 3, Memory: 64 * 1024, Threads: 4}

const (
	argonSaltLen = 16
	argonKeyLen  = 32
	maxLoginLen  = 254
)

// The audit actions of an account change (P-388): the workspace admin's on Settings → Users, the
// host's `user` subcommand, a user's own password change. Each row's subject is the account's
// identity (domain.CanonIdentity) and its data names the login and the via.
const (
	AuditUserAdded           = "user.added"
	AuditUserDisabled        = "user.disabled"
	AuditUserEnabled         = "user.enabled"
	AuditUserPasswordChanged = "user.password_changed"
)

// Accounts manages local users. There is no sign-up: an account is made on the host with
// `batondeck-kanban user add`.
type Accounts struct {
	Store store.Users
	// Sessions are the browser sessions SetPassword ends; SetPassword refuses to run without them.
	Sessions store.Sessions
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Params are the argon2id costs for new hashes; the zero value means DefaultArgon2.
	Params Argon2Params
	// Limits bound Authenticate's password checks; nil means the process-wide default.
	Limits *LoginLimits
	// Audit receives the account rows of AddAs, DisableAs, EnableAs and SetPasswordAs, which refuse
	// to run without it.
	Audit store.Audit
}

func (a *Accounts) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Accounts) params() Argon2Params {
	if a.Params == (Argon2Params{}) {
		return DefaultArgon2
	}
	return a.Params
}

// NormalizeLogin lowercases and checks a sign-in name: non-empty, at most 254 bytes, no space or
// control character. It is stored in the users.email column; an email address is the convention,
// not a requirement.
func NormalizeLogin(login string) (string, error) {
	l := strings.ToLower(strings.TrimSpace(login))
	if l == "" || len(l) > maxLoginLen || strings.IndexFunc(l, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return "", domain.Validation("a login is 1 to 254 characters with no spaces", map[string]any{"field": "login"})
	}
	return l, nil
}

func checkPassword(pw string) error {
	if pw == "" {
		return domain.Validation("the password is empty", map[string]any{"field": "password"})
	}
	return nil
}

// Add creates an enabled user, a workspace admin when admin is set. displayName "" means the
// login's part before any "@". It records nothing: AddAs does.
func (a *Accounts) Add(ctx context.Context, login, displayName, password string, admin bool) (store.User, error) {
	l, err := NormalizeLogin(login)
	if err != nil {
		return store.User{}, err
	}
	if err := checkPassword(password); err != nil {
		return store.User{}, err
	}
	if displayName == "" {
		displayName, _, _ = strings.Cut(l, "@")
	}
	hash, err := HashPassword(password, a.params())
	if err != nil {
		return store.User{}, err
	}
	id, err := randomID("U-", 8)
	if err != nil {
		return store.User{}, err
	}
	u := store.User{ID: id, Email: l, DisplayName: displayName, PasswordHash: hash, CreatedAt: a.now().UnixMilli(), WorkspaceAdmin: admin}
	if err := a.Store.CreateUser(ctx, u); err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			return store.User{}, domain.Validation("a user with that login exists", map[string]any{"field": "login", "login": l})
		}
		return store.User{}, err
	}
	return u, nil
}

// AddAs is Add, recorded: a user.added row (actor, subject the new identity, data {login, admin,
// via}) follows the account, and when that row cannot be written the account is disabled before
// the error returns, so no usable account exists unrecorded. The host's `user add` and Settings →
// Users call it; Add alone records nothing.
func (a *Accounts) AddAs(ctx context.Context, actor, via, login, displayName, password string, admin bool) (store.User, error) {
	if a.Audit == nil {
		return store.User{}, errNoAccountAudit("add")
	}
	u, err := a.Add(ctx, login, displayName, password, admin)
	if err != nil {
		return store.User{}, err
	}
	if err := a.record(ctx, AuditUserAdded, actor, u, map[string]any{"login": u.Email, "admin": u.WorkspaceAdmin, "via": via}); err != nil {
		if derr := a.Store.DisableUser(context.WithoutCancel(ctx), u.ID, a.now().UnixMilli()); derr != nil {
			return store.User{}, fmt.Errorf("the account %s was added but not recorded, and disabling it failed too (run `user disable %s`): %w", u.Email, u.Email, errors.Join(err, derr))
		}
		return store.User{}, fmt.Errorf("the account %s was not recorded, so it was left disabled: %w", u.Email, err)
	}
	return u, nil
}

// DisableAs is Disable, recorded: a user.disabled row (data {login, via}). The disable stands when
// the row cannot be written, and the error says so.
func (a *Accounts) DisableAs(ctx context.Context, actor, via, login string) (store.User, error) {
	if a.Audit == nil {
		return store.User{}, errNoAccountAudit("disable")
	}
	u, err := a.Disable(ctx, login)
	if err != nil {
		return store.User{}, err
	}
	if err := a.record(ctx, AuditUserDisabled, actor, u, map[string]any{"login": u.Email, "via": via}); err != nil {
		return store.User{}, fmt.Errorf("%s is disabled, but the audit row was not written: %w", u.Email, err)
	}
	return u, nil
}

// EnableAs is Enable, recorded: a user.enabled row (data {login, via}) when the user was disabled
// (enabling an enabled user changes nothing and records nothing). When the row cannot be written
// the user is disabled again before the error returns.
func (a *Accounts) EnableAs(ctx context.Context, actor, via, login string) (store.User, bool, error) {
	if a.Audit == nil {
		return store.User{}, false, errNoAccountAudit("enable")
	}
	before, err := a.Lookup(ctx, login)
	if err != nil {
		return store.User{}, false, err
	}
	u, was, err := a.Enable(ctx, login)
	if err != nil || !was {
		return u, was, err
	}
	if err := a.record(ctx, AuditUserEnabled, actor, u, map[string]any{"login": u.Email, "via": via}); err != nil {
		if derr := a.Store.DisableUser(context.WithoutCancel(ctx), u.ID, before.DisabledAt); derr != nil {
			return store.User{}, false, fmt.Errorf("%s was enabled but not recorded, and disabling it again failed too: %w", u.Email, errors.Join(err, derr))
		}
		return store.User{}, false, fmt.Errorf("%s was not recorded, so it stays disabled: %w", u.Email, err)
	}
	return u, true, nil
}

// SetPasswordAs is SetPassword, recorded: a user.password_changed row (data {login, via,
// sessionsEnded}). The new password and the ended sessions stand when the row cannot be written,
// and the error says so.
func (a *Accounts) SetPasswordAs(ctx context.Context, actor, via, login, password string) (int64, error) {
	if a.Audit == nil {
		return 0, errNoAccountAudit("password change")
	}
	ended, err := a.SetPassword(ctx, login, password)
	if err != nil {
		return 0, err
	}
	u, err := a.Lookup(ctx, login)
	if err == nil {
		err = a.record(ctx, AuditUserPasswordChanged, actor, u, map[string]any{"login": u.Email, "via": via, "sessionsEnded": ended})
	}
	if err != nil {
		return ended, fmt.Errorf("the password was changed and %d browser session(s) ended, but the audit row was not written: %w", ended, err)
	}
	return ended, nil
}

func errNoAccountAudit(what string) error {
	return domain.NewError(domain.CodeInternal, "account "+what+": no audit log to record it in", nil)
}

// record writes one account row: subject the account's identity in the form every identity-valued
// audit subject is stored in (domain.CanonIdentity).
func (a *Accounts) record(ctx context.Context, action, actor string, u store.User, data map[string]any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = a.Audit.AppendAudit(ctx, store.AuditRow{TS: a.now().UnixMilli(), Action: action, Actor: actor,
		Subject: domain.CanonIdentity(u.ID), Result: store.AuditOK, Data: raw})
	return err
}

// Lookup returns the user with this login, or NOT_FOUND.
func (a *Accounts) Lookup(ctx context.Context, login string) (store.User, error) {
	l, err := NormalizeLogin(login)
	if err != nil {
		return store.User{}, err
	}
	u, err := a.Store.GetUserByEmail(ctx, l)
	if errors.Is(err, store.ErrNotFound) {
		return store.User{}, domain.NotFound("user", l)
	}
	return u, err
}

// Disable marks the user disabled. Their sessions and tokens stop resolving once the account cache
// entry that ResolveCaller holds expires (AccountCacheTTL, 60 s by default). Enable is the way
// back. It records nothing: DisableAs does.
func (a *Accounts) Disable(ctx context.Context, login string) (store.User, error) {
	u, err := a.Lookup(ctx, login)
	if err != nil {
		return store.User{}, err
	}
	if err := a.Store.DisableUser(ctx, u.ID, a.now().UnixMilli()); err != nil {
		return store.User{}, err
	}
	return a.Store.GetUser(ctx, u.ID)
}

// Enable clears the user's disabled mark, returning the user after and whether they were disabled;
// enabling an enabled user changes nothing. Their unexpired sessions and tokens resolve again once
// the account cache entry expires (AccountCacheTTL, 60 s by default), as Disable's take effect.
// It records nothing: EnableAs does.
func (a *Accounts) Enable(ctx context.Context, login string) (u store.User, wasDisabled bool, err error) {
	u, err = a.Lookup(ctx, login)
	if err != nil {
		return store.User{}, false, err
	}
	if err := a.Store.EnableUser(ctx, u.ID); err != nil {
		return store.User{}, false, err
	}
	after, err := a.Store.GetUser(ctx, u.ID)
	if err != nil {
		return store.User{}, false, err
	}
	return after, u.DisabledAt != 0, nil
}

// SetPassword replaces the user's password and ends every browser session of the user, returning
// how many it ended: a reset after a leak must not leave a session the old password opened. The
// hash is written first, so a sign-in with the old password cannot land between the two writes and
// keep a session, and one whose check began before the write ends its own session when it reads
// the user again (Service.Login). API and OAuth tokens are separate credentials and stay (`token revoke` ends one;
// `user disable` ends them all). Without Sessions it refuses before writing anything. It records
// nothing: SetPasswordAs does.
func (a *Accounts) SetPassword(ctx context.Context, login, password string) (ended int64, err error) {
	if a.Sessions == nil {
		return 0, errors.New("auth: Accounts.Sessions is nil: a password change must end the user's sessions")
	}
	if err := checkPassword(password); err != nil {
		return 0, err
	}
	u, err := a.Lookup(ctx, login)
	if err != nil {
		return 0, err
	}
	hash, err := HashPassword(password, a.params())
	if err != nil {
		return 0, err
	}
	if err := a.Store.SetUserPassword(ctx, u.ID, hash); err != nil {
		return 0, err
	}
	n, err := a.Sessions.DeleteUserSessions(ctx, u.ID)
	if err != nil {
		return 0, fmt.Errorf("the password was changed but the user's sessions were not ended (run it again): %w", err)
	}
	return n, nil
}

// Name is how the account with this id is shown to people: its display name, else its login (the
// Email field holds a local account's login), as the service's memberDisplay names a member
// (internal/service/projects.go displayNames). An id with no account row is "" and no error, so a
// caller can omit the name rather than fail.
func (a *Accounts) Name(ctx context.Context, id string) (string, error) {
	u, err := a.Store.GetUser(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if u.DisplayName != "" {
		return u.DisplayName, nil
	}
	return u.Email, nil
}

// List returns every user, oldest first.
func (a *Accounts) List(ctx context.Context) ([]store.User, error) { return a.Store.ListUsers(ctx) }

// dummyHash is verified against when the login does not exist, so an unknown login costs the same
// argon2id run as a wrong password. Made on first use, not at start.
var dummyHash = sync.OnceValue(func() string {
	h, _ := HashPassword("not a password", DefaultArgon2)
	return h
})

// Authenticate checks a login and password from the client address addr. An unknown login, a wrong
// password and a disabled user are one refusal: UNAUTHENTICATED, with nothing saying which. Past
// Limits it is RATE_LIMITED, whose details carry retryAfterSeconds, without checking the password.
func (a *Accounts) Authenticate(ctx context.Context, addr, login, password string) (store.User, error) {
	l, err := NormalizeLogin(login)
	if err != nil {
		return store.User{}, errUnauthenticated()
	}
	lim := a.Limits
	if lim == nil {
		lim = loginLimits
	}
	refund, err := lim.admit(addr, a.now())
	if err != nil {
		return store.User{}, err
	}
	release, err := lim.acquire(ctx)
	if err != nil {
		return store.User{}, err
	}
	defer release()
	u, err := a.Store.GetUserByEmail(ctx, l)
	if errors.Is(err, store.ErrNotFound) {
		_ = verify(password, dummyHash())
		return store.User{}, errUnauthenticated()
	}
	if err != nil {
		return store.User{}, err
	}
	if !verify(password, u.PasswordHash) || u.DisabledAt != 0 {
		return store.User{}, errUnauthenticated()
	}
	refund()
	return u, nil
}

// verify is VerifyPassword; a test wraps it to count the checks in flight.
var verify = VerifyPassword

// HashPassword returns argon2id's encoded form: $argon2id$v=19$m=<KiB>,t=<n>,p=<n>$<salt>$<key>,
// salt and key in unpadded standard base64.
func HashPassword(password string, p Argon2Params) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, p.Memory, p.Time, p.Threads,
		b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches the encoded hash; a malformed hash matches
// nothing.
func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var v int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &v); err != nil || v != argon2.Version {
		return false
	}
	var p Argon2Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Time, &p.Threads); err != nil || p.Time == 0 || p.Threads == 0 {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, p.Time, p.Memory, p.Threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

func errUnauthenticated() error {
	return domain.NewError(domain.CodeUnauthenticated, "invalid, expired or revoked credentials", nil)
}

func randomID(prefix string, n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}
