package contract

import (
	"bytes"
	"flag"
	"os"
	"slices"
	"testing"
)

// `go test ./contract/ -run TestExpectedIsTheExactDerivation -update` rewrites expected.json from
// tools.json, admin-tools.json and deltas.json. Without -update the test refuses any difference.
var update = flag.Bool("update", false, "rewrite expected.json from the derivation")

func derived(t *testing.T) Surface {
	t.Helper()
	s, err := Derive(".")
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	return s
}

func committed(t *testing.T) Surface {
	t.Helper()
	var s Surface
	if err := ReadJSON("expected.json", &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// TestExpectedIsTheExactDerivation: expected.json is canonical(tools.json + admin-tools.json) ⊖
// deltas.json, byte for byte, so a property, tool or enum value in it without a delta fails here.
func TestExpectedIsTheExactDerivation(t *testing.T) {
	want, err := Marshal(derived(t))
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile("expected.json", want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile("expected.json")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(got, want) {
		return
	}
	d := Diff(derived(t), committed(t))
	if len(d) > 20 {
		d = append(d[:20], "…")
	}
	t.Fatalf("expected.json is not canonical(tools.json + admin-tools.json) ⊖ deltas.json; every departure needs a line in deltas.json (then -update). Differences, derivation vs committed:\n  %s", joinLines(d))
}

func joinLines(s []string) string {
	var b bytes.Buffer
	for i, l := range s {
		if i > 0 {
			b.WriteString("\n  ")
		}
		b.WriteString(l)
	}
	return b.String()
}

// TestSurfaceRefusesRemovedTools: the committed surface passes CheckSurface, and CheckSurface
// refuses each forbidden tool and a surface without delete_task.
func TestSurfaceRefusesRemovedTools(t *testing.T) {
	s := committed(t)
	if err := CheckSurface(s); err != nil {
		t.Fatalf("expected.json: %v", err)
	}
	for _, name := range Forbidden {
		bad := Surface{Tools: map[string]*Tool{name: {}, "delete_task": {}}}
		if err := CheckSurface(bad); err == nil {
			t.Errorf("CheckSurface accepted a surface carrying %s", name)
		}
	}
	if err := CheckSurface(Surface{Tools: map[string]*Tool{"move_task": {}}}); err == nil {
		t.Error("CheckSurface accepted a surface without delete_task")
	}
}

// TestDeleteTaskIsAnAddedTool: delete_task enters the surface through deltas.json's addedTools,
// the one place a tool conductor lacks may come from.
func TestDeleteTaskIsAnAddedTool(t *testing.T) {
	var d Deltas
	if err := ReadJSON("deltas.json", &d); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(d.AddedTools, func(r RawTool) bool { return r.Name == "delete_task" }) {
		t.Fatal("deltas.json addedTools lacks delete_task")
	}
	for _, name := range Forbidden {
		if !slices.Contains(d.RemovedTools, name) {
			t.Errorf("deltas.json removedTools lacks %s", name)
		}
	}
}

// TestNoRemovedStatusRemains: CANCELLED and DEAD_LETTER are gone from every enum of the surface
// (refusal), and conductor's surface does carry them (positive control: the walk can see them).
func TestNoRemovedStatusRemains(t *testing.T) {
	count := func(s Surface) (n int, at []string) {
		s.Walk(func(p string, node *Node) {
			for _, v := range node.Enum {
				if v == "CANCELLED" || v == "DEAD_LETTER" {
					n++
					at = append(at, p)
				}
			}
		})
		return n, at
	}
	if n, at := count(committed(t)); n != 0 {
		t.Errorf("expected.json still offers a removed status at %d places: %v", n, at)
	}
	var src SourceFile
	if err := ReadJSON("tools.json", &src); err != nil {
		t.Fatal(err)
	}
	s, err := FromRaw(src.Tools)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := count(s); n == 0 {
		t.Error("tools.json shows no CANCELLED or DEAD_LETTER: the residue check could not have failed")
	}
}

// TestCounts pins the scope the plan states (92 tools: 91 in-scope names plus delete_task; 40
// published tools removed) and prints every count.
func TestCounts(t *testing.T) {
	var src, admin SourceFile
	var d Deltas
	for name, v := range map[string]any{"tools.json": &src, "admin-tools.json": &admin, "deltas.json": &d} {
		if err := ReadJSON(name, v); err != nil {
			t.Fatal(err)
		}
	}
	got := map[string]int{
		"tools.json tools":              len(src.Tools),
		"admin-tools.json tools":        len(admin.Tools),
		"deltas removedTools":           len(d.RemovedTools),
		"deltas removedProps":           len(d.RemovedProps),
		"deltas removedEnumValues":      len(d.RemovedEnumValues),
		"deltas addedTools":             len(d.AddedTools),
		"deltas addedProps":             len(d.AddedProps),
		"expected.json tools":           len(committed(t).Tools),
		"expected.json tools (derived)": len(derived(t).Tools),
	}
	for _, k := range []string{"tools.json tools", "admin-tools.json tools", "deltas removedTools", "deltas removedProps", "deltas removedEnumValues", "deltas addedTools", "deltas addedProps", "expected.json tools", "expected.json tools (derived)"} {
		t.Logf("%-30s %d", k, got[k])
	}
	for k, want := range map[string]int{"tools.json tools": 130, "admin-tools.json tools": 1, "deltas removedTools": 40, "expected.json tools": 92} {
		if got[k] != want {
			t.Errorf("%s = %d, the plan's scope is %d", k, got[k], want)
		}
	}
}

// TestErrorCodes pins errors.json to conductor's error model: 13 codes, exactly STALE,
// RATE_LIMITED and INTERNAL retryable, and the payload {code, message, retryable, details?}.
func TestErrorCodes(t *testing.T) {
	var e struct {
		Shape map[string]any `json:"shape"`
		Codes []struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		} `json:"codes"`
	}
	if err := ReadJSON("errors.json", &e); err != nil {
		t.Fatal(err)
	}
	want := []string{"VALIDATION", "UNAUTHENTICATED", "FORBIDDEN", "NOT_FOUND", "STALE", "CONFLICT_LOCKED", "LEASE_EXPIRED", "INVALID_TRANSITION", "WIP_EXCEEDED", "CYCLE_DETECTED", "QUOTA_EXCEEDED", "RATE_LIMITED", "INTERNAL"}
	var codes, retryable []string
	for _, c := range e.Codes {
		codes = append(codes, c.Code)
		if c.Retryable {
			retryable = append(retryable, c.Code)
		}
	}
	if !slices.Equal(codes, want) {
		t.Errorf("codes = %v, want %v", codes, want)
	}
	if !slices.Equal(retryable, []string{"STALE", "RATE_LIMITED", "INTERNAL"}) {
		t.Errorf("retryable = %v, want STALE, RATE_LIMITED, INTERNAL", retryable)
	}
	shape, err := Canonicalize(e.Shape)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(shape.Required, []string{"code", "message", "retryable"}) || len(shape.Properties) != 4 || shape.Properties["details"] == nil {
		t.Errorf("shape = %s, want {code, message, retryable, details?}", mustJSON(shape))
	}
	enum := shape.Properties["code"].Enum
	if len(enum) != len(want) {
		t.Errorf("shape's code enum has %d values, codes has %d", len(enum), len(want))
	}
	for _, c := range want {
		if !slices.Contains(enum, any(c)) {
			t.Errorf("shape's code enum lacks %s", c)
		}
	}
}
