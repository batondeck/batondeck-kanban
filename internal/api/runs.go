package api

// The race routes (conductor app.ts:3171-3183): each one service.Runs call, the task in the path.
func runRoutes(o *Options) []route {
	r := o.Services.Runs
	return []route{
		{Method: "GET", Path: ppt + "/runs", h: call(o, r.List, byTask)},
		{Method: "POST", Path: ppt + "/runs", h: call(o, r.Start, byTask)},
		{Method: "POST", Path: ppt + "/runs/open", h: call(o, r.Open, byTask)},
		{Method: "POST", Path: ppt + "/runs/pick", h: call(o, r.Pick, byTask)},
		{Method: "POST", Path: ppt + "/runs/cancel", h: call(o, r.Cancel, byTask)},
	}
}
