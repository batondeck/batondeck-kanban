package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/service/servicefake"
	"github.com/batondeck/batondeck-kanban/internal/store/memstore"
)

// asAdmin is a workspace admin on the same server, added and signed in.
func (h *harness) asAdmin(login string) *harness {
	h.t.Helper()
	u, err := h.auth.Accounts.Add(context.Background(), login, "", testPassword, true)
	if err != nil {
		h.t.Fatal(err)
	}
	o := &harness{t: h.t, auth: h.auth, srv: h.srv, userID: u.ID}
	o.login(login, testPassword)
	return o
}

// postLogin is the SPA's sign-in: the form to POST /api/login from this origin, without a session.
func (h *harness) postLogin(login, password string) (int, string) {
	h.t.Helper()
	form := url.Values{"login": {login}, "password": {password}}
	req, _ := http.NewRequest("POST", h.srv.URL+"/api/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

type errBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func codeOf(t *testing.T, body string) string {
	t.Helper()
	var e errBody
	if err := json.Unmarshal([]byte(body), &e); err != nil {
		t.Fatalf("not the error body: %q", body)
	}
	return e.Code
}

// Requirement 3: a member reaching either users route is 403 FORBIDDEN and adds nobody; the
// workspace admin (the control) lists the accounts without their hashes and adds a member whose
// generated password, shown once, signs them in.
func TestUsersRoutesAreTheWorkspaceAdmins(t *testing.T) {
	f := servicefake.New()
	h := newHarness(t, fakeServices(f))

	t.Run("RefusalForAMember", func(t *testing.T) {
		if code, body := h.do("GET", "/api/users", "", nil); code != http.StatusForbidden || codeOf(t, body) != "FORBIDDEN" {
			t.Fatalf("GET /api/users as a member: %d %s, want 403 FORBIDDEN", code, body)
		}
		if code, body := h.do("POST", "/api/users", `{"login":"mallory"}`, nil); code != http.StatusForbidden || codeOf(t, body) != "FORBIDDEN" {
			t.Fatalf("POST /api/users as a member: %d %s, want 403 FORBIDDEN", code, body)
		}
		if _, err := h.auth.Accounts.Lookup(context.Background(), "mallory"); err == nil {
			t.Fatal("a member's refused POST created the user")
		}
	})

	admin := h.asAdmin("root")
	t.Run("ControlForTheAdmin", func(t *testing.T) {
		code, body := admin.do("POST", "/api/users", `{"login":"Bob","displayName":"Bob B"}`, nil)
		var added struct {
			User     userResp `json:"user"`
			Password string   `json:"password"`
		}
		if err := json.Unmarshal([]byte(body), &added); err != nil || code != http.StatusOK {
			t.Fatalf("add bob: %d %s", code, body)
		}
		if added.User.Login != "bob" || added.User.DisplayName != "Bob B" || added.User.Role != "member" || added.User.Disabled || len(added.Password) != 24 {
			t.Fatalf("add bob answered %+v", added)
		}
		if code, body := h.postLogin("bob", added.Password); code != http.StatusNoContent {
			t.Fatalf("bob with the shown password: %d %s", code, body)
		}

		code, body = admin.do("GET", "/api/users", "", nil)
		var list struct {
			Users []userResp `json:"users"`
		}
		if err := json.Unmarshal([]byte(body), &list); err != nil || code != http.StatusOK {
			t.Fatalf("list: %d %s", code, body)
		}
		roles := map[string]string{}
		for _, u := range list.Users {
			roles[u.Login] = u.Role
		}
		if len(list.Users) != 3 || roles["alice"] != "member" || roles["root"] != "admin" || roles["bob"] != "member" {
			t.Fatalf("list: %s", body)
		}
		if strings.Contains(body, "argon2") || strings.Contains(strings.ToLower(body), "hash") || strings.Contains(body, added.Password) {
			t.Fatalf("the list carries a password or its hash: %s", body)
		}
	})

	t.Run("NoRoleAndNoDuplicate", func(t *testing.T) {
		if code, body := admin.do("POST", "/api/users", `{"login":"eve","role":"admin"}`, nil); code != http.StatusBadRequest || codeOf(t, body) != "VALIDATION" {
			t.Fatalf("a role in the body: %d %s, want 400 VALIDATION", code, body)
		}
		if code, body := admin.do("POST", "/api/users", `{"login":"bob"}`, nil); code != http.StatusBadRequest || codeOf(t, body) != "VALIDATION" {
			t.Fatalf("a taken login: %d %s, want 400 VALIDATION", code, body)
		}
	})
}

// Requirement 1 (P-198): POST /api/logout deletes the session row, so the old cookie no longer
// resolves even when replayed, and expires both cookies; another site cannot sign a browser out.
func TestLogoutEndsTheSession(t *testing.T) {
	h := newHarness(t, fakeServices(servicefake.New()))
	if code, body := h.do("POST", "/api/logout", "", map[string]string{"Sec-Fetch-Site": "cross-site"}); code != http.StatusForbidden {
		t.Fatalf("a cross-site logout: %d %s, want 403", code, body)
	}
	if code, body := h.do("POST", "/api/logout", "", map[string]string{auth.CSRFHeader: ""}); code != http.StatusForbidden {
		t.Fatalf("a logout without the CSRF token: %d %s, want 403", code, body)
	}
	if code, _ := h.do("GET", "/api/session", "", nil); code != http.StatusOK {
		t.Fatalf("a refused logout ended the session: %d", code)
	}

	resp, err := http.DefaultClient.Do(h.request("POST", "/api/logout", "", nil))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout: %d", resp.StatusCode)
	}
	cleared := map[string]bool{}
	for _, c := range resp.Cookies() {
		if c.MaxAge < 0 && c.Value == "" {
			cleared[c.Name] = true
		}
	}
	if !cleared[auth.SessionCookie] || !cleared[auth.CSRFCookie] {
		t.Fatalf("logout's cookies %v, want both expired", resp.Cookies())
	}
	// The old cookie, replayed as a copy of it would be, no longer resolves.
	if code, body := h.do("GET", "/api/session", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("the old cookie after logout: %d %s, want 401", code, body)
	}
}

// Requirement 3 at the DEFAULT account-cache window: a disabled user's open session still answers
// at 59 s (the bound, not instant revocation) and is 401 at 60 s; the login refuses at once, with
// the error body the SPA's form shows.
func TestDisabledUserSessionEndsWithinSixtySeconds(t *testing.T) {
	st := memstore.New()
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var now atomic.Int64
	now.Store(time.Now().UnixMilli())
	a := &auth.Service{Accounts: &auth.Accounts{Store: st, Params: fastArgon}, Tokens: &auth.TokenService{Store: st}, Sessions: st,
		Now: func() time.Time { return time.UnixMilli(now.Load()) }}
	h := mountHarness(t, a, Options{Services: servicefake.New().Services()})
	bob := h.as("bob")
	if code, _ := bob.do("GET", "/api/session", "", nil); code != http.StatusOK {
		t.Fatalf("bob before: %d", code)
	}
	if _, err := a.Accounts.Disable(context.Background(), "bob"); err != nil {
		t.Fatal(err)
	}
	if code, body := h.postLogin("bob", testPassword); code != http.StatusUnauthorized || codeOf(t, body) != "UNAUTHENTICATED" || !strings.Contains(body, auth.LoginRefused) {
		t.Fatalf("bob's login once disabled: %d %s, want 401 with the refusal text", code, body)
	}
	now.Add((59 * time.Second).Milliseconds())
	if code, _ := bob.do("GET", "/api/session", "", nil); code != http.StatusOK {
		t.Fatalf("bob at 59 s: %d, want 200 (the cache window)", code)
	}
	now.Add(time.Second.Milliseconds())
	if code, body := bob.do("GET", "/api/session", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("bob at 60 s: %d %s, want 401", code, body)
	}
}
