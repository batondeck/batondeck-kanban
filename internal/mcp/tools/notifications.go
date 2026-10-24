package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("notifications", RegisterNotifications) }

// RegisterNotifications adds the two in-app feed tools, each one service.Notifications call.
func RegisterNotifications(s *mcp.Server, svc service.Services) {
	n := svc.Notifications
	mcp.Tool(s, "notifications", "list_notifications",
		"Your in-app notifications (mentions, follow-ups, review requests), newest first across projects, with your unread count. Page with `cursor`.",
		n.List)
	mcp.Tool(s, "notifications", "mark_notifications_read",
		"Mark your notifications read: `ids` for specific ones, or `all:true`. Answers how many were unread.",
		n.MarkRead)
}
