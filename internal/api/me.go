package api

// Owner: R2.5 replaces these stubs. app.ts's other six me routes (cli-tokens x3, marketplace-key x3)
// are not mounted: no tool backs them (ADR-0004; the plan dropped the marketplace key).
func meRoutes(o *Options) []route {
	return []route{
		{Method: "GET", Path: "/api/me/settings", h: stub(o, "get_my_settings", "R2.5")},
		{Method: "PATCH", Path: "/api/me/settings", h: stub(o, "set_my_settings", "R2.5")},
	}
}
