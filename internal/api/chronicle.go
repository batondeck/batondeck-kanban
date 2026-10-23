package api

// Owner: R2.7 replaces these stubs (app.ts:3393-3503); the GETs are resource reads in conductor.
func chronicleRoutes(o *Options) []route {
	const pc = pp + "/chronicle"
	const res = "resource batondeck://{projectId}/chronicle"
	return []route{
		{Method: "GET", Path: pc, h: stub(o, res, "R2.7")},
		{Method: "GET", Path: pc + "/{pageId}", h: stub(o, res+"/{pageId}", "R2.7")},
		{Method: "POST", Path: pc, h: stub(o, "create_chronicle_page", "R2.7")},
		{Method: "DELETE", Path: pc + "/{pageId}", h: stub(o, "delete_chronicle_page", "R2.7")},
		{Method: "GET", Path: pc + "/{pageId}/versions", h: stub(o, res+"/{pageId}/versions", "R2.7")},
		{Method: "GET", Path: pc + "/{pageId}/versions/{versionId}", h: stub(o, res+"/{pageId}/versions/{versionId}", "R2.7")},
		{Method: "POST", Path: pc + "/{pageId}/edit", h: stub(o, "edit_chronicle_page", "R2.7")},
		{Method: "POST", Path: pc + "/{pageId}/revert", h: stub(o, "edit_chronicle_page", "R2.7")},
	}
}
