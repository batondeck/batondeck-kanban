package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("me", RegisterMe) }

// RegisterMe adds the caller's own settings tools, each one service.Me call.
func RegisterMe(s *mcp.Server, svc service.Services) {
	m := svc.Me
	mcp.Tool(s, "me", "get_my_settings",
		"Your own preferences: ticket-token ceiling and enforcement rung (null where unset), starred assets, and the defaults. Token counts are estimates (about 4 characters per token).",
		m.GetMySettings)
	mcp.Tool(s, "me", "set_my_settings",
		"Set your own preferences. A field sent replaces yours, null clears it, a field left out is kept. maxTicketTokens is 100 to 1000000; starredAssets is at most 200 keys.",
		m.SetMySettings)
}
