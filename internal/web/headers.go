package web

import "net/http"

// AppCSP is the policy for every page the app serves. Each source is one the built SPA loads:
// script-src hashes web/dist/index.html's theme bootstrap (headers_test.go recomputes it); style-src
// is 'unsafe-inline' because React style props and mermaid's SVG carry style attributes, which no
// hash can allow; fonts come from Google Fonts; img-src admits the link favicons (AppLink.tsx asks
// www.google.com, which redirects to t*.gstatic.com, and CSP checks the redirect target) and nothing
// else external, so a chronicle page cannot make readers fetch other images.
const AppCSP = "default-src 'self'; " +
	"script-src 'self' 'sha256-psZYi3WR4pUW/2cO7EvzIngOWf45jXsrc7t7Rz8UMvs='; " +
	"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
	"font-src 'self' https://fonts.gstatic.com; " +
	"img-src 'self' data: https://www.google.com https://*.gstatic.com; " +
	"connect-src 'self'; " +
	"object-src 'none'; base-uri 'self'; frame-ancestors 'none'"

// SecurityHeaders sets nosniff, no-referrer, X-Frame-Options DENY and AppCSP on every response. They
// are set before next runs, so a handler may narrow them (the OAuth pages set a stricter policy).
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", AppCSP)
		next.ServeHTTP(w, r)
	})
}

// WithoutCSP drops the policy from a route that answers JSON, SSE or an attachment, never a page
// (/mcp, /hooks, /blobs). It wraps the route, not a path prefix, so an unmounted path under one of
// them still falls through to the SPA shell with the policy.
func WithoutCSP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Del("Content-Security-Policy")
		next.ServeHTTP(w, r)
	})
}
