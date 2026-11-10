package auth

import (
	"crypto/subtle"
	"net/http"
	"strings"
	"unicode/utf8"
)

// CSRF refuses, with 403, a state change that a page of another origin could have made. Ported
// from hdtp-gateway's csrfMiddleware (internalui/server.go:285); two checks that do not stand in
// for each other:
//
//   - Where the request came from. A browser says so: `Sec-Fetch-Site` must be same-origin (or
//     none, a person's own navigation). A browser that sends no Fetch Metadata still sends Origin on
//     a POST, which must then be this server's own. A client that is no browser sends neither and
//     is judged by its credential alone: a forged request needs a browser, and a browser says where
//     it was.
//   - For a request authenticated by a session cookie, the session's CSRF token, echoed in
//     CSRFHeader or the form field `csrf` and compared with the session row. A bearer request
//     carries no ambient credential and needs none.
//
// GET, HEAD and OPTIONS pass. Mount it inside Authenticate (which puts the session on the context);
// the login route mounts it alone. Every refusal is audited, at most refusalRows a minute of each
// actor, the requests with no credential counting as one (anyone can send those, and the audit log
// is kept).
func (s *Service) CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if !s.sameOrigin(r) {
			s.refuse(w, r, "cross_site")
			return
		}
		if sess, ok := SessionFrom(r.Context()); ok {
			h := r.Header.Get(CSRFHeader)
			if h == "" {
				r.Body = http.MaxBytesReader(w, r.Body, maxLoginBody)
				if err := r.ParseForm(); err == nil {
					h = r.PostForm.Get("csrf")
				}
			}
			if h == "" || subtle.ConstantTimeCompare([]byte(h), []byte(sess.CSRFToken)) != 1 {
				s.refuse(w, r, "csrf_token")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// refusalRows is how many CSRF refusals of one actor the audit log takes a minute, and in one
// burst, the requests that carried no credential counting as one actor; past it they are refused
// unrecorded. Anyone who reaches the server can make a refusal with no credential (a cross-site
// POST to a sign-in route), and a page of a sibling subdomain can make a signed-in browser's (its
// SameSite=Lax cookie rides a same-site POST). Audit rows are never swept, so the budget bounds how
// fast such pages add to the log, and maxRefusedPath how much of the path each row holds.
const (
	refusalRows    = 30
	maxRefusedPath = 256
)

func (s *Service) refuse(w http.ResponseWriter, r *http.Request, why string) {
	if s.Audit != nil {
		c, _ := CallerFrom(r.Context())
		if s.refusalAllowed(c.Identity) {
			s.Audit(r.Context(), AuditEvent{Action: "request.refused", Actor: c.Identity, Subject: "path:" + refusedPath(r.URL.Path),
				Result: "refused", ErrorCode: "FORBIDDEN", Data: map[string]any{"reason": why, "method": r.Method}})
		}
	}
	http.Error(w, "cross-site request refused", http.StatusForbidden)
}

// refusalAllowed takes one of actor's refusalRows a minute ("" is every request with no
// credential): false when none is left.
func (s *Service) refusalAllowed(actor string) bool {
	s.mu.Lock()
	if s.refusals == nil {
		s.refusals = newBuckets(refusalRows)
	}
	b := s.refusals
	s.mu.Unlock()
	_, ok := b.take(actor, s.now())
	return ok
}

// refusedPath is a refused request's path as its audit row names it: UTF-8 (a run of bytes that is
// none becomes U+FFFD) and at most maxRefusedPath bytes, cut where a character starts.
func refusedPath(p string) string {
	p = strings.ToValidUTF8(p, "\uFFFD")
	if len(p) <= maxRefusedPath {
		return p
	}
	i := maxRefusedPath
	for i > 0 && !utf8.RuneStart(p[i]) {
		i--
	}
	return p[:i]
}

// sameOrigin is hdtp's: Fetch Metadata when present, else Origin when present, else true (no
// browser, or one that says nothing, which the CSRF token then judges). Origin is this server's own
// when it is the origin r reached (RequestOrigin) or public_url's: behind a proxy the forwarded Host
// and scheme may be the proxy's upstream, not what the browser saw. Origin "null" says nothing about
// where a request came from (a page with referrer policy no-referrer sends it on a cross-site POST;
// this server's own pages send their origin, web.SecurityHeaders' same-origin policy): it passes
// only a request with a credential on its context, a session, which the CSRF token then judges, or
// a bearer, which is no ambient credential. A sign-in route has neither, so a page of another site
// cannot sign a browser in where no Fetch Metadata is sent (plain http).
func (s *Service) sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	switch o := r.Header.Get("Origin"); o {
	case "":
		return true
	case "null":
		_, ok := CallerFrom(r.Context())
		return ok
	default:
		return o == RequestOrigin(r) || (o == s.publicOrigin() && o != "")
	}
}

// RequestOrigin is the scheme://host r reached the server at: https when r arrived over TLS or says
// X-Forwarded-Proto: https, else http, and r.Host. The CSRF check compares Origin with it, and serve
// starts the blob URLs it signs for r with it when public_url is unset.
func RequestOrigin(r *http.Request) string {
	scheme := "http"
	if requestIsTLS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
