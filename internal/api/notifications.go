package api

// notificationRoutes are app.ts's notification routes (2667-2670): the caller's own feed, cross-
// project, each one service.Notifications call. Live updates ride /api/stream's notifications frame.
func notificationRoutes(o *Options) []route {
	n := o.Services.Notifications
	return []route{
		{Method: "GET", Path: "/api/notifications", h: call(o, n.List, in{query: []string{"limit", "cursor"}})},
		{Method: "POST", Path: "/api/notifications/read", h: call(o, n.MarkRead, in{})},
	}
}
