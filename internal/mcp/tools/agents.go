package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("agents", RegisterAgents) }

// RegisterAgents adds the two agent-session tools, each one service.Sessions call.
func RegisterAgents(s *mcp.Server, svc service.Services) {
	a := svc.Sessions
	mcp.Tool(s, "agents", "list_agent_sessions",
		"List your own MCP agent sessions: client, first and last seen, request and per-tool counts. A workspace admin may pass all:true for every identity's.",
		a.ListAgentSessions)
	mcp.Tool(s, "agents", "disconnect_agent",
		"End one agent session's live MCP connections (identityId + agentId): its next request is refused and it must initialise again. Your own agents; a workspace admin, anyone's.",
		a.DisconnectAgent)
}
