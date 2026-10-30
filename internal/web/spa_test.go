package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestClientRoutesMatchAppTsx: clientRoutes is exactly the <Route path="…"> patterns of
// web/src/App.tsx (the "*" fallback aside), so a client route cannot ship without its deep link,
// and a route the SPA dropped cannot keep answering the shell.
func TestClientRoutesMatchAppTsx(t *testing.T) {
	src, err := os.ReadFile("../../web/src/App.tsx")
	if err != nil {
		t.Fatal(err)
	}
	var app []string
	for _, m := range regexp.MustCompile(`<Route\s[^>]*?path="([^"]+)"`).FindAllStringSubmatch(string(src), -1) {
		if m[1] != "*" {
			app = append(app, m[1])
		}
	}
	if len(app) < 10 {
		t.Fatalf("read only %d routes from App.tsx: %v", len(app), app)
	}
	slices.Sort(app)
	ours := slices.Sorted(slices.Values(clientRoutes))
	if !slices.Equal(app, ours) {
		t.Fatalf("App.tsx routes %v\nclientRoutes   %v", app, ours)
	}
}

// TestClientRoutes: each route and a concrete deep link of each is a client route, as react-router
// matches one (case-insensitive, one trailing slash); every other path is not.
func TestClientRoutes(t *testing.T) {
	for _, p := range []string{"/", "/projects", "/projects/", "/projects/P-1/boards/B-1", "/projects/P-1/boards/B-1/tasks/T-1",
		"/agents", "/chronicle", "/chronicle/new", "/chronicle/CP-x", "/chronicle/CP-x/history", "/analytics", "/reports",
		"/activity", "/notifications", "/settings", "/Settings/", "/oncall", "/projects/P%2F1/boards/B-1"} {
		if !isClientRoute(p) {
			t.Errorf("%s is a client route, isClientRoute says no", p)
		}
	}
	for _, p := range []string{"/docs", "/favicon-nope.png", "/mcp/", "/healthz/", "/blobs/", "/blobs/x/y", "/hooks/I-1",
		"/projects/P-1", "/projects/P-1/boards", "/projects//boards/B-1", "/chronicle/a/b", "/settings//", "/login",
		"/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		if isClientRoute(p) {
			t.Errorf("%s is not a client route, isClientRoute says yes", p)
		}
	}
}

// TestMountSPA: the shell for a client route, 404 for any other path, the /api 404 for /api, and a
// file in dist as itself.
func TestMountSPA(t *testing.T) {
	mux := http.NewServeMux()
	MountSPA(mux)
	for _, c := range []struct {
		path   string
		status int
		ctype  string
		body   string
	}{
		{"/", 200, "text/html", "<div id=\"root\""},
		{"/projects/P-1/boards/B-1/tasks/T-1", 200, "text/html", "<div id=\"root\""},
		{"/Settings/", 200, "text/html", "<div id=\"root\""},
		{"/docs", 404, "text/plain", "404 page not found"},
		{"/mcp/", 404, "text/plain", "404 page not found"},
		{"/.well-known/oauth-authorization-server", 404, "text/plain", "404 page not found"},
		{"/api", 404, "text/plain", `{"error":"not_found"}`},
		{"/api/nothing", 404, "text/plain", `{"error":"not_found"}`},
		{"/brand/favicon.svg", 200, "image/svg+xml", "<svg"},
		{"/assets/nope.js", 404, "text/plain", "404 page not found"},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rec.Code != c.status || !strings.HasPrefix(rec.Header().Get("Content-Type"), c.ctype) || !strings.Contains(rec.Body.String(), c.body) {
			t.Errorf("%s: %d %q %.60q, want %d %s containing %q", c.path, rec.Code, rec.Header().Get("Content-Type"), rec.Body.String(), c.status, c.ctype, c.body)
		}
		if shell := c.status == 200 && c.ctype == "text/html"; shell != (rec.Header().Get("Cache-Control") == "no-store") {
			t.Errorf("%s: Cache-Control %q", c.path, rec.Header().Get("Cache-Control"))
		}
	}
}
