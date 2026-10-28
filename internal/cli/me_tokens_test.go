package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// AC2 (R2.5): a CLI token minted on the Settings page's route answers its plaintext once, the list
// never carries it, it authenticates an MCP initialize, and revoked on the page it is 401 on the next
// initialize; another user revoking it is 404 and revokes nothing; mint and revoke each write their
// audit row. On every engine, over the composition serve runs.
func TestSettingsCLITokenAuthenticatesMCPUntilRevoked(t *testing.T) {
	onJoined(t, func(t *testing.T, j *joined) {
		j.user("alice")
		j.user("bob")
		alice, bob := j.login("alice"), j.login("bob")

		code, body := j.api(alice, "POST", "/api/me/cli-tokens", `{"label":"laptop"}`)
		var minted struct {
			ID, Label, Token, Status string
			ShownOnce                bool
			LastUsedAt, RevokedAt    *string
		}
		if err := json.Unmarshal([]byte(body), &minted); err != nil || code != http.StatusOK {
			t.Fatalf("mint: %d %s", code, body)
		}
		if !strings.HasPrefix(minted.Token, auth.TokenPrefix+minted.ID+"_") || !minted.ShownOnce || minted.Status != "live" || minted.Label != "laptop" || minted.RevokedAt != nil {
			t.Fatalf("mint answered %s", body)
		}
		secret := minted.Token[len(auth.TokenPrefix+minted.ID+"_"):]
		if code, body := j.api(alice, "GET", "/api/me/cli-tokens", ""); code != http.StatusOK || !strings.Contains(body, `"id":"`+minted.ID+`"`) || strings.Contains(body, secret) {
			t.Fatalf("list: %d %s (it must name the token and never carry its secret)", code, body)
		}
		if code, body := j.api(bob, "GET", "/api/me/cli-tokens", ""); code != http.StatusOK || strings.Contains(body, minted.ID) || !strings.Contains(body, `"label":"join-bob"`) {
			t.Fatalf("bob's list: %d %q, want his own token and none of alice's", code, body)
		}

		if code := j.initialize(minted.Token); code != http.StatusOK {
			t.Fatalf("an MCP initialize with the minted token: %d, want 200", code)
		}
		if code, body := j.api(bob, "POST", "/api/me/cli-tokens/"+minted.ID+"/revoke", ""); code != http.StatusNotFound || !strings.Contains(body, "NOT_FOUND") {
			t.Fatalf("bob revoking alice's token: %d %s, want 404 NOT_FOUND", code, body)
		}
		if code := j.initialize(minted.Token); code != http.StatusOK {
			t.Fatalf("after bob's refused revoke: %d, want 200", code)
		}
		code, body = j.api(alice, "POST", "/api/me/cli-tokens/"+minted.ID+"/revoke", "")
		if code != http.StatusOK || !strings.Contains(body, `"revoked":true`) || !strings.Contains(body, `"status":"revoked"`) {
			t.Fatalf("revoke: %d %s", code, body)
		}
		if code := j.initialize(minted.Token); code != http.StatusUnauthorized {
			t.Fatalf("an MCP initialize with the revoked token: %d, want 401", code)
		}

		page, err := j.st.ListAuditLog(context.Background(), store.AuditFilter{Subject: j.userID("alice")}, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		var actions []string
		for _, r := range page.Rows {
			actions = append(actions, r.Action+"/"+string(r.Data))
		}
		want := []string{`cli_token.revoked/{"label":"laptop","tokenId":"` + minted.ID + `","via":"settings"}`, `cli_token.minted/{"label":"laptop","tokenId":"` + minted.ID + `","via":"settings"}`}
		if strings.Join(actions, " ") != strings.Join(want, " ") {
			t.Fatalf("alice's audit rows %v, want %v", actions, want)
		}
	})
}

// api is one BFF request with login's cookies and its CSRF token.
func (j *joined) api(cookies []*http.Cookie, method, path, body string) (int, string) {
	j.t.Helper()
	req, _ := http.NewRequest(method, j.url+path, strings.NewReader(body))
	for _, c := range cookies {
		req.AddCookie(c)
		if c.Name == auth.CSRFCookie {
			req.Header.Set(auth.CSRFHeader, c.Value)
		}
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		j.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// initialize is one MCP initialize with bearer, through go-sdk's client: 200 when it connects, else
// the HTTP status the server refused it with.
func (j *joined) initialize(bearer string) int {
	j.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status := http.StatusOK
	rt := statusRT{bearer: bearer, status: &status}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "settings-token", Version: "1"}, nil).Connect(ctx,
		&sdk.StreamableClientTransport{Endpoint: j.url + "/mcp", HTTPClient: &http.Client{Transport: rt}, MaxRetries: -1}, nil)
	if err != nil {
		return status
	}
	_ = cs.Close()
	return http.StatusOK
}

// statusRT presents bearer and keeps the first refusal's status.
type statusRT struct {
	bearer string
	status *int
}

func (s statusRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+s.bearer)
	resp, err := http.DefaultTransport.RoundTrip(r)
	if err == nil && resp.StatusCode >= 400 && *s.status == http.StatusOK {
		*s.status = resp.StatusCode
	}
	return resp, err
}

// userID is login's account id.
func (j *joined) userID(login string) string {
	j.t.Helper()
	u, err := (&auth.Accounts{Store: j.st}).Lookup(context.Background(), login)
	if err != nil {
		j.t.Fatal(err)
	}
	return u.ID
}
