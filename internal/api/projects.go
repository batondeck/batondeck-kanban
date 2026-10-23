package api

// projectRoutes are app.ts's projects and members routes, each one service.Projects call.
func projectRoutes(o *Options) []route {
	p := o.Services.Projects
	return []route{
		{Method: "GET", Path: "/api/projects", h: call(o, p.List, in{})},
		{Method: "POST", Path: "/api/projects", h: call(o, p.Create, in{})},
		{Method: "GET", Path: pp, h: call(o, p.Get, byProject)},
		{Method: "POST", Path: pp + "/members", h: call(o, p.AddMember, byProject)},
		{Method: "DELETE", Path: pp + "/members/{identityId}", h: call(o, p.RemoveMember, in{path: []string{"projectId", "identityId"}, noBody: true})},
		{Method: "PATCH", Path: pp + "/settings", h: call(o, p.UpdateSettings, byProject)},
	}
}
