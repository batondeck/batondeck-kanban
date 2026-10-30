package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

const (
	// SessionCookie carries the session's secret: HttpOnly, SameSite=Lax, Secure over TLS.
	SessionCookie = "bk_session"
	// CSRFCookie carries the session's CSRF token for the page's script to echo in CSRFHeader. It is
	// not HttpOnly by design; the check compares against the session row, not against this cookie.
	CSRFCookie = "bk_csrf"
	// CSRFHeader is where an unsafe request of a session echoes the token (a form may use the
	// `csrf` field instead).
	CSRFHeader = "X-CSRF-Token"
	// DefaultSessionTTL is a session's fixed life from sign-in.
	DefaultSessionTTL = 7 * 24 * time.Hour

	maxLoginBody = 64 << 10
)

// Login checks the credentials from the client address addr and opens a session. It returns the
// cookie's secret, which is stored only as its SHA-256.
func (s *Service) Login(ctx context.Context, addr, login, password string) (secret string, sess store.Session, err error) {
	u, err := s.Accounts.Authenticate(ctx, addr, login, password)
	if err != nil {
		return "", store.Session{}, err
	}
	raw := make([]byte, 32)
	csrf := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", store.Session{}, err
	}
	if _, err := rand.Read(csrf); err != nil {
		return "", store.Session{}, err
	}
	ttl := s.SessionTTL
	if ttl == 0 {
		ttl = DefaultSessionTTL
	}
	now := s.now()
	secret = base64.RawURLEncoding.EncodeToString(raw)
	sess = store.Session{IDHash: hashSecret(secret), UserID: u.ID, CSRFToken: hex.EncodeToString(csrf),
		CreatedAt: now.UnixMilli(), LastSeenAt: now.UnixMilli(), ExpiresAt: now.Add(ttl).UnixMilli()}
	if err := s.Sessions.CreateSession(ctx, sess); err != nil {
		return "", store.Session{}, err
	}
	return secret, sess, nil
}

// ResolveSession resolves a session cookie's secret: UNAUTHENTICATED when it is unknown, expired, or
// its user is disabled (as of the account cache).
func (s *Service) ResolveSession(ctx context.Context, secret string) (domain.Caller, store.Session, error) {
	if secret == "" || len(secret) > maxBearerLen {
		return domain.Caller{}, store.Session{}, errUnauthenticated()
	}
	sess, err := s.Sessions.GetSession(ctx, hashSecret(secret))
	if errors.Is(err, store.ErrNotFound) {
		return domain.Caller{}, store.Session{}, errUnauthenticated()
	}
	if err != nil {
		return domain.Caller{}, store.Session{}, err
	}
	if sess.ExpiresAt <= s.now().UnixMilli() {
		return domain.Caller{}, store.Session{}, errUnauthenticated()
	}
	e, err := s.account(ctx, sess.UserID)
	if err != nil {
		return domain.Caller{}, store.Session{}, err
	}
	if !e.usable {
		return domain.Caller{}, store.Session{}, errUnauthenticated()
	}
	return callerFor(sess.UserID, e), sess, nil
}

