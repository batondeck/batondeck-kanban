package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("tasks", RegisterTasks) }

// RegisterTasks adds the task tools, each one service.Tasks call.
func RegisterTasks(s *mcp.Server, svc service.Services) {
	t := svc.Tasks
	mcp.Tool(s, "tasks", "create_task",
		"Create a task on a board. It lands TODO, in the board's first TODO column unless `columnId` names another TODO column. Needs the master role.",
		t.Create)
	mcp.Tool(s, "tasks", "get_task",
		"Get a task by id.",
		t.Get)
	mcp.Tool(s, "tasks", "update_task",
		"Update a task's fields (master role; version-checked). In `patch`, null clears a nullable field. `autoAssign` is not supported yet: set `patch.assignee`.",
		t.Update)
	mcp.Tool(s, "tasks", "move_task",
		"Move a task to a column or status and/or reorder it (master role; version-checked). The status table is fixed (get_board); a full IN_PROGRESS column or another identity's live lease refuses it.",
		t.Move)
	mcp.Tool(s, "tasks", "list_tasks",
		"List a board's tasks with optional filters, a page at a time: `limit` 1 to 200 (default 50), then `nextCursor` as `cursor`. `assignee: null` lists the unassigned.",
		t.List)
	mcp.Tool(s, "tasks", "delete_task",
		"Delete a task with its subtasks and runs, their context, memory and attachments. Irreversible. Needs the master role; version-checked.",
		t.Delete)
}
