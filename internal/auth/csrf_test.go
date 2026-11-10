package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func (f *fixture) wantRefusalAudited(t *testing.T, before int, reason string) {
	t.Helper()
	a := f.audited()
	if len(a) != before+1 {
		t.Fatalf("%d audit rows, want %d", len(a), before+1)
	}
	e := a[len(a)-1]
	if e.Action != "request.refused" || e.Subject != "path:/api/x" || e.ErrorCode != "FORBIDDEN" || e.Data["reason"] != reason {
		t.Fatalf("audit row %+v, want reason %s", e, reason)
	}
}

// AC2: a session passes with Sec-Fetch-Site same-origin and is refused, audited, cross-site.
func TestRefusalCSRFCrossSite(t *testing.T) {
	f := newFixture(t)
	u := f.addUser(t, "alice", "pw")
	sess, csrf := f.session(t, "alice")
	t.Run("Control", func(t *testing.T) {
		w := f.post(req{session: sess, csrf: csrf, hdr: map[string]string{"Sec-Fetch-Site": "same-origin"}})
		if w.Code != http.StatusOK || w.Header().Get("X-Identity") != u.ID {
			t.Fatalf("same-origin: %d %q", w.Code, w.Header().Get("X-Identity"))
		}
	})
	t.Run("Refused", func(t *testing.T) {
		n := len(f.audited())
		w := f.post(req{session: sess, csrf: csrf, hdr: map[string]string{"Sec-Fetch-Site": "cross-site"}})
		if w.Code != http.StatusForbidden {
			t.Fatalf("cross-site: %d", w.Code)
		}
		f.wantRefusalAudited(t, n, "cross_site")
		if a := f.audited(); a[len(a)-1].Actor != u.ID {
			t.Fatalf("actor %q", a[len(a)-1].Actor)
		}
		// Another origin of the same site (a sibling subdomain, another port) is not this origin.
		n = len(f.audited())
		if w := f.post(req{session: sess, csrf: csrf, hdr: map[string]string{"Sec-Fetch-Site": "same-site"}}); w.Code != http.StatusForbidden {
			t.Fatalf("same-site: %d", w.Code)
		}
		f.wantRefusalAudited(t, n, "cross_site")
	})
}

// AC2: a bearer request with no Fetch Metadata passes: no browser, judged by the token alone.
func TestCSRFBearerWithoutFetchMetadataPasses(t *testing.T) {
	f := newFixture(t)
	u := f.addUser(t, "alice", "pw")
	tok, _, _ := f.svc.Tokens.Create(t.Context(), u.ID, u.Email, "cli", 0)
	if w := f.post(req{bearer: tok}); w.Code != http.StatusOK || w.Header().Get("X-Identity") != u.ID {
		t.Fatalf("bearer: %d", w.Code)
	}
	n := len(f.audited())
	if w := f.post(req{bearer: tok, hdr: map[string]string{"Sec-Fetch-Site": "cross-site"}}); w.Code != http.StatusForbidden {
		t.Fatalf("a bearer from a browser on another site: %d", w.Code)
	}
	f.wantRefusalAudited(t, n, "cross_site")
}

