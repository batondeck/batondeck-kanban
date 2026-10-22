package contract

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

// dtoTools maps each tool to the service method whose request and response types are its input and
// output schemas: the 42 CORE tools of plan §Target surface, cancel_task (ADR-0001, after the 42
// was counted) and list_agent_sessions (service.Sessions.ListAgentSessions).
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
}

// toolShaped are the interfaces every method of which is one tool: a method added to one without a
// row in dtoTools fails TestDTOCanonical.
var toolShaped = []reflect.Type{
	reflect.TypeFor[service.Projects](), reflect.TypeFor[service.Boards](), reflect.TypeFor[service.Tasks](),
	reflect.TypeFor[service.Lifecycle](), reflect.TypeFor[service.Context](), reflect.TypeFor[service.Media](),
}

// openObjects are the DTOs whose conductor schema allows properties it does not name. A Go struct
// infers additionalProperties:false, so these are inferred and then opened, inner types first.
var openObjects = []reflect.Type{
	reflect.TypeFor[service.ModelHintInput](),
	reflect.TypeFor[service.TaskPatch](),
	reflect.TypeFor[service.AnomalyPolicyPatch](),
	reflect.TypeFor[service.RetryPolicyPatch](),
	reflect.TypeFor[service.SelectionWeightsPatch](),
	reflect.TypeFor[service.ColumnSpec](),
	reflect.TypeFor[service.Shard](),
	reflect.TypeFor[service.ArtifactInput](),
	reflect.TypeFor[service.LargeArtifact](),
}

func enumSchema[T ~string](values []T) *jsonschema.Schema {
	s := &jsonschema.Schema{Type: "string"}
	for _, v := range values {
		s.Enum = append(s.Enum, string(v))
	}
	return s
}

// dtoSchemaOptions is the inference recipe for the DTOs: the enums from their Go values, a custom
// field's value as string|number|boolean, True as const true, and the open objects. Slices are
// fixed after inference (dtoSchema), since jsonschema-go types every Go slice as [null, array].
func dtoSchemaOptions(t *testing.T) *jsonschema.ForOptions {
	t.Helper()
	isTrue := any(true)
	opts := &jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[domain.Status]():               enumSchema(domain.AllStatuses()),
		reflect.TypeFor[domain.Role]():                 enumSchema(domain.AllRoles()),
		reflect.TypeFor[domain.Priority]():             enumSchema([]domain.Priority{domain.PriorityLow, domain.PriorityNormal, domain.PriorityHigh, domain.PriorityUrgent}),
		reflect.TypeFor[domain.MemoryScope]():          enumSchema([]domain.MemoryScope{domain.MemoryScopeTask, domain.MemoryScopeAgent, domain.MemoryScopeShared, domain.MemoryScopeProject, domain.MemoryScopeAgentGlobal}),
		reflect.TypeFor[service.ArtifactKind]():        enumSchema(service.ArtifactKindValues),
		reflect.TypeFor[service.AttachmentStatus]():    enumSchema(service.AttachmentStatusValues),
		reflect.TypeFor[service.CapabilityMatchTier](): enumSchema(service.CapabilityMatchTierValues),
		reflect.TypeFor[service.ContextKind]():         enumSchema(service.ContextKindValues),
		reflect.TypeFor[service.ContextSection]():      enumSchema(service.ContextSectionValues),
		reflect.TypeFor[service.DependencyType]():      enumSchema(service.DependencyTypeValues),
		reflect.TypeFor[service.Effort]():              enumSchema(service.EffortValues),
		reflect.TypeFor[service.FollowUpStatus]():      enumSchema(service.FollowUpStatusValues),
		reflect.TypeFor[service.MediaKind]():           enumSchema(service.MediaKindValues),
		reflect.TypeFor[service.NextStrategy]():        enumSchema(service.NextStrategyValues),
		reflect.TypeFor[service.PolicyMode]():          enumSchema(service.PolicyModeValues),
		reflect.TypeFor[service.ReadMemoryScope]():     enumSchema(service.ReadMemoryScopeValues),
		reflect.TypeFor[service.RecallMemoryScope]():   enumSchema(service.RecallMemoryScopeValues),
		reflect.TypeFor[service.SearchMode]():          enumSchema(service.SearchModeValues),
		reflect.TypeFor[service.SessionKind]():         enumSchema(service.SessionKindValues),
		reflect.TypeFor[service.SprintStatus]():        enumSchema(service.SprintStatusValues),
		reflect.TypeFor[service.FieldValue]():          {Types: []string{"string", "number", "boolean"}},
		reflect.TypeFor[service.True]():                {Type: "boolean", Const: &isTrue},
	}}
	for _, typ := range openObjects {
		s, err := jsonschema.ForType(typ, opts)
		if err != nil {
			t.Fatalf("infer %s: %v", typ, err)
		}
		s.AdditionalProperties = nil
		opts.TypeSchemas[typ] = s
	}
	return opts
}

// dtoSchema infers typ's schema and returns it decoded, every slice's [null, array] made array:
// a DTO slice is never null on the wire (a response fills it, a request omits it). The surface
// holds no nullable array, so the rewrite cannot hide one; a *[]T would come back as array and
// fail the comparison against a nullable node.
func dtoSchema(t *testing.T, typ reflect.Type, opts *jsonschema.ForOptions) any {
	t.Helper()
	s, err := jsonschema.ForType(typ, opts)
	if err != nil {
		t.Fatalf("infer %s: %v", typ, err)
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var m any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	var fix func(any)
	fix = func(v any) {
		switch n := v.(type) {
		case map[string]any:
			if ts, ok := n["type"].([]any); ok && len(ts) == 2 && ts[0] == "null" && ts[1] == "array" {
				n["type"] = "array"
			}
			for _, c := range n {
				fix(c)
			}
		case []any:
			for _, c := range n {
				fix(c)
			}
		}
	}
	fix(m)
	return m
}

// TestDTOCanonical: the request and response types of every tool-shaped service method,
// inferred by jsonschema-go and canonicalised, equal contract/expected.json's input and output
// for that tool, exactly.
func TestDTOCanonical(t *testing.T) {
	want := committed(t)
	opts := dtoSchemaOptions(t)
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
		in, err := Canonicalize(dtoSchema(t, mt.In(2), opts))
		if err != nil {
			t.Fatalf("%s input (%s): %v", d.tool, mt.In(2), err)
		}
		out, err := Canonicalize(dtoSchema(t, mt.Out(0), opts))
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
	if len(names) != 44 {
		t.Errorf("compared %d tools; the table holds the 42 CORE tools, cancel_task and list_agent_sessions (44)", len(names))
	}
}
