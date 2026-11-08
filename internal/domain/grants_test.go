package domain

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// Ported from conductor test/unit/grants.test.ts, case for case, with conductor's names. Changed with
// the folds: fail_task's second outcome is BLOCKED (poison), not DEAD_LETTER, so it refuses as
// status:BLOCKED; the two status:CANCELLED cases (cancel_runs, pick_run) are gone, because the
// losing runs are now deleted rather than moved, so cancel_runs lands TODO only and pick_run lands
// where a completion does. One deviation: a task with a priority outside the four is refused under a
// maxPriority grant, where conductor's lookup let it through. Added: the classification invariants
// conductor holds in src/authz/manifest.ts (assertManifestInvariants) and authz-matrix.test.ts.

var grantTask = &GrantTask{BoardID: "B-1", Labels: []string{"ingest"}, Priority: PriorityNormal}

func boolp(b bool) *bool { return &b }

func expectGrant(t *testing.T, g CapabilityGrant, s GrantSubject, want string) {
	t.Helper()
	if got := CheckGrant(g, s); got != want {
		t.Errorf("CheckGrant(%+v, %+v) = %q, want %q", g, s, got, want)
	}
}

func TestCheckGrant(t *testing.T) {
	t.Run("an empty grant restricts nothing", func(t *testing.T) {
		expectGrant(t, CapabilityGrant{}, GrantSubject{Tool: "complete_task", Task: grantTask}, "")
	})
	t.Run("allowlist", func(t *testing.T) {
		g := CapabilityGrant{Tools: []string{"claim_task", "complete_task"}}
		expectGrant(t, g, GrantSubject{Tool: "complete_task"}, "")
		expectGrant(t, g, GrantSubject{Tool: "update_task"}, "tool:update_task")
	})
	t.Run("an empty allowlist admits nothing", func(t *testing.T) {
		expectGrant(t, CapabilityGrant{Tools: []string{}}, GrantSubject{Tool: "claim_task"}, "tool:claim_task")
	})
	t.Run("deny beats allow", func(t *testing.T) {
		expectGrant(t, CapabilityGrant{Tools: []string{"move_task"}, Deny: []string{"move_task"}}, GrantSubject{Tool: "move_task"}, "tool:move_task")
	})
	t.Run("labels against the task, then a labels argument", func(t *testing.T) {
		g := CapabilityGrant{Labels: []string{"ingest"}}
		expectGrant(t, g, GrantSubject{Tool: "complete_task", Task: grantTask}, "")
		expectGrant(t, g, GrantSubject{Tool: "complete_task", Task: &GrantTask{BoardID: "B-1", Labels: []string{"deploy"}, Priority: PriorityNormal}}, "label:ingest")
		expectGrant(t, g, GrantSubject{Tool: "create_task", Labels: []string{"ingest"}}, "")
	})
	t.Run("boards by the task board or a boardId argument", func(t *testing.T) {
		g := CapabilityGrant{Boards: []string{"B-1"}}
		expectGrant(t, g, GrantSubject{Tool: "claim_next", BoardID: "B-1"}, "")
		expectGrant(t, g, GrantSubject{Tool: "claim_next", BoardID: "B-2"}, "board:B-2")
		expectGrant(t, g, GrantSubject{Tool: "complete_task", Task: grantTask}, "")
	})
	t.Run("caps priority", func(t *testing.T) {
		g := CapabilityGrant{MaxPriority: PriorityNormal}
		expectGrant(t, g, GrantSubject{Tool: "complete_task", Task: grantTask}, "")
		expectGrant(t, g, GrantSubject{Tool: "complete_task", Task: &GrantTask{BoardID: "B-1", Priority: PriorityUrgent}}, "priority<=normal")
		// DEVIATION: an unknown priority fails closed; conductor's PRIORITY_RANK lookup compared undefined and let it pass.
		expectGrant(t, g, GrantSubject{Tool: "complete_task", Task: &GrantTask{BoardID: "B-1", Priority: "critical"}}, "priority<=normal")
	})
	t.Run("fails closed when a task-scoped grant cannot resolve the target", func(t *testing.T) {
		expectGrant(t, CapabilityGrant{Boards: []string{"B-1"}}, GrantSubject{Tool: "update_settings"}, "board:*")
		expectGrant(t, CapabilityGrant{Labels: []string{"ingest"}}, GrantSubject{Tool: "update_settings"}, "label:ingest")
		expectGrant(t, CapabilityGrant{MaxPriority: PriorityLow}, GrantSubject{Tool: "update_settings"}, "priority<=low")
	})
	t.Run("toStatus and memory scope arguments", func(t *testing.T) {
		g := CapabilityGrant{Statuses: []Status{Todo, InProgress, Review}, MemoryScopes: []MemoryScope{MemoryScopeTask}}
		expectGrant(t, g, GrantSubject{Tool: "move_task", ToStatus: Review}, "")
		expectGrant(t, g, GrantSubject{Tool: "move_task", ToStatus: Done}, "status:DONE")
		expectGrant(t, WorkerDefaultGrant(), GrantSubject{Tool: "write_memory", MemoryScope: MemoryScopeTask}, "")
		expectGrant(t, WorkerDefaultGrant(), GrantSubject{Tool: "write_memory", MemoryScope: MemoryScopeShared}, "memory-scope:shared")
	})
}