// Requirement 6: a browser-like request without Fetch Metadata is judged by Origin, and its
// session by the CSRF token.
func TestRefusalCSRFWithoutFetchMetadata(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "alice", "pw")
	sess, csrf := f.session(t, "alice")
	t.Run("Refused", func(t *testing.T) {
		n := len(f.audited())
		if w := f.post(req{session: sess, csrf: csrf, hdr: map[string]string{"Origin": "https://evil.test"}}); w.Code != http.StatusForbidden {
			t.Fatalf("a foreign Origin: %d", w.Code)
		}
		f.wantRefusalAudited(t, n, "cross_site")
		n++
		if w := f.post(req{session: sess, hdr: map[string]string{"Origin": "http://board.test"}}); w.Code != http.StatusForbidden {
			t.Fatalf("no CSRF token: %d", w.Code)
		}
		f.wantRefusalAudited(t, n, "csrf_token")
		n++
		if w := f.post(req{session: sess, csrf: csrf + "0"}); w.Code != http.StatusForbidden {
			t.Fatalf("a wrong CSRF token: %d", w.Code)
		}
		f.wantRefusalAudited(t, n, "csrf_token")
	})
	t.Run("Control", func(t *testing.T) {
		if w := f.post(req{session: sess, csrf: csrf, hdr: map[string]string{"Origin": "http://board.test"}}); w.Code != http.StatusOK {
			t.Fatalf("own Origin and the token: %d", w.Code)
		}
		if w := f.post(req{session: sess, csrf: csrf, hdr: map[string]string{"Origin": "null"}}); w.Code != http.StatusOK {
			t.Fatalf("Origin null and the token: %d", w.Code)
		}
	})
}

func TestCSRFSafeMethodsPass(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "alice", "pw")
	sess, _ := f.session(t, "alice")
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		r, _ := http.NewRequest(m, "http://board.test/api/x", nil)
		r.AddCookie(&http.Cookie{Name: SessionCookie, Value: sess})
		r.Header.Set("Sec-Fetch-Site", "cross-site")
		w := httptest.NewRecorder()
		f.protected().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d", m, w.Code)
		}
	}
}

// An HTML form cannot set a header, so a session's token may come in the `csrf` form field.
func TestCSRFFormFieldCarriesTheToken(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "alice", "pw")
	sess, csrf := f.session(t, "alice")
	post := func(body string) int {
		r := httptest.NewRequest(http.MethodPost, "http://board.test/api/x", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		r.AddCookie(&http.Cookie{Name: SessionCookie, Value: sess})
		w := httptest.NewRecorder()
		f.protected().ServeHTTP(w, r)
		return w.Code
	}
	if code := post("csrf=" + csrf + "&x=1"); code != http.StatusOK {
		t.Fatalf("the right token in the form: %d", code)
	}
	n := len(f.audited())
	if code := post("csrf=" + csrf + "0"); code != http.StatusForbidden {
		t.Fatalf("a wrong token in the form: %d", code)
	}
	f.wantRefusalAudited(t, n, "csrf_token")
}

// A refusal of a request with no credential (anyone's cross-site sign-in POST) is audited at most
// refusalRows a minute, so anyone who reaches the server cannot grow the audit log faster; a refusal
// of a signed-in request is audited whatever that budget holds.
func TestAnonymousRefusalsAreAuditedWithinABudget(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "alice", "pw")
	sess, csrf := f.session(t, "alice")
	cross := map[string]string{"Sec-Fetch-Site": "cross-site"}
	for i := 0; i < refusalRows+5; i++ {
		if resp := f.login(t, "alice", "pw", cross, false); resp.StatusCode != http.StatusForbidden {
			t.Fatalf("cross-site sign-in %d: %d", i, resp.StatusCode)
		}
	}
	if n := len(f.audited()); n != refusalRows {
		t.Fatalf("%d refusals audited of %d, want %d", n, refusalRows+5, refusalRows)
	}
	for _, e := range f.audited() {
		if e.Action != "request.refused" || e.Actor != "" || e.Subject != "path:/login" {
			t.Fatalf("audit row %+v", e)
		}
	}
	n := len(f.audited())
	if w := f.post(req{session: sess, csrf: csrf, hdr: cross}); w.Code != http.StatusForbidden {
		t.Fatalf("a signed-in cross-site POST: %d", w.Code)
	}
	f.wantRefusalAudited(t, n, "cross_site")
	f.clk.add(time.Minute)
	if resp := f.login(t, "alice", "pw", cross, false); resp.StatusCode != http.StatusForbidden || len(f.audited()) != n+2 {
		t.Fatalf("a minute later: %d, %d rows, want %d", resp.StatusCode, len(f.audited()), n+2)
	}
}

