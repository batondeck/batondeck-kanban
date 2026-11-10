package api

// feedRoutes are the board feed (app.ts:3267, service.Feed.BoardFeed) and the stream. The feed takes
// taskId (one task's events: the ticket's Activity), limit and cursor in its query.
func feedRoutes(o *Options) []route {
	return []route{
		{Method: "GET", Path: ppb + "/feed", h: call(o, o.Services.Feed.BoardFeed, in{path: byBoard.path, query: []string{"taskId", "limit", "cursor"}})},
		{Method: "GET", Path: "/api/stream", h: newStream(o)},
	}
}
