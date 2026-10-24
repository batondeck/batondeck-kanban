package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("rank", RegisterRank) }

// RegisterRank adds rank_tasks and orchestrate_subtasks, each one service.Ranking call.
func RegisterRank(s *mcp.Server, svc service.Services) {
	r := svc.Ranking
	mcp.Tool(s, "rank", "rank_tasks",
		"Rank a board's claimable TODO tasks by score (priority, deadline, fan-out, capability fit, aging), each with its factors and reason. Read-only.",
		r.Rank)
	mcp.Tool(s, "rank", "orchestrate_subtasks",
		"Fan out the task you hold: create TODO children that block it and park it BLOCKED, lease dropped, assigned to you, so its unblock rings you. Master role.",
		r.Orchestrate)
}
