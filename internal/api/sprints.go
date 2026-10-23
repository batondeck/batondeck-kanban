package api

// Owner: R2.2 replaces these stubs (app.ts:2868-2877).
func sprintRoutes(o *Options) []route {
	return []route{
		{Method: "GET", Path: pp + "/sprints", h: stub(o, "list_sprints", "R2.2")},
		{Method: "GET", Path: pp + "/sprints/{sprintId}", h: stub(o, "get_sprint", "R2.2")},
		{Method: "POST", Path: pp + "/sprints/{sprintId}", h: stub(o, "update_sprint", "R2.2")},
	}
}
