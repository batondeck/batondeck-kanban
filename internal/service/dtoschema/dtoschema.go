// Package dtoschema is the JSON Schema inference recipe for the service DTOs (internal/service's
// contract files): the one recipe both the MCP tool registration and contract/dto_test.go use, so
// the test compares exactly the schemas the server serves.
package dtoschema

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

// Optionals are the tri-state request fields' instantiations (service.Optional, service.Nullable).
// Each is typed as its value: Optional[T] as T, Nullable[T] as *T (T or null), so a field keeps the
// schema it had as a value or a pointer while carrying absent apart from false, 0 and null.
var Optionals = []reflect.Type{
	reflect.TypeFor[service.Optional[bool]](),
	reflect.TypeFor[service.Optional[float64]](),
	reflect.TypeFor[service.Optional[int64]](),
	reflect.TypeFor[service.Nullable[string]](),
	reflect.TypeFor[service.Nullable[int64]](),
	reflect.TypeFor[service.Nullable[float64]](),
	reflect.TypeFor[service.Optional[string]](),
	reflect.TypeFor[service.Nullable[service.ModelHintInput]](),
	reflect.TypeFor[service.Optional[service.AnomalyPolicyPatch]](),
	reflect.TypeFor[service.Nullable[domain.ChronicleCoverage]](),
	reflect.TypeFor[service.Optional[service.SprintStatus]](),
	reflect.TypeFor[service.Nullable[service.TokenPolicy]](),
}

// OpenObjects are the DTOs whose conductor schema allows properties it does not name. A Go struct
// infers additionalProperties:false, so these are inferred and then opened, inner types first.
var OpenObjects = []reflect.Type{
	reflect.TypeFor[service.ModelHintInput](),
	reflect.TypeFor[service.TaskPatch](),
	reflect.TypeFor[service.AnomalyPolicyPatch](),
	reflect.TypeFor[service.RetryPolicyPatch](),
	reflect.TypeFor[service.SelectionWeightsPatch](),
	reflect.TypeFor[service.ColumnSpec](),
	reflect.TypeFor[service.Shard](),
	reflect.TypeFor[service.ArtifactInput](),
	reflect.TypeFor[service.ArtifactSelector](),
	reflect.TypeFor[service.LargeArtifact](),
	reflect.TypeFor[service.SprintPatchIn](),
}

func enumSchema[T ~string](values []T) *jsonschema.Schema {
	s := &jsonschema.Schema{Type: "string"}
	for _, v := range values {
		s.Enum = append(s.Enum, string(v))
	}
	return s
}

var options = sync.OnceValue(build)

// Options is the inference recipe: the enums from their Go values, a custom field's value as
// string|number|boolean, True as const true, the tri-state optionals, and the open objects. Slices
// are fixed after inference (For), since jsonschema-go types every Go slice as [null, array]. The
// value is shared: a caller must not change it.
func Options() *jsonschema.ForOptions { return options() }

