package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("boards", RegisterBoards) }

// RegisterBoards adds the boards and columns tools, each one service.Boards call.
func RegisterBoards(s *mcp.Server, svc service.Services) {
	b := svc.Boards
	mcp.Tool(s, "boards", "create_board",
		"Create a board in a project. Without `columns` it gets one column per status (To do, In progress, Blocked, Review, Done), Review left out when the project's review is off.",
		b.Create)
	mcp.Tool(s, "boards", "list_boards",
		"List the boards in a project.",
		b.List)
	mcp.Tool(s, "boards", "get_board",
		"Get a board with its task count per column and its status transition table, which is fixed.",
		b.Get)
	mcp.Tool(s, "boards", "add_column",
		"Add a column, mapped to a status, to a board.",
		b.AddColumn)
	mcp.Tool(s, "boards", "update_column",
		"Update a board column: rename it, change its mapped status, WIP limit or order.",
		b.UpdateColumn)
	mcp.Tool(s, "boards", "remove_column",
		"Remove a board column. It must be empty, and a board keeps at least one column.",
		b.RemoveColumn)
}
