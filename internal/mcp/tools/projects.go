package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("projects", RegisterProjects) }

// RegisterProjects adds the projects, settings and membership tools, each one service.Projects call.
func RegisterProjects(s *mcp.Server, svc service.Services) {
	p := svc.Projects
	mcp.Tool(s, "projects", "create_project",
		"Create a project. The caller becomes its admin.",
		p.Create)
	mcp.Tool(s, "projects", "get_project",
		"Get a project with its members and settings. `memberDisplay` maps each member id to a name a human recognises (the user's display name, else their login), derived at read; a member absent from it has no better label than its id.",
		p.Get)
	mcp.Tool(s, "projects", "list_projects",
		"List the projects the caller is a member of, with the caller's role in each. `all: true` is refused: for a workspace admin with VALIDATION (this deployment has no read of every project), for anyone else with FORBIDDEN.",
		p.List)
	mcp.Tool(s, "projects", "update_settings",
		"Update project settings (project admin): the review toggle, the work-selection weights and aging ceiling, `capabilityAliases`, the artifact, checkpoint, self-approval and token policies (off|warn|enforce), `defaultReviewer`, the retry policy, the anomaly thresholds, the run and ticket-token limits and the colour. `quotaEnabled` and `txnCap` are refused: this deployment enforces no quota.",
		p.UpdateSettings)
	mcp.Tool(s, "projects", "delete_project",
		"Delete a project and everything in it (boards, tasks, events, memory). Irreversible. Requires the project's admin role, or workspace admin.",
		p.Delete)
	mcp.Tool(s, "projects", "add_member",
		"Add a member to a project, or change a member's role (project admin). `identityId` is a user id or a user's login; anything else is kept as given. Demoting the last admin is refused.",
		p.AddMember)
	mcp.Tool(s, "projects", "remove_member",
		"Remove a member from a project (project admin). Removing the last admin is refused.",
		p.RemoveMember)
}
