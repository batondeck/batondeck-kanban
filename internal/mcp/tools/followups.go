package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("followups", RegisterFollowUps) }

// RegisterFollowUps adds the follow-up tools, each one service.FollowUps call.
func RegisterFollowUps(s *mcp.Server, svc service.Services) {
	f := svc.FollowUps
	mcp.Tool(s, "followups", "add_follow_up",
		"Direct a follow-up at a task's lease holder, else its assignee, and wake them (another agent of your own account is rung, not notified). reopen:true sends a REVIEW or DONE task back to TODO; from other statuses it is recorded only.",
		f.AddFollowUp)
	mcp.Tool(s, "followups", "ack_follow_up",
		"Acknowledge a follow-up once acted on; it leaves the open list. Only its target or a project admin may ack a targeted one.",
		f.AckFollowUp)
	mcp.Tool(s, "followups", "list_follow_ups",
		"List a task's follow-ups oldest first; openOnly keeps the open ones. Paginated via cursor.",
		f.ListFollowUps)
}
