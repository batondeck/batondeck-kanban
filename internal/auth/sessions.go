package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
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

// Login checks the credentials and opens a session. It returns the cookie's secret, which is stored
// only as its SHA-256.
func (s *Service) Login(ctx context.Context, login, password string) (secret string, sess store.Session, err error) {
	u, err := s.Accounts.Authenticate(ctx, login, password)
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
// on success, 401 otherwise. Wrap it in CSRF, so a page of another site cannot sign a browser in.
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
		secret, sess, err := s.Login(r.Context(), r.PostForm.Get("login"), r.PostForm.Get("password"))
		if err != nil {
			if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeUnauthenticated {
				http.Error(w, "invalid credentials", http.StatusUnauthorized)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		expires := time.UnixMilli(sess.ExpiresAt)
		secure := requestIsTLS(r)
		http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: secret, Path: "/", Expires: expires,
			HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
		http.SetCookie(w, &http.Cookie{Name: CSRFCookie, Value: sess.CSRFToken, Path: "/", Expires: expires,
			HttpOnly: false, Secure: secure, SameSite: http.SameSiteLaxMode})
		w.WriteHeader(http.StatusNoContent)
	})
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

func sessionFrom(ctx context.Context) (store.Session, bool) {
	sess, ok := ctx.Value(sessionKey).(store.Session)
	return sess, ok
}

// Authenticate requires a principal: an `Authorization: Bearer` token, else the session cookie.
// Neither, or one that does not resolve, is 401. The bearer wins when both are present, and such a
// request is judged by CSRF as a non-browser one.
func (s *Service) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var err error
		if h := r.Header.Get("Authorization"); h != "" {
			var c domain.Caller
			if c, err = s.ResolveCaller(ctx, h); err == nil {
				next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, callerKey, c)))
				return
			}
		} else if ck, cerr := r.Cookie(SessionCookie); cerr == nil {
			var c domain.Caller
			var sess store.Session
			if c, sess, err = s.ResolveSession(ctx, ck.Value); err == nil {
				ctx = context.WithValue(context.WithValue(ctx, callerKey, c), sessionKey, sess)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		} else {
			err = errUnauthenticated()
		}
		if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeUnauthenticated {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
	})
}
