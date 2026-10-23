package domain

import (
	"reflect"
	"slices"
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

// targetSurface is the 93 tools of the plan's target surface: conductor's 91 in scope, delete_task
// (plan §Fold 2) and cancel_task (ADR-0001). Written out, so a tool dropped from or added to the
// posture table fails here.
var targetSurface = []string{"ack_follow_up", "add_artifact", "add_column", "add_comment", "add_context_item", "add_dependency", "add_follow_up", "add_member", "add_subtask", "attach_file", "block_task", "cancel_runs", "cancel_task", "claim_next", "claim_task", "complete_task", "create_board", "create_chronicle_page", "create_cli_token", "create_integration", "create_project", "create_sprint", "create_task", "delete_chronicle_page", "delete_integration", "delete_project", "delete_task", "detect_anomalies", "disconnect_agent", "edit_chronicle_page", "fail_task", "find_tools", "get_board", "get_my_settings", "get_project", "get_skill_stats", "get_sprint", "get_task", "get_task_context", "get_tool_schema", "grant_capabilities", "handoff_task", "heartbeat_task", "ingest_chronicle_page", "invoke_tool", "list_agent_sessions", "list_attachments", "list_audit_log", "list_boards", "list_cli_tokens", "list_comments", "list_deliveries", "list_follow_ups", "list_integrations", "list_notifications", "list_projects", "list_runs", "list_sprints", "list_subtasks", "list_tasks", "mark_notifications_read", "move_task", "next_task", "open_run", "orchestrate_subtasks", "pick_run", "prune_agent_sessions", "rank_tasks", "read_delivery_raw", "read_memory", "reap_stale_leases", "recall_memory", "register_agent_profile", "release_task", "remove_column", "remove_dependency", "remove_member", "replay_delivery", "retract_artifact", "revoke_cli_token", "rotate_integration_secret", "search_tasks", "send_test_event", "set_my_settings", "set_sprint", "set_summary", "start_runs", "update_column", "update_integration", "update_settings", "update_sprint", "update_task", "write_memory"}

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
		"create_cli_token", "create_project", "find_tools", "get_board", "get_my_settings", "get_project",
		"get_skill_stats", "get_sprint", "get_task", "get_task_context", "get_tool_schema", "invoke_tool",
		"list_attachments", "list_boards", "list_cli_tokens", "list_comments", "list_follow_ups",
		"list_notifications", "list_runs", "list_sprints", "list_subtasks", "list_tasks",
		"mark_notifications_read", "next_task", "rank_tasks", "read_memory", "recall_memory",
		"revoke_cli_token", "search_tasks", "set_my_settings",
	}
	if got := GrantExemptTools(); !reflect.DeepEqual(got, exempt) {
		t.Errorf("exempt set\n got %q\nwant %q", got, exempt)
	}
	unscoped := []string{"disconnect_agent", "list_agent_sessions", "list_audit_log", "list_projects", "prune_agent_sessions"}
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
