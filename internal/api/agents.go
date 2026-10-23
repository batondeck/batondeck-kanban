package api

// Owner: R2.5 (agent sessions) replaces these stubs (app.ts:2583-2590).
func agentRoutes(o *Options) []route {
	return []route{
		{Method: "GET", Path: "/api/agents/sessions", h: stub(o, "list_agent_sessions", "R2.5")},
		{Method: "POST", Path: "/api/agents/sessions/disconnect", h: stub(o, "disconnect_agent", "R2.5")},
	}
}
