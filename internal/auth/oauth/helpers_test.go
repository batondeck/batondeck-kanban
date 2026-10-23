package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// fixture is an authorization server over a real SQLite file, served by httptest, with one user,
// alice (a workspace admin), and a moveable clock.
type fixture struct {
	st    store.Store
	clk   *clock
	svc   *auth.Service
	as    *Server
	srv   *httptest.Server
	alice store.User
	mu    sync.Mutex
	audit []auth.AuditEvent
}

const (
	alicePW     = "alice-secret-pw"
	callbackURI = "http://127.0.0.1:43111/oauth/callback"
)

func newFixture(t *testing.T, mod ...func(*Options)) *fixture {
	t.Helper()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "oauth.db"), "FULL")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	f := &fixture{st: st, clk: &clock{t: time.UnixMilli(1_800_000_000_000)}}
	f.svc = &auth.Service{
		Accounts: &auth.Accounts{Store: st, Now: f.clk.now, Params: auth.Argon2Params{Time: 1, Memory: 64, Threads: 1}},
		Tokens:   &auth.TokenService{Store: st, Now: f.clk.now},
		Sessions: st,
		Now:      f.clk.now,
		OAuth:    st,
		Audit: func(_ context.Context, e auth.AuditEvent) {
			f.mu.Lock()
			f.audit = append(f.audit, e)
			f.mu.Unlock()
		},
	}
	mux := http.NewServeMux()
	f.srv = httptest.NewUnstartedServer(mux)
	f.srv.Start()
	t.Cleanup(f.srv.Close)
	o := Options{PublicURL: f.srv.URL, Store: st, Auth: f.svc, Now: f.clk.now}
	for _, m := range mod {
		m(&o)
	}
	f.as, err = New(o)
	if err != nil {
		t.Fatal(err)
	}
	f.svc.OAuthResource = f.as.Resource()
	f.as.Mount(mux)
	f.alice, err = f.svc.Accounts.Add(context.Background(), "alice", "Alice", alicePW, true)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) audited(action string) []auth.AuditEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []auth.AuditEvent
	for _, e := range f.audit {
		if e.Action == action {
			out = append(out, e)
		}
	}
	return out
}

// noFollow answers redirects instead of following them.
func noFollow(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// browser is a cookie-holding client that does not follow redirects.
func (f *fixture) browser(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar, CheckRedirect: noFollow}
}

func body(t *testing.T, r *http.Response) string {
	t.Helper()
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func decode(t *testing.T, r *http.Response) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body(t, r)), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// register registers a public client with one redirect URI and returns its id.
func (f *fixture) register(t *testing.T, redirect string) string {
	t.Helper()
	resp, err := http.Post(f.srv.URL+"/register", "application/json",
		strings.NewReader(`{"client_name":"test client","redirect_uris":["`+redirect+`"],"token_endpoint_auth_method":"none"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register: %d %s", resp.StatusCode, body(t, resp))
	}
	return decode(t, resp)["client_id"].(string)
}

// pkce is a fresh verifier and its S256 challenge.
func pkce(t *testing.T) (verifier, challenge string) {
	t.Helper()
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	return verifier, s256(verifier)
}

// authQuery is an authorization request's query.
func authQuery(clientID, redirect, challenge string, extra map[string]string) url.Values {
	q := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {"st-123"}}
	for k, v := range extra {
		q.Set(k, v)
	}
	return q
}

// signIn signs the browser in as alice through POST /login, as the sign-in page does.
func (f *fixture) signIn(t *testing.T, b *http.Client) {
	t.Helper()
	resp, err := b.PostForm(f.srv.URL+"/login", url.Values{"login": {"alice"}, "password": {alicePW}, "next": {"/authorize"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("sign-in: %d %s", resp.StatusCode, body(t, resp))
	}
	resp.Body.Close()
}

var csrfField = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

// consentForm opens the consent page for q and returns the form it would post, CSRF token included.
func (f *fixture) consentForm(t *testing.T, b *http.Client, q url.Values) url.Values {
	t.Helper()
	resp, err := b.Get(f.srv.URL + "/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	page := body(t, resp)
	m := csrfField.FindStringSubmatch(page)
	if resp.StatusCode != http.StatusOK || m == nil {
		t.Fatalf("consent page: %d, csrf field found %v: %s", resp.StatusCode, m != nil, page)
	}
	form := url.Values{}
	for k, v := range q {
		form[k] = v
	}
	form.Set("csrf", m[1])
	return form
}

// approve posts the consent form with role and returns the code the redirect carries.
func (f *fixture) approve(t *testing.T, b *http.Client, form url.Values, role string) string {
	t.Helper()
	form.Set("decision", "approve")
	form.Set("role", role)
	resp, err := b.PostForm(f.srv.URL+"/authorize", form)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("approve: %d %s", resp.StatusCode, body(t, resp))
	}
	resp.Body.Close()
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || loc.Query().Get("code") == "" || loc.Query().Get("state") != "st-123" {
		t.Fatalf("approve redirected to %q", resp.Header.Get("Location"))
	}
	return loc.Query().Get("code")
}

// authorize runs sign-in and consent for a new client and returns the client, the code and its
// verifier.
func (f *fixture) authorize(t *testing.T, role string, extra map[string]string) (clientID, code, verifier string) {
	t.Helper()
	clientID = f.register(t, callbackURI)
	verifier, challenge := pkce(t)
	b := f.browser(t)
	f.signIn(t, b)
	code = f.approve(t, b, f.consentForm(t, b, authQuery(clientID, callbackURI, challenge, extra)), role)
	return clientID, code, verifier
}

// tokenCall posts a /token form and returns the status and the decoded body.
func (f *fixture) tokenCall(t *testing.T, form url.Values) (int, map[string]any) {
	t.Helper()
	resp, err := http.PostForm(f.srv.URL+"/token", form)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, decode(t, resp)
}

func (f *fixture) exchange(t *testing.T, clientID, code, verifier string) (int, map[string]any) {
	t.Helper()
	return f.tokenCall(t, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier},
		"client_id": {clientID}, "redirect_uri": {callbackURI}})
}

func (f *fixture) refreshCall(t *testing.T, clientID, refresh string) (int, map[string]any) {
	t.Helper()
	return f.tokenCall(t, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}})
}

// resolves reports whether bearer resolves at the server's resource, and to what.
func (f *fixture) resolves(t *testing.T, bearer string) (domain.Caller, bool) {
	t.Helper()
	c, err := f.svc.ResolveCaller(context.Background(), "Bearer "+bearer)
	if err == nil {
		return c, true
	}
	if e, ok := domain.AsBatonDeckError(err); !ok || e.Code != domain.CodeUnauthenticated {
		t.Fatalf("resolve: %v, want nil or UNAUTHENTICATED", err)
	}
	return domain.Caller{}, false
}

func wantOAuthError(t *testing.T, status int, m map[string]any, wantStatus int, code string) {
	t.Helper()
	if status != wantStatus || m["error"] != code {
		t.Fatalf("got %d %v, want %d %s", status, m, wantStatus, code)
	}
}
