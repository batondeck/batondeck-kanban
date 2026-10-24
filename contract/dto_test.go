package contract

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/dtoschema"
)

// dtoTools maps each tool to the service method whose request and response types are its input and
// output schemas: the 42 CORE tools of plan §Target surface, cancel_task (ADR-0001, after the 42
// was counted), list_agent_sessions and disconnect_agent (service.Sessions), get_my_settings and set_my_settings
// (service.Me), list_audit_log (service.Audit, admin-tools.json), the 4 chronicle tools
// (R2.6), the nine on-call tools (service.Oncall, R2.8) and the ten runs and sprints tools (R2.2).
var dtoTools = []struct {
	tool   string
	iface  reflect.Type
	method string
}{
	{"create_project", reflect.TypeFor[service.Projects](), "Create"},
	{"get_project", reflect.TypeFor[service.Projects](), "Get"},
	{"list_projects", reflect.TypeFor[service.Projects](), "List"},
	{"update_settings", reflect.TypeFor[service.Projects](), "UpdateSettings"},
	{"delete_project", reflect.TypeFor[service.Projects](), "Delete"},
	{"add_member", reflect.TypeFor[service.Projects](), "AddMember"},
	{"remove_member", reflect.TypeFor[service.Projects](), "RemoveMember"},
	{"create_board", reflect.TypeFor[service.Boards](), "Create"},
	{"list_boards", reflect.TypeFor[service.Boards](), "List"},
	{"get_board", reflect.TypeFor[service.Boards](), "Get"},
	{"add_column", reflect.TypeFor[service.Boards](), "AddColumn"},
	{"update_column", reflect.TypeFor[service.Boards](), "UpdateColumn"},
	{"remove_column", reflect.TypeFor[service.Boards](), "RemoveColumn"},
	{"create_task", reflect.TypeFor[service.Tasks](), "Create"},
	{"get_task", reflect.TypeFor[service.Tasks](), "Get"},
	{"update_task", reflect.TypeFor[service.Tasks](), "Update"},
	{"move_task", reflect.TypeFor[service.Tasks](), "Move"},
	{"list_tasks", reflect.TypeFor[service.Tasks](), "List"},
	{"delete_task", reflect.TypeFor[service.Tasks](), "Delete"},
	{"claim_task", reflect.TypeFor[service.Lifecycle](), "Claim"},
	{"claim_next", reflect.TypeFor[service.Lifecycle](), "ClaimNext"},
	{"heartbeat_task", reflect.TypeFor[service.Lifecycle](), "Heartbeat"},
	{"release_task", reflect.TypeFor[service.Lifecycle](), "Release"},
	{"next_task", reflect.TypeFor[service.Lifecycle](), "Next"},
	{"complete_task", reflect.TypeFor[service.Lifecycle](), "Complete"},
	{"block_task", reflect.TypeFor[service.Lifecycle](), "Block"},
	{"fail_task", reflect.TypeFor[service.Lifecycle](), "Fail"},
	{"reap_stale_leases", reflect.TypeFor[service.Lifecycle](), "Reap"},
	{"handoff_task", reflect.TypeFor[service.Lifecycle](), "Handoff"},
	{"cancel_task", reflect.TypeFor[service.Lifecycle](), "Cancel"},
	{"add_context_item", reflect.TypeFor[service.Context](), "AddItem"},
	{"get_task_context", reflect.TypeFor[service.Context](), "GetTaskContext"},
	{"set_summary", reflect.TypeFor[service.Context](), "SetSummary"},
	{"write_memory", reflect.TypeFor[service.Context](), "WriteMemory"},
	{"read_memory", reflect.TypeFor[service.Context](), "ReadMemory"},
	{"recall_memory", reflect.TypeFor[service.Context](), "RecallMemory"},
	{"add_dependency", reflect.TypeFor[service.Context](), "AddDependency"},
	{"remove_dependency", reflect.TypeFor[service.Context](), "RemoveDependency"},
	{"add_subtask", reflect.TypeFor[service.Context](), "AddSubtask"},
	{"list_subtasks", reflect.TypeFor[service.Context](), "ListSubtasks"},
	{"attach_file", reflect.TypeFor[service.Media](), "AttachFile"},
	{"list_attachments", reflect.TypeFor[service.Media](), "ListAttachments"},
	{"search_tasks", reflect.TypeFor[service.Media](), "SearchTasks"},
	{"list_agent_sessions", reflect.TypeFor[service.Sessions](), "ListAgentSessions"},
	{"disconnect_agent", reflect.TypeFor[service.Sessions](), "DisconnectAgent"},
	{"get_my_settings", reflect.TypeFor[service.Me](), "GetMySettings"},
	{"set_my_settings", reflect.TypeFor[service.Me](), "SetMySettings"},
	{"list_audit_log", reflect.TypeFor[service.Audit](), "ListAuditLog"},
	{"add_artifact", reflect.TypeFor[service.Artifacts](), "AddArtifact"},
	{"retract_artifact", reflect.TypeFor[service.Artifacts](), "RetractArtifact"},
	{"add_comment", reflect.TypeFor[service.Comments](), "AddComment"},
	{"list_comments", reflect.TypeFor[service.Comments](), "ListComments"},
	{"add_follow_up", reflect.TypeFor[service.FollowUps](), "AddFollowUp"},
	{"ack_follow_up", reflect.TypeFor[service.FollowUps](), "AckFollowUp"},
	{"list_follow_ups", reflect.TypeFor[service.FollowUps](), "ListFollowUps"},
	{"create_chronicle_page", reflect.TypeFor[service.Chronicle](), "CreatePage"},
	{"ingest_chronicle_page", reflect.TypeFor[service.Chronicle](), "IngestPage"},
	{"edit_chronicle_page", reflect.TypeFor[service.Chronicle](), "EditPage"},
	{"delete_chronicle_page", reflect.TypeFor[service.Chronicle](), "DeletePage"},
	{"create_integration", reflect.TypeFor[service.Oncall](), "CreateIntegration"},
	{"update_integration", reflect.TypeFor[service.Oncall](), "UpdateIntegration"},
	{"delete_integration", reflect.TypeFor[service.Oncall](), "DeleteIntegration"},
	{"list_integrations", reflect.TypeFor[service.Oncall](), "ListIntegrations"},
	{"rotate_integration_secret", reflect.TypeFor[service.Oncall](), "RotateIntegrationSecret"},
	{"list_deliveries", reflect.TypeFor[service.Oncall](), "ListDeliveries"},
	{"read_delivery_raw", reflect.TypeFor[service.Oncall](), "ReadDeliveryRaw"},
	{"replay_delivery", reflect.TypeFor[service.Oncall](), "ReplayDelivery"},
	{"send_test_event", reflect.TypeFor[service.Oncall](), "SendTestEvent"},
	{"start_runs", reflect.TypeFor[service.Runs](), "Start"},
	{"open_run", reflect.TypeFor[service.Runs](), "Open"},
	{"pick_run", reflect.TypeFor[service.Runs](), "Pick"},
	{"cancel_runs", reflect.TypeFor[service.Runs](), "Cancel"},
	{"list_runs", reflect.TypeFor[service.Runs](), "List"},
	{"create_sprint", reflect.TypeFor[service.Sprints](), "Create"},
	{"update_sprint", reflect.TypeFor[service.Sprints](), "Update"},
	{"set_sprint", reflect.TypeFor[service.Sprints](), "Set"},
	{"get_sprint", reflect.TypeFor[service.Sprints](), "Get"},
	{"list_sprints", reflect.TypeFor[service.Sprints](), "List"},
}

