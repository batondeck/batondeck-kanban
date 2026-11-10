package domain

import (
	"sort"
	"strings"
)

// AuditDescriptor is what one sensitive call records, besides the actor, agent, projectId and time
// the writer stamps on the row.
type AuditDescriptor struct {
	// Action is a stable dotted name; part of the audit log's read API.
	Action string
	// Subject is who or what the action targets, "" when it targets only its own scope.
	Subject string
	// Data is a curated payload: only named arguments, never the raw call.
	Data map[string]any
}

// sensitiveActions is the one place a sensitive tool is declared, and the opposite polarity to
// grants: a tool absent here is not audited, because auditing every call would bury the actions that
// change who may do what, or destroy data. Conductor's rows for the tools in scope, with
// update_settings no longer recording transitionTable (the table is fixed), set_transitions gone with
// its tool, and delete_task and delete_chronicle_page added.
var sensitiveActions = map[string]func(args map[string]any) AuditDescriptor{
	// Project membership and privilege.
	"add_member": func(a map[string]any) AuditDescriptor {
		return AuditDescriptor{Action: "member.added", Subject: canonID(a, "identityId"), Data: pick(a, "role")}
	},
	"remove_member": func(a map[string]any) AuditDescriptor {
		return AuditDescriptor{Action: "member.removed", Subject: canonID(a, "identityId"), Data: map[string]any{}}
	},
	"grant_capabilities": func(a map[string]any) AuditDescriptor {
		return AuditDescriptor{Action: "capabilities.granted", Subject: canonID(a, "identityId"), Data: pick(a, "scope")}
	},

	// Destructive. A deleted project's or task's own event log goes with it, so this row is the trace.
	"delete_project": func(a map[string]any) AuditDescriptor {
		return AuditDescriptor{Action: "project.deleted", Subject: str(a, "projectId"), Data: map[string]any{}}
	},
	"delete_task": func(a map[string]any) AuditDescriptor {
		return AuditDescriptor{Action: "task.deleted", Subject: str(a, "taskId"), Data: map[string]any{}}
	},
	// A deleted Chronicle page keeps no version, so this row is the trace; the row carries the project.
	"delete_chronicle_page": func(a map[string]any) AuditDescriptor {
		return AuditDescriptor{Action: "chronicle.page_deleted", Subject: str(a, "pageId"), Data: map[string]any{}}
	},

	// Configuration that changes who can do what. color is cosmetic and not recorded.
	"update_settings": func(a map[string]any) AuditDescriptor {
		return AuditDescriptor{Action: "settings.updated", Subject: str(a, "projectId"), Data: pick(a,
			"reviewEnabled", "quotaEnabled", "txnCap", "artifactPolicy", "selfApprovalPolicy",
			"defaultReviewer", "selectionWeights", "agingCeilingMs", "capabilityAliases", "retryPolicy",
			"anomalyPolicy", "maxRunsPerTask")}
	},

	// Revocation, keyed on the identity: the agent rides in data so a search by account finds it.
	"disconnect_agent": func(a map[string]any) AuditDescriptor {
		return AuditDescriptor{Action: "agent.disconnected", Subject: canonID(a, "identityId"), Data: pick(a, "agentId")}
	},

	// Oncall integrations: each is an inbound write credential for a board. Ids and shapes only.
	"create_integration": func(a map[string]any) AuditDescriptor {
		return AuditDescriptor{Action: "integration.created", Data: pick(a, "projectId", "name", "source", "boardId")}
	},
	"update_integration": func(a map[string]any) AuditDescriptor {
		return AuditDescriptor{Action: "integration.updated", Data: pick(a, "projectId", "integrationId")}
	},
	"rotate_integration_secret": func(a map[string]any) AuditDescriptor {
		return AuditDescriptor{Action: "integration.secret_rotated", Data: pick(a, "projectId", "integrationId", "revokePrevious")}
	},
	"delete_integration": func(a map[string]any) AuditDescriptor {
		return AuditDescriptor{Action: "integration.deleted", Data: pick(a, "projectId", "integrationId")}
	},
}

// AuditedTools returns the audited tool names, sorted.
func AuditedTools() []string {
	out := make([]string, 0, len(sensitiveActions))
	for name := range sensitiveActions {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// DescribeAudit returns what a call to tool records, or false when the tool is not audited.
func DescribeAudit(tool string, args map[string]any) (AuditDescriptor, bool) {
	describe, ok := sensitiveActions[tool]
	if !ok {
		return AuditDescriptor{}, false
	}
	return describe(args), true
}

// str reads a non-empty string argument without trusting its type.
func str(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return s
}

// canonID canonicalises an identity-valued subject (CanonIdentity), so the row matches a query by the
// identity the tool actually acted on. Project and task ids are opaque and case-significant and go
// through str instead.
func canonID(args map[string]any, key string) string {
	return CanonIdentity(str(args, key))
}

// CanonIdentity is the form an identity-valued audit subject is stored in: trimmed, lower-cased. The
// ids the API hands out are "U-…", so list_audit_log matches its subject filter both as given and in
// this form (a project or task id keeps its upper-case prefix, so its canonical form matches no project or task row).
func CanonIdentity(id string) string {
	return strings.ToLower(strings.TrimSpace(id))
}

// pick copies only the named arguments that are present (a JSON null counts as present).
func pick(args map[string]any, keys ...string) map[string]any {
	out := map[string]any{}
	for _, k := range keys {
		if v, ok := args[k]; ok {
			out[k] = v
		}
	}
	return out
}
