package api

// Owner: R2.11 replaces this stub (app.ts:2406); conductor proxied its analytics service, no tool.
func analyticsRoutes(o *Options) []route {
	return []route{
		{Method: "GET", Path: "/api/analytics/operations", h: stub(o, "the operations summary over tool_calls", "R2.11")},
	}
}
