package auth

import (
	"crypto/subtle"
	"net/http"
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
// the login route mounts it alone. Every refusal is audited.
func (s *Service) CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if !sameOrigin(r) {
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

func (s *Service) refuse(w http.ResponseWriter, r *http.Request, why string) {
	if s.Audit != nil {
		actor := ""
		if c, ok := CallerFrom(r.Context()); ok {
			actor = c.Identity
		}
		s.Audit(r.Context(), AuditEvent{Action: "request.refused", Actor: actor, Subject: "path:" + r.URL.Path,
			Result: "refused", ErrorCode: "FORBIDDEN", Data: map[string]any{"reason": why, "method": r.Method}})
	}
	http.Error(w, "cross-site request refused", http.StatusForbidden)
}

// sameOrigin is hdtp's: Fetch Metadata when present, else Origin when present and not "null", else
// true (no browser, or one that says nothing, which the CSRF token then judges).
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" {
		return site == "same-origin" || site == "none"
	}
	if o := r.Header.Get("Origin"); o != "" && o != "null" {
		return o == RequestOrigin(r)
	}
	return true
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
