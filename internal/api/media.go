package api

// mediaRoutes are app.ts's attachments and search routes, each one service.Media call (search is
// F10's search_tasks).
func mediaRoutes(o *Options) []route {
	m := o.Services.Media
	return []route{
		{Method: "GET", Path: ppt + "/attachments", h: call(o, m.ListAttachments, byTask)},
		{Method: "POST", Path: ppt + "/attachments", h: call(o, m.AttachFile, byTask)},
		{Method: "GET", Path: pp + "/search", h: call(o, m.SearchTasks, in{path: byProject.path, query: []string{"query", "boardId", "k"}})},
	}
}
