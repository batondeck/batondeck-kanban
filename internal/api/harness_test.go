package api

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
	"github.com/batondeck/batondeck-kanban/internal/store/memstore"
)

// harness is the BFF over an auth service, signed in as one user through the real POST /api/login:
// every request carries the session cookie and, unless told otherwise, the CSRF token and
// Sec-Fetch-Site: same-origin. newHarness builds it over memstore, newStackHarness over a real
// stack's store, the auth service and the services sharing one engine.
type harness struct {
	t      *testing.T
	auth   *auth.Service
	srv    *httptest.Server
	userID string
	cookie *http.Cookie
	csrf   string

	mu    sync.Mutex
	audit []auth.AuditEvent
}

const testPassword = "correct horse battery"

// newHarness mounts the BFF with build(st)'s options (Auth and Logger set here) over a fresh
// memstore.
func newHarness(t *testing.T, build func(st *memstore.Store) Options) *harness {
	t.Helper()
	st := memstore.New()
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	a := &auth.Service{Accounts: &auth.Accounts{Store: st, Params: fastArgon}, Tokens: &auth.TokenService{Store: st, Audit: st}, Sessions: st}
	return mountHarness(t, a, build(st))
}

// newStackHarness is the BFF over the stack's real services and bus, its auth service over the
// stack's store (R1.18: the engine servicetest selects).
func newStackHarness(t *testing.T, s *servicetest.Stack, o Options) *harness {
	t.Helper()
	return newStackHarnessAt(t, s, o, nil)
}

// newStackHarnessAt is newStackHarness with now as the auth service's clock.
func newStackHarnessAt(t *testing.T, s *servicetest.Stack, o Options, now func() time.Time) *harness {
	t.Helper()
	o.Services, o.Bus = s.Services, s.Bus
	a := &auth.Service{Accounts: &auth.Accounts{Store: s.Store, Params: fastArgon}, Tokens: &auth.TokenService{Store: s.Store, Audit: s.Store}, Sessions: s.Store, Now: now}
	return mountHarness(t, a, o)
}

var fastArgon = auth.Argon2Params{Time: 1, Memory: 64, Threads: 1}

// mountHarness records a's audit events, adds alice, mounts the BFF with o and signs her in.
func mountHarness(t *testing.T, a *auth.Service, o Options) *harness {
	t.Helper()
	h := &harness{t: t, auth: a}
	a.Audit = func(_ context.Context, e auth.AuditEvent) {
		h.mu.Lock()
		h.audit = append(h.audit, e)
		h.mu.Unlock()
	}
	u, err := h.auth.Accounts.Add(context.Background(), "alice", "Alice", testPassword, false)
	if err != nil {
		t.Fatal(err)
	}
	h.userID = u.ID
	o.Auth = h.auth
	if o.Users == nil {
		o.Users = h.auth.Accounts
	}
	if o.Tokens == nil {
		o.Tokens = h.auth.Tokens
	}
	if o.Logger == nil {
		o.Logger = log.New(io.Discard, "", 0)
	}
	mux := http.NewServeMux()
	Mount(mux, o)
	h.srv = httptest.NewServer(mux)
	t.Cleanup(h.srv.Close)
	h.login("alice", testPassword)
	return h
}

// as is a second user on the same server: login is added and signed in.
func (h *harness) as(login string) *harness {
	h.t.Helper()
	u, err := h.auth.Accounts.Add(context.Background(), login, login, testPassword, false)
	if err != nil {
		h.t.Fatal(err)
	}
	o := &harness{t: h.t, auth: h.auth, srv: h.srv, userID: u.ID}
	o.login(login, testPassword)
	return o
}

func (h *harness) login(login, password string) {
	h.t.Helper()
	form := url.Values{"login": {login}, "password": {password}}
	req, _ := http.NewRequest("POST", h.srv.URL+"/api/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		h.t.Fatalf("login: %d", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		switch c.Name {
		case auth.SessionCookie:
			h.cookie = c
		case auth.CSRFCookie:
			h.csrf = c.Value
		}
	}
	if h.cookie == nil || h.csrf == "" {
		h.t.Fatal("login set no session or CSRF cookie")
	}
}

// request is a request of the signed-in user (none when cookie is nil: a bearer caller); header entries override the defaults (an empty
// value deletes one).
func (h *harness) request(method, path, body string, header map[string]string) *http.Request {
	h.t.Helper()
	req, err := http.NewRequest(method, h.srv.URL+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	if h.cookie != nil {
		req.AddCookie(h.cookie)
	}
	req.Header.Set(auth.CSRFHeader, h.csrf)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range header {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	return req
}

// do sends the request and answers its status and body.
func (h *harness) do(method, path, body string, header map[string]string) (int, string) {
	h.t.Helper()
	resp, err := http.DefaultClient.Do(h.request(method, path, body, header))
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func (h *harness) audits() []auth.AuditEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]auth.AuditEvent(nil), h.audit...)
}
