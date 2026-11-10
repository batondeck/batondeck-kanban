package web

import (
	"fmt"
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
// and a route the SPA dropped cannot keep answering the shell. appRoutes refuses any <Route> it
// cannot read a literal path from, so the pin cannot be passed by spelling a route another way.
func TestClientRoutesMatchAppTsx(t *testing.T) {
	src, err := os.ReadFile("../../web/src/App.tsx")
	if err != nil {
		t.Fatal(err)
	}
	app, err := appRoutes(string(src))
	if err != nil {
		t.Fatal(err)
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

// TestAppRoutesReadsEverySpelling: appRoutes reads a literal path in any attribute position and
// across lines, and refuses what it cannot pin: a <Route> with no literal path (a computed one, an
// index route) other than the <Shell /> layout route, a tag that never closes, and a pattern
// matchRoute does not implement (optional or splat).
func TestAppRoutesReadsEverySpelling(t *testing.T) {
	layout := "<Route element={<Shell />}>"
	for _, c := range []struct {
		src  string
		want []string
	}{
		{layout + `<Route path="/a" element={<A />} /></Route>`, []string{"/a"}},
		{`<Route element={<Agents />} path="/teams" />`, []string{"/teams"}},
		{`<Route element={<X a="}>" b={{ c: '>' }} />} path='/q' />`, []string{"/q"}},
		{"<Route\n  path=\"/oncall\"\n  element={\n    <Gate><Oncall /></Gate>\n  }\n/>", []string{"/oncall"}},
		{`<Routes><Route path="*" element={<Navigate to="/projects" replace />} /></Routes>`, nil},
	} {
		got, err := appRoutes(c.src)
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("appRoutes(%q) = %v, %v; want %v", c.src, got, err, c.want)
		}
	}
	for _, src := range []string{
		`<Route path={"/teams"} element={<Agents />} />`,
		`<Route element={<Agents />} path={teamsPath} />`,
		`<Route element={<Agents />} />`,
		`<Route index element={<Home />} />`,
		`<Route path="/chronicle/:pageId?" element={<C />} />`,
		`<Route path="/files/*" element={<F />} />`,
		`<Route path="/a" element={<A />}`,
	} {
		if got, err := appRoutes(src); err == nil {
			t.Errorf("appRoutes(%q) = %v, want an error", src, got)
		}
	}
}

// appRoutes is every <Route>'s literal path in src, the "*" fallback aside. It walks each opening
// tag to its closing ">" outside braces and quotes, so an attribute's JSX or string cannot end it.
func appRoutes(src string) ([]string, error) {
	var out []string
	for _, loc := range regexp.MustCompile(`<Route[\s/>]`).FindAllStringIndex(src, -1) {
		start, depth, end := loc[0], 0, -1
		var top strings.Builder // the tag with everything inside braces dropped
		for i := start + len("<Route"); i < len(src) && end < 0; i++ {
			switch ch := src[i]; {
			case ch == '"' || ch == '\'' || ch == '`':
				j := i + 1
				for j < len(src) && src[j] != ch {
					if src[j] == '\\' {
						j++
					}
					j++
				}
				if depth == 0 {
					top.WriteString(src[i:min(j+1, len(src))])
				}
				i = j
			case ch == '{':
				if depth == 0 {
					top.WriteByte('{')
				}
				depth++
			case ch == '}':
				depth--
				if depth == 0 {
					top.WriteByte('}')
				}
			case ch == '>' && depth == 0:
				end = i
			case depth == 0:
				top.WriteByte(ch)
			}
		}
		tag := src[start:max(end+1, start)]
		if end < 0 {
			return nil, fmt.Errorf("a <Route> tag at byte %d never closes", start)
		}
		attrs := top.String()
		m := regexp.MustCompile(`(?:^|\s)path\s*=\s*(?:"([^"]*)"|'([^']*)')`).FindStringSubmatch(attrs)
		switch {
		case m == nil && tag == "<Route element={<Shell />}>":
			continue
		case m == nil:
			return nil, fmt.Errorf("%s: a <Route> other than the <Shell /> layout route without a literal path (a computed path={…} cannot be pinned)", tag)
		}
		path := m[1] + m[2]
		if path == "*" {
			continue
		}
		if strings.ContainsAny(path, "?*") {
			return nil, fmt.Errorf("%s: matchRoute has no optional or splat segments", tag)
		}
		out = append(out, path)
	}
	return out, nil
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

// api-http#10: /assets/ serves the hashed files and nothing else. The directory, or any directory
// under it, is 404 rather than http.FileServer's listing, and a 404 is not cached as immutable;
// a real asset still is.
func TestAssetsServeFilesNotListings(t *testing.T) {
	entries, err := os.ReadDir("../../web/dist/assets")
	if err != nil || len(entries) == 0 {
		t.Fatalf("web/dist/assets: %v, %d entries", err, len(entries))
	}
	mux := http.NewServeMux()
	MountSPA(mux)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	asset := get("/assets/" + entries[0].Name())
	if asset.Code != 200 || asset.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("an asset: %d, Cache-Control %q", asset.Code, asset.Header().Get("Cache-Control"))
	}
	for _, path := range []string{"/assets/", "/assets/nope.js"} {
		rec := get(path)
		if rec.Code != 404 || strings.Contains(rec.Body.String(), "<a href") {
			t.Errorf("%s: %d %.80q, want 404 and no listing", path, rec.Code, rec.Body.String())
		}
		if cc := rec.Header().Get("Cache-Control"); strings.Contains(cc, "immutable") {
			t.Errorf("%s: a 404 with Cache-Control %q", path, cc)
		}
	}
}
