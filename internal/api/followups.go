package api

// Owner: R2.1 replaces these stubs (app.ts:2830-2844).
func followUpRoutes(o *Options) []route {
	return []route{
		{Method: "GET", Path: ppt + "/follow-ups", h: stub(o, "list_follow_ups", "R2.1")},
		{Method: "POST", Path: ppt + "/follow-ups", h: stub(o, "add_follow_up", "R2.1")},
		{Method: "POST", Path: ppt + "/follow-ups/{followUpId}/ack", h: stub(o, "ack_follow_up", "R2.1")},
	}
}
