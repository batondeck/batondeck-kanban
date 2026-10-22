package auth

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// login POSTs the form to the login handler (wrapped in CSRF, as it is mounted) and returns the
// response.
func (f *fixture) login(t *testing.T, login, password string, hdr map[string]string, tlsOn bool) *http.Response {
	t.Helper()
	form := url.Values{"login": {login}, "password": {password}}
	r := httptest.NewRequest(http.MethodPost, "http://board.test/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	if tlsOn {
		r.TLS = &tls.ConnectionState{}
	}
	w := httptest.NewRecorder()
	f.svc.CSRF(f.svc.LoginHandler()).ServeHTTP(w, r)
	return w.Result()
}

func cookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestLoginSetsTheSessionCookie(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "alice", "pw")
	resp := f.login(t, "alice", "pw", map[string]string{"Sec-Fetch-Site": "same-origin"}, false)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d", resp.StatusCode)
	}
	sc, cc := cookie(resp, SessionCookie), cookie(resp, CSRFCookie)
	if sc == nil || cc == nil {
		t.Fatalf("cookies %v", resp.Cookies())
	}
	if !sc.HttpOnly || sc.SameSite != http.SameSiteLaxMode || sc.Secure || sc.Path != "/" {
		t.Fatalf("session cookie %+v", sc)
	}
	if cc.HttpOnly || cc.Value == "" {
		t.Fatalf("csrf cookie %+v", cc)
	}
	if want := f.clk.now().Add(DefaultSessionTTL).Unix(); sc.Expires.Unix() != want {
		t.Fatalf("expires %v, want %v", sc.Expires, time.Unix(want, 0))
	}
	// Over TLS the cookies are Secure.
	resp = f.login(t, "alice", "pw", nil, true)
	if c := cookie(resp, SessionCookie); c == nil || !c.Secure || !cookie(resp, CSRFCookie).Secure {
		t.Fatalf("over TLS: %v", resp.Cookies())
	}
	// Only the hash is stored.
	if _, err := f.st.GetSession(t.Context(), sc.Value); err == nil {
		t.Fatal("the session row is keyed by the plaintext secret")
	}
	if _, err := f.st.GetSession(t.Context(), hashSecret(sc.Value)); err != nil {
		t.Fatal(err)
	}
}

func TestLoginRefusesBadCredentialsAndGet(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "alice", "pw")
	if resp := f.login(t, "alice", "nope", nil, false); resp.StatusCode != http.StatusUnauthorized || cookie(resp, SessionCookie) != nil {
		t.Fatalf("wrong password: %d %v", resp.StatusCode, resp.Cookies())
	}
	w := httptest.NewRecorder()
	f.svc.LoginHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/login", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", w.Code)
	}
	if resp := f.login(t, "alice", "pw", map[string]string{"Sec-Fetch-Site": "cross-site"}, false); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("a cross-site login: %d", resp.StatusCode)
	}
}

// protected is a state-changing route as the server mounts one: Authenticate, then CSRF.
func (f *fixture) protected() http.Handler {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, _ := CallerFrom(r.Context())
		w.Header().Set("X-Identity", c.Identity)
		w.WriteHeader(http.StatusOK)
	})
	return f.svc.Authenticate(f.svc.CSRF(ok))
}

type req struct {
	session, csrf, bearer string
	hdr                   map[string]string
}

func (f *fixture) post(q req) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "http://board.test/api/x", nil)
	if q.session != "" {
		r.AddCookie(&http.Cookie{Name: SessionCookie, Value: q.session})
	}
	if q.csrf != "" {
		r.Header.Set(CSRFHeader, q.csrf)
	}
	if q.bearer != "" {
		r.Header.Set("Authorization", "Bearer "+q.bearer)
	}
	for k, v := range q.hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.protected().ServeHTTP(w, r)
	return w
}

func (f *fixture) session(t *testing.T, login string) (secret, csrf string) {
	t.Helper()
	resp := f.login(t, login, "pw", nil, false)
	return cookie(resp, SessionCookie).Value, cookie(resp, CSRFCookie).Value
}

func TestRefusalExpiredSession(t *testing.T) {
	f := newFixture(t)
	f.svc.SessionTTL = time.Hour
	f.addUser(t, "alice", "pw")
	old, oldCSRF := f.session(t, "alice")
	f.clk.add(30 * time.Minute)
	fresh, freshCSRF := f.session(t, "alice")
	f.clk.add(30 * time.Minute) // old is exactly an hour old: expired
	same := map[string]string{"Sec-Fetch-Site": "same-origin"}
	t.Run("Refused", func(t *testing.T) {
		if w := f.post(req{session: old, csrf: oldCSRF, hdr: same}); w.Code != http.StatusUnauthorized {
			t.Fatalf("expired session: %d", w.Code)
		}
		if w := f.post(req{session: "not-a-session", csrf: oldCSRF, hdr: same}); w.Code != http.StatusUnauthorized {
			t.Fatalf("unknown session: %d", w.Code)
		}
	})
	t.Run("Control", func(t *testing.T) {
		if w := f.post(req{session: fresh, csrf: freshCSRF, hdr: same}); w.Code != http.StatusOK {
			t.Fatalf("live session: %d", w.Code)
		}
	})
}

func TestDisabledUserSessionFailsWithinTheCacheTTL(t *testing.T) {
	f := newFixture(t)
	f.svc.AccountCacheTTL = 50 * time.Millisecond
	f.addUser(t, "alice", "pw")
	sess, csrf := f.session(t, "alice")
	same := map[string]string{"Sec-Fetch-Site": "same-origin"}
	if w := f.post(req{session: sess, csrf: csrf, hdr: same}); w.Code != http.StatusOK {
		t.Fatalf("before: %d", w.Code)
	}
	if _, err := f.svc.Accounts.Disable(t.Context(), "alice"); err != nil {
		t.Fatal(err)
	}
	f.clk.add(50 * time.Millisecond)
	if w := f.post(req{session: sess, csrf: csrf, hdr: same}); w.Code != http.StatusUnauthorized {
		t.Fatalf("after the window: %d", w.Code)
	}
}

func TestNoCredentialIs401(t *testing.T) {
	f := newFixture(t)
	if w := f.post(req{}); w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("%d %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}
	if w := f.post(req{bearer: "nope"}); w.Code != http.StatusUnauthorized {
		t.Fatalf("bad bearer: %d", w.Code)
	}
}
