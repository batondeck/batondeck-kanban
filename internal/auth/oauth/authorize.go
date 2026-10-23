package oauth

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

const maxStateLen = 2048

// authRequest is an authorization request that passed every check: what the consent page shows and
// what its code is bound to.
type authRequest struct {
	client      store.OAuthClient
	redirectURI string
	state       string
	challenge   string
	resource    string
	scope       string        // as requested, echoed by the consent form
	offered     []domain.Role // the roles the consent may grant
	preselected domain.Role
}

// refusal is a request that failed: direct (rendered here, never redirected: the redirect URI is
// not trusted yet, RFC 6749 §4.1.2.1) or, once client and redirect URI are known good, an error
// redirected back to the client with its state.
type refusal struct {
	direct      bool
	code, desc  string
	redirectURI string
	state       string
}

func (f *refusal) write(w http.ResponseWriter, r *http.Request) {
	if f.direct {
		oauthError(w, http.StatusBadRequest, f.code, f.desc)
		return
	}
	http.Redirect(w, r, withParams(f.redirectURI, map[string]string{"error": f.code, "error_description": f.desc, "state": f.state}), http.StatusSeeOther)
}

// withParams is base with params set in its query (empty values left out), keeping the query it has.
func withParams(base string, params map[string]string) string {
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	q := u.Query()
	for k, v := range params {
		if v != "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// parseAuthorize checks an authorization request's parameters, from the query of the GET or the
// form of the consent POST, which re-checks every one of them rather than trusting the page.
func (s *Server) parseAuthorize(r *http.Request, v url.Values) (authRequest, *refusal) {
	if k := singleValued(v); k != "" {
		return authRequest{}, &refusal{direct: true, code: "invalid_request", desc: "parameter given more than once: " + k}
	}
	clientID, redirectURI := v.Get("client_id"), v.Get("redirect_uri")
	if clientID == "" {
		return authRequest{}, &refusal{direct: true, code: "invalid_request", desc: "client_id is required"}
	}
	if redirectURI == "" {
		return authRequest{}, &refusal{direct: true, code: "invalid_request", desc: "redirect_uri is required"}
	}
	c, err := s.o.Store.GetOAuthClient(r.Context(), clientID)
	if errors.Is(err, store.ErrNotFound) {
		return authRequest{}, &refusal{direct: true, code: "invalid_client", desc: "unknown client_id; register again"}
	}
	if err != nil {
		s.o.Logger.Printf("oauth: authorize: reading client: %+v", err)
		return authRequest{}, &refusal{direct: true, code: "server_error", desc: "could not read the client"}
	}
	// Exact match only: conductor also took a loopback URI on any port (RFC 8252 §7.3); this server
	// does not (plan §Architecture "OAuth AS": exact redirect_uri match against the DCR record).
	if !slices.Contains(c.RedirectURIs, redirectURI) {
		return authRequest{}, &refusal{direct: true, code: "invalid_request", desc: "redirect_uri is not registered for this client"}
	}
	state := v.Get("state")
	fail := func(code, desc string) (authRequest, *refusal) {
		return authRequest{}, &refusal{code: code, desc: desc, redirectURI: redirectURI, state: state}
	}
	if len(state) > maxStateLen {
		return authRequest{}, &refusal{direct: true, code: "invalid_request", desc: "state is longer than 2048 bytes"}
	}
	if v.Get("response_type") != "code" {
		return fail("unsupported_response_type", "only response_type=code is supported")
	}
	challenge := v.Get("code_challenge")
	if challenge == "" {
		return fail("invalid_request", "code_challenge is required (PKCE)")
	}
	if v.Get("code_challenge_method") != "S256" {
		return fail("invalid_request", "code_challenge_method must be S256")
	}
	if !pkceValue(challenge) {
		return fail("invalid_request", "code_challenge must be 43 to 128 characters of [A-Za-z0-9-._~]")
	}
	resource, ok := s.targetResource(v.Get("resource"))
	if !ok {
		return fail("invalid_target", "resource must be this server's MCP endpoint, "+s.Resource())
	}
	offered, pre := offeredRoles(v.Get("scope"))
	return authRequest{client: c, redirectURI: redirectURI, state: state, challenge: challenge,
		resource: resource, scope: v.Get("scope"), offered: offered, preselected: pre}, nil
}

// offeredRoles is what the consent page may grant for a requested scope. Scope values are the role
// names; the highest requested is the most the page offers, every role below it is offered too, and
// a request naming none offers all five. Values that are not roles are ignored (RFC 6749 §3.3 lets
// the server grant less than asked, and the token response says what was granted). The preselected
// role is worker, the agent persona, when it is offered, else the highest offered.
func offeredRoles(scope string) ([]domain.Role, domain.Role) {
	top := -1
	for _, s := range strings.Fields(scope) {
		if n := domain.RoleRank(domain.Role(s)); n > top {
			top = n
		}
	}
	all := domain.AllRoles()
	if top < 0 {
		top = len(all) - 1
	}
	offered := all[:top+1]
	if slices.Contains(offered, domain.RoleWorker) {
		return offered, domain.RoleWorker
	}
	return offered, offered[len(offered)-1]
}

// pkceValue reports whether v is a PKCE verifier or S256 challenge's shape: 43 to 128 unreserved
// characters (RFC 7636 §4.1, §4.2).
func pkceValue(v string) bool {
	if len(v) < 43 || len(v) > 128 {
		return false
	}
	for _, c := range v {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '.', c == '_', c == '~':
		default:
			return false
		}
	}
	return true
}

// authorizePage answers a valid authorization request with the sign-in page when the browser has no
// live session, else the consent page.
func (s *Server) authorizePage(w http.ResponseWriter, r *http.Request) {
	req, f := s.parseAuthorize(r, r.URL.Query())
	if f != nil {
		f.write(w, r)
		return
	}
	caller, sess, ok := s.session(r)
	if !ok {
		s.render(w, http.StatusOK, "login.html", loginPage{
			Client: clientLabel(req.client), Next: r.URL.RequestURI(), Failed: r.URL.Query().Get("login") == "failed"})
		return
	}
	login := caller.Identity
	if u, err := s.o.Auth.Accounts.Store.GetUser(r.Context(), caller.Identity); err == nil {
		login = u.Email
	}
	s.render(w, http.StatusOK, "consent.html", s.consentPage(req, login, sess.CSRFToken))
}

// session is the browser's live session, if it has one.
func (s *Server) session(r *http.Request) (domain.Caller, store.Session, bool) {
	ck, err := r.Cookie(auth.SessionCookie)
	if err != nil {
		return domain.Caller{}, store.Session{}, false
	}
	c, sess, err := s.o.Auth.ResolveSession(r.Context(), ck.Value)
	if err != nil {
		return domain.Caller{}, store.Session{}, false
	}
	return c, sess, true
}

func clientLabel(c store.OAuthClient) string {
	if c.ClientName != "" {
		return c.ClientName
	}
	return c.ClientID
}
