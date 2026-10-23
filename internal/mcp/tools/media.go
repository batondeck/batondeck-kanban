package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("media", RegisterMedia) }

// RegisterMedia adds the attachment tools, each one service.Media call.
func RegisterMedia(s *mcp.Server, svc service.Services) {
	m := svc.Media
	mcp.Tool(s, "media", "attach_file",
		"Create a pending attachment on a task and return a signed URL to PUT its bytes to, valid until expiresAt. The upload makes it ready.",
		m.AttachFile)
	mcp.Tool(s, "media", "list_attachments",
		"List a task's attachments; each ready one carries a signed download URL valid for 15 minutes.",
		m.ListAttachments)
}
