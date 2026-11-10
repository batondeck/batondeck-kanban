package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("audit", RegisterAudit) }

// RegisterAudit adds list_audit_log, workspace admin only (listed to the admin alone, refused to
// anyone else by service.authorize).
func RegisterAudit(s *mcp.Server, svc service.Services) {
	mcp.Tool(s, "audit", "list_audit_log",
		"Workspace admin: the audit log, newest first: who (actor) did what (action) to whom (subject), whether it took effect. Filters ANDed; days default 90 (max 365); nextCursor as cursor pages older.",
		svc.Audit.ListAuditLog)
}
