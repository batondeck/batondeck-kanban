package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/service/servicefake"
)

// A user changes their own password from Settings (POST /api/me/password): the current password
// must be right and the new one at least auth.MinPasswordLen characters (400 VALIDATION, nothing
// changed, otherwise). A change ends every other session of the user, leaves this browser signed in
// on fresh cookies, and the old password no longer signs in, so whoever handed the account out
// keeps no working credential. A bearer cannot change it.
func TestChangeOwnPassword(t *testing.T) {
	h := newHarness(t, fakeServices(servicefake.New()))
	other := &harness{t: t, auth: h.auth, srv: h.srv, userID: h.userID}
	other.login("alice", testPassword)
	const fresh = "a-new-password-of-mine"

	for _, tc := range []struct{ why, body string }{
		{"a wrong current password", `{"currentPassword":"nope","newPassword":"` + fresh + `"}`},
		{"a short new password", `{"currentPassword":"` + testPassword + `","newPassword":"elevenchars"}`},
		{"an unknown field", `{"currentPassword":"` + testPassword + `","newPassword":"` + fresh + `","login":"bob"}`},
		{"trailing bytes", `{"currentPassword":"` + testPassword + `","newPassword":"` + fresh + `"}]`},
	} {
		if code, body := h.do("POST", "/api/me/password", tc.body, nil); code != http.StatusBadRequest || codeOf(t, body) != "VALIDATION" {
			t.Fatalf("%s: %d %s, want 400 VALIDATION", tc.why, code, body)
		}
	}
	if code := h.loginStatus("alice", testPassword); code != http.StatusNoContent {
		t.Fatalf("the old password after refused changes: %d", code)
	}
	if code, body := h.do("POST", "/api/me/password", `{"currentPassword":"`+testPassword+`","newPassword":"`+fresh+`"}`,
		map[string]string{"Sec-Fetch-Site": "cross-site"}); code != http.StatusForbidden {
		t.Fatalf("a cross-site change: %d %s", code, body)
	}

	resp, err := http.DefaultClient.Do(h.request("POST", "/api/me/password", `{"currentPassword":"`+testPassword+`","newPassword":"`+fresh+`"}`, nil))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		OtherSessionsEnded int `json:"otherSessionsEnded"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	resp.Body.Close()
	// alice's sessions: h's, other's, and the one loginStatus opened.
	if resp.StatusCode != http.StatusOK || out.OtherSessionsEnded != 2 {
		t.Fatalf("the change: %d %+v, want 200 and 2 other sessions ended", resp.StatusCode, out)
	}
	old := h.cookie
	for _, c := range resp.Cookies() {
		switch c.Name {
		case auth.SessionCookie:
			h.cookie = c
		case auth.CSRFCookie:
			h.csrf = c.Value
		}
	}
	if h.cookie == old || h.cookie.Value == old.Value {
		t.Fatal("the change set no new session cookie")
	}
	if code, _ := h.do("GET", "/api/session", "", nil); code != http.StatusOK {
		t.Fatalf("this browser on its new cookie: %d, want 200", code)
	}
	if code, _ := other.do("GET", "/api/session", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("alice's other session after the change: %d, want 401", code)
	}
	stale := &harness{t: t, auth: h.auth, srv: h.srv, cookie: old, csrf: "x"}
	if code, _ := stale.do("GET", "/api/session", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("this browser's old cookie after the change: %d, want 401", code)
	}
	if code := h.loginStatus("alice", testPassword); code != http.StatusUnauthorized {
		t.Fatalf("the old password after the change: %d, want 401", code)
	}
	if code := h.loginStatus("alice", fresh); code != http.StatusNoContent {
		t.Fatalf("the new password: %d, want 204", code)
	}

	tok, _, err := h.auth.Tokens.Create(t.Context(), h.userID, "alice", "cli", 0)
	if err != nil {
		t.Fatal(err)
	}
	bearer := &harness{t: t, auth: h.auth, srv: h.srv}
	code, body := bearer.do("POST", "/api/me/password", `{"currentPassword":"`+fresh+`","newPassword":"yet-another-password"}`,
		map[string]string{"Authorization": "Bearer " + tok, auth.CSRFHeader: ""})
	if code != http.StatusForbidden || !strings.Contains(body, "FORBIDDEN") {
		t.Fatalf("a bearer's change: %d %s, want 403 FORBIDDEN", code, body)
	}
}
