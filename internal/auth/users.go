// Package auth is who a request is: local users with argon2id passwords, browser sessions with
// CSRF, API tokens (the TokenService) and ResolveCaller, which turns a bearer into a domain.Caller.
// It reaches the database only through internal/store's Users, Sessions and Tokens.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
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

// Accounts manages local users. There is no sign-up: an account is made on the host with
// `batondeck-kanban user add`.
type Accounts struct {
	Store store.Users
	// Now is the clock; nil means time.Now.
	Now func() time.Time
	// Params are the argon2id costs for new hashes; the zero value means DefaultArgon2.
	Params Argon2Params
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

// Add creates an enabled user. displayName "" means the login's part before any "@".
func (a *Accounts) Add(ctx context.Context, login, displayName, password string) (store.User, error) {
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
	u := store.User{ID: id, Email: l, DisplayName: displayName, PasswordHash: hash, CreatedAt: a.now().UnixMilli()}
	if err := a.Store.CreateUser(ctx, u); err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			return store.User{}, domain.Validation("a user with that login exists", map[string]any{"field": "login", "login": l})
		}
		return store.User{}, err
	}
	return u, nil
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
// entry that ResolveCaller holds expires (AccountCacheTTL, 60 s by default).
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

// SetPassword replaces the user's password.
func (a *Accounts) SetPassword(ctx context.Context, login, password string) error {
	if err := checkPassword(password); err != nil {
		return err
	}
	u, err := a.Lookup(ctx, login)
	if err != nil {
		return err
	}
	hash, err := HashPassword(password, a.params())
	if err != nil {
		return err
	}
	return a.Store.SetUserPassword(ctx, u.ID, hash)
}

// List returns every user, oldest first.
func (a *Accounts) List(ctx context.Context) ([]store.User, error) { return a.Store.ListUsers(ctx) }

// dummyHash is verified against when the login does not exist, so an unknown login costs the same
// argon2id run as a wrong password. Made on first use, not at start.
var dummyHash = sync.OnceValue(func() string {
	h, _ := HashPassword("not a password", DefaultArgon2)
	return h
})

// Authenticate checks a login and password. An unknown login, a wrong password and a disabled user
// are one refusal: UNAUTHENTICATED, with nothing saying which.
func (a *Accounts) Authenticate(ctx context.Context, login, password string) (store.User, error) {
	l, err := NormalizeLogin(login)
	if err != nil {
		return store.User{}, errUnauthenticated()
	}
	u, err := a.Store.GetUserByEmail(ctx, l)
	if errors.Is(err, store.ErrNotFound) {
		_ = VerifyPassword(password, dummyHash())
		return store.User{}, errUnauthenticated()
	}
	if err != nil {
		return store.User{}, err
	}
	if !VerifyPassword(password, u.PasswordHash) || u.DisabledAt != 0 {
		return store.User{}, errUnauthenticated()
	}
	return u, nil
}

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
