package api

// The agent-session routes (app.ts:2583-2590): each one service.Sessions call, the same as the
// list_agent_sessions and disconnect_agent tools.
func agentRoutes(o *Options) []route {
	s := o.Services.Sessions
	return []route{
		{Method: "GET", Path: "/api/agents/sessions", h: call(o, s.ListAgentSessions, in{query: []string{"limit", "all"}})},
		{Method: "POST", Path: "/api/agents/sessions/disconnect", h: call(o, s.DisconnectAgent, in{})},
	}
}
