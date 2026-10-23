package contract

import (
	"fmt"
	"slices"
)

// Pending is pending.json: the contract's tools and resource templates whose lane has not landed,
// keyed by lane. The live comparison subtracts them, and refuses a pending name that is registered,
// so a lane that registers its tools removes them here in the same change and the list only
// shrinks.
type Pending struct {
	Tools             map[string][]string `json:"tools"`
	ResourceTemplates map[string][]string `json:"resourceTemplates"`
}

// ToolNames is every pending tool, sorted.
func (p Pending) ToolNames() []string { return flatten(p.Tools) }

// TemplateNames is every pending resource template, sorted.
func (p Pending) TemplateNames() []string { return flatten(p.ResourceTemplates) }

func flatten(byLane map[string][]string) []string {
	var out []string
	for _, names := range byLane {
		out = append(out, names...)
	}
	slices.Sort(out)
	return out
}

// CheckLive compares served, the canonical tools/list of a running server, with want, the
// contract, less pending. It answers every departure, each naming its tool or pointer, in this
// order: a pending name twice or not in want; a pending tool that is registered; CheckSurface's
// refusal (a removed tool served, an added one missing); a served tool want lacks; a want tool
// neither served nor pending; then contract.Diff over the tools both hold. None is exact.
func CheckLive(want, served Surface, pending []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, name := range pending {
		if seen[name] {
			out = append(out, fmt.Sprintf("pending.json lists %s twice", name))
		}
		seen[name] = true
		if _, ok := want.Tools[name]; !ok {
			out = append(out, fmt.Sprintf("pending.json lists %s, which is not a tool of the contract", name))
		}
		if _, ok := served.Tools[name]; ok {
			out = append(out, fmt.Sprintf("%s is registered and still listed in pending.json: its lane removes it from pending.json when it registers it", name))
		}
	}
	if err := CheckSurface(served); err != nil {
		out = append(out, "tools/list: "+err.Error())
	}
	w, g := Surface{Tools: map[string]*Tool{}}, Surface{Tools: map[string]*Tool{}}
	for _, name := range sortedNames(served) {
		if _, ok := want.Tools[name]; !ok {
			out = append(out, fmt.Sprintf("%s is registered and is not a tool of the contract (a tool conductor lacks enters through deltas.json addedTools)", name))
			continue
		}
		w.Tools[name], g.Tools[name] = want.Tools[name], served.Tools[name]
	}
	for _, name := range sortedNames(want) {
		if _, ok := served.Tools[name]; !ok && !seen[name] {
			out = append(out, fmt.Sprintf("%s is a tool of the contract, is not registered and is not in pending.json", name))
		}
	}
	return append(out, Diff(w, g)...)
}

func sortedNames(s Surface) []string {
	names := make([]string, 0, len(s.Tools))
	for name := range s.Tools {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
