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

// A login with next redirects there (303) on success and back with login=failed on a wrong
// password, but only to a path on this server: any other next is 400 and signs nobody in.
func TestLoginNextRedirectsOnlyToLocalPaths(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "alice", "pw-alice-1")
	post := func(password, next string) *http.Response {
		form := url.Values{"login": {"alice"}, "password": {password}, "next": {next}}
		r := httptest.NewRequest(http.MethodPost, "http://board.test/login", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		f.svc.CSRF(f.svc.LoginHandler()).ServeHTTP(w, r)
		return w.Result()
	}
	if r := post("pw-alice-1", "/authorize?client_id=c&state=s"); r.StatusCode != http.StatusSeeOther ||
		r.Header.Get("Location") != "/authorize?client_id=c&state=s" || cookie(r, SessionCookie) == nil {
		t.Fatalf("local next: %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	if r := post("wrong", "/authorize?client_id=c"); r.StatusCode != http.StatusSeeOther ||
		r.Header.Get("Location") != "/authorize?client_id=c&login=failed" || cookie(r, SessionCookie) != nil {
		t.Fatalf("wrong password: %d %q", r.StatusCode, r.Header.Get("Location"))
	}
	for _, next := range []string{"https://evil.example/", "//evil.example/x", "/\\evil.example", "javascript:alert(1)", "authorize"} {
		if r := post("pw-alice-1", next); r.StatusCode != http.StatusBadRequest || cookie(r, SessionCookie) != nil {
			t.Fatalf("next %q: %d, want 400 and no session", next, r.StatusCode)
		}
	}
}

// Behind a TLS proxy that sends no X-Forwarded-Proto (nginx's bare proxy_pass), an https public_url
// makes both cookies Secure, so a later plain-http request cannot carry the session in clear; and a
// browser without Fetch Metadata whose Origin is the public URL signs in, although the Host the
// proxy forwarded is another. A proxy chain's "https, http" is https. Without a public_url the
// header alone decides (the control).
func TestPublicURLMakesTheCookiesSecure(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "alice", "pw")
	login := func(hdr map[string]string) *http.Response {
		t.Helper()
		form := url.Values{"login": {"alice"}, "password": {"pw"}}
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/login", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		f.svc.CSRF(f.svc.LoginHandler()).ServeHTTP(w, r)
		return w.Result()
	}
	secure := func(resp *http.Response) bool {
		sc, cc := cookie(resp, SessionCookie), cookie(resp, CSRFCookie)
		return sc != nil && cc != nil && sc.Secure && cc.Secure
	}
	if resp := login(nil); resp.StatusCode != http.StatusNoContent || secure(resp) {
		t.Fatalf("no public_url, no header: %d, Secure %t", resp.StatusCode, secure(resp))
	}
	if resp := login(map[string]string{"X-Forwarded-Proto": "https, http"}); !secure(resp) {
		t.Fatalf("X-Forwarded-Proto %q: not Secure", "https, http")
	}
	origin := map[string]string{"Origin": "https://board.example.com"}
	if resp := login(origin); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("no public_url, Origin the proxy's: %d, want 403", resp.StatusCode)
	}
	f.svc.PublicURL = "https://board.example.com/"
	if resp := login(nil); resp.StatusCode != http.StatusNoContent || !secure(resp) {
		t.Fatalf("an https public_url, no header: %d, Secure %t", resp.StatusCode, secure(resp))
	}
	if resp := login(origin); resp.StatusCode != http.StatusNoContent || !secure(resp) {
		t.Fatalf("an https public_url, Origin it: %d, Secure %t", resp.StatusCode, secure(resp))
	}
	if resp := login(map[string]string{"Origin": "https://evil.example"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("an https public_url, another Origin: %d, want 403", resp.StatusCode)
	}
	f.svc.PublicURL = "http://board.example.com"
	if resp := login(nil); secure(resp) {
		t.Fatal("an http public_url made the cookies Secure")
	}
	// A browser's Origin never names its scheme's default port, so a public_url that does still
	// matches it.
	for pub, o := range map[string]string{"https://Board.example.com:443/": "https://board.example.com", "http://board.example.com:80": "http://board.example.com"} {
		f.svc.PublicURL = pub
		if resp := login(map[string]string{"Origin": o}); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("public_url %s, Origin %s: %d, want 204", pub, o, resp.StatusCode)
		}
	}
	f.svc.PublicURL = "https://board.example.com:8443"
	if resp := login(map[string]string{"Origin": "https://board.example.com"}); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("public_url on port 8443, Origin on 443: %d, want 403", resp.StatusCode)
	}
}

// An Authorization header of another scheme (a reverse proxy's HTTP Basic auth, which the browser
// then sends with every request) is no bearer and does not shadow the session cookie; a Bearer one
// still wins over the cookie, so a bad bearer is 401 whatever cookie rides along.
func TestAnotherAuthorizationSchemeLeavesTheSession(t *testing.T) {
	f := newFixture(t)
	u := f.addUser(t, "alice", "pw")
	sess, csrf := f.session(t, "alice")
	get := func(authz string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "http://board.test/api/x", nil)
		r.AddCookie(&http.Cookie{Name: SessionCookie, Value: sess})
		r.Header.Set("Authorization", authz)
		w := httptest.NewRecorder()
		f.protected().ServeHTTP(w, r)
		return w
	}
	if w := get("Basic dXNlcjpwYXNz"); w.Code != http.StatusOK || w.Header().Get("X-Identity") != u.ID {
		t.Fatalf("a session with a Basic header: %d %q", w.Code, w.Header().Get("X-Identity"))
	}
	if w := get("Bearer bk_not-a-token"); w.Code != http.StatusUnauthorized {
		t.Fatalf("a session with a bad Bearer: %d, want 401", w.Code)
	}
	// The session it resolved to is a session: an unsafe request still needs the CSRF token.
	if w := f.post(req{session: sess, hdr: map[string]string{"Authorization": "Basic dXNlcjpwYXNz", "Sec-Fetch-Site": "same-origin"}}); w.Code != http.StatusForbidden {
		t.Fatalf("a Basic header and the cookie, no CSRF token: %d, want 403", w.Code)
	}
	if w := f.post(req{session: sess, csrf: csrf, hdr: map[string]string{"Authorization": "Basic dXNlcjpwYXNz", "Sec-Fetch-Site": "same-origin"}}); w.Code != http.StatusOK {
		t.Fatalf("a Basic header, the cookie and its CSRF token: %d", w.Code)
	}
}
