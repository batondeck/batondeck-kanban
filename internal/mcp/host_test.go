package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/service/servicefake"
)

// initializeAs posts initialize to url (a loopback listener) with the Host header host, as tokenAda.
func initializeAs(t *testing.T, url, host string) *http.Response {
	return initializeWith(t, url, host, tokenAda)
}

// initializeWith is initializeAs with bearer; "" sends no Authorization header.
func initializeWith(t *testing.T, url, host, bearer string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(initBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// TestPublicURLHostAdmitted: behind a reverse proxy on the same machine, a request reaches the
// loopback listener with Host = public_url's host, and initialize opens a session.
func TestPublicURLHostAdmitted(t *testing.T) {
	h := bootHarness(t, servicefake.New().Services(), resolver(), func(o *Options) { o.PublicURL = "https://kanban.example.com" })
	for _, host := range []string{"kanban.example.com", "Kanban.Example.com:443", "localhost"} {
		resp := initializeAs(t, h.url, host)
		if resp.StatusCode != http.StatusOK || resp.Header.Get("Mcp-Session-Id") == "" {
			t.Errorf("Host %s: status %d, session %q; want 200 with a session id", host, resp.StatusCode, resp.Header.Get("Mcp-Session-Id"))
		}
	}
}

// TestForeignHostRefusedOnLoopback: a Host that is neither loopback nor public_url's is the
// DNS-rebinding shape, refused as 403 FORBIDDEN in the {code, message} body; with public_url unset
// its host is foreign too.
func TestForeignHostRefusedOnLoopback(t *testing.T) {
	for _, tc := range []struct{ publicURL, host string }{
		{"https://kanban.example.com", "evil.example"},
		{"https://kanban.example.com", "kanban.example.com.evil.example"},
		{"", "kanban.example.com"},
	} {
		h := bootHarness(t, servicefake.New().Services(), resolver(), func(o *Options) { o.PublicURL = tc.publicURL })
		resp := initializeAs(t, h.url, tc.host)
		var body struct{ Code, Message string }
		err := json.NewDecoder(resp.Body).Decode(&body)
		if resp.StatusCode != http.StatusForbidden || resp.Header.Get("Content-Type") != "application/json" || err != nil ||
			body.Code != "FORBIDDEN" || !strings.Contains(body.Message, tc.host) {
			t.Errorf("public_url %q, Host %s: status %d, type %q, body %+v (%v); want 403 application/json {FORBIDDEN, naming the host}",
				tc.publicURL, tc.host, resp.StatusCode, resp.Header.Get("Content-Type"), body, err)
		}
	}
}

// TestForeignHostRefusedBeforeTheBearer: the host check sits in front of the bearer, so a foreign
// Host with no bearer is 403, never the 401 that would send a rebinding page into the OAuth flow.
func TestForeignHostRefusedBeforeTheBearer(t *testing.T) {
	h := bootHarness(t, servicefake.New().Services(), resolver(), func(o *Options) { o.PublicURL = "https://kanban.example.com" })
	if resp := initializeWith(t, h.url, "evil.example", ""); resp.StatusCode != http.StatusForbidden {
		t.Errorf("Host evil.example, no bearer: status %d; want 403 (the host check runs before the bearer)", resp.StatusCode)
	}
	if resp := initializeWith(t, h.url, "kanban.example.com", ""); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("Host kanban.example.com, no bearer: status %d; want 401", resp.StatusCode)
	}
}
