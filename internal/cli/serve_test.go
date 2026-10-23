package cli

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/store"
)

// /mcp is mounted over the store's API tokens: a live token initialises a session, a revoked one
// and none are 401 with a Bearer challenge.
func TestServeMountsMCPOverTokens(t *testing.T) {
	path := migratedDB(t)
	withStdin(t, "pw\n")
	if code, _, errs := run("user", "add", "alice"); code != 0 {
		t.Fatalf("user add: %s", errs)
	}
	_, live, _ := run("token", "create", "--user", "alice")
	_, revoked, _ := run("token", "create", "--user", "alice")
	live, revoked = strings.TrimSpace(live), strings.TrimSpace(revoked)
	if code, _, errs := run("token", "revoke", revoked[:3+16]); code != 0 {
		t.Fatalf("revoke: %s", errs)
	}
	st, err := store.OpenSQLite(path, "FULL")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := httptest.NewServer(handler("x", mcpHandler(st, "x", log.New(io.Discard, "", 0))))
	defer srv.Close()

	post := func(bearer string) *http.Response {
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
	for name, bearer := range map[string]string{"none": "", "revoked": revoked} {
		if r := post(bearer); r.StatusCode != http.StatusUnauthorized || r.Header.Get("WWW-Authenticate") != "Bearer" {
			t.Errorf("%s: %d %q, want 401 Bearer", name, r.StatusCode, r.Header.Get("WWW-Authenticate"))
		}
	}
	if r := post(live); r.StatusCode != http.StatusOK || r.Header.Get("Mcp-Session-Id") == "" {
		t.Errorf("live: %d, Mcp-Session-Id %q", r.StatusCode, r.Header.Get("Mcp-Session-Id"))
	}
}
