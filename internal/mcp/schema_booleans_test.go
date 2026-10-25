package mcp_test

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/service/servicefake"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// booleanSchemas lists the positions in schema that hold a boolean schema. additionalProperties
// false is allowed: it is the closed-object form every client takes, and has no object spelling
// a client reads better (P-301).
func booleanSchemas(schema any, at string) []string {
	var out []string
	var walk func(v any, at string)
	walk = func(v any, at string) {
		if b, ok := v.(bool); ok {
			if !b && strings.HasSuffix(at, ".additionalProperties") {
				return
			}
			out = append(out, fmt.Sprintf("%s = %v", at, b))
			return
		}
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		for k, c := range m {
			switch k {
			case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas":
				named, _ := c.(map[string]any)
				for n, s := range named {
					walk(s, at+"."+k+"."+n)
				}
			case "anyOf", "allOf", "oneOf", "prefixItems":
				list, _ := c.([]any)
				for i, s := range list {
					walk(s, fmt.Sprintf("%s.%s[%d]", at, k, i))
				}
			case "items", "additionalItems", "additionalProperties", "unevaluatedProperties", "unevaluatedItems",
				"contains", "propertyNames", "not", "if", "then", "else", "contentSchema":
				if list, ok := c.([]any); ok {
					for i, s := range list {
						walk(s, fmt.Sprintf("%s.%s[%d]", at, k, i))
					}
					continue
				}
				walk(c, at+"."+k)
			}
		}
	}
	walk(schema, at)
	slices.Sort(out)
	return out
}

func decodeSchema(t *testing.T, s any) any {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// TestNoBooleanSchemaServed: no tool's input or output schema in an admin's tools/list holds a
// boolean schema at any depth. Cursor refuses the whole tools/list over one (rev76 I1).
func TestNoBooleanSchemaServed(t *testing.T) {
	h := bootTools(t, servicefake.New().Services())
	listed, err := h.Connect(t, bearerAdmin).ListTools(testCtx(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) == 0 {
		t.Fatal("tools/list is empty")
	}
	var found []string
	for _, tl := range listed.Tools {
		found = append(found, booleanSchemas(decodeSchema(t, tl.InputSchema), tl.Name+".inputSchema")...)
		found = append(found, booleanSchemas(decodeSchema(t, tl.OutputSchema), tl.Name+".outputSchema")...)
	}
	if len(found) > 0 {
		t.Fatalf("%d boolean schemas in tools/list:\n  %s", len(found), strings.Join(found, "\n  "))
	}
	t.Logf("%d tools, input and output: no boolean schema", len(listed.Tools))
}

// TestBooleanSchemaWalk: the walk finds a boolean schema in each schema position, and passes a
// strict object, a described any and boolean-valued keywords that are not schemas.
func TestBooleanSchemaWalk(t *testing.T) {
	refused := map[string]string{
		`{"type":"object","properties":{"value":true}}`:                      "s.properties.value = true",
		`{"type":"array","items":true}`:                                      "s.items = true",
		`{"anyOf":[false,{"type":"null"}]}`:                                  "s.anyOf[0] = false",
		`{"type":"object","additionalProperties":true}`:                      "s.additionalProperties = true",
		`{"type":"object","properties":{"a":{"items":{"not":true}}}}`:        "s.properties.a.items.not = true",
		`{"$defs":{"x":{"type":"object","properties":{"v":false}}}}`:         "s.$defs.x.properties.v = false",
		`{"type":"object","properties":{"o":{"additionalProperties":true}}}`: "s.properties.o.additionalProperties = true",
	}
	for src, want := range refused {
		var s any
		if err := json.Unmarshal([]byte(src), &s); err != nil {
			t.Fatal(err)
		}
		if got := booleanSchemas(s, "s"); !slices.Equal(got, []string{want}) {
			t.Errorf("%s: found %q, want [%q]", src, got, want)
		}
	}
	control := `{"type":"object","additionalProperties":false,"required":["value"],"properties":{` +
		`"value":{"description":"any JSON value"},"flag":{"type":"boolean","const":true,"default":false},` +
		`"meta":{"type":"object","additionalProperties":{"description":"any JSON value"}},` +
		`"list":{"type":"array","items":{"type":"object","additionalProperties":false}}}}`
	var s any
	if err := json.Unmarshal([]byte(control), &s); err != nil {
		t.Fatal(err)
	}
	if got := booleanSchemas(s, "s"); len(got) > 0 {
		t.Errorf("control: found %q, want none", got)
	}
}

// TestWriteMemoryTakesEveryJSONType: write_memory's value takes a string, a number, an object, an
// array and a boolean, and read_memory answers each as written, on every selected engine.
func TestWriteMemoryTakesEveryJSONType(t *testing.T) {
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) {
		c := &caller{t: t, cs: bootStack(t, s).Connect(t, stackOwner), called: map[string]int{}}
		p := str(c.call("create_project", map[string]any{"name": "Memory"})["projectId"])
		b := str(obj(c.call("create_board", map[string]any{"projectId": p, "name": "Main"})["board"])["id"])
		id := str(obj(c.call("create_task", map[string]any{"projectId": p, "boardId": b, "title": "values"})["task"])["id"])
		values := map[string]any{
			"string": "a note",
			"number": 42.5,
			"object": map[string]any{"a": 1.0, "b": []any{"x", true}},
			"array":  []any{1.0, "two", nil},
			"bool":   false,
		}
		for key, v := range values {
			c.call("write_memory", map[string]any{"projectId": p, "taskId": id, "scope": "task", "key": key, "value": v})
			entries, _ := c.call("read_memory", map[string]any{"projectId": p, "taskId": id, "key": key})["entries"].([]any)
			if len(entries) != 1 {
				t.Fatalf("read_memory %s: %d entries, want 1", key, len(entries))
			}
			if got := obj(entries[0])["value"]; !jsonEqual(got, v) {
				t.Errorf("read_memory %s: value %v, want %v", key, got, v)
			}
		}
	})
}