func TestCheckGrantLandingStatus(t *testing.T) {
	working := CapabilityGrant{Statuses: []Status{Todo, InProgress, Review}}
	t.Run("a completion landing DONE is refused, one landing REVIEW permitted", func(t *testing.T) {
		expectGrant(t, working, GrantSubject{Tool: "complete_task", ReviewEnabled: boolp(false)}, "status:DONE")
		expectGrant(t, working, GrantSubject{Tool: "complete_task", ReviewEnabled: boolp(true)}, "")
		expectGrant(t, CapabilityGrant{Statuses: []Status{Done}}, GrantSubject{Tool: "complete_task", ReviewEnabled: boolp(false)}, "")
	})
	t.Run("every tool that moves a task, not just the one naming a status", func(t *testing.T) {
		expectGrant(t, working, GrantSubject{Tool: "claim_task"}, "")
		expectGrant(t, working, GrantSubject{Tool: "block_task"}, "status:BLOCKED")
		expectGrant(t, working, GrantSubject{Tool: "fail_task"}, "status:BLOCKED")
		expectGrant(t, working, GrantSubject{Tool: "reap_stale_leases"}, "status:BLOCKED")
		expectGrant(t, working, GrantSubject{Tool: "orchestrate_subtasks"}, "status:BLOCKED")
		expectGrant(t, working, GrantSubject{Tool: "cancel_runs"}, "")
		expectGrant(t, working, GrantSubject{Tool: "pick_run", ReviewEnabled: boolp(true)}, "")
		expectGrant(t, working, GrantSubject{Tool: "pick_run", ReviewEnabled: boolp(false)}, "status:DONE")
		expectGrant(t, working, GrantSubject{Tool: "cancel_task"}, "status:DONE")
	})
	t.Run("status-inert tools are left alone", func(t *testing.T) {
		for _, tool := range []string{"add_context_item", "add_artifact", "heartbeat_task", "write_memory", "create_task", "delete_task"} {
			expectGrant(t, working, GrantSubject{Tool: tool}, "")
		}
	})
	t.Run("fails closed on what it cannot resolve", func(t *testing.T) {
		expectGrant(t, working, GrantSubject{Tool: "some_future_tool"}, "status:*")
		expectGrant(t, working, GrantSubject{Tool: "complete_task"}, "status:*")
		expectGrant(t, working, GrantSubject{Tool: "move_task", ToColumnID: "C-1"}, "status:*")
		expectGrant(t, working, GrantSubject{Tool: "move_task", ToColumnID: "C-1", ToStatus: Todo}, "")
		expectGrant(t, CapabilityGrant{Tools: []string{"some_future_tool"}}, GrantSubject{Tool: "some_future_tool"}, "")
	})
}

