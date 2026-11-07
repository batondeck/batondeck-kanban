//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

// scenario is one realistic flow. tools, prompts and templates are what it is the scenario for: the
// coverage guard holds every listed one to at least one scenario naming it, and holds each scenario
// to answering, at least once, every one it names.
type scenario struct {
	area      string // the tool area (internal/mcp/tools), or prompts, resources, transport
	name      string
	tools     []string
	prompts   []string
	templates []string
	run       func(t *testing.T, e *env)
}

func (s scenario) id() string { return s.area + "/" + s.name }

var scenarios []scenario

// register adds a scenario; each area file registers its own in an init.
func register(s scenario) { scenarios = append(scenarios, s) }

// env is one engine's running server and a session per identity.
type env struct {
	srv                                                       *Server
	admin, master, worker, workerB, worker2, viewer, outsider *Client
}

// surface is what the live server lists.
type surface struct {
	tools, prompts, templateNames, templateURIs []string
}

func TestE2E(t *testing.T) {
	for _, eng := range engines(t) {
		t.Run(eng.name, func(t *testing.T) { runEngine(t, eng) })
	}
}

func runEngine(t *testing.T, eng engine) {
	began := time.Now()
	srv := startServer(t, eng)
	e := &env{srv: srv}
	e.admin = srv.connect(t, srv.tokens["admin"], "e2e-admin")
	e.master = srv.connect(t, srv.tokens["master"], "e2e-master")
	e.worker = srv.connect(t, srv.tokens["worker/e2e-wa"], "e2e-worker-a")
	e.workerB = srv.connect(t, srv.tokens["worker/e2e-wb"], "e2e-worker-b")
	e.worker2 = srv.connect(t, srv.tokens["worker2"], "e2e-worker2")
	e.viewer = srv.connect(t, srv.tokens["viewer"], "e2e-viewer")
	e.outsider = srv.connect(t, srv.tokens["outsider"], "e2e-outsider")

	live := surface{
		tools:   e.admin.List(t, "tools/list", "tools", "name"),
		prompts: e.admin.List(t, "prompts/list", "prompts", "name"),
	}
	live.templateNames = e.admin.List(t, "resources/templates/list", "resourceTemplates", "name")
	live.templateURIs = e.admin.List(t, "resources/templates/list", "resourceTemplates", "uriTemplate")
	srv.cov.setTemplates(live.templateNames, live.templateURIs)
	t.Logf("%s: the live server lists %d tools, %d prompts, %d resource templates", eng.name, len(live.tools), len(live.prompts), len(live.templateNames))

	t.Run("guard/registered", func(t *testing.T) { guardRegistered(t, live, scenarios) })
	t.Run("guard/areas", func(t *testing.T) { guardAreas(t, scenarios) })

	ran := map[string]bool{}
	passed := map[string]bool{}
	for _, sc := range sorted(scenarios) {
		// A scenario -run filters out is never entered, and t.Run answers true for it: only the
		// closure marks a scenario as run.
		ok := t.Run(sc.id(), func(t *testing.T) {
			ran[sc.id()] = true
			srv.cov.setScenario(sc.id())
			defer srv.cov.setScenario("")
			sc.run(t, e)
		})
		passed[sc.id()] = ran[sc.id()] && ok
	}
	t.Run("guard/exercised", func(t *testing.T) { guardExercised(t, srv.cov, scenarios, ran, passed) })
	report(t, eng.name, live, srv.cov, scenarios, ran, passed, time.Since(began))
}

func sorted(list []scenario) []scenario {
	out := slices.Clone(list)
	sort.SliceStable(out, func(i, j int) bool { return out[i].id() < out[j].id() })
	return out
}

// guardRegistered fails, naming each one, for every tool, prompt and resource template the live
// server lists that no scenario names, and for every name a scenario gives that the server does not
// list (a renamed or removed tool's scenario would otherwise pass vacuously).
func guardRegistered(t *testing.T, live surface, list []scenario) {
	for _, k := range []struct {
		kind   string
		listed []string
		named  func(scenario) []string
	}{
		{"tool", live.tools, func(s scenario) []string { return s.tools }},
		{"prompt", live.prompts, func(s scenario) []string { return s.prompts }},
		{"resource template", live.templateNames, func(s scenario) []string { return s.templates }},
	} {
		named := map[string]bool{}
		for _, s := range list {
			for _, n := range k.named(s) {
				named[n] = true
				if !has(k.listed, n) {
					t.Errorf("scenario %s names %s %q, which the live server does not list", s.id(), k.kind, n)
				}
			}
		}
		var missing []string
		for _, n := range k.listed {
			if !named[n] {
				missing = append(missing, n)
			}
		}
		for _, n := range missing {
			t.Errorf("%s %q is listed by the live server and has no e2e scenario", k.kind, n)
		}
		if len(missing) == 0 {
			t.Logf("every listed %s (%d) has a scenario", k.kind, len(k.listed))
		}
	}
}