func build() *jsonschema.ForOptions {
	isTrue := any(true)
	opts := &jsonschema.ForOptions{TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[domain.Status]():                enumSchema(domain.AllStatuses()),
		reflect.TypeFor[domain.Role]():                  enumSchema(domain.AllRoles()),
		reflect.TypeFor[domain.Priority]():              enumSchema([]domain.Priority{domain.PriorityLow, domain.PriorityNormal, domain.PriorityHigh, domain.PriorityUrgent}),
		reflect.TypeFor[domain.ChroniclePageKind]():     enumSchema(domain.ChroniclePageKinds()),
		reflect.TypeFor[domain.ChronicleCoverage]():     enumSchema(domain.ChronicleCoverages()),
		reflect.TypeFor[domain.MemoryScope]():           enumSchema([]domain.MemoryScope{domain.MemoryScopeTask, domain.MemoryScopeAgent, domain.MemoryScopeShared, domain.MemoryScopeProject, domain.MemoryScopeAgentGlobal}),
		reflect.TypeFor[service.ArtifactKind]():         enumSchema(service.ArtifactKindValues),
		reflect.TypeFor[service.AttachmentStatus]():     enumSchema(service.AttachmentStatusValues),
		reflect.TypeFor[service.CapabilityMatchTier]():  enumSchema(service.CapabilityMatchTierValues),
		reflect.TypeFor[service.ContextKind]():          enumSchema(service.ContextKindValues),
		reflect.TypeFor[service.ContextSection]():       enumSchema(service.ContextSectionValues),
		reflect.TypeFor[service.DependencyType]():       enumSchema(service.DependencyTypeValues),
		reflect.TypeFor[service.Effort]():               enumSchema(service.EffortValues),
		reflect.TypeFor[service.CostClass]():            enumSchema(service.CostClassValues),
		reflect.TypeFor[service.AnomalyKind]():          enumSchema(service.AnomalyKindValues),
		reflect.TypeFor[service.AnomalySeverity]():      enumSchema(service.AnomalySeverityValues),
		reflect.TypeFor[domain.NotificationType]():      enumSchema([]domain.NotificationType{domain.NotificationFollowUp, domain.NotificationMention, domain.NotificationReviewRequest}),
		reflect.TypeFor[service.FollowUpStatus]():       enumSchema(service.FollowUpStatusValues),
		reflect.TypeFor[service.MediaKind]():            enumSchema(service.MediaKindValues),
		reflect.TypeFor[service.NextStrategy]():         enumSchema(service.NextStrategyValues),
		reflect.TypeFor[service.PolicyMode]():           enumSchema(service.PolicyModeValues),
		reflect.TypeFor[service.ReadMemoryScope]():      enumSchema(service.ReadMemoryScopeValues),
		reflect.TypeFor[service.RecallMemoryScope]():    enumSchema(service.RecallMemoryScopeValues),
		reflect.TypeFor[service.SearchMode]():           enumSchema(service.SearchModeValues),
		reflect.TypeFor[service.SessionKind]():          enumSchema(service.SessionKindValues),
		reflect.TypeFor[service.TokenPolicy]():          enumSchema(service.TokenPolicyValues),
		reflect.TypeFor[service.SprintStatus]():         enumSchema(service.SprintStatusValues),
		reflect.TypeFor[service.SprintAction]():         enumSchema(service.SprintActionValues),
		reflect.TypeFor[service.SprintInclude]():        enumSchema(service.SprintIncludeValues),
		reflect.TypeFor[service.AttentionWhy]():         enumSchema(service.AttentionWhyValues),
		reflect.TypeFor[service.IntegrationSource]():    enumSchema(service.IntegrationSourceValues),
		reflect.TypeFor[service.IntegrationAuthMode]():  enumSchema(service.IntegrationAuthModeValues),
		reflect.TypeFor[service.IntegrationLifecycle](): enumSchema(service.IntegrationLifecycleValues),
		reflect.TypeFor[service.RoutingMode]():          enumSchema(service.RoutingModeValues),
		reflect.TypeFor[service.AlertStatus]():          enumSchema(service.AlertStatusValues),
		reflect.TypeFor[service.DeliveryKind]():         enumSchema(service.DeliveryKindValues),
		reflect.TypeFor[service.RawContentType]():       enumSchema(service.RawContentTypeValues),
		reflect.TypeFor[service.FieldValue]():           {Types: []string{"string", "number", "boolean"}},
		reflect.TypeFor[service.True]():                 {Type: "boolean", Const: &isTrue},
		reflect.TypeFor[service.JSONObject]():           {Type: "object", AdditionalProperties: &jsonschema.Schema{Description: "any JSON value"}},
	}}
	for _, typ := range Optionals {
		v, _ := typ.FieldByName("V")
		elem := v.Type
		if _, nullable := typ.FieldByName("Null"); nullable {
			elem = reflect.PointerTo(elem)
		}
		s := infer(elem, opts)
		if v.Type.Kind() == reflect.Struct {
			s.AdditionalProperties = nil // conductor's nested objects are open, as OpenObjects below
		}
		opts.TypeSchemas[typ] = s
	}
	for _, typ := range OpenObjects {
		s := infer(typ, opts)
		s.AdditionalProperties = nil
		opts.TypeSchemas[typ] = s
	}
	return opts
}

func infer(typ reflect.Type, opts *jsonschema.ForOptions) *jsonschema.Schema {
	s, err := jsonschema.ForType(typ, opts)
	if err != nil {
		panic(fmt.Sprintf("dtoschema: infer %s: %v", typ, err))
	}
	return s
}

// For is typ's schema under Options, a fresh tree per call, with every slice's [null, array] made
// array: a DTO slice is never null on the wire (a response fills it, a request omits it). The
// surface holds no nullable array, so the rewrite cannot hide one; a *[]T would come back as array
// and fail contract/dto_test.go against a nullable node. It panics when typ cannot be inferred: a
// DTO that does not infer is a bug in a contract file, found at registration.
func For(typ reflect.Type) *jsonschema.Schema {
	b, err := json.Marshal(infer(typ, Options()))
	if err != nil {
		panic(fmt.Sprintf("dtoschema: marshal %s: %v", typ, err))
	}
	var m any
	if err := json.Unmarshal(b, &m); err != nil {
		panic(fmt.Sprintf("dtoschema: decode %s: %v", typ, err))
	}
	unnullArrays(m)
	nullableEnums(m)
	if b, err = json.Marshal(m); err != nil {
		panic(fmt.Sprintf("dtoschema: marshal %s: %v", typ, err))
	}
	out := new(jsonschema.Schema)
	if err := json.Unmarshal(b, out); err != nil {
		panic(fmt.Sprintf("dtoschema: decode %s: %v", typ, err))
	}
	return out
}

// nullableEnums rewrites a nullable enum (a pointer to an enum type infers type [null, string]
// beside the enum) as anyOf [the enum, null]: the enum keyword applies to null too, so the inferred
// form refuses the null it declares, and go-sdk's output validation turns that into INTERNAL. The
// rewrite canonicalises to the same node (contract/canonical.go merges the one-structured-branch
// anyOf), as conductor's z.enum(...).nullable() does.
func nullableEnums(v any) {
	switch n := v.(type) {
	case map[string]any:
		if ts, ok := n["type"].([]any); ok && len(ts) == 2 && ts[0] == "null" && ts[1] == "string" && n["enum"] != nil {
			n["anyOf"] = []any{map[string]any{"type": "string", "enum": n["enum"]}, map[string]any{"type": "null"}}
			delete(n, "type")
			delete(n, "enum")
		}
		for _, c := range n {
			nullableEnums(c)
		}
	case []any:
		for _, c := range n {
			nullableEnums(c)
		}
	}
}

func unnullArrays(v any) {
	switch n := v.(type) {
	case map[string]any:
		if ts, ok := n["type"].([]any); ok && len(ts) == 2 && ts[0] == "null" && ts[1] == "array" {
			n["type"] = "array"
		}
		for _, c := range n {
			unnullArrays(c)
		}
	case []any:
		for _, c := range n {
			unnullArrays(c)
		}
	}
}
