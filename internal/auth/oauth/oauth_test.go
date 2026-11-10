package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/retention"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// AC5: the RFC 8414 document carries no jwks_uri, offers S256 only and the public-client method.
func TestMetadataHasNoJWKSAndAdvertisesS256AndNone(t *testing.T) {
	f := newFixture(t)
	resp, err := http.Get(f.srv.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, resp)
	if _, has := m["jwks_uri"]; has {
		t.Fatalf("metadata carries jwks_uri: %v", m)
	}
	if m["issuer"] != f.srv.URL || m["token_endpoint"] != f.srv.URL+"/token" || m["registration_endpoint"] != f.srv.URL+"/register" ||
		m["authorization_endpoint"] != f.srv.URL+"/authorize" {
		t.Fatalf("endpoints not built from public_url %s: %v", f.srv.URL, m)
	}
	if fmt.Sprint(m["code_challenge_methods_supported"]) != "[S256]" || fmt.Sprint(m["token_endpoint_auth_methods_supported"]) != "[none]" {
		t.Fatalf("methods: %v", m)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("metadata is not readable cross-origin")
	}
}

// The RFC 9728 document names this server at both well-known paths.
func TestProtectedResourceDocumentNamesThisServer(t *testing.T) {
	f := newFixture(t)
	if f.as.ResourceMetadataURL() != f.srv.URL+"/.well-known/oauth-protected-resource" {
		t.Fatalf("ResourceMetadataURL = %s", f.as.ResourceMetadataURL())
	}
	for _, p := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		resp, err := http.Get(f.srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		m := decode(t, resp)
		if m["resource"] != f.srv.URL+"/mcp" || fmt.Sprint(m["authorization_servers"]) != "["+f.srv.URL+"]" {
			t.Fatalf("%s: %v", p, m)
		}
	}
}

// Requirement 4: /register over the limit is 429 with Retry-After; under it, 201; the window's end
// lets the address register again.
func TestRefusalRegisterOverTheRateLimit(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.RegisterLimit, o.RegisterWindow = 3, time.Hour })
	post := func() *http.Response {
		resp, err := http.Post(f.srv.URL+"/register", "application/json", strings.NewReader(`{"redirect_uris":["`+callbackURI+`"]}`))
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	for i := range 3 {
		if r := post(); r.StatusCode != http.StatusCreated {
			t.Fatalf("registration %d under the limit: %d %s", i+1, r.StatusCode, body(t, r))
		}
	}
	r := post()
	if r.StatusCode != http.StatusTooManyRequests || r.Header.Get("Retry-After") != "3600" {
		t.Fatalf("over the limit: %d, Retry-After %q, want 429 and 3600", r.StatusCode, r.Header.Get("Retry-After"))
	}
	body(t, r)
	f.clk.add(time.Hour)
	if r := post(); r.StatusCode != http.StatusCreated {
		t.Fatalf("after the window: %d", r.StatusCode)
	}
}

// The limiter counts per address: another address is not held by the first's budget.
func TestRegisterLimitIsPerAddress(t *testing.T) {
	l := newLimiter(1, time.Hour)
	now := time.Unix(0, 0)
	if ok, _ := l.allow("10.0.0.1", now); !ok {
		t.Fatal("first call refused")
	}
	if ok, _ := l.allow("10.0.0.1", now); ok {
		t.Fatal("second call from the same address allowed")
	}
	if ok, _ := l.allow("10.0.0.2", now); !ok {
		t.Fatal("another address refused")
	}
}

