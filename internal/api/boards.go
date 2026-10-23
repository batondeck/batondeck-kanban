package api

// boardRoutes are app.ts's boards, columns and board-tasks routes. The transitions pair is gone:
// the table is fixed (plan §Ticket states).
func boardRoutes(o *Options) []route {
	b, t := o.Services.Boards, o.Services.Tasks
	column := in{path: []string{"projectId", "boardId", "columnId"}}
	return []route{
		{Method: "GET", Path: pp + "/boards", h: call(o, b.List, byProject)},
		{Method: "POST", Path: pp + "/boards", h: call(o, b.Create, byProject)},
		{Method: "GET", Path: ppb, h: call(o, b.Get, byBoard)},
		{Method: "POST", Path: ppb + "/columns", h: call(o, b.AddColumn, byBoard)},
		{Method: "PATCH", Path: ppb + "/columns/{columnId}", h: call(o, b.UpdateColumn, column)},
		{Method: "DELETE", Path: ppb + "/columns/{columnId}", h: call(o, b.RemoveColumn, in{path: column.path, noBody: true})},
		{Method: "GET", Path: ppb + "/tasks", h: call(o, t.List, in{path: byBoard.path,
			query: []string{"limit", "unblockedOnly", "status", "assignee", "label", "sprintId", "cursor"}})},
		{Method: "POST", Path: ppb + "/tasks", h: call(o, t.Create, byBoard)},
	}
}
