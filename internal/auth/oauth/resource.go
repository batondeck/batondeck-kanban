package oauth

import (
	"net/http"
	"net/url"
	"slices"
	"strings"
)

// prmPath is the RFC 9728 well-known path. A resource with a path also has the document at that
// path appended (§3.1), which is where a client deriving it from the resource URL looks; /mcp's 401
// names the bare path.
const prmPath = "/.well-known/oauth-protected-resource"

// prmPaths is each well-known path and the resource its document describes: the bare path for the
// first resource, and every resource's suffixed path.
func (s *Server) prmPaths() map[string]string {
	out := map[string]string{prmPath: s.o.Resources[0]}
	for _, r := range s.o.Resources {
		if u, err := url.Parse(r); err == nil && u.Path != "" {
			if _, taken := out[prmPath+u.Path]; !taken {
				out[prmPath+u.Path] = r
			}
		}
	}
	return out
}

// protectedResource is the RFC 9728 document of resource: this server is its authorization server.
func (s *Server) protectedResource(resource string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"resource":                 resource,
			"authorization_servers":    []string{s.issuer},
			"scopes_supported":         roles(),
			"bearer_methods_supported": []string{"header"},
			"resource_name":            "BatonDeck",
		})
	}
}

// targetResource is the resource (RFC 8707) a request names, "" naming none, resolved against the
// resources this server issues for: the first when none is named, ok false for one it does not
// protect. A trailing slash is not a difference.
func (s *Server) targetResource(named string) (string, bool) {
	if named == "" {
		return s.o.Resources[0], true
	}
	named = strings.TrimRight(named, "/")
	if slices.Contains(s.o.Resources, named) {
		return named, true
	}
	return "", false
}
