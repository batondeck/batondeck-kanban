package api

import (
	"context"
	"net/http"
	"sync/atomic"

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

// sessionResp is ui-backend's /api/session answer, less the hosted-only keys (tenant, pullBase),
// plus displayName: the account's display name, else its login, absent when the identity has no
// account row (P-314). Identity is an opaque account id, and an account in no project is named by
// no project's memberDisplay, so this is the one read that can name the SPA's identity chip.
type sessionResp struct {
	Identity             string         `json:"identity"`
	DisplayName          string         `json:"displayName,omitempty"`
	IsAdmin              bool           `json:"isAdmin"`
	Permissions          []string       `json:"permissions"`
	CSRFToken            *string        `json:"csrfToken"`
	SubscriptionsEnabled bool           `json:"subscriptionsEnabled"`
	UISettings           map[string]any `json:"uiSettings"`
}

// publicConfigResp is ui-backend's /api/public-config (neither hosted sign-in exists here), plus
// whether the OAuth server runs and whether the server has no account yet (the sign-in screen's
// `user add` hint).
type publicConfigResp struct {
	DemoLogin   bool `json:"demoLogin"`
	WorkosLogin bool `json:"workosLogin"`
	OAuth       bool `json:"oauth"`
	NoUsers     bool `json:"noUsers"`
}

// sessionRoutes are the session, the public config, the local sign-in and the sign-out. POST
// /api/login and POST /api/logout are the routes ui-backend does not have: its sign-in and sign-out
// were WorkOS's, and here they are R0.12's form login (fields login and password; 204 and the
// session and CSRF cookies) and the logout that deletes the session row (R1.15).
func sessionRoutes(o *Options) []route {
	// Accounts are disabled, never deleted, so once one exists the answer stays false.
	var hasUsers atomic.Bool
	return []route{
		{Method: "GET", Path: "/api/public-config", public: true, h: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := publicConfigResp{OAuth: o.OAuth}
			if o.Users != nil && !hasUsers.Load() {
				// ponytail: lists every account until the first exists; a store count if that ever matters.
				if us, err := o.Users.List(r.Context()); err == nil {
					hasUsers.Store(len(us) > 0)
					resp.NoUsers = len(us) == 0
				}
			}
			writeJSON(w, http.StatusOK, resp)
		})},
		{Method: "POST", Path: "/api/login", public: true, h: o.Auth.LoginHandler()},
		{Method: "POST", Path: "/api/logout", h: o.Auth.LogoutHandler()},
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
			resp.DisplayName = accountName(r.Context(), o, c.Identity)
			writeJSON(w, http.StatusOK, resp)
		})},
	}
}

// accountName is the identity's display name, else its login (auth.Accounts.Name). Empty when there
// are no accounts or no row for the identity: the field is then omitted, and the SPA falls back to
// memberDisplay and then a shortened id. A failed read is logged and omitted too: a missing name
// must not fail the session read the whole SPA boots on.
func accountName(ctx context.Context, o *Options, identity string) string {
	if o.Users == nil || o.Users.Store == nil {
		return ""
	}
	name, err := o.Users.Name(ctx, identity)
	if err != nil {
		o.logger().Printf("api: session display name: %v", err)
		return ""
	}
	return name
}
