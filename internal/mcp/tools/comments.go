package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("comments", RegisterComments) }

// RegisterComments adds the comment tools, each one service.Comments call.
func RegisterComments(s *mcp.Server, svc service.Services) {
	c := svc.Comments
	mcp.Tool(s, "comments", "add_comment",
		"Add a markdown comment to a task; parentCommentId replies to one. @mentions of project members are recorded. Wakes nobody. Replays on idempotencyKey.",
		c.AddComment)
	mcp.Tool(s, "comments", "list_comments",
		"List a task's comments oldest first. Paginated via cursor.",
		c.ListComments)
}
