package api

// Owner: R2.2 replaces these stubs (app.ts:3171-3183).
func runRoutes(o *Options) []route {
	return []route{
		{Method: "GET", Path: ppt + "/runs", h: stub(o, "list_runs", "R2.2")},
		{Method: "POST", Path: ppt + "/runs", h: stub(o, "start_runs", "R2.2")},
		{Method: "POST", Path: ppt + "/runs/open", h: stub(o, "open_run", "R2.2")},
		{Method: "POST", Path: ppt + "/runs/pick", h: stub(o, "pick_run", "R2.2")},
		{Method: "POST", Path: ppt + "/runs/cancel", h: stub(o, "cancel_runs", "R2.2")},
	}
}
