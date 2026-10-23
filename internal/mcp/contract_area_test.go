package mcp_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/contract"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicefake"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
)

// TestAreaSchemasEqualExpected: per area, the input and output schema of every registered tool, as
// a real client reads them from tools/list, canonicalised, equal that tool's contract/expected.json
// entry exactly. A lane's drift fails here, in its own area, before R1.10's whole-surface test. It
// runs over servicefake and over the real service on every selected engine, the registry serve builds.
func TestAreaSchemasEqualExpected(t *testing.T) {
	t.Run("servicefake", func(t *testing.T) { areaSchemasEqualExpected(t, servicefake.New().Services()) })
	servicetest.Run(t, func(t *testing.T, s *servicetest.Stack) { areaSchemasEqualExpected(t, s.Services) })
}

func areaSchemasEqualExpected(t *testing.T, svc service.Services) {
	var want contract.Surface
	if err := contract.ReadJSON(filepath.Join("..", "..", "contract", "expected.json"), &want); err != nil {
		t.Fatal(err)
	}
	h := bootTools(t, svc)
	listed, err := h.Connect(t, bearerAdmin).ListTools(testCtx(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	served := map[string][2]any{}
	for _, tl := range listed.Tools {
		served[tl.Name] = [2]any{tl.InputSchema, tl.OutputSchema}
	}
	byArea := map[string][]string{}
	for _, e := range h.Server().Manifest() {
		byArea[e.Area] = append(byArea[e.Area], e.Name)
	}
	if len(byArea) == 0 {
		t.Fatal("no tool registered")
	}
	areas := make([]string, 0, len(byArea))
	for a := range byArea {
		areas = append(areas, a)
	}
	slices.Sort(areas)
	for _, area := range areas {
		t.Run(area, func(t *testing.T) {
			exp := contract.Surface{Tools: map[string]*contract.Tool{}}
			got := contract.Surface{Tools: map[string]*contract.Tool{}}
			for _, name := range byArea[area] {
				w, ok := want.Tools[name]
				if !ok {
					t.Errorf("%s is registered and not in contract/expected.json", name)
					continue
				}
				s, ok := served[name]
				if !ok {
					t.Errorf("%s is registered and not in tools/list", name)
					continue
				}
				in, err := contract.Canonicalize(s[0])
				if err != nil {
					t.Fatalf("%s input: %v", name, err)
				}
				out, err := contract.Canonicalize(s[1])
				if err != nil {
					t.Fatalf("%s output: %v", name, err)
				}
				exp.Tools[name], got.Tools[name] = w, &contract.Tool{Input: in, Output: out}
			}
			if d := contract.Diff(exp, got); len(d) > 0 {
				t.Fatalf("area %s: the served schemas differ from contract/expected.json at %d nodes (expected.json vs served):\n  %s", area, len(d), strings.Join(d, "\n  "))
			}
			t.Logf("area %s: %d tools equal contract/expected.json, input and output: %s", area, len(byArea[area]), strings.Join(byArea[area], " "))
		})
	}
}