func TestWorkerDefaultGrant(t *testing.T) {
	g := WorkerDefaultGrant()
	for _, tool := range []string{"claim_next", "claim_task", "heartbeat_task", "release_task", "complete_task", "add_context_item", "add_artifact"} {
		expectGrant(t, g, GrantSubject{Tool: tool, ReviewEnabled: boolp(true)}, "")
	}
	expectGrant(t, g, GrantSubject{Tool: "complete_task", ReviewEnabled: boolp(false)}, "status:DONE")
	if slices.Contains(g.Tools, "move_task") {
		t.Fatal("move_task is master tier; the worker grant must not list it")
	}
	for _, tool := range []string{"fail_task", "block_task", "reap_stale_leases", "add_comment", "attach_file", "set_summary"} {
		expectGrant(t, g, GrantSubject{Tool: tool}, "tool:"+tool)
	}
	g.Tools[0] = "changed"
	if WorkerDefaultGrant().Tools[0] != "claim_task" {
		t.Fatal("WorkerDefaultGrant must return a fresh copy")
	}
}

func TestGrantGovernance(t *testing.T) {
	if IsGrantGoverned("get_task") || IsGrantGoverned("list_tasks") || !IsGrantGoverned("complete_task") {
		t.Fatal("reads are never governed, mutations always are")
	}
	// AC4: a tool nobody classified is governed: fail closed, not open.
	if !IsGrantGoverned("some_future_tool") {
		t.Fatal("an unclassified tool must be governed")
	}
	if GrantNeedsTask(CapabilityGrant{Tools: []string{"complete_task"}, Statuses: []Status{Todo}}) ||
		!GrantNeedsTask(CapabilityGrant{Labels: []string{"ingest"}}) ||
		!GrantNeedsTask(CapabilityGrant{Boards: []string{"B-1"}}) ||
		!GrantNeedsTask(CapabilityGrant{MaxPriority: PriorityLow}) {
		t.Fatal("only the task-attribute restrictions need the task")
	}
}

// targetSurface is the 86 tools of the plan's target surface: conductor's 91 in scope, delete_task
// (plan §Fold 2) and cancel_task (ADR-0001), less ADR-0004's 7. Written out, so a tool dropped
// from or added to the posture table fails here.
var targetSurface = []string{"ack_follow_up", "add_artifact", "add_column", "add_comment", "add_context_item", "add_dependency", "add_follow_up", "add_member", "add_subtask", "attach_file", "block_task", "cancel_runs", "cancel_task", "claim_next", "claim_task", "complete_task", "create_board", "create_chronicle_page", "create_integration", "create_project", "create_sprint", "create_task", "delete_chronicle_page", "delete_integration", "delete_project", "delete_task", "detect_anomalies", "disconnect_agent", "edit_chronicle_page", "fail_task", "get_board", "get_my_settings", "get_project", "get_skill_stats", "get_sprint", "get_task", "get_task_context", "grant_capabilities", "handoff_task", "heartbeat_task", "ingest_chronicle_page", "list_agent_sessions", "list_attachments", "list_audit_log", "list_boards", "list_comments", "list_deliveries", "list_follow_ups", "list_integrations", "list_notifications", "list_projects", "list_runs", "list_sprints", "list_subtasks", "list_tasks", "mark_notifications_read", "move_task", "next_task", "open_run", "orchestrate_subtasks", "pick_run", "rank_tasks", "read_delivery_raw", "read_memory", "reap_stale_leases", "recall_memory", "register_agent_profile", "release_task", "remove_column", "remove_dependency", "remove_member", "replay_delivery", "retract_artifact", "rotate_integration_secret", "search_tasks", "send_test_event", "set_my_settings", "set_sprint", "set_summary", "start_runs", "update_column", "update_integration", "update_settings", "update_sprint", "update_task", "write_memory"}

func TestPostureCoversTheTargetSurface(t *testing.T) {
	want := slices.Clone(targetSurface)
	slices.Sort(want)
	if got := ClassifiedTools(); !reflect.DeepEqual(got, want) {
		t.Fatalf("classified tools differ from the target surface\n got %q\nwant %q", got, want)
	}
}

