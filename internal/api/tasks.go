package api

// taskRoutes are app.ts's task read, patch and move routes, each one service.Tasks call.
func taskRoutes(o *Options) []route {
	t := o.Services.Tasks
	return []route{
		{Method: "GET", Path: ppt, h: call(o, t.Get, byTask)},
		{Method: "PATCH", Path: ppt, h: call(o, t.Update, byTask)},
		{Method: "POST", Path: ppt + "/move", h: call(o, t.Move, byTask)},
	}
}
