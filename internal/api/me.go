package api

// Owner: R2.5 replaces these stubs: settings over get_my_settings/set_my_settings, CLI tokens over R0.12's
// TokenService (ADR-0004: the REST settings page covers the dropped MCP tools). No marketplace-key routes.
func meRoutes(o *Options) []route {
	return []route{
		{Method: "GET", Path: "/api/me/settings", h: stub(o, "get_my_settings", "R2.5")},
		{Method: "PATCH", Path: "/api/me/settings", h: stub(o, "set_my_settings", "R2.5")},
		{Method: "GET", Path: "/api/me/cli-tokens", h: stub(o, "the CLI-token list over the TokenService", "R2.5")},
		{Method: "POST", Path: "/api/me/cli-tokens", h: stub(o, "the CLI-token create over the TokenService", "R2.5")},
		{Method: "POST", Path: "/api/me/cli-tokens/{id}/revoke", h: stub(o, "the CLI-token revoke over the TokenService", "R2.5")},
	}
}
