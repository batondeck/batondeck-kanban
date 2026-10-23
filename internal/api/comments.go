package api

// Owner: R2.1 replaces these stubs (app.ts:2808-2820).
func commentRoutes(o *Options) []route {
	return []route{
		{Method: "GET", Path: ppt + "/comments", h: stub(o, "list_comments", "R2.1")},
		{Method: "POST", Path: ppt + "/comments", h: stub(o, "add_comment", "R2.1")},
	}
}
