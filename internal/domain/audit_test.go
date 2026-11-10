package domain

import (
	"reflect"
	"testing"
)

// Ported from conductor test/unit/audit-coverage.test.ts. Dropped with their tools: decide_access,
// set_billing, set_feature_flags (the feature_flags.set case), get_marketplace_key and the plugin
// studio rows (out of scope), set_transitions (the table is fixed). "Every audited name is a
// registered tool" becomes "every audited name is classified": there is no tool registry yet, and the
// posture table is the target surface.

func TestAuditCoveragePinned(t *testing.T) {
	want := []string{
		"add_member", "create_integration", "delete_chronicle_page", "delete_integration", "delete_project",
		"delete_task", "disconnect_agent", "grant_capabilities", "remove_member", "rotate_integration_secret",
		"update_integration", "update_settings",
	}
	if got := AuditedTools(); !reflect.DeepEqual(got, want) {
		t.Fatalf("audited tools\n got %q\nwant %q", got, want)
	}
}

func TestAuditedToolsAreClassified(t *testing.T) {
	for _, tool := range AuditedTools() {
		if _, ok := ToolPosture(tool); !ok {
			t.Errorf("%s is audited but not a tool of the target surface: it would record nothing", tool)
		}
	}
}

func TestDescribeAudit(t *testing.T) {
	// Negative control: an ordinary write or read records nothing.
	for _, tool := range []string{"create_task", "get_task", "move_task", "cancel_task"} {
		if _, ok := DescribeAudit(tool, map[string]any{"projectId": "P-1", "taskId": "T-1"}); ok {
			t.Errorf("%s must not be audited", tool)
		}
	}
	d, ok := DescribeAudit("delete_task", map[string]any{"projectId": "P-1", "taskId": "T-7", "version": 3})
	if !ok || !reflect.DeepEqual(d, AuditDescriptor{Action: "task.deleted", Subject: "T-7", Data: map[string]any{}}) {
		t.Fatalf("delete_task: %+v %v", d, ok)
	}
	// A chronicle page's delete removes every version and body irreversibly: this row is its trace.
	d, ok = DescribeAudit("delete_chronicle_page", map[string]any{"projectId": "P-1", "pageId": "CP-abc"})
	if !ok || !reflect.DeepEqual(d, AuditDescriptor{Action: "chronicle.page_deleted", Subject: "CP-abc", Data: map[string]any{}}) {
		t.Fatalf("delete_chronicle_page: %+v %v", d, ok)
	}
	d, _ = DescribeAudit("remove_member", map[string]any{"projectId": "P-1", "identityId": " Bob@X.com "})
	if d.Action != "member.removed" || d.Subject != "bob@x.com" {
		t.Fatalf("an identity subject is canonicalised as the handler does: %+v", d)
	}
	d, _ = DescribeAudit("delete_project", map[string]any{"projectId": "P-AbC"})
	if d.Subject != "P-AbC" {
		t.Fatalf("a project id is opaque and keeps its case: %+v", d)
	}
}

func TestAuditCuratesThePayload(t *testing.T) {
	d, _ := DescribeAudit("add_member", map[string]any{
		"projectId": "P-1", "identityId": "u@x.com", "role": "admin", "somethingSensitiveAddedLater": "must-not-be-captured",
	})
	if !reflect.DeepEqual(d.Data, map[string]any{"role": "admin"}) {
		t.Fatalf("only named arguments are copied: %v", d.Data)
	}
	d, _ = DescribeAudit("update_settings", map[string]any{
		"projectId": "P-1", "reviewEnabled": false, "retryPolicy": map[string]any{"poisonAfterAttempts": 3},
		"transitionTable": map[string]any{"TODO": []string{"DONE"}}, "color": "#fff",
	})
	want := map[string]any{"reviewEnabled": false, "retryPolicy": map[string]any{"poisonAfterAttempts": 3}}
	if d.Action != "settings.updated" || !reflect.DeepEqual(d.Data, want) {
		t.Fatalf("update_settings records the policy fields, not transitionTable or color: %+v", d)
	}
	// checkpointPolicy is enforced by complete_task; tokenPolicy and maxTicketTokens are policy too.
	d, _ = DescribeAudit("update_settings", map[string]any{
		"projectId": "P-1", "checkpointPolicy": "off", "tokenPolicy": "off", "maxTicketTokens": 1000000,
	})
	want = map[string]any{"checkpointPolicy": "off", "tokenPolicy": "off", "maxTicketTokens": 1000000}
	if !reflect.DeepEqual(d.Data, want) {
		t.Fatalf("update_settings records checkpointPolicy, tokenPolicy and maxTicketTokens: %+v", d)
	}
}