func requestIsTLS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// LoginHandler answers POST with the form fields `login` and `password`: 204 and the two cookies
// on success, 401 {code, message} otherwise (the BFF's error body, which the SPA's form shows), or 429
// RATE_LIMITED with Retry-After past the sign-in limits (LoginLimits). With a `next` field that is a path on this server (the OAuth
// server's sign-in page sends one), success is instead 303 to next, and failure 303 to next with
// `login=failed` added, so a plain HTML form lands back where it started. Any other next is 400.
// Wrap it in CSRF, so a page of another site cannot sign a browser in.
func (s *Service) LoginHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxLoginBody)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		next := r.PostForm.Get("next")
		if next != "" && !LocalPath(next) {
			http.Error(w, "next is not a path on this server", http.StatusBadRequest)
			return
		}
		secret, sess, err := s.Login(r.Context(), s.Proxies.ClientAddr(r), r.PostForm.Get("login"), r.PostForm.Get("password"))
		if err != nil {
			e, ok := domain.AsBatonDeckError(err)
			if ok && e.Code == domain.CodeRateLimited {
				w.Header().Set("Retry-After", fmt.Sprint(e.Details["retryAfterSeconds"]))
				if next != "" {
					http.Redirect(w, r, withQuery(next, "login", "failed"), http.StatusSeeOther)
					return
				}
				WriteError(w, http.StatusTooManyRequests, domain.CodeRateLimited, e.Message)
				return
			}
			if ok && e.Code == domain.CodeUnauthenticated {
				if next != "" {
					http.Redirect(w, r, withQuery(next, "login", "failed"), http.StatusSeeOther)
					return
				}
				WriteError(w, http.StatusUnauthorized, domain.CodeUnauthenticated, LoginRefused)
				return
			}
			WriteError(w, http.StatusInternalServerError, domain.CodeInternal, "internal error")
			return
		}
		expires := time.UnixMilli(sess.ExpiresAt)
		secure := requestIsTLS(r)
		http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: secret, Path: "/", Expires: expires,
			HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
		http.SetCookie(w, &http.Cookie{Name: CSRFCookie, Value: sess.CSRFToken, Path: "/", Expires: expires,
			HttpOnly: false, Secure: secure, SameSite: http.SameSiteLaxMode})
		if next != "" {
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// LoginRefused is the login's one refusal: an unknown login, a wrong password and a disabled user
// read the same, so the answer says nothing about which.
const LoginRefused = "The login or password is wrong, or the account is disabled."

// LogoutHandler ends the request's browser session: it deletes the session row, so the cookie no
// longer resolves anywhere, and expires both cookies; 204. A bearer request has no session to end
// and gets the same 204. Mount it inside Authenticate and CSRF, so another site cannot sign a
// browser out.
func (s *Service) LogoutHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sess, ok := SessionFrom(r.Context()); ok {
			if err := s.Sessions.DeleteSession(r.Context(), sess.IDHash); err != nil {
				WriteError(w, http.StatusInternalServerError, domain.CodeInternal, "internal error")
				return
			}
		}
		secure := requestIsTLS(r)
		for _, name := range []string{SessionCookie, CSRFCookie} {
			http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1,
				HttpOnly: name == SessionCookie, Secure: secure, SameSite: http.SameSiteLaxMode})
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// LocalPath reports whether p is a path on this server, safe to redirect a browser to: it starts
// with one "/", names no scheme or host, and holds no backslash or control character (a browser
// reads "/\\host" as another host).
func LocalPath(p string) bool {
	if len(p) > 8192 || !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsAny(p, "\\") {
		return false
	}
	for _, c := range p {
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	u, err := url.Parse(p)
	return err == nil && u.Scheme == "" && u.Host == "" && u.User == nil
}

// withQuery is p with key=value set in its query.
func withQuery(p, key, value string) string {
	u, err := url.Parse(p)
	if err != nil {
		return p
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.String()
}

type ctxKey int

const (
	callerKey ctxKey = iota
	sessionKey
)

// CallerFrom is the principal Authenticate put on the request's context.
func CallerFrom(ctx context.Context) (domain.Caller, bool) {
	c, ok := ctx.Value(callerKey).(domain.Caller)
	return c, ok
}

// SessionFrom is the browser session Authenticate put on the request's context: absent when the
// request was authenticated by a bearer.
func SessionFrom(ctx context.Context) (store.Session, bool) {
	sess, ok := ctx.Value(sessionKey).(store.Session)
	return sess, ok
}

// Authenticate requires a principal: an `Authorization: Bearer` token, else the session cookie.
// Neither, or one that does not resolve, is 401. The bearer wins when both are present, and such a
// request is judged by CSRF as a non-browser one.
func (s *Service) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, sess, err := s.resolveRequest(r)
		if err == nil {
			ctx := context.WithValue(r.Context(), callerKey, c)
			if sess != nil {
				ctx = context.WithValue(ctx, sessionKey, *sess)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}
		if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeUnauthenticated {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
	})
}

// Reauthenticate resolves r's credential again, as Authenticate did: a long-lived response
// (/api/stream) learns from its UNAUTHENTICATED that the session was ended, the user disabled (as
// of the account cache) or the token revoked.
func (s *Service) Reauthenticate(r *http.Request) (domain.Caller, error) {
	c, _, err := s.resolveRequest(r)
	return c, err
}

// resolveRequest is r's principal: the bearer when there is one, else the session cookie (with its
// session); neither is UNAUTHENTICATED.
func (s *Service) resolveRequest(r *http.Request) (domain.Caller, *store.Session, error) {
	ctx := r.Context()
	if h := r.Header.Get("Authorization"); h != "" {
		c, err := s.ResolveCaller(ctx, h)
		return c, nil, err
	}
	ck, err := r.Cookie(SessionCookie)
	if err != nil {
		return domain.Caller{}, nil, errUnauthenticated()
	}
	c, sess, err := s.ResolveSession(ctx, ck.Value)
	if err != nil {
		return domain.Caller{}, nil, err
	}
	return c, &sess, nil
}