// A page of a sibling subdomain is same-site, so a signed-in browser's SameSite=Lax cookie rides
// its POSTs to the board, and each is refused with the victim as actor. Each actor's refusals are
// audited at most refusalRows a minute too (another actor's budget is their own), and the row names
// at most maxRefusedPath bytes of the path, as UTF-8, so such a page cannot fill the log with a
// victim's rows.
func TestEachActorsRefusalsAreAuditedWithinABudget(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "alice", "pw")
	f.addUser(t, "bob", "pw")
	alice, _ := f.session(t, "alice")
	bob, _ := f.session(t, "bob")
	site := map[string]string{"Sec-Fetch-Site": "same-site"}
	n := len(f.audited())
	for i := 0; i < refusalRows+5; i++ {
		if w := f.post(req{session: alice, hdr: site}); w.Code != http.StatusForbidden {
			t.Fatalf("a same-site POST %d: %d", i, w.Code)
		}
	}
	if got := len(f.audited()) - n; got != refusalRows {
		t.Fatalf("%d of alice's %d refusals audited, want %d", got, refusalRows+5, refusalRows)
	}
	n = len(f.audited())
	r := httptest.NewRequest(http.MethodPost, "http://board.test/api/x/"+strings.Repeat("%ff", 4000), nil)
	r.AddCookie(&http.Cookie{Name: SessionCookie, Value: bob})
	r.Header.Set("Sec-Fetch-Site", "same-site")
	w := httptest.NewRecorder()
	f.protected().ServeHTTP(w, r)
	a := f.audited()
	if w.Code != http.StatusForbidden || len(a) != n+1 {
		t.Fatalf("bob's refusal: %d, %d rows, want 403 and %d", w.Code, len(a), n+1)
	}
	if e := a[n]; len(e.Subject) > len("path:")+maxRefusedPath || !utf8.ValidString(e.Subject) || !strings.HasPrefix(e.Subject, "path:/api/x/") {
		t.Fatalf("a long path's row: %d bytes, UTF-8 %t, %.40q", len(e.Subject), utf8.ValidString(e.Subject), e.Subject)
	}
}

// `Origin: null` is what a page with referrer policy no-referrer sends on a cross-site form POST,
// and it is no evidence of where the request came from. A request with no credential (the
// sign-in routes) is then refused, so another site cannot sign a browser in where no Fetch
// Metadata is sent (plain http); a session request still has its CSRF token judge it, and a
// bearer request carries no ambient credential to forge.
func TestOriginNullIsCrossSiteWithoutACredential(t *testing.T) {
	f := newFixture(t)
	u := f.addUser(t, "alice", "pw")
	null := map[string]string{"Origin": "null"}
	n := len(f.audited())
	if resp := f.login(t, "alice", "pw", null, false); resp.StatusCode != http.StatusForbidden || cookie(resp, SessionCookie) != nil {
		t.Fatalf("a sign-in with Origin null: %d %v, want 403 and no cookie", resp.StatusCode, resp.Cookies())
	}
	if a := f.audited(); len(a) != n+1 || a[n].Data["reason"] != "cross_site" {
		t.Fatalf("audit %+v", a)
	}
	if resp := f.login(t, "alice", "pw", map[string]string{"Origin": "http://board.test"}, false); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("a sign-in with its own Origin: %d", resp.StatusCode)
	}
	sess, csrf := f.session(t, "alice")
	if w := f.post(req{session: sess, hdr: null}); w.Code != http.StatusForbidden {
		t.Fatalf("a session POST with Origin null and no token: %d", w.Code)
	}
	if w := f.post(req{session: sess, csrf: csrf, hdr: null}); w.Code != http.StatusOK {
		t.Fatalf("a session POST with Origin null and the token: %d", w.Code)
	}
	tok, _, _ := f.svc.Tokens.Create(t.Context(), u.ID, u.Email, "cli", 0)
	if w := f.post(req{bearer: tok, hdr: null}); w.Code != http.StatusOK {
		t.Fatalf("a bearer POST with Origin null: %d", w.Code)
	}
}
