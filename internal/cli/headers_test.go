package cli

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/web"
)

// TestSecurityHeaders drives the composed handler: the SPA, the REST BFF's 401, the OAuth login page
// and an unknown /api path carry all four headers; /mcp, /hooks and /blobs carry nosniff and no CSP.
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
	a := mustCompose(t, st, public, log.New(io.Discard, "", 0))
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
		{"GET", "/login?next=/x", "text/html", web.AppCSP},
		{"GET", "/api/projects", "", web.AppCSP},
		{"GET", "/api/nope", "", web.AppCSP},
		{"GET", authorize, "text/html", consentCSP}, // the OAuth page keeps its stricter policy
		{"POST", "/mcp", "", ""},
		{"POST", "/hooks/I-none", "", ""},
		{"GET", "/blobs/x", "", ""},
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
		for k, want := range map[string]string{"X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer", "X-Frame-Options": "DENY"} {
			if got := h.Get(k); got != want {
				t.Errorf("%s %s (%d): %s %q, want %q", c.method, c.path, resp.StatusCode, k, got, want)
			}
		}
	}
}
