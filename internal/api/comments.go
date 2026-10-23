package api

// commentRoutes are app.ts's comment routes (2808-2820), each one service.Comments call.
func commentRoutes(o *Options) []route {
	c := o.Services.Comments
	list := in{path: byTask.path, query: []string{"limit", "cursor"}}
	return []route{
		{Method: "GET", Path: ppt + "/comments", h: call(o, c.ListComments, list)},
		{Method: "POST", Path: ppt + "/comments", h: call(o, c.AddComment, byTask)},
	}
}
