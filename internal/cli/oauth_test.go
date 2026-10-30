package cli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
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

// With public_url set, serve mounts the OAuth server and /mcp takes its tokens: a bare request is
// 401 naming the RFC 9728 document, which names this server; dynamic registration, sign-in,
// consent and the code exchange give a token that initialises an MCP session, beside an API token.
func TestServeOAuthToAnMCPSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "bk.db"), "FULL")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := (&auth.Accounts{Store: st}).Add(ctx, "alice", "Alice", "pw-alice", false)
	if err != nil {
		t.Fatal(err)
	}
	apiToken, _, err := (&auth.TokenService{Store: st}).Create(ctx, user.ID, "", "test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewUnstartedServer(nil)
	public := "http://" + srv.Listener.Addr().String()
	a, err := compose(st, "x", public, log.New(io.Discard, "", 0), nil, service.MediaOptions{}, core.Retention{})
	if err != nil || a.as == nil {
		t.Fatalf("compose: %v %v", a, err)
	}
	defer a.core.Close(context.Background())
	defer a.bus.Close()
	srv.Config.Handler = a.handler("x")
	srv.Start()
	defer srv.Close()

	initialize := func(bearer string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	getJSON := func(u string) map[string]any {
		resp, err := http.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
			t.Fatalf("%s: %v", u, err)
		}
		return m
	}

	r := initialize("")
	want := `Bearer resource_metadata="` + public + `/.well-known/oauth-protected-resource"`
	if r.StatusCode != http.StatusUnauthorized || r.Header.Get("WWW-Authenticate") != want {
		t.Fatalf("bare /mcp: %d %q, want 401 %q", r.StatusCode, r.Header.Get("WWW-Authenticate"), want)
	}
	prm := getJSON(public + "/.well-known/oauth-protected-resource")
	if prm["resource"] != public+"/mcp" || len(prm["authorization_servers"].([]any)) != 1 || prm["authorization_servers"].([]any)[0] != public {
		t.Fatalf("RFC 9728 document: %v", prm)
	}
	asm := getJSON(public + "/.well-known/oauth-authorization-server")
	if asm["issuer"] != public {
		t.Fatalf("RFC 8414 document: %v", asm)
	}

	// Dynamic registration.
	callback := "http://127.0.0.1:43999/cb"
	resp, err := http.Post(asm["registration_endpoint"].(string), "application/json", strings.NewReader(`{"client_name":"probe","redirect_uris":["`+callback+`"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var reg map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&reg)
	resp.Body.Close()
	clientID, _ := reg["client_id"].(string)
	if resp.StatusCode != http.StatusCreated || clientID == "" {
		t.Fatalf("register: %d %v", resp.StatusCode, reg)
	}

	// Sign-in and consent in a browser that keeps cookies and does not follow redirects.
	jar, _ := cookiejar.New(nil)
	browser := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	verifier := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {callback}, "state": {"s1"},
		"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}, "resource": {prm["resource"].(string)}}
	authorizeURL := asm["authorization_endpoint"].(string) + "?" + q.Encode()
	resp, err = browser.PostForm(public+"/login", url.Values{"login": {"alice"}, "password": {"pw-alice"}, "next": {"/authorize?" + q.Encode()}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("sign-in: %d", resp.StatusCode)
	}
	resp, err = browser.Get(authorizeURL)
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
	code := loc.Query().Get("code")
	if resp.StatusCode != http.StatusSeeOther || code == "" || !strings.HasPrefix(loc.String(), callback) {
		t.Fatalf("consent: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}

	// The code exchange, then the token at /mcp.
	resp, err = http.PostForm(asm["token_endpoint"].(string), url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"code_verifier": {verifier}, "client_id": {clientID}, "redirect_uri": {callback}, "resource": {prm["resource"].(string)}})
	if err != nil {
		t.Fatal(err)
	}
	var tok map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&tok)
	resp.Body.Close()
	access, _ := tok["access_token"].(string)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(access, auth.OAuthAccessPrefix) {
		t.Fatalf("token: %d %v", resp.StatusCode, tok)
	}
	for name, bearer := range map[string]string{"oauth": access, "api token": apiToken} {
		if r := initialize(bearer); r.StatusCode != http.StatusOK || r.Header.Get("Mcp-Session-Id") == "" {
			t.Fatalf("%s: initialize %d, Mcp-Session-Id %q", name, r.StatusCode, r.Header.Get("Mcp-Session-Id"))
		}
	}
	if r := initialize(auth.OAuthAccessPrefix + "forged"); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("a forged OAuth token: %d", r.StatusCode)
	}
}

// Without public_url there is no OAuth server: /mcp's challenge names no metadata.
func TestServeWithoutPublicURLHasNoOAuth(t *testing.T) {
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "bk.db"), "FULL")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	a, err := compose(st, "x", "", log.New(io.Discard, "", 0), nil, service.MediaOptions{}, core.Retention{})
	if err != nil || a.as != nil {
		t.Fatalf("compose: %v %v", a, err)
	}
	defer a.core.Close(context.Background())
	srv := httptest.NewServer(a.handler("x"))
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/token", "application/x-www-form-urlencoded", strings.NewReader("grant_type=refresh_token"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusBadRequest {
		t.Fatalf("/token without public_url answered %d, as if mounted", resp.StatusCode)
	}
	if _, err := compose(st, "x", "https://bk.example/base", log.New(io.Discard, "", 0), nil, service.MediaOptions{}, core.Retention{}); err == nil || !strings.Contains(err.Error(), "public_url") {
		t.Fatalf("a public_url with a path: %v", err)
	}
}

// TestWellKnownIs404WithOAuthOff (P-302): with public_url unset there is no OAuth server, so the
// RFC 8414 and RFC 9728 discovery documents answer 404, not the SPA shell a client would try to
// parse as JSON; with it set they are the JSON documents (control). On each engine.
func TestWellKnownIs404WithOAuthOff(t *testing.T) {
	paths := []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"}
	for _, eng := range []struct {
		name string
		open func(t *testing.T) store.Store
	}{
		{"sqlite", func(t *testing.T) store.Store { return storetest.SQLite(t) }},
		{"postgres", func(t *testing.T) store.Store { return storetest.Postgres(t) }},
	} {
		for _, oauthOn := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/oauth=%t", eng.name, oauthOn), func(t *testing.T) {
				srv := httptest.NewUnstartedServer(nil)
				public := ""
				if oauthOn {
					public = "http://" + srv.Listener.Addr().String()
				}
				a, err := compose(eng.open(t), "x", public, log.New(io.Discard, "", 0), nil, service.MediaOptions{}, core.Retention{})
				if err != nil || (a.as != nil) != oauthOn {
					t.Fatalf("compose: %v, OAuth server %t", err, a != nil && a.as != nil)
				}
				defer a.core.Close(context.Background())
				defer a.bus.Close()
				srv.Config.Handler = a.handler("x")
				srv.Start()
				defer srv.Close()
				for _, p := range paths {
					resp, err := http.Get(srv.URL + p)
					if err != nil {
						t.Fatal(err)
					}
					var doc map[string]any
					jsonErr := json.NewDecoder(resp.Body).Decode(&doc)
					resp.Body.Close()
					ct := resp.Header.Get("Content-Type")
					if !oauthOn && (resp.StatusCode != http.StatusNotFound || strings.HasPrefix(ct, "text/html")) {
						t.Errorf("OAuth off, %s: %d %q, want 404 and not a page", p, resp.StatusCode, ct)
					}
					if oauthOn && (resp.StatusCode != http.StatusOK || !strings.HasPrefix(ct, "application/json") || jsonErr != nil ||
						(doc["issuer"] != public && doc["resource"] != public+"/mcp")) {
						t.Errorf("OAuth on, %s: %d %q %v %v, want the JSON document naming %s", p, resp.StatusCode, ct, jsonErr, doc, public)
					}
				}
			})
		}
	}
}
