package api

// followUpRoutes are app.ts's follow-up routes (2830-2844), each one service.FollowUps call. The ack
// names its follow-up by the URL and takes no body, as app.ts's refusedUnknownBodyKeys(req, []).
func followUpRoutes(o *Options) []route {
	f := o.Services.FollowUps
	list := in{path: byTask.path, query: []string{"openOnly", "limit", "cursor"}}
	return []route{
		{Method: "GET", Path: ppt + "/follow-ups", h: call(o, f.ListFollowUps, list)},
		{Method: "POST", Path: ppt + "/follow-ups", h: call(o, f.AddFollowUp, byTask)},
		{Method: "POST", Path: ppt + "/follow-ups/{followUpId}/ack", h: call(o, f.AckFollowUp, in{path: []string{"projectId", "taskId", "followUpId"}, noBody: true})},
	}
}
