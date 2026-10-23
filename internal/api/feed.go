package api

// feedRoutes are the board feed (app.ts:3267, service.Feed.BoardFeed) and the stream.
func feedRoutes(o *Options) []route {
	return []route{
		{Method: "GET", Path: ppb + "/feed", h: call(o, o.Services.Feed.BoardFeed, byBoard)},
		{Method: "GET", Path: "/api/stream", h: newStream(o)},
	}
}
