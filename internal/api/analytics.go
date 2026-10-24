package api

// analyticsRoutes is the Analytics and Reports screens' operations summary (ui-backend app.ts:2406):
// conductor proxied its analytics service, which has no tool; here it is service.Analytics, which
// checks the membership on the explicit projectId (a non-member is 403 FORBIDDEN) and clamps days.
func analyticsRoutes(o *Options) []route {
	return []route{
		{Method: "GET", Path: "/api/analytics/operations", h: call(o, o.Services.Analytics.Operations, in{query: []string{"projectId", "days"}})},
	}
}
