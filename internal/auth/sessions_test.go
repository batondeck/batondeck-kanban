package auth

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
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

// oauthAccess stores a live OAuth access token of identity with scope, issued for the service's
// resource, and returns its value.
func (f *fixture) oauthAccess(t *testing.T, identity, scope string) string {
	t.Helper()
	ctx := t.Context()
	f.svc.OAuth, f.svc.OAuthResource = f.st, "https://bd.test/mcp"
	now := f.clk.now().UnixMilli()
	client := "c-" + identity + "-" + scope
	if err := f.st.CreateOAuthClient(ctx, store.OAuthClient{ClientID: client, ClientName: "connector", RedirectURIs: []string{"http://127.0.0.1/cb"}, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	fam := "F-" + client
	if err := f.st.CreateOAuthFamily(ctx, store.OAuthFamily{ID: fam, ClientID: client, IdentityID: identity, Scope: scope, Resource: f.svc.OAuthResource, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	v, h, err := MintOAuthValue(OAuthAccessPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.CreateOAuthToken(ctx, store.OAuthToken{TokenHash: h, Kind: store.OAuthAccess, FamilyID: fam, ClientID: client, IdentityID: identity,
		Scope: scope, Resource: f.svc.OAuthResource, CreatedAt: now, ExpiresAt: now + time.Hour.Milliseconds()}); err != nil {
		t.Fatal(err)
	}
	return v
}

// An OAuth access token is issued for the /mcp resource (RFC 8707) and capped at the role granted
// at consent: the BFF (Authenticate, and Reauthenticate for /api/stream) is another resource and
// refuses it as UNAUTHENTICATED, so a viewer-scoped connector cannot reach the REST routes that know
// no cap (the CLI-token mint above all). ResolveCaller, which /mcp uses, still resolves it; an API
// token still opens the BFF.
func TestAnOAuthAccessTokenDoesNotOpenTheBFF(t *testing.T) {
	f := newFixture(t)
	alice := f.addUser(t, "alice", "pw")
	access := f.oauthAccess(t, alice.ID, "viewer")
	if c, err := f.svc.ResolveCaller(t.Context(), "Bearer "+access); err != nil || c.Role != "viewer" {
		t.Fatalf("ResolveCaller (the /mcp path): %+v %v", c, err)
	}
	if w := f.post(req{bearer: access}); w.Code != http.StatusUnauthorized || w.Header().Get("X-Identity") != "" {
		t.Fatalf("an OAuth access token at the BFF: %d %q, want 401", w.Code, w.Header().Get("X-Identity"))
	}
	r := httptest.NewRequest(http.MethodGet, "http://board.test/api/stream", nil)
	r.Header.Set("Authorization", "Bearer "+access)
	if _, err := f.svc.Reauthenticate(r); err == nil {
		t.Fatal("Reauthenticate accepted an OAuth access token")
	} else {
		wantCode(t, err, domain.CodeUnauthenticated)
	}
	plain, _, err := f.svc.Tokens.Create(t.Context(), alice.ID, "alice", "ci", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if w := f.post(req{bearer: plain}); w.Code != http.StatusOK || w.Header().Get("X-Identity") != alice.ID {
		t.Fatalf("an API token at the BFF: %d", w.Code)
	}
}
