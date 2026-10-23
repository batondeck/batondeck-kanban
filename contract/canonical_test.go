package contract

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func decode(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decode %s: %v", s, err)
	}
	return v
}

func reduce(t *testing.T, s string) string {
	t.Helper()
	n, err := Canonicalize(decode(t, s))
	if err != nil {
		t.Fatalf("Canonicalize(%s): %v", s, err)
	}
	return mustJSON(n)
}

func TestCanonicalizeEquivalences(t *testing.T) {
	cases := []struct{ name, a, b string }{
		{"Zod anyOf:[T,null] equals Go type:[null,T]",
			`{"anyOf":[{"type":"string"},{"type":"null"}]}`,
			`{"type":["null","string"]}`},
		{"a stripped description leaves the form unchanged",
			`{"type":"object","description":"the task","properties":{"id":{"type":"string","description":"T-n"}},"required":["id"]}`,
			`{"type":"object","properties":{"id":{"type":"string"}},"required":["id"]}`},
		{"every stripped keyword",
			`{"$schema":"x","type":"string","default":"a","title":"t","minLength":1,"maxLength":9,"pattern":"^a","format":"date-time"}`,
			`{"type":"string"}`},
		{"Zod nullable object equals Go pointer to struct",
			`{"anyOf":[{"type":"object","properties":{"a":{"type":"number"}},"required":["a"],"additionalProperties":false},{"type":"null"}]}`,
			`{"type":["null","object"],"properties":{"a":{"type":"number"}},"required":["a"],"additionalProperties":false}`},
		{"required is sorted",
			`{"type":"object","properties":{"a":{},"b":{}},"required":["b","a"]}`,
			`{"type":"object","properties":{"a":{},"b":{}},"required":["a","b"]}`},
		{"const is a one-value enum",
			`{"type":"boolean","const":true}`,
			`{"type":"boolean","enum":[true]}`},
		{"additionalProperties true is absent",
			`{"type":"object","additionalProperties":true}`,
			`{"type":"object"}`},
		{"a record's propertyNames is stripped",
			`{"type":"object","propertyNames":{"type":"string"},"additionalProperties":{"type":"number"}}`,
			`{"type":"object","additionalProperties":{"type":"number"}}`},
		{"primitive branches merge into one typeSet",
			`{"anyOf":[{"type":"string"},{"type":"number"},{"type":"boolean"}]}`,
			`{"type":["boolean","number","string"]}`},
		{"the JSON-value union, the empty schema and true are all any",
			`{"definitions":{"v":{"anyOf":[{"type":"string"},{"type":"number"},{"type":"boolean"},{"type":"null"},{"type":"array","items":{"$ref":"#/definitions/v"}},{"type":"object","propertyNames":{"type":"string"},"additionalProperties":{"$ref":"#/definitions/v"}}]}},"type":"object","properties":{"a":{"$ref":"#/definitions/v"},"b":{"allOf":[{"$ref":"#/definitions/v"}]},"c":{"anyOf":[{"$ref":"#/definitions/v"},{"type":"null"}]}}}`,
			`{"type":"object","properties":{"a":{},"b":true,"c":{"description":"anything"}}}`},
		{"enum order does not matter",
			`{"type":"string","enum":["b","a"]}`,
			`{"type":"string","enum":["a","b"]}`},
	}
	for _, c := range cases {
		if a, b := reduce(t, c.a), reduce(t, c.b); a != b {
			t.Errorf("%s:\n  %s\n  %s", c.name, a, b)
		}
	}
}

func TestCanonicalizeDistinctions(t *testing.T) {
	cases := []struct{ name, a, b string }{
		{"integer is not number", `{"type":"integer"}`, `{"type":"number"}`},
		{"nullable is not plain", `{"anyOf":[{"type":"string"},{"type":"null"}]}`, `{"type":"string"}`},
		{"additionalProperties false is not absent", `{"type":"object","additionalProperties":false}`, `{"type":"object"}`},
		{"required differs", `{"type":"object","properties":{"a":{}},"required":["a"]}`, `{"type":"object","properties":{"a":{}}}`},
		{"enum values differ", `{"type":"string","enum":["a"]}`, `{"type":"string","enum":["a","b"]}`},
	}
	for _, c := range cases {
		if a, b := reduce(t, c.a), reduce(t, c.b); a == b {
			t.Errorf("%s: both reduce to %s", c.name, a)
		}
	}
}

func TestCanonicalizeSeveralStructuredBranches(t *testing.T) {
	a := reduce(t, `{"anyOf":[{"type":"object","properties":{"x":{"type":"string"}}},{"type":"object","properties":{"y":{"type":"number"}}},{"type":"null"}]}`)
	b := reduce(t, `{"anyOf":[{"type":"null"},{"type":"object","properties":{"y":{"type":"number"}}},{"type":"object","properties":{"x":{"type":"string"}}}]}`)
	if a != b {
		t.Errorf("branch order changed the form:\n  %s\n  %s", a, b)
	}
	if !strings.Contains(a, `"anyOf"`) || !strings.Contains(a, `"typeSet":["null"]`) {
		t.Errorf("want a sorted anyOf list with null in the typeSet, got %s", a)
	}
}