// Governed <=> declares a landing; no exempt tool carries one (conductor assertManifestInvariants).
func TestPostureLandingInvariant(t *testing.T) {
	for _, name := range ClassifiedTools() {
		p, _ := ToolPosture(name)
		if p.GrantExempt == (p.Landing != nil) {
			t.Errorf("%s: grantExempt=%v but landing set=%v", name, p.GrantExempt, p.Landing != nil)
		}
	}
}

func TestGrantSetsPinned(t *testing.T) {
	exempt := []string{
		"create_project", "get_board", "get_my_settings", "get_project", "get_skill_stats", "get_sprint",
		"get_task", "get_task_context", "list_attachments", "list_boards", "list_comments",
		"list_follow_ups", "list_notifications", "list_runs", "list_sprints", "list_subtasks",
		"list_tasks", "mark_notifications_read", "next_task", "rank_tasks", "read_memory",
		"recall_memory", "search_tasks", "set_my_settings",
	}
	if got := GrantExemptTools(); !reflect.DeepEqual(got, exempt) {
		t.Errorf("exempt set\n got %q\nwant %q", got, exempt)
	}
	unscoped := []string{"disconnect_agent", "list_agent_sessions", "list_audit_log", "list_projects"}
	if got := GrantUnscopedTools(); !reflect.DeepEqual(got, unscoped) {
		t.Errorf("unscoped set\n got %q\nwant %q", got, unscoped)
	}
}

func TestPostureFoldedRows(t *testing.T) {
	land := func(tool string, f LandingFacts) []Status {
		p, ok := ToolPosture(tool)
		if !ok || p.Landing == nil {
			t.Fatalf("%s: no landing", tool)
		}
		s, _ := p.Landing(f)
		return s
	}
	if got := land("fail_task", LandingFacts{}); !reflect.DeepEqual(got, []Status{Todo, Blocked}) {
		t.Errorf("fail_task lands %v", got)
	}
	for _, tool := range []string{"create_task", "add_subtask"} { // ADR-0005: a created task lands TODO
		if got := land(tool, LandingFacts{}); !reflect.DeepEqual(got, []Status{Todo}) {
			t.Errorf("%s lands %v, want [TODO]", tool, got)
		}
	}
	if got := land("delete_task", LandingFacts{}); len(got) != 0 {
		t.Errorf("delete_task lands %v", got)
	}
	if p, _ := ToolPosture("delete_task"); p.Tier != TierMaster || !p.TenantScoped {
		t.Errorf("delete_task posture %+v: master tier, tenant scoped", p)
	}
	if p, _ := ToolPosture("list_audit_log"); p.AdminOnly != AdminOnlyOnly {
		t.Errorf("list_audit_log is workspace-admin only: %+v", p)
	}
	for _, gone := range []string{"requeue_task", "get_transitions", "set_transitions"} {
		if _, ok := ToolPosture(gone); ok {
			t.Errorf("%s is not ported and must not be classified", gone)
		}
	}
}

