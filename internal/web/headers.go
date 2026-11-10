package web

import "net/http"

// AppCSP is the policy for every page the app serves. Each source is one the built SPA loads:
// script-src hashes web/dist/index.html's theme bootstrap (headers_test.go recomputes it); style-src
// is 'unsafe-inline' because React style props and mermaid's SVG carry style attributes, which no
// hash can allow. No source is another host: the fonts are bundled and the link cards ask no favicon
// service, so a page cannot tell a third party what a viewer reads, and a chronicle page cannot make
// readers fetch an external image.
const AppCSP = "default-src 'self'; " +
	"script-src 'self' 'sha256-psZYi3WR4pUW/2cO7EvzIngOWf45jXsrc7t7Rz8UMvs='; " +
	"style-src 'self' 'unsafe-inline'; " +
	"font-src 'self'; " +
	"img-src 'self' data:; " +
	"connect-src 'self'; " +
	"object-src 'none'; base-uri 'self'; frame-ancestors 'none'"

// SecurityHeaders sets nosniff, Referrer-Policy same-origin, X-Frame-Options DENY and AppCSP on every
// response. They are set before next runs, so a handler may narrow them (the OAuth pages set a
// stricter policy). same-origin sends no Referer to any other origin, as no-referrer would, and lets
// the app's own POSTs carry their Origin: under no-referrer a browser sends `Origin: null`, which the
// CSRF check cannot tell from another site's form where no Fetch Metadata is sent (plain http).
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", AppCSP)
		next.ServeHTTP(w, r)
	})
}

// WithoutCSP drops the policy from a route that answers JSON, SSE or an attachment, never a page
// (/mcp, /hooks, /blobs). It wraps the route, not a path prefix, so an unmounted path under one of
// them still falls through to the catch-all (its 404) with the policy.
func WithoutCSP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Del("Content-Security-Policy")
		next.ServeHTTP(w, r)
	})
}
