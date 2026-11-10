package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/web"
)

// TestSecurityHeaders drives the composed handler: the SPA, the REST BFF's 401, the OAuth login page
// and an unknown /api path carry all four headers; the /mcp, /hooks and /blobs routes all but the CSP.
func TestSecurityHeaders(t *testing.T) {
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "bk.db"), "FULL")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(nil)
	public := "http://" + srv.Listener.Addr().String()
	env := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32))
	keys, err := core.OpenKeyring("", func(k string) (string, bool) { return env, k == core.MasterKeyEnv })
	if err != nil {
		t.Fatal(err)
	}
	logger := log.New(io.Discard, "", 0)
	media, err := mediaOptions(&core.Config{PublicURL: public, Blob: core.Blob{Dir: t.TempDir()}}, keys, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	a, err := compose(st, "x", public, logger, keys, media, core.Retention{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.core.Close(context.Background())
	defer a.bus.Close()
	srv.Config.Handler = a.handler("x")
	srv.Start()
	defer srv.Close()

	resp, err := http.Post(public+"/register", "application/json", strings.NewReader(`{"client_name":"probe","redirect_uris":["http://127.0.0.1:43999/cb"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var reg map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&reg)
	resp.Body.Close()
	clientID, _ := reg["client_id"].(string)
	if clientID == "" {
		t.Fatalf("register: %d %v", resp.StatusCode, reg)
	}
	authorize := "/authorize?" + url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {"http://127.0.0.1:43999/cb"},
		"state": {"s"}, "code_challenge": {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuIrjgaXg8M"}, "code_challenge_method": {"S256"}, "resource": {public + "/mcp"}}.Encode()
	consentCSP := "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'"

	for _, c := range []struct {
		method, path, ctype, csp string
	}{
		{"GET", "/", "text/html", web.AppCSP},
		{"GET", "/projects?next=/x", "text/html", web.AppCSP},
		{"GET", "/login?next=/x", "text/plain", web.AppCSP}, // not a client route: the catch-all's 404 (P-303)
		{"GET", "/api/projects", "", web.AppCSP},
		{"GET", "/api/nope", "", web.AppCSP},
		{"GET", authorize, "text/html", consentCSP}, // the OAuth page keeps its stricter policy
		{"POST", "/mcp", "text/plain", ""},
		{"POST", "/hooks/I-none", "application/json", ""},
		{"GET", "/blobs/x", "application/json", ""},
		{"PUT", "/blobs/x", "application/json", ""},
		// A path under an exempt route that no route answers is the catch-all's 404, which keeps the
		// policy (P-303: it is no longer the shell).
		{"GET", "/hooks/I-none", "text/plain", web.AppCSP},
		{"GET", "/blobs/x/y", "text/plain", web.AppCSP},
	} {
		req, _ := http.NewRequest(c.method, public+c.path, strings.NewReader("{}"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		h := resp.Header
		if !strings.HasPrefix(h.Get("Content-Type"), c.ctype) {
			t.Errorf("%s %s: Content-Type %q, want %q", c.method, c.path, h.Get("Content-Type"), c.ctype)
		}
		if got := h.Get("Content-Security-Policy"); got != c.csp {
			t.Errorf("%s %s (%d): CSP %q, want %q", c.method, c.path, resp.StatusCode, got, c.csp)
		}
		for k, want := range map[string]string{"X-Content-Type-Options": "nosniff", "Referrer-Policy": "same-origin", "X-Frame-Options": "DENY"} {
			if got := h.Get(k); got != want {
				t.Errorf("%s %s (%d): %s %q, want %q", c.method, c.path, resp.StatusCode, k, got, want)
			}
		}
	}
}
