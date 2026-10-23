package mcp_test

import (
	"slices"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	bk "github.com/batondeck/batondeck-kanban/internal/mcp"
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
	"create_task":      {tier: domain.TierMaster, tenantScoped: true},
	"get_task":         {tier: domain.TierRead, tenantScoped: true},
	"update_task":      {tier: domain.TierMaster, tenantScoped: true},
	"move_task":        {tier: domain.TierMaster, tenantScoped: true},
	"list_tasks":       {tier: domain.TierRead, tenantScoped: true},
	"delete_task":      {tier: domain.TierMaster, tenantScoped: true},
	"attach_file":      {tier: domain.TierWrite, tenantScoped: true},
	"list_attachments": {tier: domain.TierRead, tenantScoped: true},
	"search_tasks":     {tier: domain.TierRead, tenantScoped: true},
	// The lease-guarded outcomes and cancel_task carry no tier: the held lease (or, for
	// cancel_task, the master role or the live lease) is the guard.
	"claim_task":        {tier: domain.TierWrite, tenantScoped: true},
	"claim_next":        {tier: domain.TierWrite, tenantScoped: true},
	"heartbeat_task":    {tier: "", tenantScoped: true},
	"release_task":      {tier: "", tenantScoped: true},
	"next_task":         {tier: domain.TierRead, tenantScoped: true},
	"complete_task":     {tier: "", tenantScoped: true},
	"block_task":        {tier: "", tenantScoped: true},
	"fail_task":         {tier: "", tenantScoped: true},
	"reap_stale_leases": {tier: domain.TierWrite, tenantScoped: true},
	"handoff_task":      {tier: "", tenantScoped: true},
	"cancel_task":       {tier: "", tenantScoped: true},
	// recall_memory is not tenant-scoped: agent_global answers with no projectId.
	"add_context_item":  {tier: domain.TierWrite, tenantScoped: true},
	"get_task_context":  {tier: domain.TierRead, tenantScoped: true},
	"set_summary":       {tier: domain.TierWrite, tenantScoped: true},
	"write_memory":      {tier: domain.TierWrite, tenantScoped: true},
	"read_memory":       {tier: domain.TierRead, tenantScoped: true},
	"recall_memory":     {tier: "", tenantScoped: false},
	"add_dependency":    {tier: domain.TierMaster, tenantScoped: true},
	"remove_dependency": {tier: domain.TierMaster, tenantScoped: true},
	"add_subtask":       {tier: domain.TierMaster, tenantScoped: true},
	"list_subtasks":     {tier: domain.TierRead, tenantScoped: true},
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

// landingCase is one call's facts and the statuses the tool must say it lands its task in; ok false
// is a landing a statuses grant cannot decide from the call.
type landingCase struct {
	facts    domain.LandingFacts
	statuses []domain.Status
	ok       bool
}

// expectedLanding pins where each lifecycle tool that moves a task leaves it (domain.Posture.Landing,
// which a capability grant's statuses are judged against): five states, so a release, a reap, a
// retry and a handoff land on TODO (ADR-0005). next_task moves nothing and is grant-exempt.
var expectedLanding = func() map[string][]landingCase {
	on, off := true, false
	fixed := func(s ...domain.Status) []landingCase {
		return []landingCase{{statuses: s, ok: true}}
	}
	return map[string][]landingCase{
		"claim_task":     fixed(domain.InProgress),
		"claim_next":     fixed(domain.InProgress),
		"heartbeat_task": fixed(),
		"release_task":   fixed(domain.Todo),
		"complete_task": {
			{facts: domain.LandingFacts{ReviewEnabled: &on}, statuses: []domain.Status{domain.Review}, ok: true},
			{facts: domain.LandingFacts{ReviewEnabled: &off}, statuses: []domain.Status{domain.Done}, ok: true},
			{facts: domain.LandingFacts{}, ok: false},
		},
		"block_task":        fixed(domain.Blocked),
		"fail_task":         fixed(domain.Todo, domain.Blocked),
		"reap_stale_leases": fixed(domain.Todo, domain.Blocked),
		"handoff_task":      fixed(domain.Todo),
		"cancel_task":       fixed(domain.Done),
	}
}()

// TestLifecycleLandingPinned: each lifecycle tool's landing, read through the manifest, equals its
// pin; next_task is grant-exempt with no landing; and no landing of any registered tool names a
// status outside the five (no READY, no BACKLOG).
func TestLifecycleLandingPinned(t *testing.T) {
	h := bootTools(t, servicefake.New().Services())
	byName := map[string]bk.ManifestEntry{}
	for _, e := range h.Server().Manifest() {
		byName[e.Name] = e
	}
	for name, cases := range expectedLanding {
		e, ok := byName[name]
		if !ok || e.Landing == nil || e.GrantExempt {
			t.Errorf("%s: registered %v, landing set %v, exempt %v; want a governed tool with a landing", name, ok, ok && e.Landing != nil, e.GrantExempt)
			continue
		}
		for _, c := range cases {
			got, ok := e.Landing(c.facts)
			if ok != c.ok || !slices.Equal(got, c.statuses) {
				t.Errorf("%s with %+v lands %v (decidable %v), pinned %v (%v)", name, c.facts, got, ok, c.statuses, c.ok)
			}
		}
	}
	if e, ok := byName["next_task"]; !ok || !e.GrantExempt || e.Landing != nil {
		t.Errorf("next_task: registered %v, exempt %v, landing set %v; want grant-exempt with no landing", ok, e.GrantExempt, e.Landing != nil)
	}
	on, off := true, false
	probes := []domain.LandingFacts{{}, {ReviewEnabled: &on}, {ReviewEnabled: &off}}
	for _, s := range domain.AllStatuses() {
		probes = append(probes, domain.LandingFacts{ToStatus: s})
	}
	seen := map[domain.Status]bool{}
	for _, e := range byName {
		if e.Landing == nil {
			continue
		}
		for _, f := range probes {
			got, _ := e.Landing(f)
			for _, s := range got {
				seen[s] = true
				if !slices.Contains(domain.AllStatuses(), s) || s == "READY" || s == "BACKLOG" {
					t.Errorf("%s lands %q, not one of the five states %v", e.Name, s, domain.AllStatuses())
				}
			}
		}
	}
	t.Logf("%d lifecycle landings pinned; statuses landed on by the registered tools: %v", len(expectedLanding), seen)
}