func TestCanonicalizeRefuses(t *testing.T) {
	cases := map[string]string{
		"unknown keyword":   `{"type":"string","oneOf":[]}`,
		"tuple items":       `{"type":"array","items":[{"type":"string"}]}`,
		"unresolved ref":    `{"$ref":"#/definitions/none"}`,
		"allOf of two":      `{"allOf":[{"type":"string"},{"type":"string"}]}`,
		"false schema":      `false`,
		"recursive non-any": `{"definitions":{"n":{"type":"object","properties":{"next":{"$ref":"#/definitions/n"}}}},"$ref":"#/definitions/n"}`,
	}
	for name, s := range cases {
		if _, err := Canonicalize(decode(t, s)); err == nil {
			t.Errorf("%s: accepted %s", name, s)
		}
	}
}

func surface(t *testing.T) Surface {
	t.Helper()
	s, err := FromRaw([]RawTool{{
		Name:         "move_task",
		InputSchema:  decode(t, `{"type":"object","properties":{"toStatus":{"type":"string","enum":["TODO","CANCELLED","REVIEW"]},"order":{"type":"string"}},"required":["toStatus"],"additionalProperties":false}`),
		OutputSchema: decode(t, `{"type":"object","properties":{"ok":{"type":"boolean"}}}`),
	}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestApply(t *testing.T) {
	s := surface(t)
	err := Apply(s, Deltas{
		RemovedProps:      []string{"/tools/move_task/input/properties/order"},
		RemovedEnumValues: []string{"/tools/move_task/input/properties/toStatus/enum/CANCELLED"},
		RenamedEnumValues: []RenamedEnum{{Pointer: "/tools/move_task/input/properties/toStatus/enum/TODO", To: "BLOCKED"}},
		AddedTools:        []RawTool{{Name: "delete_task", InputSchema: decode(t, `{"type":"object"}`), OutputSchema: decode(t, `{"type":"object"}`)}},
		AddedProps:        []AddedProp{{Pointer: "/tools/move_task/output/properties/poisoned", Required: true, Schema: decode(t, `{"type":"number"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := Marshal(s)
	want := `{"tools":{"delete_task":{"input":{"typeSet":["object"]},"output":{"typeSet":["object"]}},"move_task":{"input":{"typeSet":["object"],"properties":{"toStatus":{"typeSet":["string"],"enum":["BLOCKED","REVIEW"]}},"required":["toStatus"],"additionalProperties":false},"output":{"typeSet":["object"],"properties":{"ok":{"typeSet":["boolean"]},"poisoned":{"typeSet":["number"]}},"required":["poisoned"]}}}}`
	var compact bytes.Buffer
	if err := json.Compact(&compact, got); err != nil {
		t.Fatal(err)
	}
	if compact.String() != want {
		t.Errorf("Apply:\n got %s\nwant %s", compact.String(), want)
	}
}

// TestApplyRefusesStaleDeltas: every delta must hit, so a line that no longer matches the source
// fails instead of silently doing nothing.
func TestApplyRefusesStaleDeltas(t *testing.T) {
	cases := map[string]Deltas{
		"removed tool absent":        {RemovedTools: []string{"get_transitions"}},
		"removed prop absent":        {RemovedProps: []string{"/tools/move_task/input/properties/transitions"}},
		"removed prop path absent":   {RemovedProps: []string{"/tools/move_task/input/properties/nope/properties/x"}},
		"removed enum value absent":  {RemovedEnumValues: []string{"/tools/move_task/input/properties/toStatus/enum/DEAD_LETTER"}},
		"enum emptied":               {RemovedEnumValues: []string{"/tools/move_task/input/properties/toStatus/enum/TODO", "/tools/move_task/input/properties/toStatus/enum/CANCELLED", "/tools/move_task/input/properties/toStatus/enum/REVIEW"}},
		"renamed enum value absent":  {RenamedEnumValues: []RenamedEnum{{Pointer: "/tools/move_task/input/properties/toStatus/enum/DEAD_LETTER", To: "X"}}},
		"renamed onto a present one": {RenamedEnumValues: []RenamedEnum{{Pointer: "/tools/move_task/input/properties/toStatus/enum/TODO", To: "REVIEW"}}},
		"renamed off an enum":        {RenamedEnumValues: []RenamedEnum{{Pointer: "/tools/move_task/input/properties/toStatus", To: "X"}}},
		"added tool present":         {AddedTools: []RawTool{{Name: "move_task", InputSchema: map[string]any{}, OutputSchema: map[string]any{}}}},
		"added prop present":         {AddedProps: []AddedProp{{Pointer: "/tools/move_task/input/properties/order", Schema: map[string]any{}}}},
		"pointer outside tools":      {RemovedProps: []string{"/move_task/input/properties/order"}},
	}
	for name, d := range cases {
		if err := Apply(surface(t), d); err == nil {
			t.Errorf("%s: applied", name)
		}
	}
}

func TestDiffNamesThePointer(t *testing.T) {
	a, b := surface(t), surface(t)
	if d := Diff(a, b); len(d) != 0 {
		t.Fatalf("equal surfaces differ: %v", d)
	}
	b.Tools["move_task"].Input.Properties["extra"] = &Node{TypeSet: []string{"string"}}
	d := Diff(a, b)
	if len(d) != 1 || d[0] != "extra /tools/move_task/input/properties/extra" {
		t.Errorf("Diff = %v", d)
	}
}
