package api

// auditRoutes is the ActivityLog screen's audit panel: list_audit_log over GET, its filters in the
// query string. New (conductor read its audit log through the tool alone); workspace admin only,
// refused by service.authorize as the tool is.
func auditRoutes(o *Options) []route {
	return []route{
		{Method: "GET", Path: "/api/audit", h: call(o, o.Services.Audit.ListAuditLog, in{query: []string{"actor", "subject", "action", "days", "limit"}})},
	}
}
