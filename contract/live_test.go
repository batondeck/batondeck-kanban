package contract

import (
	"slices"
	"strings"
	"testing"
)

// The live comparison itself runs in internal/cli (TestLiveContract), against serve's composition.
// These pin CheckLive's verdicts on small surfaces, refusal and positive control alike, and hold
// pending.json and errorDeltas to the contract.

func liveFixture() (want, served Surface) {
	tool := func(props ...string) *Tool {
		in := &Node{TypeSet: []string{"object"}, Properties: map[string]*Node{}, AdditionalProperties: &Additional{False: true}}
		for _, p := range props {
			in.Properties[p] = &Node{TypeSet: []string{"string"}}
		}
		return &Tool{Input: in, Output: &Node{TypeSet: []string{"object"}}}
	}
	want = Surface{Tools: map[string]*Tool{"get_task": tool("taskId"), "cancel_task": tool(), "delete_task": tool(), "list_runs": tool()}}
	served = Surface{Tools: map[string]*Tool{"get_task": tool("taskId"), "cancel_task": tool(), "delete_task": tool()}}
	return want, served
}

// TestCheckLive: an exact surface with its pending tool answers nothing; each departure answers a
// line naming its tool or pointer.
func TestCheckLive(t *testing.T) {
	want, served := liveFixture()
	if d := CheckLive(want, served, []string{"list_runs"}); len(d) != 0 {
		t.Fatalf("the exact surface departs: %v", d)
	}
	for _, c := range []struct {
		name    string
		pending []string
		edit    func(want, served Surface)
		says    string
	}{
		{"a served property no delta names", []string{"list_runs"}, func(_, s Surface) {
			s.Tools["get_task"].Input.Properties["shiny"] = &Node{TypeSet: []string{"string"}}
		}, "extra /tools/get_task/input/properties/shiny"},
		{"a removed tool served", []string{"list_runs"}, func(_, s Surface) { s.Tools["get_transitions"] = &Tool{} }, "get_transitions"},
		{"an added tool the contract lacks", []string{"list_runs"}, func(w, _ Surface) { delete(w.Tools, "delete_task") }, "delete_task is registered and is not a tool of the contract"},
		{"a pending tool registered", []string{"list_runs"}, func(w, s Surface) { s.Tools["list_runs"] = w.Tools["list_runs"] }, "list_runs is registered and still listed in pending.json"},
		{"a contract tool neither served nor pending", nil, func(_, _ Surface) {}, "list_runs is a tool of the contract, is not registered and is not in pending.json"},
		{"a pending name the contract lacks", []string{"list_runs", "nope"}, func(_, _ Surface) {}, "pending.json lists nope, which is not a tool of the contract"},
		{"a pending name twice", []string{"list_runs", "list_runs"}, func(_, _ Surface) {}, "pending.json lists list_runs twice"},
	} {
		w, s := liveFixture()
		c.edit(w, s)
		d := CheckLive(w, s, c.pending)
		if !slices.ContainsFunc(d, func(l string) bool { return strings.Contains(l, c.says) }) {
			t.Errorf("%s: CheckLive answered %v, want a line saying %q", c.name, d, c.says)
		}
	}
}

// TestPendingIsInTheContract: every name pending.json lists is a tool of expected.json (templates:
// of tools.json), once, and the two lists are not empty, so the file says what it claims.
func TestPendingIsInTheContract(t *testing.T) {
	var p Pending
	if err := ReadJSON("pending.json", &p); err != nil {
		t.Fatal(err)
	}
	exp := committed(t)
	names := p.ToolNames()
	if len(names) == 0 || len(p.TemplateNames()) == 0 {
		t.Fatalf("pending.json lists %d tools and %d templates; remove an empty list rather than keep it", len(names), len(p.TemplateNames()))
	}
	for i, n := range names {
		if _, ok := exp.Tools[n]; !ok {
			t.Errorf("pending.json lists %s, which expected.json does not have", n)
		}
		if i > 0 && names[i-1] == n {
			t.Errorf("pending.json lists %s twice", n)
		}
	}
	var src struct {
		ResourceTemplates []struct {
			Name string `json:"name"`
		} `json:"resourceTemplates"`
	}
	if err := ReadJSON("tools.json", &src); err != nil {
		t.Fatal(err)
	}
	for _, n := range p.TemplateNames() {
		if !slices.ContainsFunc(src.ResourceTemplates, func(r struct {
			Name string `json:"name"`
		}) bool {
			return r.Name == n
		}) {
			t.Errorf("pending.json lists resource template %s, which tools.json does not have", n)
		}
	}
	t.Logf("pending.json: %d tools of expected.json's %d, %d resource templates", len(names), len(exp.Tools), len(p.TemplateNames()))
}

// TestErrorDeltasNameCodes: each errorDeltas line names two different codes of errors.json and why.
func TestErrorDeltasNameCodes(t *testing.T) {
	var d Deltas
	if err := ReadJSON("deltas.json", &d); err != nil {
		t.Fatal(err)
	}
	var e struct {
		Codes []struct {
			Code string `json:"code"`
		} `json:"codes"`
	}
	if err := ReadJSON("errors.json", &e); err != nil {
		t.Fatal(err)
	}
	known := func(c string) bool {
		return slices.ContainsFunc(e.Codes, func(k struct {
			Code string `json:"code"`
		}) bool {
			return k.Code == c
		})
	}
	for _, x := range d.ErrorDeltas {
		if x.Case == "" || x.Why == "" || !known(x.Conductor) || !known(x.Go) || x.Conductor == x.Go {
			t.Errorf("errorDeltas %+v: want a case, a why and two different codes of errors.json", x)
		}
	}
	t.Logf("deltas.json errorDeltas: %d", len(d.ErrorDeltas))
}
