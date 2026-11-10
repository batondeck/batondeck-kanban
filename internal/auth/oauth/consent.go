package oauth

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"net/url"
	"slices"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

//go:embed templates/*.html
var templateFiles embed.FS

var pages = template.Must(template.ParseFS(templateFiles, "templates/*.html"))

// roleText is what each grant lets the client do, as the consent page says it.
var roleText = map[domain.Role]string{
	domain.RoleViewer: "read boards and tasks",
	domain.RoleWorker: "claim tasks, work them and report on them",
	domain.RoleMaster: "also plan: create, move and wire tasks",
	domain.RoleMember: "also edit boards and columns",
	domain.RoleAdmin:  "everything your own project role allows, and your workspace-admin rights",
}

type loginPage struct {
	Client string
	Next   string
	Failed bool // the login and password did not match (login=failed)
	// Limited is the sign-in limit (login=limited): no password was checked.
	Limited bool
}

type roleOption struct {
	Name     string
	Text     string
	Selected bool
}

type consentPage struct {
	Client      string
	ClientID    string
	Login       string
	RedirectURI string
	Resource    string
	Roles       []roleOption
	Hidden      map[string]string
	CSRF        string
}

func (s *Server) consentPage(req authRequest, login, csrf string) consentPage {
	p := consentPage{Client: clientLabel(req.client), ClientID: req.client.ClientID, Login: login,
		RedirectURI: req.redirectURI, Resource: req.resource, CSRF: csrf,
		Hidden: map[string]string{
			"response_type": "code", "client_id": req.client.ClientID, "redirect_uri": req.redirectURI,
			"code_challenge": req.challenge, "code_challenge_method": "S256", "state": req.state,
			"resource": req.resource, "scope": req.scope,
		}}
	for _, r := range req.offered {
		p.Roles = append(p.Roles, roleOption{Name: string(r), Text: roleText[r], Selected: r == req.preselected})
	}
	return p
}

// render writes a page with the headers every page of the flow carries: never cached, never framed
// (a framed consent page is a clickjacking target), no script.
func (s *Server) render(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := pages.ExecuteTemplate(&buf, name, data); err != nil {
		s.o.Logger.Printf("oauth: rendering %s: %+v", name, err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	_, _ = w.Write(buf.Bytes())
}

// consent is the consent page's POST, mounted inside Authenticate and CSRF: the browser's session
// signed it and echoed its CSRF token. It re-checks the whole request from the posted fields, then
// either sends the client access_denied or issues a code bound to the session's user, the request's
// client, redirect URI, PKCE challenge and resource, and the role granted.
func (s *Server) consent(w http.ResponseWriter, r *http.Request) {
	sess, ok := auth.SessionFrom(r.Context())
	caller, _ := auth.CallerFrom(r.Context())
	if !ok {
		http.Error(w, "consent needs a browser session, not a bearer", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	form := url.Values{}
	for k, vs := range r.PostForm {
		if k != "csrf" && k != "decision" && k != "role" {
			form[k] = vs
		}
	}
	req, f := s.parseAuthorize(r, form)
	if f != nil {
		f.write(w, r)
		return
	}
	switch r.PostForm.Get("decision") {
	case "deny":
		s.audit(r.Context(), auth.AuditEvent{Action: "oauth.consent.denied", Actor: caller.Identity,
			Subject: "oauth_client:" + req.client.ClientID, Result: "ok"})
		(&refusal{code: "access_denied", desc: "the user denied the request", redirectURI: req.redirectURI, state: req.state}).write(w, r)
		return
	case "approve":
	default:
		oauthError(w, http.StatusBadRequest, "invalid_request", "decision must be approve or deny")
		return
	}
	role := domain.Role(r.PostForm.Get("role"))
	if !slices.Contains(req.offered, role) {
		oauthError(w, http.StatusBadRequest, "invalid_scope", "role must be one the consent page offered")
		return
	}
	code, hash, err := auth.MintOAuthValue(auth.OAuthCodePrefix)
	if err != nil {
		oauthError(w, http.StatusInternalServerError, "server_error", "could not issue a code")
		return
	}
	now := s.now()
	row := store.OAuthCode{CodeHash: hash, ClientID: req.client.ClientID, IdentityID: sess.UserID,
		RedirectURI: req.redirectURI, CodeChallenge: req.challenge, Scope: string(role), Resource: req.resource,
		CreatedAt: now.UnixMilli(), ExpiresAt: now.Add(s.o.CodeTTL).UnixMilli()}
	err = s.o.Store.Atomically(r.Context(), func(tx store.Store) error {
		if err := tx.CreateOAuthCode(r.Context(), row); err != nil {
			return err
		}
		return tx.TouchOAuthClient(r.Context(), req.client.ClientID, now.UnixMilli())
	})
	if err != nil {
		s.o.Logger.Printf("oauth: issuing a code: %+v", err)
		oauthError(w, http.StatusInternalServerError, "server_error", "could not issue a code")
		return
	}
	s.audit(r.Context(), auth.AuditEvent{Action: "oauth.consent.granted", Actor: sess.UserID,
		Subject: "oauth_client:" + req.client.ClientID, Result: "ok", Data: map[string]any{"role": string(role), "resource": req.resource}})
	http.Redirect(w, r, withParams(req.redirectURI, map[string]string{"code": code, "state": req.state}), http.StatusSeeOther)
}
