package mcp_test

import (
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service/servicefake"
)

type pinnedPosture struct {
	tier         domain.Tier
	tenantScoped bool
	adminOnly    domain.AdminOnly
}

// expectedPosture pins each registered tool's guard, as conductor's test/unit/authz-matrix.test.ts
// does: a posture widened in internal/domain/grants.go, or a tool registered with no row here,
// fails. The tiers are conductor's manifest.ts entries for these tools.
var expectedPosture = map[string]pinnedPosture{
	"create_project":   {tier: "", tenantScoped: false},
	"get_project":      {tier: domain.TierRead, tenantScoped: true},
	"list_projects":    {tier: "", tenantScoped: false, adminOnly: domain.AdminOnlyConditional},
	"update_settings":  {tier: domain.TierAdmin, tenantScoped: true, adminOnly: domain.AdminOnlyConditional},
	"delete_project":   {tier: domain.TierAdmin, tenantScoped: true, adminOnly: domain.AdminOnlyConditional},
	"add_member":       {tier: domain.TierAdmin, tenantScoped: true},
	"remove_member":    {tier: domain.TierAdmin, tenantScoped: true},
	"create_board":     {tier: domain.TierWrite, tenantScoped: true},
	"list_boards":      {tier: domain.TierRead, tenantScoped: true},
	"get_board":        {tier: domain.TierRead, tenantScoped: true},
	"add_column":       {tier: domain.TierWrite, tenantScoped: true},
	"update_column":    {tier: domain.TierStructural, tenantScoped: true},
	"remove_column":    {tier: domain.TierStructural, tenantScoped: true},
	"attach_file":      {tier: domain.TierWrite, tenantScoped: true},
	"list_attachments": {tier: domain.TierRead, tenantScoped: true},
}

// TestAuthzMatrix: every registered tool's posture, read from domain.ToolPosture through the
// manifest, equals its pinned row, every pinned tool is registered, and every tool served in
// tools/list is in the manifest.
func TestAuthzMatrix(t *testing.T) {
	h := bootTools(t, servicefake.New().Services())
	registered := map[string]bool{}
	for _, e := range h.Server().Manifest() {
		registered[e.Name] = true
		want, ok := expectedPosture[e.Name]
		if !ok {
			t.Errorf("%s is registered with no row in expectedPosture: pin its tier", e.Name)
			continue
		}
		got := pinnedPosture{tier: e.Tier, tenantScoped: e.TenantScoped, adminOnly: e.AdminOnly}
		if got != want {
			t.Errorf("%s posture = %+v, pinned %+v", e.Name, got, want)
		}
		if p, _ := domain.ToolPosture(e.Name); p.Tier != e.Tier || p.TenantScoped != e.TenantScoped || p.AdminOnly != e.AdminOnly {
			t.Errorf("%s: the manifest carries %+v, domain.ToolPosture %+v", e.Name, e.Posture, p)
		}
	}
	for name := range expectedPosture {
		if !registered[name] {
			t.Errorf("%s is pinned and not registered", name)
		}
	}
	// Every tool served is in the manifest: one registered beside mcp.Tool (mcp.AddTool skips the
	// posture check and the manifest) would be served unpinned and unchecked by the area test.
	listed, err := h.Connect(t, bearerAdmin).ListTools(testCtx(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tl := range listed.Tools {
		if !registered[tl.Name] {
			t.Errorf("%s is served in tools/list and not in the manifest: register it through mcp.Tool", tl.Name)
		}
	}
	t.Logf("%d tools pinned", len(registered))
}
