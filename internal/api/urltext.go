package api

import (
	"log"
	"net/http"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// urlText refuses, as VALIDATION, a request whose path values or query values carry a NUL or a
// byte that is not UTF-8 (domain.CheckText), before next runs. They are percent-decoded, so
// unlike a JSON body they can carry either, and Postgres refuses both in text: without this a
// route that reads its URL itself (fromURL, the on-call raw body, a token revoke, the stream)
// answered INTERNAL on Postgres where SQLite answered NOT_FOUND or an empty page. call checks its
// arguments again, path values included.
func urlText(pattern string, next http.Handler, logger *log.Logger) http.Handler {
	var names []string
	for _, seg := range strings.Split(pattern, "/") {
		if n, ok := strings.CutPrefix(seg, "{"); ok {
			if n = strings.TrimSuffix(strings.TrimSuffix(n, "}"), "..."); n != "$" {
				names = append(names, n)
			}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := make(map[string]any, len(names))
		for _, n := range names {
			path[n] = r.PathValue(n)
		}
		err := domain.CheckText(path)
		for k, vs := range r.URL.Query() {
			for _, v := range vs {
				if err == nil {
					err = domain.CheckText(map[string]any{k: v})
				}
			}
		}
		if err != nil {
			writeServiceError(w, err, logger)
			return
		}
		next.ServeHTTP(w, r)
	})
}
