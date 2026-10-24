package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("agents_profile", RegisterAgentProfiles) }

// RegisterAgentProfiles adds the capability registry's two tools, each one service.Profiles call.
func RegisterAgentProfiles(s *mcp.Server, svc service.Services) {
	p := svc.Profiles
	mcp.Tool(s, "agents_profile", "register_agent_profile",
		"Register or overwrite your capability profile in a project: capabilities, tags, limits. Omitted fields reset; send the whole profile. useProfile on rank_tasks matches against it.",
		p.Register)
	mcp.Tool(s, "agents_profile", "get_skill_stats",
		"An agent's profile and per-capability outcome stats in a project: completions, blocks, reliability and mean handle time. Omit agentKey for your own.",
		p.SkillStats)
}