// toolAreas is each tool's area as internal/mcp/tools registers it (mcp.Tool(s, "<area>",
// "<name>", ...)): the areas the scenarios are grouped by.
func toolAreas(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join("..", "..", "internal", "mcp", "tools", "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("reading internal/mcp/tools: %v", err)
	}
	re := regexp.MustCompile(`mcp\.Tool\(s, "([a-z_]+)", "([a-z_]+)"`)
	out := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			out[m[2]] = m[1]
		}
	}
	return out
}

// guardAreas holds each scenario to its area: every tool it names is registered in that area.
func guardAreas(t *testing.T, list []scenario) {
	areas := toolAreas(t)
	for _, s := range list {
		for _, tool := range s.tools {
			if a, ok := areas[tool]; !ok {
				t.Errorf("scenario %s names %s, which internal/mcp/tools registers in no area", s.id(), tool)
			} else if a != s.area {
				t.Errorf("scenario %s names %s, registered in area %s", s.id(), tool, a)
			}
		}
		for _, p := range s.prompts {
			if s.area != "prompts" {
				t.Errorf("scenario %s names prompt %s outside area prompts", s.id(), p)
			}
		}
		for _, r := range s.templates {
			if s.area != "resources" {
				t.Errorf("scenario %s names template %s outside area resources", s.id(), r)
			}
		}
	}
}

// guardExercised holds every scenario that ran to having answered each tool, prompt and template
// it names at least once.
func guardExercised(t *testing.T, cov *coverage, list []scenario, ran, passed map[string]bool) {
	for _, s := range list {
		if !ran[s.id()] || !passed[s.id()] {
			continue // a failed scenario has already failed the run
		}
		for _, k := range []struct {
			kind  string
			names []string
		}{{kindTool, s.tools}, {kindPrompt, s.prompts}, {kindTemplate, s.templates}} {
			for _, n := range k.names {
				if !cov.in(k.kind, n, s.id(), false) {
					t.Errorf("scenario %s names %s %s and never had it answer", s.id(), k.kind, n)
				}
			}
		}
	}
}

// report logs the per-area counts and the coverage totals.
func report(t *testing.T, engine string, live surface, cov *coverage, list []scenario, ran, passed map[string]bool, took time.Duration) {
	type tally struct {
		scenarios, passed int
		names             map[string]bool
	}
	areas := map[string]*tally{}
	covered := map[string]map[string]bool{kindTool: {}, kindPrompt: {}, kindTemplate: {}}
	refusals := map[string]bool{}
	for _, s := range list {
		if !ran[s.id()] {
			continue
		}
		a := areas[s.area]
		if a == nil {
			a = &tally{names: map[string]bool{}}
			areas[s.area] = a
		}
		a.scenarios++
		if passed[s.id()] {
			a.passed++
		}
		for kind, names := range map[string][]string{kindTool: s.tools, kindPrompt: s.prompts, kindTemplate: s.templates} {
			for _, n := range names {
				a.names[n] = true
				if passed[s.id()] && cov.in(kind, n, s.id(), false) {
					covered[kind][n] = true
				}
				if cov.in(kind, n, s.id(), true) {
					refusals[kind+":"+n] = true
				}
			}
		}
	}
	names := make([]string, 0, len(areas))
	for a := range areas {
		names = append(names, a)
	}
	sort.Strings(names)
	var b strings.Builder
	fmt.Fprintf(&b, "%s: per-area counts (area: scenarios passed/run, tools|prompts|templates named)\n", engine)
	for _, a := range names {
		fmt.Fprintf(&b, "  %-15s %2d/%-2d  %d\n", a, areas[a].passed, areas[a].scenarios, len(areas[a].names))
	}
	count := func(kind string, listed []string) (int, []string) {
		n, refusedIn := 0, []string{}
		for _, x := range listed {
			if covered[kind][x] {
				n++
			}
			if !refusals[kind+":"+x] {
				refusedIn = append(refusedIn, x)
			}
		}
		return n, refusedIn
	}
	nt, noRefusalT := count(kindTool, live.tools)
	np, _ := count(kindPrompt, live.prompts)
	nr, _ := count(kindTemplate, live.templateNames)
	fmt.Fprintf(&b, "%s: covered tools %d/%d, prompts %d/%d, resource templates %d/%d\n", engine, nt, len(live.tools), np, len(live.prompts), nr, len(live.templateNames))
	fmt.Fprintf(&b, "%s: tools refused at least once in their own scenario: %d/%d; not refused: %s\n", engine, len(live.tools)-len(noRefusalT), len(live.tools), strings.Join(noRefusalT, " "))
	fmt.Fprintf(&b, "%s: runtime %s", engine, took.Round(time.Millisecond))
	t.Log(b.String())
}
