package web

// Serving the embedded SPA. The UI is a React app compiled into the binary; this
// file is the only place that knows that.

import (
	"io/fs"
	"net/http"
	"strings"

	webdist "github.com/batondeck/batondeck-kanban/web"
)

// MountSPA registers the asset routes and the history fallback. The catch-all runs
// LAST in ServeMux precedence: any registered route (/healthz) wins over it. It answers the shell
// only for a client route (clientRoutes) and 404 for any other path, so a path the board does not
// have (/docs, /mcp/, an OAuth discovery document while OAuth is off) is not a 200 web page.
func MountSPA(mux *http.ServeMux) {
	files, fileServer := spaFiles()

	// Hashed build assets are immutable by construction — the name changes when
	// the content does — so the browser may keep them for as long as it likes. Only files: a
	// directory (/assets/ itself) is 404, not http.FileServer's listing.
	mux.Handle("GET /assets/", assetCache(http.FileServer(filesOnly{files})))

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		// An unmatched /api path must fail like an API, not answer HTML that a
		// fetch() would try to JSON-parse.
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			http.Error(w, `{"error":"not_found"}`, http.StatusNotFound)
			return
		}
		// A real file in dist (the favicon, the brand images) serves as itself.
		if p != "" && !strings.Contains(p, "..") {
			if f, err := files.Open("/" + p); err == nil {
				if st, serr := f.Stat(); serr == nil && !st.IsDir() {
					_ = f.Close()
					fileServer.ServeHTTP(w, r)
					return
				}
				_ = f.Close()
			}
		}
		if !isClientRoute(r.URL.EscapedPath()) {
			http.NotFound(w, r)
			return
		}
		// A client route is a view the SPA routes client-side. The shell is
		// no-store: it references hashed assets, and a cached stale shell after
		// an upgrade would reference assets this binary no longer embeds.
		w.Header().Set("Cache-Control", "no-store")
		r.URL.Path = "/"
		fileServer.ServeHTTP(w, r)
	})
}

// clientRoutes are the paths web/src/App.tsx routes, in react-router's syntax: a ":name" segment is
// one non-empty segment ("/" is matched by isClientRoute itself). spa_test.go reads every <Route>
// tag of App.tsx and fails when the two lists differ, when a route other than the <Shell /> layout
// has no literal path, or when a path has an optional or splat segment matchRoute cannot match, so
// a route added to the SPA without this list (its deep link a 404) does not ship.
var clientRoutes = []string{
	"/",
	"/projects",
	"/projects/:projectId/boards/:boardId",
	"/projects/:projectId/boards/:boardId/tasks/:taskId",
	"/agents",
	"/chronicle",
	"/chronicle/new",
	"/chronicle/:pageId",
	"/chronicle/:pageId/history",
	"/analytics",
	"/reports",
	"/activity",
	"/notifications",
	"/settings",
	"/oncall",
}

// isClientRoute says whether escapedPath (a request's URL.EscapedPath) is one of clientRoutes as
// react-router matches it: static segments case-insensitively, a ":name" segment any one non-empty
// segment, and one trailing slash allowed.
func isClientRoute(escapedPath string) bool {
	if escapedPath == "/" || escapedPath == "" {
		return true
	}
	got := strings.Split(strings.TrimPrefix(strings.TrimSuffix(escapedPath, "/"), "/"), "/")
	for _, route := range clientRoutes {
		if matchRoute(strings.Split(strings.TrimPrefix(route, "/"), "/"), got) {
			return true
		}
	}
	return false
}

func matchRoute(route, got []string) bool {
	if len(route) != len(got) {
		return false
	}
	for i, seg := range route {
		if got[i] == "" {
			return false
		}
		if !strings.HasPrefix(seg, ":") && !strings.EqualFold(seg, got[i]) {
			return false
		}
	}
	return true
}

func assetCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		next.ServeHTTP(w, r)
	})
}

// filesOnly is fs with its directories hidden: opening one is fs.ErrNotExist, so a file server on
// it answers 404 where it would list the directory.
type filesOnly struct{ fs http.FileSystem }

func (f filesOnly) Open(name string) (http.File, error) {
	file, err := f.fs.Open(name)
	if err != nil {
		return nil, err
	}
	if st, err := file.Stat(); err != nil || st.IsDir() {
		_ = file.Close()
		return nil, fs.ErrNotExist
	}
	return file, nil
}

func spaFiles() (http.FileSystem, http.Handler) {
	sub, err := fs.Sub(webdist.Dist, "dist")
	if err != nil {
		panic("webui: embedded dist missing: " + err.Error()) // build-time invariant
	}
	files := http.FS(sub)
	return files, http.FileServer(files)
}
