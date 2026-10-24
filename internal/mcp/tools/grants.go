package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("grants", RegisterGrants) }

// RegisterGrants adds grant_capabilities, one service.Grants call.
func RegisterGrants(s *mcp.Server, svc service.Services) {
	mcp.Tool(s, "grants", "grant_capabilities",
		"Narrow a member's tools below their role (project admin only); never widens. No scope: the worker default; null removes it. Fields: tools, deny, labels, boards, maxPriority, statuses, memoryScopes.",
		svc.Grants.Grant)
}
