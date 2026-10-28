package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("integrations", RegisterIntegrations) }

// RegisterIntegrations adds the on-call integration tools, each one service.Oncall call. All are
// workspace-admin only (their posture), so a non-admin's tools/list does not carry them.
func RegisterIntegrations(s *mcp.Server, svc service.Services) {
	o := svc.Oncall
	mcp.Tool(s, "integrations", "create_integration",
		"Create an on-call webhook integration for a telemetry source. For hmac the server mints the secret and returns it once (secret, shownOnce); pass externalSecret for PagerDuty.",
		o.CreateIntegration)
	mcp.Tool(s, "integrations", "update_integration",
		"Update an on-call integration: enable or disable it, retarget it, change mapping, routing, lifecycle or limits. Version-checked. A new auth mode mints a fresh hmac secret.",
		o.UpdateIntegration)
	mcp.Tool(s, "integrations", "delete_integration",
		"Delete an on-call integration: its endpoint stops answering at once. A tombstone: its delivery log and incident tickets stay. Version-checked; a second delete is NOT_FOUND.",
		o.DeleteIntegration)
	mcp.Tool(s, "integrations", "list_integrations",
		"List a project's on-call integrations, newest first. Auth is shown as its mode, rotation time and dual-accept window, never a secret. includeDeleted adds tombstones.",
		o.ListIntegrations)
	mcp.Tool(s, "integrations", "rotate_integration_secret",
		"Rotate an hmac integration's secret, returned once. The previous secret keeps verifying for 24 hours; revokePrevious ends that at once. externalSecret for PagerDuty.",
		o.RotateIntegrationSecret)
}