// toolShaped are the interfaces every method of which is one tool: a method added to one without a
// row in dtoTools fails TestDTOCanonical.
var toolShaped = []reflect.Type{
	reflect.TypeFor[service.Projects](), reflect.TypeFor[service.Boards](), reflect.TypeFor[service.Tasks](),
	reflect.TypeFor[service.Lifecycle](), reflect.TypeFor[service.Context](), reflect.TypeFor[service.Media](),
	reflect.TypeFor[service.Artifacts](), reflect.TypeFor[service.Comments](), reflect.TypeFor[service.FollowUps](),
	reflect.TypeFor[service.Oncall](), reflect.TypeFor[service.Runs](), reflect.TypeFor[service.Sprints](),
	reflect.TypeFor[service.Me](), reflect.TypeFor[service.Audit](),
}

// dtoSchema is typ's schema as internal/service/dtoschema builds it for the tool registration,
// decoded, so the comparison is of exactly what the server serves.
func dtoSchema(t *testing.T, typ reflect.Type) any {
	t.Helper()
	b, err := json.Marshal(dtoschema.For(typ))
	if err != nil {
		t.Fatal(err)
	}
	var m any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestDTOCanonical: the request and response types of every tool-shaped service method,
// inferred by jsonschema-go and canonicalised, equal contract/expected.json's input and output
// for that tool, exactly.
func TestDTOCanonical(t *testing.T) {
	want := committed(t)
	mapped := map[string]bool{}
	got := Surface{Tools: map[string]*Tool{}}
	sub := Surface{Tools: map[string]*Tool{}}
	for _, d := range dtoTools {
		m, ok := d.iface.MethodByName(d.method)
		if !ok {
			t.Fatalf("%s has no method %s", d.iface, d.method)
		}
		mapped[d.iface.String()+"."+d.method] = true
		mt := m.Type
		if mt.NumIn() != 3 || mt.NumOut() != 2 {
			t.Fatalf("%s.%s is not (ctx, domain.Caller, Req) (Resp, error)", d.iface, d.method)
		}
		if mt.In(1) != reflect.TypeFor[domain.Caller]() || mt.Out(1) != reflect.TypeFor[error]() {
			t.Fatalf("%s.%s is not (ctx, domain.Caller, Req) (Resp, error)", d.iface, d.method)
		}
		w, ok := want.Tools[d.tool]
		if !ok {
			t.Fatalf("%s is not in expected.json", d.tool)
		}
		sub.Tools[d.tool] = w
		in, err := Canonicalize(dtoSchema(t, mt.In(2)))
		if err != nil {
			t.Fatalf("%s input (%s): %v", d.tool, mt.In(2), err)
		}
		out, err := Canonicalize(dtoSchema(t, mt.Out(0)))
		if err != nil {
			t.Fatalf("%s output (%s): %v", d.tool, mt.Out(0), err)
		}
		got.Tools[d.tool] = &Tool{Input: in, Output: out}
	}
	for _, iface := range toolShaped {
		for i := range iface.NumMethod() {
			if name := iface.String() + "." + iface.Method(i).Name; !mapped[name] {
				t.Errorf("%s has no row in dtoTools: a tool-shaped method's DTOs must be compared", name)
			}
		}
	}
	if d := Diff(sub, got); len(d) > 0 {
		t.Fatalf("the service DTOs differ from contract/expected.json at %d nodes (expected.json vs the DTOs):\n  %s", len(d), strings.Join(d, "\n  "))
	}
	names := make([]string, 0, len(got.Tools))
	for name := range got.Tools {
		names = append(names, name)
	}
	slices.Sort(names)
	t.Logf("%d tools compared, input and output, exactly: %s", len(names), strings.Join(names, " "))
	if len(names) != 78 {
		t.Errorf("compared %d tools; the table holds the 42 CORE tools, cancel_task, list_agent_sessions, the 7 artifact, comment and follow-up tools (R2.1), the 4 chronicle tools, the 9 on-call tools, the 10 runs and sprints tools and R2.5's disconnect_agent, get_my_settings, set_my_settings and list_audit_log (78)", len(names))
	}
}

// requestPointers are the request fields deliberately left pointers: conductor treats their null
// as their absence (it creates with `?? null`), so absent and null need not be told apart.
var requestPointers = map[string]bool{
	"AddColumnReq.WIPLimit":      true, // add_column: wipLimit ?? null
	"ColumnSpec.WIPLimit":        true, // create_board columns[]: wipLimit ?? null
	"CreateTaskReq.ParentTaskID": true, // create_task: parentTaskId ?? null
	// The chronicle's: origin .default(null) and sourcePath .default(null), so null is absent.
	"ChronicleBlockInput.Origin":        true,
	"IngestChroniclePageReq.SourcePath": true,
}

// TestRequestPointersAreDeliberate: a pointer and a service.Nullable infer one schema, so
// TestDTOCanonical cannot tell a request field that drops its null from one that keeps it. Every
// pointer reachable from a tool's request type is on requestPointers, or this fails naming it.
func TestRequestPointersAreDeliberate(t *testing.T) {
	seen := map[reflect.Type]bool{}
	found := map[string]bool{}
	var walk func(typ reflect.Type)
	walk = func(typ reflect.Type) {
		switch typ.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map:
			walk(typ.Elem())
			return
		case reflect.Struct:
		default:
			return
		}
		if seen[typ] {
			return
		}
		seen[typ] = true
		for i := range typ.NumField() {
			f := typ.Field(i)
			if f.Type.Kind() == reflect.Pointer {
				name := typ.Name() + "." + f.Name
				found[name] = true
				if !requestPointers[name] {
					t.Errorf("%s is a pointer in a request type: its null decodes as its absence. Make it service.Nullable, or list it in requestPointers if conductor's null means absent", name)
				}
			}
			walk(f.Type)
		}
	}
	for _, d := range dtoTools {
		m, _ := d.iface.MethodByName(d.method)
		walk(m.Type.In(2))
	}
	for name := range requestPointers {
		if !found[name] {
			t.Errorf("requestPointers lists %s, which is no longer a request pointer", name)
		}
	}
}
