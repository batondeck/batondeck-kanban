package cli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// oauthServer is the real composition with public_url set, over st, on a TCP listener.
func oauthServer(t *testing.T, st store.Store) (public string) {
	t.Helper()
	srv := httptest.NewUnstartedServer(nil)
	public = "http://" + srv.Listener.Addr().String()
	a, err := compose(st, "x", public, log.New(io.Discard, "", 0), nil, service.MediaOptions{}, core.Retention{})
	if err != nil || a.as == nil {
		t.Fatalf("compose: %v", err)
	}
	t.Cleanup(func() { a.core.Close(context.Background()) })
	t.Cleanup(a.bus.Close)
	srv.Config.Handler = a.handler("x")
	srv.Start()
	t.Cleanup(srv.Close)
	return public
}

// oauthGrant runs dynamic registration, sign-in, consent and the code exchange against public for
// login, and returns the client, the access token and the refresh token.
func oauthGrant(t *testing.T, public, login, password string) (clientID, access, refresh string) {
	t.Helper()
	callback := "http://127.0.0.1:43999/cb"
	resource := public + "/mcp"
	resp, err := http.Post(public+"/register", "application/json", strings.NewReader(`{"client_name":"wake","redirect_uris":["`+callback+`"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var reg map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&reg)
	resp.Body.Close()
	clientID, _ = reg["client_id"].(string)
	if resp.StatusCode != http.StatusCreated || clientID == "" {
		t.Fatalf("register: %d %v", resp.StatusCode, reg)
	}
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	verifier := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {callback}, "state": {"s"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}, "resource": {resource}}
	resp, err = browser.PostForm(public+"/login", url.Values{"login": {login}, "password": {password}, "next": {"/authorize?" + q.Encode()}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	resp, err = browser.Get(public + "/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := regexp.MustCompile(`name="csrf" value="([^"]+)"`).FindSubmatch(page)
	if m == nil {
		t.Fatalf("consent page has no CSRF field: %s", page)
	}
	form := url.Values{}
	for k, v := range q {
		form[k] = v
	}
	form.Set("csrf", string(m[1]))
	form.Set("decision", "approve")
	form.Set("role", "worker")
	resp, err = browser.PostForm(public+"/authorize", form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, _ := url.Parse(resp.Header.Get("Location"))
	tok := tokenCall(t, public, url.Values{"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")},
		"code_verifier": {verifier}, "client_id": {clientID}, "redirect_uri": {callback}, "resource": {resource}})
	access, _ = tok["access_token"].(string)
	refresh, _ = tok["refresh_token"].(string)
	if !strings.HasPrefix(access, auth.OAuthAccessPrefix) || refresh == "" {
		t.Fatalf("code exchange: %v", tok)
	}
	return clientID, access, refresh
}

func tokenCall(t *testing.T, public string, form url.Values) map[string]any {
	t.Helper()
	resp, err := http.PostForm(public+"/token", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	m["_status"] = resp.StatusCode
	return m
}

func statusOf(t *testing.T, method, u, bearer string) int {
	t.Helper()
	var body io.Reader
	if method == http.MethodPost {
		body = strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`)
	}
	req, _ := http.NewRequest(method, u, body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+bearer)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func oauthStore(t *testing.T) (*store.SQLite, string) {
	t.Helper()
	st := storetest.SQLite(t)
	user, err := (&auth.Accounts{Store: st}).Add(context.Background(), "alice", "Alice", "pw-alice", false)
	if err != nil {
		t.Fatal(err)
	}
	return st, user.ID
}

// One OAuth access token opens /mcp; a token issued for another server's resource (RFC 8707) is
// refused. GET /wake is gone (R0.25): 404 whatever the token.
func TestOAuthTokenOnMCP(t *testing.T) {
	st, _ := oauthStore(t)
	here, there := oauthServer(t, st), oauthServer(t, st)
	_, access, _ := oauthGrant(t, here, "alice", "pw-alice")
	if code := statusOf(t, http.MethodPost, here+"/mcp", access); code != http.StatusOK {
		t.Fatalf("/mcp with the token: %d", code)
	}
	if code := statusOf(t, http.MethodGet, here+"/wake", access); code != http.StatusNotFound {
		t.Fatalf("/wake with the token: %d, want 404", code)
	}
	if code := statusOf(t, http.MethodPost, there+"/mcp", access); code != http.StatusUnauthorized {
		t.Fatalf("/mcp at another resource with this token: %d, want 401", code)
	}
}
