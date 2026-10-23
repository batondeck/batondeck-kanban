package api

// Owner: R2.3 replaces these stubs (app.ts:2667-2670).
func notificationRoutes(o *Options) []route {
	return []route{
		{Method: "GET", Path: "/api/notifications", h: stub(o, "list_notifications", "R2.3")},
		{Method: "POST", Path: "/api/notifications/read", h: stub(o, "mark_notifications_read", "R2.3")},
	}
}
