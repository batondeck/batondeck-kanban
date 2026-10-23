package api

// contextRoutes are app.ts's context, context items, summary, memory, subtasks and dependencies
// routes, each one service.Context call. The subtask pair names the task parentTaskId.
func contextRoutes(o *Options) []route {
	c := o.Services.Context
	parent := in{path: []string{"projectId", "taskId"}, as: []string{"projectId", "parentTaskId"}}
	return []route{
		{Method: "GET", Path: ppt + "/context", h: call(o, c.GetTaskContext, byTask)},
		{Method: "POST", Path: ppt + "/context-items", h: call(o, c.AddItem, byTask)},
		{Method: "POST", Path: ppt + "/summary", h: call(o, c.SetSummary, byTask)},
		{Method: "GET", Path: ppt + "/subtasks", h: call(o, c.ListSubtasks, parent)},
		{Method: "POST", Path: ppt + "/subtasks", h: call(o, c.AddSubtask, parent)},
		{Method: "GET", Path: ppt + "/memory", h: call(o, c.ReadMemory, byTask)},
		{Method: "POST", Path: ppt + "/memory", h: call(o, c.WriteMemory, byTask)},
		{Method: "POST", Path: pp + "/dependencies", h: call(o, c.AddDependency, byProject)},
		{Method: "DELETE", Path: pp + "/dependencies/{edgeId}", h: call(o, c.RemoveDependency, in{path: []string{"projectId", "edgeId"}, noBody: true})},
	}
}