// Every tool has an Effect, the MCP annotations' source. The read and destructive sets are written
// out, so a row typed wrong fails here, and the name prefixes are a second check on the table.
func TestPostureEffect(t *testing.T) {
	reads := []string{
		"get_board", "get_my_settings", "get_project", "get_skill_stats", "get_sprint",
		"get_task", "get_task_context", "list_agent_sessions", "list_attachments", "list_audit_log",
		"list_boards", "list_comments", "list_deliveries", "list_follow_ups", "list_integrations",
		"list_notifications", "list_projects", "list_runs", "list_sprints", "list_subtasks", "list_tasks",
		"next_task", "rank_tasks", "read_delivery_raw", "read_memory", "recall_memory", "search_tasks",
	}
	destructive := []string{
		"cancel_runs", "delete_chronicle_page", "delete_integration", "delete_project", "delete_task",
		"disconnect_agent", "grant_capabilities", "pick_run", "remove_column", "remove_dependency",
		"remove_member", "retract_artifact", "rotate_integration_secret",
	}
	var gotReads, gotDestructive []string
	for _, name := range ClassifiedTools() {
		p, _ := ToolPosture(name)
		switch p.Effect {
		case EffectRead:
			gotReads = append(gotReads, name)
		case EffectDestructive:
			gotDestructive = append(gotDestructive, name)
		case EffectWrite:
		default:
			t.Errorf("%s: no Effect: classify it in toolPosture", name)
		}
		if p.Tier == TierRead && p.Effect != EffectRead {
			t.Errorf("%s: read tier but Effect %d", name, p.Effect)
		}
		for _, prefix := range []string{"get_", "list_", "search_", "recall_", "read_"} {
			if strings.HasPrefix(name, prefix) && p.Effect != EffectRead {
				t.Errorf("%s: a %s tool must be EffectRead, is %d", name, prefix, p.Effect)
			}
		}
		if strings.HasPrefix(name, "delete_") && p.Effect != EffectDestructive {
			t.Errorf("%s: a delete_ tool must be EffectDestructive, is %d", name, p.Effect)
		}
	}
	if !reflect.DeepEqual(gotReads, reads) {
		t.Errorf("read set\n got %q\nwant %q", gotReads, reads)
	}
	if !reflect.DeepEqual(gotDestructive, destructive) {
		t.Errorf("destructive set\n got %q\nwant %q", gotDestructive, destructive)
	}
}

// GrantHiddenTools hides a governed project tool only when every membership's grant refuses it by
// name: one membership with no grant, or one whose allowlist names the tool, keeps it listed. Exempt
// reads, unscoped tools and the per-call restrictions hide nothing.
func TestGrantHiddenTools(t *testing.T) {
	only := CapabilityGrant{Tools: []string{"claim_task"}}
	hidden := GrantHiddenTools([]CapabilityGrant{only})
	for _, name := range []string{"write_memory", "create_task", "grant_capabilities"} {
		if !slices.Contains(hidden, name) {
			t.Errorf("{tools:[claim_task]} lists %s", name)
		}
	}
	for _, name := range []string{"claim_task", "get_task", "list_projects", "create_project", "list_notifications"} {
		if slices.Contains(hidden, name) {
			t.Errorf("{tools:[claim_task]} hides %s (granted, an exempt read or unscoped)", name)
		}
	}
	if got := GrantHiddenTools([]CapabilityGrant{only, {Tools: []string{"claim_task", "write_memory"}}}); slices.Contains(got, "write_memory") ||
		!slices.Contains(got, "create_task") {
		t.Errorf("one membership granting write_memory: hidden %v, want write_memory listed and create_task hidden", got)
	}
	if got := GrantHiddenTools([]CapabilityGrant{only, {}}); got != nil {
		t.Errorf("a membership with no grant: hidden %v, want none", got)
	}
	if got := GrantHiddenTools([]CapabilityGrant{{Deny: []string{"fail_task"}}}); !slices.Equal(got, []string{"fail_task"}) {
		t.Errorf("{deny:[fail_task]} hides %v, want [fail_task]", got)
	}
	if got := GrantHiddenTools([]CapabilityGrant{{Statuses: []Status{Todo}, Labels: []string{"x"}, MemoryScopes: []MemoryScope{MemoryScopeTask}}}); got != nil {
		t.Errorf("per-call restrictions hide %v, want none", got)
	}
	if got := GrantHiddenTools(nil); got != nil {
		t.Errorf("no membership hides %v", got)
	}
}

