package api

// The sprint routes (conductor app.ts:2868-2877): each one service.Sprints call. create_sprint and
// set_sprint stay MCP-only, as conductor's: defining an objective is the agent's planning act.
func sprintRoutes(o *Options) []route {
	sp := o.Services.Sprints
	sprint := in{path: []string{"projectId", "sprintId"}}
	return []route{
		{Method: "GET", Path: pp + "/sprints", h: call(o, sp.List, in{path: byProject.path, query: []string{"boardId", "status", "limit", "cursor"}})},
		{Method: "GET", Path: pp + "/sprints/{sprintId}", h: call(o, sp.Get, sprint)},
		{Method: "POST", Path: pp + "/sprints/{sprintId}", h: call(o, sp.Update, sprint)},
	}
}
