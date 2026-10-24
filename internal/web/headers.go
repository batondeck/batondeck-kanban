package web

import (
	"net/http"
	"strings"
)

// AppCSP is the policy for every page the app serves. Each source is one the built SPA loads:
// script-src hashes web/dist/index.html's theme bootstrap (headers_test.go recomputes it); style-src
// is 'unsafe-inline' because React style props and mermaid's SVG carry style attributes, which no
// hash can allow; fonts come from Google Fonts; img-src admits www.google.com for the link favicons
// (AppLink.tsx) and nothing else external, so a chronicle page cannot make readers fetch images.
const AppCSP = "default-src 'self'; " +
	"script-src 'self' 'sha256-psZYi3WR4pUW/2cO7EvzIngOWf45jXsrc7t7Rz8UMvs='; " +
	"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; " +
	"font-src 'self' https://fonts.gstatic.com; " +
	"img-src 'self' data: https://www.google.com; " +
	"connect-src 'self'; " +
	"object-src 'none'; base-uri 'self'; frame-ancestors 'none'"

// SecurityHeaders sets nosniff, no-referrer and X-Frame-Options DENY on every response, and AppCSP on
// all but /mcp, /hooks/ and /blobs/, which answer JSON, SSE or attachments. They are set before next
// runs, so a handler may narrow them (the OAuth pages set a stricter policy of their own).
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		p := r.URL.Path
		if p != "/mcp" && !strings.HasPrefix(p, "/hooks/") && !strings.HasPrefix(p, "/blobs/") {
			h.Set("Content-Security-Policy", AppCSP)
		}
		next.ServeHTTP(w, r)
	})
}
