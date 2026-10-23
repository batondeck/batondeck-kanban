package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("search", RegisterSearch) }

// RegisterSearch adds search_tasks, one service.Media call.
func RegisterSearch(s *mcp.Server, svc service.Services) {
	mcp.Tool(s, "search", "search_tasks",
		"Search a project's tasks by keyword over title, description and tokens; every term must match, word* is a prefix. Best first, at most k (10). mode semantic is refused.",
		svc.Media.SearchTasks)
}