func TestRegisterRefusesUnsafeRedirectURIs(t *testing.T) {
	f := newFixture(t)
	for _, u := range []string{"http://evil.example/cb", "https://app.example/cb#frag", "javascript:alert(1)", "/relative", "data:text/html,x",
		// userinfo: the consent page's "Returns to" would read as claude.ai while the code goes to evil.example
		"https://claude.ai@evil.example/api/mcp/auth_callback", "http://claude.ai:x@127.0.0.1:9/cb"} {
		resp, err := http.Post(f.srv.URL+"/register", "application/json", strings.NewReader(`{"redirect_uris":["`+u+`"]}`))
		if err != nil {
			t.Fatal(err)
		}
		if m := decode(t, resp); resp.StatusCode != http.StatusBadRequest || m["error"] != "invalid_redirect_uri" {
			t.Fatalf("%s: %d %v, want 400 invalid_redirect_uri", u, resp.StatusCode, m)
		}
	}
	for _, u := range []string{"https://claude.ai/api/mcp/auth_callback", "http://localhost:6274/oauth/callback", "http://[::1]:9/cb", "cursor://anysphere.cursor-retrieval/oauth/callback"} {
		f.register(t, u)
	}
	resp, err := http.Post(f.srv.URL+"/register", "application/json", strings.NewReader(`{"redirect_uris":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("no redirect URI: %d", resp.StatusCode)
	}
	body(t, resp)
}

// A registration no authorization used within 24 h is deleted at the next registration; one that
// was used is kept.
func TestRegisterCollectsUnusedRegistrations(t *testing.T) {
	f := newFixture(t)
	unused := f.register(t, callbackURI)
	used, code, verifier := f.authorize(t, "worker", nil)
	if st, m := f.exchange(t, used, code, verifier); st != http.StatusOK {
		t.Fatalf("exchange: %d %v", st, m)
	}
	f.clk.add(UnusedClientTTL - time.Minute)
	f.register(t, callbackURI)
	if _, err := f.st.GetOAuthClient(context.Background(), unused); err != nil {
		t.Fatalf("collected before 24 h: %v", err)
	}
	f.clk.add(2 * time.Minute)
	f.register(t, callbackURI)
	if _, err := f.st.GetOAuthClient(context.Background(), unused); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an unused registration after 24 h: %v, want deleted", err)
	}
	if _, err := f.st.GetOAuthClient(context.Background(), used); err != nil {
		t.Fatalf("a used registration was deleted: %v", err)
	}
}

// Requirement 4: a redirect_uri the client did not register is refused directly (no redirect to
// it), at /authorize and at /token; the registered one is served (the control).
func TestRefusalRedirectURIMismatch(t *testing.T) {
	f := newFixture(t)
	clientID := f.register(t, callbackURI)
	_, challenge := pkce(t)
	b := f.browser(t)
	f.signIn(t, b)
	for _, wrong := range []string{"http://127.0.0.1:43111/oauth/callback/x", "http://127.0.0.1:43112/oauth/callback", "https://evil.example/cb"} {
		resp, err := b.Get(f.srv.URL + "/authorize?" + authQuery(clientID, wrong, challenge, nil).Encode())
		if err != nil {
			t.Fatal(err)
		}
		m := decode(t, resp)
		if resp.StatusCode != http.StatusBadRequest || resp.Header.Get("Location") != "" || m["error"] != "invalid_request" {
			t.Fatalf("%s: %d Location %q %v, want a direct 400", wrong, resp.StatusCode, resp.Header.Get("Location"), m)
		}
	}
	// The consent POST re-checks it: a page tampered to another URI is refused the same way.
	form := f.consentForm(t, b, authQuery(clientID, callbackURI, challenge, nil))
	form.Set("redirect_uri", "https://evil.example/cb")
	form.Set("decision", "approve")
	form.Set("role", "worker")
	resp, err := b.PostForm(f.srv.URL+"/authorize", form)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest || resp.Header.Get("Location") != "" {
		t.Fatalf("tampered consent: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	body(t, resp)
	// The control, and the same check at /token.
	verifier, challenge := pkce(t)
	code := f.approve(t, b, f.consentForm(t, b, authQuery(clientID, callbackURI, challenge, nil)), "worker")
	st, m := f.tokenCall(t, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier},
		"client_id": {clientID}, "redirect_uri": {"https://evil.example/cb"}})
	wantOAuthError(t, st, m, http.StatusBadRequest, "invalid_grant")
	clientID, code, verifier = f.authorize(t, "worker", nil)
	if st, m := f.exchange(t, clientID, code, verifier); st != http.StatusOK {
		t.Fatalf("control: %d %v", st, m)
	}
}

// Requirement 4: a code exchanged twice is refused, and the tokens its first exchange issued stop
// working (RFC 6749 §4.1.2); the first exchange is the control.
func TestRefusalCodeExchangedTwice(t *testing.T) {
	f := newFixture(t)
	clientID, code, verifier := f.authorize(t, "worker", nil)
	st, first := f.exchange(t, clientID, code, verifier)
	if st != http.StatusOK || first["token_type"] != "Bearer" || first["scope"] != "worker" {
		t.Fatalf("first exchange: %d %v", st, first)
	}
	access := first["access_token"].(string)
	if _, ok := f.resolves(t, access); !ok {
		t.Fatal("the first exchange's access token does not resolve")
	}
	st, again := f.exchange(t, clientID, code, verifier)
	wantOAuthError(t, st, again, http.StatusBadRequest, "invalid_grant")
	if _, ok := f.resolves(t, access); ok {
		t.Fatal("the access token outlived the code's reuse")
	}
	if st, m := f.refreshCall(t, clientID, first["refresh_token"].(string)); st != http.StatusBadRequest || m["error"] != "invalid_grant" {
		t.Fatalf("the refresh token outlived the code's reuse: %d %v", st, m)
	}
	if n := len(f.audited("oauth.grant.revoked")); n != 1 {
		t.Fatalf("%d oauth.grant.revoked audit events, want 1", n)
	}
}

// Of two concurrent exchanges of one code exactly one gets tokens.
func TestConcurrentExchangesHaveOneWinner(t *testing.T) {
	f := newFixture(t)
	clientID, code, verifier := f.authorize(t, "worker", nil)
	var wg sync.WaitGroup
	statuses := make([]int, 5)
	for i := range statuses {
		wg.Go(func() { statuses[i], _ = f.exchange(t, clientID, code, verifier) })
	}
	wg.Wait()
	ok := 0
	for _, s := range statuses {
		if s == http.StatusOK {
			ok++
		}
	}
	if ok != 1 {
		t.Fatalf("statuses %v: %d got tokens, want exactly 1", statuses, ok)
	}
}

// Requirement 4: a missing or wrong PKCE verifier is refused, and a wrong one spends the code; the
// right verifier on a fresh code is the control.
func TestRefusalPKCEVerifier(t *testing.T) {
	f := newFixture(t)
	clientID, code, verifier := f.authorize(t, "worker", nil)
	st, m := f.tokenCall(t, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID}, "redirect_uri": {callbackURI}})
	wantOAuthError(t, st, m, http.StatusBadRequest, "invalid_request")
	wrong, _ := pkce(t)
	st, m = f.exchange(t, clientID, code, wrong)
	wantOAuthError(t, st, m, http.StatusBadRequest, "invalid_grant")
	if m["error_description"] != "PKCE verification failed" {
		t.Fatalf("wrong verifier: %v", m)
	}
	st, m = f.exchange(t, clientID, code, verifier)
	wantOAuthError(t, st, m, http.StatusBadRequest, "invalid_grant") // spent by the wrong attempt
	clientID, code, verifier = f.authorize(t, "worker", nil)
	if st, m := f.exchange(t, clientID, code, verifier); st != http.StatusOK {
		t.Fatalf("control: %d %v", st, m)
	}
}

// PKCE S256 is required at /authorize: no challenge, or method plain, goes back as invalid_request.
func TestAuthorizeRequiresS256(t *testing.T) {
	f := newFixture(t)
	clientID := f.register(t, callbackURI)
	b := f.browser(t)
	f.signIn(t, b)
	_, challenge := pkce(t)
	for name, q := range map[string]url.Values{
		"no challenge": authQuery(clientID, callbackURI, "", nil),
		"plain":        authQuery(clientID, callbackURI, challenge, map[string]string{"code_challenge_method": "plain"}),
	} {
		resp, err := b.Get(f.srv.URL + "/authorize?" + q.Encode())
		if err != nil {
			t.Fatal(err)
		}
		body(t, resp)
		loc, _ := url.Parse(resp.Header.Get("Location"))
		if resp.StatusCode != http.StatusSeeOther || loc.Query().Get("error") != "invalid_request" || loc.Query().Get("state") != "st-123" {
			t.Fatalf("%s: %d %q", name, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
}

// AC4 and requirement 4: after a rotation, the old refresh token is invalid_grant and its family is
// revoked, so the new refresh token and the access tokens stop too; a fresh sign-in works.
func TestRefusalRefreshTokenReusedRevokesTheFamily(t *testing.T) {
	f := newFixture(t)
	clientID, code, verifier := f.authorize(t, "master", nil)
	_, first := f.exchange(t, clientID, code, verifier)
	st, second := f.refreshCall(t, clientID, first["refresh_token"].(string))
	if st != http.StatusOK || second["refresh_token"] == first["refresh_token"] || second["scope"] != "master" {
		t.Fatalf("rotation: %d %v", st, second)
	}
	if _, ok := f.resolves(t, second["access_token"].(string)); !ok {
		t.Fatal("the rotated access token does not resolve")
	}
	st, m := f.refreshCall(t, clientID, first["refresh_token"].(string))
	wantOAuthError(t, st, m, http.StatusBadRequest, "invalid_grant")
	st, m = f.refreshCall(t, clientID, second["refresh_token"].(string))
	wantOAuthError(t, st, m, http.StatusBadRequest, "invalid_grant")
	for _, a := range []any{first["access_token"], second["access_token"]} {
		if _, ok := f.resolves(t, a.(string)); ok {
			t.Fatal("an access token of the revoked family still resolves")
		}
	}
	fam, err := f.st.GetOAuthFamily(context.Background(), familyOf(auth.HashOAuthValue(code)))
	if err != nil || fam.RevokedReason != "refresh_reuse" {
		t.Fatalf("family: %+v %v, want revoked for refresh_reuse", fam, err)
	}
	clientID, code, verifier = f.authorize(t, "master", nil)
	st, fresh := f.exchange(t, clientID, code, verifier)
	if _, ok := f.resolves(t, fresh["access_token"].(string)); st != http.StatusOK || !ok {
		t.Fatalf("a fresh sign-in after the revocation: %d %v", st, fresh)
	}
}

// P-125: the retention sweep keeps a lapsed OAuth row for LapsedGrace, so the reuse rules still
// hold inside it. A rotated refresh token presented again after its expiry, and after a sweep, still
// revokes its family; so does an authorization code exchanged again after its expiry and a sweep.
func TestReuseInsideTheSweepsGraceStillRevokesTheFamily(t *testing.T) {
	const day = 24 * time.Hour
	f := newFixture(t)
	ctx := context.Background()
	sw := &retention.Sweeper{Store: f.st, Now: f.clk.now, Report: func(step string, err error) { t.Errorf("%s: %v", step, err) }}

	clientID, code, verifier := f.authorize(t, "master", nil)
	_, first := f.exchange(t, clientID, code, verifier)
	if st, second := f.refreshCall(t, clientID, first["refresh_token"].(string)); st != http.StatusOK {
		t.Fatalf("rotation: %d %v", st, second)
	}
	// Past the rotated token's expiry by six days, inside the grace: the sweep takes the code and
	// the two access tokens, lapsed by more than the grace, and keeps both refresh tokens.
	f.clk.add(DefaultRefreshTTL + retention.LapsedGrace - day)
	if got := sw.Sweep(ctx); got.OAuthCodes != 1 || got.OAuthTokens != 2 {
		t.Fatalf("the first sweep: %+v, want the code and the two access tokens", got)
	}
	st, m := f.refreshCall(t, clientID, first["refresh_token"].(string))
	wantOAuthError(t, st, m, http.StatusBadRequest, "invalid_grant")
	fam, err := f.st.GetOAuthFamily(ctx, familyOf(auth.HashOAuthValue(code)))
	if err != nil || fam.RevokedReason != "refresh_reuse" {
		t.Fatalf("family: %+v %v, want revoked for refresh_reuse after the sweep", fam, err)
	}

	clientID, code, verifier = f.authorize(t, "master", nil)
	if st, m := f.exchange(t, clientID, code, verifier); st != http.StatusOK {
		t.Fatalf("the second grant: %d %v", st, m)
	}
	// Past the second code's expiry by six days, inside the grace: the sweep takes the first grant's
	// refresh tokens, now lapsed by more than the grace, and keeps the code.
	f.clk.add(retention.LapsedGrace - day)
	if got := sw.Sweep(ctx); got.OAuthCodes != 0 || got.OAuthTokens != 2 {
		t.Fatalf("the second sweep: %+v, want the first grant's two refresh tokens only", got)
	}
	st, m = f.exchange(t, clientID, code, verifier)
	wantOAuthError(t, st, m, http.StatusBadRequest, "invalid_grant")
	fam, err = f.st.GetOAuthFamily(ctx, familyOf(auth.HashOAuthValue(code)))
	if err != nil || fam.RevokedReason != "code_reuse" {
		t.Fatalf("family: %+v %v, want revoked for code_reuse after the sweep", fam, err)
	}
}

// A refresh token is bound to its client: another client_id is refused without spending it.
func TestRefreshBoundToItsClient(t *testing.T) {
	f := newFixture(t)
	clientID, code, verifier := f.authorize(t, "worker", nil)
	_, first := f.exchange(t, clientID, code, verifier)
	other := f.register(t, callbackURI)
	st, m := f.refreshCall(t, other, first["refresh_token"].(string))
	wantOAuthError(t, st, m, http.StatusBadRequest, "invalid_grant")
	if st, m := f.refreshCall(t, clientID, first["refresh_token"].(string)); st != http.StatusOK {
		t.Fatalf("the owner's refresh after a stranger's attempt: %d %v", st, m)
	}
}

// A refresh may ask for a narrower scope, never a wider one.
func TestRefreshScopeNarrowsOnly(t *testing.T) {
	f := newFixture(t)
	clientID, code, verifier := f.authorize(t, "master", nil)
	_, first := f.exchange(t, clientID, code, verifier)
	st, m := f.tokenCall(t, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {first["refresh_token"].(string)},
		"client_id": {clientID}, "scope": {"admin"}})
	wantOAuthError(t, st, m, http.StatusBadRequest, "invalid_scope")
	st, m = f.tokenCall(t, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {first["refresh_token"].(string)},
		"client_id": {clientID}, "scope": {"viewer"}})
	if st != http.StatusOK || m["scope"] != "viewer" {
		t.Fatalf("narrowing: %d %v", st, m)
	}
	if c, ok := f.resolves(t, m["access_token"].(string)); !ok || c.Role != "viewer" {
		t.Fatalf("the narrowed token: %+v %v", c, ok)
	}
}

// Requirement 4: a token issued for one resource is refused at another (RFC 8707); at its own it
// resolves (the control). A resource this server does not protect is never issued for.
func TestRefusalTokenAtTheWrongResource(t *testing.T) {
	var a, b string
	f := newFixture(t, func(o *Options) {
		a, b = o.PublicURL+"/mcp", o.PublicURL+"/other"
		o.Resources = []string{a, b}
	})
	clientID, code, verifier := f.authorize(t, "worker", map[string]string{"resource": b})
	st, forB := f.exchange(t, clientID, code, verifier)
	if st != http.StatusOK {
		t.Fatalf("exchange: %d %v", st, forB)
	}
	if _, ok := f.resolves(t, forB["access_token"].(string)); ok {
		t.Fatalf("a token for %s resolved at %s", b, a)
	}
	clientID, code, verifier = f.authorize(t, "worker", map[string]string{"resource": a + "/"})
	_, forA := f.exchange(t, clientID, code, verifier)
	if _, ok := f.resolves(t, forA["access_token"].(string)); !ok {
		t.Fatalf("a token for %s did not resolve there", a)
	}
	// A foreign resource at /authorize is sent back as invalid_target.
	clientID = f.register(t, callbackURI)
	_, challenge := pkce(t)
	br := f.browser(t)
	f.signIn(t, br)
	resp, err := br.Get(f.srv.URL + "/authorize?" + authQuery(clientID, callbackURI, challenge, map[string]string{"resource": "https://elsewhere.example/mcp"}).Encode())
	if err != nil {
		t.Fatal(err)
	}
	body(t, resp)
	if loc, _ := url.Parse(resp.Header.Get("Location")); loc == nil || loc.Query().Get("error") != "invalid_target" {
		t.Fatalf("foreign resource: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	// And /token refuses a resource other than the code's.
	clientID, code, verifier = f.authorize(t, "worker", map[string]string{"resource": a})
	st, m := f.tokenCall(t, url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {verifier},
		"client_id": {clientID}, "redirect_uri": {callbackURI}, "resource": {b}})
	wantOAuthError(t, st, m, http.StatusBadRequest, "invalid_target")
}

// The consent POST needs the session's CSRF token, and a page of another site is refused; the
// token from the page is the control.
func TestRefusalConsentWithoutCSRF(t *testing.T) {
	f := newFixture(t)
	clientID := f.register(t, callbackURI)
	_, challenge := pkce(t)
	b := f.browser(t)
	f.signIn(t, b)
	form := f.consentForm(t, b, authQuery(clientID, callbackURI, challenge, nil))
	form.Set("decision", "approve")
	form.Set("role", "worker")
	good := form.Get("csrf")
	for name, mutate := range map[string]func(url.Values, http.Header){
		"no token":    func(v url.Values, _ http.Header) { v.Del("csrf") },
		"wrong token": func(v url.Values, _ http.Header) { v.Set("csrf", "0000") },
		"cross-site":  func(_ url.Values, h http.Header) { h.Set("Sec-Fetch-Site", "cross-site") },
	} {
		v := url.Values{}
		for k, vs := range form {
			v[k] = slices.Clone(vs)
		}
		h := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}
		mutate(v, h)
		req, _ := http.NewRequest(http.MethodPost, f.srv.URL+"/authorize", strings.NewReader(v.Encode()))
		req.Header = h
		resp, err := b.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body(t, resp)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: %d, want 403", name, resp.StatusCode)
		}
	}
	if n := len(f.audited("request.refused")); n != 3 {
		t.Fatalf("%d request.refused audit events, want 3", n)
	}
	form.Set("csrf", good)
	f.approve(t, b, form, "worker")
	// Without a session the consent POST is 401, whatever the form says.
	resp, err := http.PostForm(f.srv.URL+"/authorize", form)
	if err != nil {
		t.Fatal(err)
	}
	body(t, resp)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no session: %d", resp.StatusCode)
	}
}

func TestConsentDeniedSendsAccessDenied(t *testing.T) {
	f := newFixture(t)
	clientID := f.register(t, callbackURI)
	_, challenge := pkce(t)
	b := f.browser(t)
	f.signIn(t, b)
	form := f.consentForm(t, b, authQuery(clientID, callbackURI, challenge, nil))
	form.Set("decision", "deny")
	resp, err := b.PostForm(f.srv.URL+"/authorize", form)
	if err != nil {
		t.Fatal(err)
	}
	body(t, resp)
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if resp.StatusCode != http.StatusSeeOther || loc.Query().Get("error") != "access_denied" || loc.Query().Get("state") != "st-123" || loc.Query().Get("code") != "" {
		t.Fatalf("deny: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

// Without a session /authorize is the sign-in page, which posts to /login and comes back; a wrong
// password comes back flagged.
func TestAuthorizeSignsInFirst(t *testing.T) {
	f := newFixture(t)
	clientID := f.register(t, callbackURI)
	_, challenge := pkce(t)
	b := f.browser(t)
	q := authQuery(clientID, callbackURI, challenge, nil)
	resp, err := b.Get(f.srv.URL + "/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	page := body(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, `action="/login"`) || !strings.Contains(page, "test client") ||
		resp.Header.Get("X-Frame-Options") != "DENY" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("sign-in page: %d %v %s", resp.StatusCode, resp.Header, page)
	}
	next := "/authorize?" + q.Encode()
	resp, err = b.PostForm(f.srv.URL+"/login", url.Values{"login": {"alice"}, "password": {"wrong"}, "next": {next}})
	if err != nil {
		t.Fatal(err)
	}
	body(t, resp)
	if loc, _ := url.Parse(resp.Header.Get("Location")); resp.StatusCode != http.StatusSeeOther || loc.Query().Get("login") != "failed" {
		t.Fatalf("wrong password: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, err = b.PostForm(f.srv.URL+"/login", url.Values{"login": {"alice"}, "password": {alicePW}, "next": {next}})
	if err != nil {
		t.Fatal(err)
	}
	body(t, resp)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != next {
		t.Fatalf("sign-in: %d %q, want 303 to %s", resp.StatusCode, resp.Header.Get("Location"), next)
	}
	f.consentForm(t, b, q)
}

// The role granted at consent is the token's role ceiling; only the uncapped grant keeps the
// account's workspace-admin flag.
func TestConsentedRoleIsTheCallersCap(t *testing.T) {
	f := newFixture(t)
	for role, admin := range map[string]bool{"viewer": false, "master": false, "admin": true} {
		clientID, code, verifier := f.authorize(t, role, nil)
		st, m := f.exchange(t, clientID, code, verifier)
		c, ok := f.resolves(t, m["access_token"].(string))
		if st != http.StatusOK || !ok || c.Role != role || c.WorkspaceAdmin != admin || c.Identity != f.alice.ID || m["scope"] != role {
			t.Fatalf("%s: %d %+v %v", role, st, c, m)
		}
	}
	// A role the page did not offer is refused: scope=viewer offers viewer alone.
	clientID := f.register(t, callbackURI)
	_, challenge := pkce(t)
	b := f.browser(t)
	f.signIn(t, b)
	form := f.consentForm(t, b, authQuery(clientID, callbackURI, challenge, map[string]string{"scope": "viewer"}))
	form.Set("decision", "approve")
	form.Set("role", "admin")
	resp, err := b.PostForm(f.srv.URL+"/authorize", form)
	if err != nil {
		t.Fatal(err)
	}
	body(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("a role above the request: %d, want 400", resp.StatusCode)
	}
}

func TestOfferedRoles(t *testing.T) {
	for scope, want := range map[string]string{
		"":                                  "[viewer worker master member admin] worker",
		"viewer worker master member admin": "[viewer worker master member admin] worker",
		"viewer":                            "[viewer] viewer",
		"master openid":                     "[viewer worker master] worker",
		"openid email":                      "[viewer worker master member admin] worker",
	} {
		offered, pre := offeredRoles(scope)
		if got := fmt.Sprint(offered) + " " + string(pre); got != want {
			t.Fatalf("%q: %s, want %s", scope, got, want)
		}
	}
}

// A code past its ten minutes is refused; an access token past its hour stops resolving.
func TestExpiry(t *testing.T) {
	f := newFixture(t)
	clientID, code, verifier := f.authorize(t, "worker", nil)
	f.clk.add(DefaultCodeTTL)
	st, m := f.exchange(t, clientID, code, verifier)
	wantOAuthError(t, st, m, http.StatusBadRequest, "invalid_grant")
	clientID, code, verifier = f.authorize(t, "worker", nil)
	_, m = f.exchange(t, clientID, code, verifier)
	access := m["access_token"].(string)
	if _, ok := f.resolves(t, access); !ok {
		t.Fatal("a fresh access token does not resolve")
	}
	f.clk.add(DefaultAccessTTL)
	if _, ok := f.resolves(t, access); ok {
		t.Fatal("an access token past its hour resolves")
	}
	if st, m := f.refreshCall(t, clientID, m["refresh_token"].(string)); st != http.StatusOK {
		t.Fatalf("the refresh token within its 14 days: %d %v", st, m)
	}
}

// A repeated parameter is refused at /authorize (directly) and at /token.
func TestRepeatedParametersRefused(t *testing.T) {
	f := newFixture(t)
	clientID := f.register(t, callbackURI)
	_, challenge := pkce(t)
	q := authQuery(clientID, callbackURI, challenge, nil)
	q.Add("scope", "admin")
	q.Add("scope", "viewer")
	resp, err := http.Get(f.srv.URL + "/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if m := decode(t, resp); resp.StatusCode != http.StatusBadRequest || m["error"] != "invalid_request" {
		t.Fatalf("authorize: %d %v", resp.StatusCode, m)
	}
	st, m := f.tokenCall(t, url.Values{"grant_type": {"refresh_token", "authorization_code"}})
	wantOAuthError(t, st, m, http.StatusBadRequest, "invalid_request")
}

func TestNewRefusesAPublicURLWithAPath(t *testing.T) {
	for _, u := range []string{"", "localhost:8080", "ftp://x", "https://x.example/base", "https://x.example?q=1"} {
		if _, err := New(Options{PublicURL: u, Store: nil}); err == nil || !strings.Contains(err.Error(), "public_url") {
			t.Fatalf("%q: %v", u, err)
		}
	}
	if got, err := issuerOf("https://bk.example:8443/"); err != nil || got != "https://bk.example:8443" {
		t.Fatalf("issuerOf: %q %v", got, err)
	}
}

// A browser-run client (the MCP Inspector) may read the metadata and call /register and /token from
// another origin: none of them reads a cookie. The consent routes carry no CORS header.
func TestCORSOnTheCookielessRoutesOnly(t *testing.T) {
	f := newFixture(t)
	for _, p := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource", "/register", "/token"} {
		req, _ := http.NewRequest(http.MethodOptions, f.srv.URL+p, nil)
		req.Header.Set("Origin", "http://localhost:6274")
		req.Header.Set("Access-Control-Request-Method", "POST")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body(t, resp)
		if resp.StatusCode != http.StatusNoContent || resp.Header.Get("Access-Control-Allow-Origin") != "*" ||
			!strings.Contains(resp.Header.Get("Access-Control-Allow-Headers"), "Content-Type") {
			t.Fatalf("preflight %s: %d %v", p, resp.StatusCode, resp.Header)
		}
	}
	resp, err := http.Get(f.srv.URL + "/authorize")
	if err != nil {
		t.Fatal(err)
	}
	body(t, resp)
	if resp.Header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("/authorize carries a CORS header")
	}
}

// A code is redeemed only by the client it was issued to: another registered client presenting it,
// with the right verifier and redirect URI, gets invalid_grant and no tokens. The owner redeeming a
// code of its own is the control.
func TestRefusalCodeRedeemedByAnotherClient(t *testing.T) {
	f := newFixture(t)
	_, code, verifier := f.authorize(t, "worker", nil)
	other := f.register(t, callbackURI)
	st, m := f.exchange(t, other, code, verifier)
	wantOAuthError(t, st, m, http.StatusBadRequest, "invalid_grant")
	if m["error_description"] != "the code was not issued to this client_id" || m["access_token"] != nil {
		t.Fatalf("another client's exchange: %v", m)
	}
	owner, code, verifier := f.authorize(t, "worker", nil)
	if st, m := f.exchange(t, owner, code, verifier); st != http.StatusOK || m["access_token"] == nil {
		t.Fatalf("control, the owner's exchange: %d %v", st, m)
	}
}

// The sign-in and consent pages cannot be framed (a framed consent page is a clickjacking target)
// and run no script: both say so in X-Frame-Options and the Content-Security-Policy.
func TestSignInAndConsentPagesCannotBeFramed(t *testing.T) {
	f := newFixture(t)
	clientID := f.register(t, callbackURI)
	_, challenge := pkce(t)
	q := authQuery(clientID, callbackURI, challenge, nil)
	b := f.browser(t)
	check := func(name string) {
		resp, err := b.Get(f.srv.URL + "/authorize?" + q.Encode())
		if err != nil {
			t.Fatal(err)
		}
		page := body(t, resp)
		csp := resp.Header.Get("Content-Security-Policy")
		if !strings.Contains(page, name) || resp.Header.Get("X-Frame-Options") != "DENY" ||
			!strings.Contains(csp, "frame-ancestors 'none'") || !strings.Contains(csp, "default-src 'none'") {
			t.Fatalf("%s page: X-Frame-Options %q, Content-Security-Policy %q", name, resp.Header.Get("X-Frame-Options"), csp)
		}
	}
	check("Sign in to BatonDeck")
	f.signIn(t, b)
	check("Allow test client?")
}

// The sign-in page says why it is back. login=failed is a login and password that did not
// match; login=limited is the sign-in limit, where no password was checked, so it says to wait
// rather than that the password was wrong.
func TestSignInPageSaysWhyItIsBack(t *testing.T) {
	f := newFixture(t)
	clientID := f.register(t, callbackURI)
	_, challenge := pkce(t)
	page := func(login string) string {
		t.Helper()
		resp, err := http.Get(f.srv.URL + "/authorize?" + authQuery(clientID, callbackURI, challenge, map[string]string{"login": login}).Encode())
		if err != nil {
			t.Fatal(err)
		}
		return body(t, resp)
	}
	const mismatch, limited = "did not match an account", "Too many sign-in attempts"
	if p := page("failed"); !strings.Contains(p, mismatch) || strings.Contains(p, limited) {
		t.Fatalf("login=failed page: %s", p)
	}
	if p := page("limited"); !strings.Contains(p, limited) || strings.Contains(p, mismatch) {
		t.Fatalf("login=limited page: %s", p)
	}
	if p := page(""); strings.Contains(p, limited) || strings.Contains(p, mismatch) {
		t.Fatalf("a first visit: %s", p)
	}
}

// A client_name is shown on the sign-in and consent pages: one holding a Unicode format character
// (a bidi override such as U+202E, a zero-width joiner) can make the page read another name, so it
// is refused as a control character is.
func TestRegisterRefusesFormatCharactersInClientName(t *testing.T) {
	f := newFixture(t)
	for _, name := range []string{"Claude\u202egnp.exe", "Cla\u200bude", "tab\there"} {
		resp, err := http.Post(f.srv.URL+"/register", "application/json",
			strings.NewReader(`{"client_name":"`+name+`","redirect_uris":["`+callbackURI+`"]}`))
		if err != nil {
			t.Fatal(err)
		}
		if m := decode(t, resp); resp.StatusCode != http.StatusBadRequest || m["error"] != "invalid_client_metadata" {
			t.Fatalf("client_name %q: %d %v, want 400 invalid_client_metadata", name, resp.StatusCode, m)
		}
	}
	resp, err := http.Post(f.srv.URL+"/register", "application/json",
		strings.NewReader(`{"client_name":"Claude Desktop – Mac","redirect_uris":["`+callbackURI+`"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if m := decode(t, resp); resp.StatusCode != http.StatusCreated {
		t.Fatalf("a plain client_name: %d %v, want 201", resp.StatusCode, m)
	}
}

// A registration made before the userinfo rule (still in the store) is refused at /authorize too,
// directly: no page names its redirect URI and nothing is sent there.
func TestAuthorizeRefusesAStoredRedirectURIWithUserinfo(t *testing.T) {
	f := newFixture(t)
	const spoof = "https://claude.ai@evil.example/cb"
	if err := f.st.CreateOAuthClient(context.Background(), store.OAuthClient{ClientID: "bdc_old", ClientName: "Claude",
		RedirectURIs: []string{spoof}, CreatedAt: f.clk.now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	_, challenge := pkce(t)
	b := f.browser(t)
	f.signIn(t, b)
	resp, err := b.Get(f.srv.URL + "/authorize?" + authQuery("bdc_old", spoof, challenge, nil).Encode())
	if err != nil {
		t.Fatal(err)
	}
	if page := body(t, resp); resp.StatusCode != http.StatusBadRequest || resp.Header.Get("Location") != "" || strings.Contains(page, "Allow") {
		t.Fatalf("authorize with a stored userinfo redirect URI: %d %q %s, want a direct 400", resp.StatusCode, resp.Header.Get("Location"), page)
	}
}

// A client_name stored before /register refused format characters is shown without them: the
// sign-in and consent pages never render a bidi override or a zero-width character that could make
// the name read as another.
func TestAuthorizeShowsAStoredClientNameWithoutFormatCharacters(t *testing.T) {
	f := newFixture(t)
	const redirect = "https://claude.ai/api/mcp/auth_callback"
	if err := f.st.CreateOAuthClient(context.Background(), store.OAuthClient{ClientID: "bdc_old", ClientName: "Claude\u202egnp.exe\u200b",
		RedirectURIs: []string{redirect}, CreatedAt: f.clk.now().UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	_, challenge := pkce(t)
	b := f.browser(t)
	u := f.srv.URL + "/authorize?" + authQuery("bdc_old", redirect, challenge, nil).Encode()
	for _, step := range []string{"sign-in", "consent"} {
		if step == "consent" {
			f.signIn(t, b)
		}
		resp, err := b.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		page := body(t, resp)
		if resp.StatusCode != http.StatusOK || strings.ContainsAny(page, "\u202e\u200b") || !strings.Contains(page, "Claudegnp.exe") {
			t.Fatalf("%s page for a stored client_name with format characters: %d, want 200 naming %q with none of them:\n%s", step, resp.StatusCode, "Claudegnp.exe", page)
		}
	}
}

// A disabled user's grant does not refresh: the refresh token is refused (invalid_grant) and kept,
// so the grant cannot slide its 14 days forward while the account is disabled, and a code issued
// before the disable is not exchanged. Once the user is enabled again the unexpired refresh token
// works, as their API tokens resolve again (user enable).
func TestADisabledUsersGrantDoesNotRefresh(t *testing.T) {
	f := newFixture(t)
	clientID, code, verifier := f.authorize(t, "worker", nil)
	_, first := f.exchange(t, clientID, code, verifier)
	laterClient, laterCode, laterVerifier := f.authorize(t, "worker", nil)
	if _, err := f.svc.Accounts.Disable(context.Background(), "alice"); err != nil {
		t.Fatal(err)
	}
	st, m := f.refreshCall(t, clientID, first["refresh_token"].(string))
	wantOAuthError(t, st, m, http.StatusBadRequest, "invalid_grant")
	st, m = f.exchange(t, laterClient, laterCode, laterVerifier)
	wantOAuthError(t, st, m, http.StatusBadRequest, "invalid_grant")
	if _, _, err := f.svc.Accounts.Enable(context.Background(), "alice"); err != nil {
		t.Fatal(err)
	}
	if st, m := f.refreshCall(t, clientID, first["refresh_token"].(string)); st != http.StatusOK {
		t.Fatalf("the refresh once alice is enabled again: %d %v", st, m)
	}
}
