package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("sprints", RegisterSprints) }

// RegisterSprints adds the five sprint tools, each one service.Sprints call.
func RegisterSprints(s *mcp.Server, svc service.Services) {
	sp := svc.Sprints
	mcp.Tool(s, "sprints", "create_sprint",
		"Open a sprint (a named cross-ticket objective) on a board, PROPOSED. Add members with set_sprint; activate with update_sprint once its proposal task is DONE. Master role.",
		sp.Create)
	mcp.Tool(s, "sprints", "update_sprint",
		"Patch a sprint (version-checked): name, goal, acceptance, checkpoint, proposalTaskId, and status PROPOSED→ACTIVE|ABANDONED, ACTIVE→CLOSED|ABANDONED. Activating needs admin and a DONE proposal.",
		sp.Update)
	mcp.Tool(s, "sprints", "set_sprint",
		"Add a task to or remove it from a PROPOSED or ACTIVE sprint on its board (version-checked on the task). A task is in one open sprint at a time; re-adding a member is a no-op. Master role.",
		sp.Set)
	mcp.Tool(s, "sprints", "get_sprint",
		"Read a sprint to resume from it: the sprint, its member tasks, counts, the claimable frontier, what needs attention, the board's lanes and policies. `include` adds deliverables and memory.",
		sp.Get)
	mcp.Tool(s, "sprints", "list_sprints",
		"List a project's sprints oldest first, by board and status, a page at a time (`limit` 1 to 200, then `nextCursor`). `status: ACTIVE` finds the sprint to resume.",
		sp.List)
}
