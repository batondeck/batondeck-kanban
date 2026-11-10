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

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// The auth service's events reach the audit log of the composed app, not only a test hook: a
// cross-site sign-in refused with no credential, a dynamic registration, a consent, a replayed
// authorization code that revokes its grant, and a signed-in POST without the CSRF token each
// leave one row, on each engine.
func TestServeAuditsTheAuthEvents(t *testing.T) {
	for _, eng := range []struct {
		name string
		open func(t *testing.T) store.Store
	}{
		{"sqlite", func(t *testing.T) store.Store { return storetest.SQLite(t) }},
		{"postgres", func(t *testing.T) store.Store { return storetest.Postgres(t) }},
	} {
		t.Run(eng.name, func(t *testing.T) {
			ctx := context.Background()
			st := eng.open(t)
			alice, err := (&auth.Accounts{Store: st, Params: auth.Argon2Params{Time: 1, Memory: 64, Threads: 1}}).Add(ctx, "alice", "Alice", "pw-alice", false)
			if err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewUnstartedServer(nil)
			public := "http://" + srv.Listener.Addr().String()
			a, err := compose(st, "x", public, log.New(io.Discard, "", 0), nil, service.MediaOptions{}, core.Retention{})
			if err != nil {
				t.Fatal(err)
			}
			defer a.core.Close(context.Background())
			defer a.bus.Close()
			srv.Config.Handler = a.handler("x")
			srv.Start()
			defer srv.Close()

			do := func(c *http.Client, method, u string, body url.Values, hdr map[string]string) *http.Response {
				t.Helper()
				req, _ := http.NewRequest(method, u, strings.NewReader(body.Encode()))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				for k, v := range hdr {
					req.Header.Set(k, v)
				}
				resp, err := c.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				return resp
			}
			jar, _ := cookiejar.New(nil)
			browser := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			same := map[string]string{"Sec-Fetch-Site": "same-origin"}

			// 1. Another site's sign-in POST, which carries no credential.
			resp := do(http.DefaultClient, http.MethodPost, public+"/api/login", url.Values{"login": {"alice"}, "password": {"pw-alice"}},
				map[string]string{"Sec-Fetch-Site": "cross-site"})
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("cross-site sign-in: %d", resp.StatusCode)
			}
			// 2. A dynamic registration.
			callback := "http://127.0.0.1:43999/cb"
			resp, err = http.Post(public+"/register", "application/json", strings.NewReader(`{"client_name":"probe","redirect_uris":["`+callback+`"]}`))
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
			// 3. Sign-in and consent.
			if resp = do(browser, http.MethodPost, public+"/api/login", url.Values{"login": {"alice"}, "password": {"pw-alice"}}, same); resp.StatusCode != http.StatusNoContent {
				t.Fatalf("sign-in: %d", resp.StatusCode)
			}
			resp.Body.Close()
			raw := make([]byte, 32)
			_, _ = rand.Read(raw)
			verifier := base64.RawURLEncoding.EncodeToString(raw)
			sum := sha256.Sum256([]byte(verifier))
			q := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {callback}, "state": {"s1"},
				"code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}, "resource": {public + "/mcp"}}
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
			resp = do(browser, http.MethodPost, public+"/authorize", form, same)
			resp.Body.Close()
			loc, _ := url.Parse(resp.Header.Get("Location"))
			code := loc.Query().Get("code")
			if resp.StatusCode != http.StatusSeeOther || code == "" {
				t.Fatalf("consent: %d %q", resp.StatusCode, resp.Header.Get("Location"))
			}
			// 4. The code exchanged twice: the replay revokes the grant.
			exchange := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier}, "client_id": {clientID},
				"redirect_uri": {callback}, "resource": {public + "/mcp"}}
			for i, want := range []int{http.StatusOK, http.StatusBadRequest} {
				resp = do(http.DefaultClient, http.MethodPost, public+"/token", exchange, nil)
				resp.Body.Close()
				if resp.StatusCode != want {
					t.Fatalf("exchange %d: %d, want %d", i+1, resp.StatusCode, want)
				}
			}
			// 5. A signed-in POST without the CSRF token.
			resp = do(browser, http.MethodPost, public+"/api/logout", nil, same)
			resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("a POST without the CSRF token: %d", resp.StatusCode)
			}

			page2, err := st.ListAuditLog(ctx, store.AuditFilter{}, "", 500)
			if err != nil {
				t.Fatal(err)
			}
			type row struct{ action, actor, subject, result, code string }
			got := map[row]int{}
			for _, r := range page2.Rows {
				got[row{r.Action, r.Actor, r.Subject, string(r.Result), r.ErrorCode}]++
			}
			for _, want := range []row{
				{"request.refused", auth.AnonymousActor, "path:/api/login", "error", "FORBIDDEN"},
				{"oauth.client.registered", auth.AnonymousActor, "oauth_client:" + clientID, "ok", ""},
				{"oauth.consent.granted", alice.ID, "oauth_client:" + clientID, "ok", ""},
				{"request.refused", alice.ID, "path:/api/logout", "error", "FORBIDDEN"},
			} {
				if got[want] != 1 {
					t.Errorf("%d rows %+v, want 1; the log: %+v", got[want], want, page2.Rows)
				}
			}
			revoked := 0
			for _, r := range page2.Rows {
				if r.Action == "oauth.grant.revoked" && r.Actor == alice.ID && r.ErrorCode == "code_reuse" && r.Result == store.AuditError {
					revoked++
				}
			}
			if revoked != 1 {
				t.Errorf("%d oauth.grant.revoked rows for the replayed code, want 1; the log: %+v", revoked, page2.Rows)
			}
		})
	}
}
