package api

import (
	"net/http"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// The permission slugs the SPA gates its admin and on-call screens on (conductor's
// WORKSPACE_ADMIN_PERMISSION and ONCALL_PERMISSION), synthesised for a workspace admin as
// ui-backend's /api/session does.
const (
	permSuperadmin = "batondeck:superadmin"
	permOncall     = "batondeck:oncall"
)

// sessionResp is ui-backend's /api/session answer, less the hosted-only keys (tenant, pullBase).
type sessionResp struct {
	Identity             string         `json:"identity"`
	IsAdmin              bool           `json:"isAdmin"`
	Permissions          []string       `json:"permissions"`
	CSRFToken            *string        `json:"csrfToken"`
	SubscriptionsEnabled bool           `json:"subscriptionsEnabled"`
	UISettings           map[string]any `json:"uiSettings"`
}

// publicConfigResp is ui-backend's /api/public-config: neither hosted sign-in exists here.
type publicConfigResp struct {
	DemoLogin   bool `json:"demoLogin"`
	WorkosLogin bool `json:"workosLogin"`
}

// sessionRoutes are the session, the public config and the local sign-in. POST /api/login is the
// one route ui-backend does not have: its sign-in was WorkOS's, and here it is R0.12's form login
// (fields login and password; 204 and the session and CSRF cookies).
func sessionRoutes(o *Options) []route {
	return []route{
		{Method: "GET", Path: "/api/public-config", public: true, h: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, publicConfigResp{})
		})},
		{Method: "POST", Path: "/api/login", public: true, h: o.Auth.LoginHandler()},
		{Method: "GET", Path: "/api/session", h: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, ok := auth.CallerFrom(r.Context())
			if !ok {
				auth.WriteError(w, http.StatusUnauthorized, domain.CodeUnauthenticated, "unauthenticated")
				return
			}
			resp := sessionResp{Identity: c.Identity, IsAdmin: c.WorkspaceAdmin, Permissions: []string{}, UISettings: map[string]any{}}
			if c.WorkspaceAdmin {
				resp.Permissions = []string{permSuperadmin, permOncall}
			}
			if sess, ok := auth.SessionFrom(r.Context()); ok {
				resp.CSRFToken = &sess.CSRFToken
			}
			writeJSON(w, http.StatusOK, resp)
		})},
	}
}