// CheckGrantEnforceable refuses a deny or tools entry naming no tool (P-438), an exempt tool (P-438)
// or a tool no grant reaches, naming the field, every refused entry and each reason; a grant naming
// governed project tools only is the positive control, as is every governed project tool at once.
func TestCheckGrantEnforceable(t *testing.T) {
	for _, c := range []struct {
		g       CapabilityGrant
		field   string
		tools   []string
		reasons map[string][]string
	}{
		{CapabilityGrant{Deny: []string{"claim_task", "list_projects"}}, "deny", []string{"list_projects"},
			map[string][]string{"unscoped": {"list_projects"}}},
		{CapabilityGrant{Tools: []string{"disconnect_agent", "list_agent_sessions"}}, "tools", []string{"disconnect_agent", "list_agent_sessions"},
			map[string][]string{"unscoped": {"disconnect_agent", "list_agent_sessions"}}},
		{CapabilityGrant{Tools: []string{"no_such_tool"}}, "tools", []string{"no_such_tool"},
			map[string][]string{"unknown": {"no_such_tool"}}},
		{CapabilityGrant{Deny: []string{"add_coment", "add_comment", "get_task", "Claim_Task"}}, "deny", []string{"add_coment", "get_task", "Claim_Task"},
			map[string][]string{"unknown": {"add_coment", "Claim_Task"}, "exempt": {"get_task"}}},
		// deny is judged first; tools' typo is named once deny is clean.
		{CapabilityGrant{Deny: []string{"get_board"}, Tools: []string{"claim_taks"}}, "deny", []string{"get_board"},
			map[string][]string{"exempt": {"get_board"}}},
		{CapabilityGrant{Deny: []string{"add_comment"}, Tools: []string{"claim_task", "claim_taks", "list_audit_log"}}, "tools", []string{"claim_taks", "list_audit_log"},
			map[string][]string{"unknown": {"claim_taks"}, "unscoped": {"list_audit_log"}}},
	} {
		e, ok := AsBatonDeckError(CheckGrantEnforceable(c.g))
		if !ok || e.Code != CodeValidation || e.Details["field"] != c.field || !reflect.DeepEqual(e.Details["tools"], c.tools) {
			t.Errorf("CheckGrantEnforceable(%+v) = %v, want VALIDATION on %s naming %v", c.g, e, c.field, c.tools)
			continue
		}
		for _, reason := range []string{"unknown", "exempt", "unscoped"} {
			got, _ := e.Details[reason].([]string)
			if !slices.Equal(got, c.reasons[reason]) {
				t.Errorf("CheckGrantEnforceable(%+v) details.%s = %v, want %v", c.g, reason, got, c.reasons[reason])
			}
		}
		for _, name := range c.tools {
			if !strings.Contains(e.Message, name) {
				t.Errorf("CheckGrantEnforceable(%+v) message %q does not name %s", c.g, e.Message, name)
			}
		}
	}
	if err := CheckGrantEnforceable(WorkerDefaultGrant()); err != nil {
		t.Errorf("the worker default grant: %v", err)
	}
	governed := sortedTools(func(_ string, p Posture) bool { return p.TenantScoped && !p.GrantExempt })
	if err := CheckGrantEnforceable(CapabilityGrant{Tools: governed, Deny: governed}); err != nil {
		t.Errorf("every governed project tool: %v", err)
	}
}

// A grant stored before CheckGrantEnforceable judged names still decides calls as it did (P-438): an
// unknown name in deny restricts nothing, and an allowlist holding one admits exactly the governed
// tools it also lists.
func TestStoredGrantWithUnknownNamesStillDecides(t *testing.T) {
	deny := CapabilityGrant{Deny: []string{"add_coment"}}
	if got := CheckGrant(deny, GrantSubject{Tool: "add_comment"}); got != "" {
		t.Errorf("deny [add_coment] refused add_comment: %q", got)
	}
	allow := CapabilityGrant{Tools: []string{"claim_taks", "heartbeat_task"}}
	if got := CheckGrant(allow, GrantSubject{Tool: "heartbeat_task"}); got != "" {
		t.Errorf("tools [claim_taks heartbeat_task] refused heartbeat_task: %q", got)
	}
	if got := CheckGrant(allow, GrantSubject{Tool: "claim_task"}); got != "tool:claim_task" {
		t.Errorf("tools [claim_taks heartbeat_task] on claim_task: %q, want tool:claim_task", got)
	}
}
