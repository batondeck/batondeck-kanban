package tools

import (
	"fmt"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/auth/authfake"
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicefake"
)

func panicOf(f func()) (p any) {
	defer func() { p = recover() }()
	f()
	return nil
}

// TestRegisterRefusesUnknownAndDuplicateAreas: a hook for an area not in areas, or a second hook
// for one, panics naming it; the registered areas' hooks are the positive control.
func TestRegisterRefusesUnknownAndDuplicateAreas(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"no_such_area", "area no_such_area is not in areas"},
		{"projects", "area projects registered twice"},
	} {
		p := panicOf(func() { register(c.name, func(*mcp.Server, service.Services) {}) })
		if !strings.Contains(fmt.Sprint(p), c.want) {
			t.Errorf("register(%q): panic %v, want %q", c.name, p, c.want)
		}
	}
	for _, a := range []string{"projects", "boards"} {
		if _, ok := hooks[a]; !ok {
			t.Errorf("area %s has no hook", a)
		}
	}
}

// TestRegisterAddsEveryAreasTools: Register adds the projects, boards, tasks, lifecycle, context,
// media, search, artifacts, comments, follow-ups, chronicle, integrations, deliveries, runs,
// sprints, agents, me and audit tools, each under its area.
func TestRegisterAddsEveryAreasTools(t *testing.T) {
	svc := servicefake.New().Services()
	s := mcp.New(mcp.Options{Version: "test", Resolver: authfake.Resolver{}, Services: svc})
	Register(s, svc)
	got := map[string][]string{}
	for _, e := range s.Manifest() {
		got[e.Area] = append(got[e.Area], e.Name)
	}
	want := map[string]string{
		"projects": "add_member create_project delete_project get_project list_projects remove_member update_settings",
		"boards":   "add_column create_board get_board list_boards remove_column update_column",
		"tasks":    "create_task delete_task get_task list_tasks move_task update_task",
		"lifecycle": "block_task cancel_task claim_next claim_task complete_task fail_task handoff_task heartbeat_task " +
			"next_task reap_stale_leases release_task",
		"context": "add_context_item add_dependency add_subtask get_task_context list_subtasks read_memory recall_memory " +
			"remove_dependency set_summary write_memory",
		"media":          "attach_file list_attachments",
		"search":         "search_tasks",
		"integrations":   "create_integration delete_integration list_integrations rotate_integration_secret update_integration",
		"deliveries":     "list_deliveries read_delivery_raw replay_delivery send_test_event",
		"chronicle":      "create_chronicle_page delete_chronicle_page edit_chronicle_page ingest_chronicle_page",
		"artifacts":      "add_artifact retract_artifact",
		"comments":       "add_comment list_comments",
		"followups":      "ack_follow_up add_follow_up list_follow_ups",
		"runs":           "cancel_runs list_runs open_run pick_run start_runs",
		"sprints":        "create_sprint get_sprint list_sprints set_sprint update_sprint",
		"notifications":  "list_notifications mark_notifications_read",
		"agents_profile": "get_skill_stats register_agent_profile",
		"anomalies":      "detect_anomalies",
		"rank":           "orchestrate_subtasks rank_tasks",
		"agents":         "disconnect_agent list_agent_sessions",
		"me":             "get_my_settings set_my_settings",
		"audit":          "list_audit_log",
	}
	if len(got) != len(want) {
		t.Errorf("areas registered = %v, want projects, boards, tasks, lifecycle, context, media, search, artifacts, comments, followups, chronicle, integrations, deliveries, runs, sprints, notifications, agents_profile, anomalies and rank", got)
	}
	for area, names := range want {
		if strings.Join(got[area], " ") != names {
			t.Errorf("area %s registered %v, want %s", area, got[area], names)
		}
	}
}
