package cli

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// serve with an https public_url issues Secure session cookies to a sign-in its TLS proxy forwarded
// as plain http with no X-Forwarded-Proto (nginx's bare proxy_pass), and accepts the browser's
// Origin of that URL.
func TestServeHTTPSPublicURLMakesTheCookiesSecure(t *testing.T) {
	st := storetest.SQLite(t)
	if _, err := (&auth.Accounts{Store: st, Params: auth.Argon2Params{Time: 1, Memory: 64, Threads: 1}}).Add(context.Background(), "alice", "", "pw-alice", false); err != nil {
		t.Fatal(err)
	}
	a, err := compose(st, "x", "https://board.example.com", log.New(io.Discard, "", 0), nil, service.MediaOptions{}, core.Retention{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.core.Close(context.Background())
	defer a.bus.Close()
	srv := httptest.NewServer(a.handler("x"))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/login", strings.NewReader(url.Values{"login": {"alice"}, "password": {"pw-alice"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://board.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || len(resp.Cookies()) != 2 {
		t.Fatalf("sign-in: %d %v", resp.StatusCode, resp.Cookies())
	}
	for _, c := range resp.Cookies() {
		if !c.Secure {
			t.Errorf("cookie %s is not Secure", c.Name)
		}
	}
}
