package cli

import (
	"context"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/contract"
)

// The live contract (R1.10; plan §Architecture "Contract fidelity", §Verification "Contract"): the
// serve composition over each selected engine, read by a workspace admin's go-sdk client, against
// contract/. It lives in the composition root because only internal/mcp and internal/cli may hold
// an MCP client (ADR-0003 §C row 7) and only here is serve's wiring (compose, serveUntil).

var contractDir = filepath.Join("..", "..", "contract")

// liveSurface is what a's session lists: every tool canonicalised, and the prompt and resource
// template lists.
type liveSurface struct {
	tools     contract.Surface
	prompts   []*sdk.Prompt
	templates []*sdk.ResourceTemplate
}

func (a *agent) surface() liveSurface {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s := liveSurface{tools: contract.Surface{Tools: map[string]*contract.Tool{}}}
	for tl, err := range a.cs.Tools(ctx, nil) {
		if err != nil {
			a.t.Fatalf("tools/list: %v", err)
		}
		in, err := contract.Canonicalize(tl.InputSchema)
		if err != nil {
			a.t.Fatalf("%s input: %v", tl.Name, err)
		}
		out, err := contract.Canonicalize(tl.OutputSchema)
		if err != nil {
			a.t.Fatalf("%s output: %v", tl.Name, err)
		}
		if _, dup := s.tools.Tools[tl.Name]; dup {
			a.t.Fatalf("tools/list names %s twice", tl.Name)
		}
		s.tools.Tools[tl.Name] = &contract.Tool{Input: in, Output: out}
	}
	for p, err := range a.cs.Prompts(ctx, nil) {
		if err != nil {
			a.t.Fatalf("prompts/list: %v", err)
		}
		s.prompts = append(s.prompts, p)
	}
	for r, err := range a.cs.ResourceTemplates(ctx, nil) {
		if err != nil {
			a.t.Fatalf("resources/templates/list: %v", err)
		}
		s.templates = append(s.templates, r)
	}
	return s
}

// templatesAdded are the resource templates this server serves that conductor does not (plan
// §Target surface: board-feed and its -cursor form).
var templatesAdded = map[string]string{"board-feed-cursor": "batondeck://{projectId}/board/{boardId}/feed{?cursor}"}

type promptArg struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
}

// conductorSurface is contract/tools.json's prompts and resource templates.
type conductorSurface struct {
	Prompts []struct {
		Name      string      `json:"name"`
		Arguments []promptArg `json:"arguments"`
	} `json:"prompts"`
	ResourceTemplates []struct {
		Name        string `json:"name"`
		URITemplate string `json:"uriTemplate"`
	} `json:"resourceTemplates"`
}

func readContract(t *testing.T, name string, v any) {
	t.Helper()
	if err := contract.ReadJSON(filepath.Join(contractDir, name), v); err != nil {
		t.Fatal(err)
	}
}

// TestLiveContract: a workspace admin's tools/list, canonicalised, equals contract/expected.json
// less contract/pending.json exactly, input and output schema of every tool, and equals the
// derivation canonical(tools.json + admin-tools.json) ⊖ deltas.json computed afresh, so a deltas.json
// edit fails here even before expected.json is rewritten. A pending tool that is registered fails,
// and so does a contract tool neither registered nor pending. prompts/list equals tools.json's
// prompts by name and arguments; resources/templates/list equals tools.json's templates less the
// pending ones plus templatesAdded, by name and URI template.
func TestLiveContract(t *testing.T) {
	var pending contract.Pending
	readContract(t, "pending.json", &pending)
	var expected contract.Surface
	readContract(t, "expected.json", &expected)
	derived, err := contract.Derive(contractDir)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	var conductor conductorSurface
	readContract(t, "tools.json", &conductor)

	onJoined(t, func(t *testing.T, j *joined) {
		live := j.account("root", true).surface()
		for _, want := range []struct {
			name string
			s    contract.Surface
		}{{"contract/expected.json", expected}, {"the derivation from tools.json, admin-tools.json and deltas.json", derived}} {
			if d := contract.CheckLive(want.s, live.tools, pending.ToolNames()); len(d) > 0 {
				t.Errorf("tools/list departs from %s less contract/pending.json at %d place(s):\n  %s", want.name, len(d), strings.Join(d, "\n  "))
			}
		}
		t.Logf("%s: tools/list %d tools compared with contract/expected.json exactly (input and output); pending %d (contract/pending.json); contract %d",
			j.engine, len(live.tools.Tools), len(pending.ToolNames()), len(expected.Tools))

		wantPrompts, gotPrompts := map[string][]promptArg{}, map[string][]promptArg{}
		for _, p := range conductor.Prompts {
			wantPrompts[p.Name] = p.Arguments
		}
		for _, p := range live.prompts {
			args := []promptArg{}
			for _, a := range p.Arguments {
				args = append(args, promptArg{a.Name, a.Required})
			}
			gotPrompts[p.Name] = args
		}
		if !maps.EqualFunc(wantPrompts, gotPrompts, slices.Equal) {
			t.Errorf("prompts/list = %v, want contract/tools.json's %v", gotPrompts, wantPrompts)
		}

		wantTpl := maps.Clone(templatesAdded)
		conductorTpl := map[string]bool{}
		for _, r := range conductor.ResourceTemplates {
			conductorTpl[r.Name] = true
			if !slices.Contains(pending.TemplateNames(), r.Name) {
				wantTpl[r.Name] = r.URITemplate
			}
		}
		gotTpl := map[string]string{}
		for _, r := range live.templates {
			gotTpl[r.Name] = r.URITemplate
		}
		for _, name := range pending.TemplateNames() {
			if !conductorTpl[name] {
				t.Errorf("pending.json lists resource template %s, which contract/tools.json does not have", name)
			}
			if _, ok := gotTpl[name]; ok {
				t.Errorf("resource template %s is served and still listed in pending.json", name)
			}
		}
		if !maps.Equal(wantTpl, gotTpl) {
			t.Errorf("resources/templates/list = %v, want %v", gotTpl, wantTpl)
		}
		t.Logf("%s: prompts %d compared with contract/tools.json; templates %d compared with %d of contract/tools.json - %d pending + %d added",
			j.engine, len(gotPrompts), len(gotTpl), len(conductor.ResourceTemplates), len(pending.TemplateNames()), len(templatesAdded))
	})
}

// The whole served surface by name (conductor's test/integration/contract.test.ts:53-54): a tool,
// prompt or template that appears or vanishes fails here, beside the pending.json change that
// should come with it.
var (
	snapshotTools = []string{
		"add_column", "add_context_item", "add_dependency", "add_member", "add_subtask", "attach_file",
		"block_task", "cancel_task", "claim_next", "claim_task", "complete_task", "create_board",
		"create_project", "create_task", "delete_project", "delete_task", "fail_task", "get_board",
		"get_project", "get_task", "get_task_context", "handoff_task", "heartbeat_task",
		"list_attachments", "list_boards", "list_projects", "list_subtasks", "list_tasks", "move_task",
		"next_task", "read_memory", "reap_stale_leases", "recall_memory", "release_task",
		"remove_column", "remove_dependency", "remove_member", "search_tasks", "set_summary",
		"update_column", "update_settings", "update_task", "write_memory",
	}
	// snapshotAdminOnly are listed to a workspace admin alone (ADR-0004: the on-call tools).
	snapshotAdminOnly = []string{
		"create_integration", "delete_integration", "list_deliveries", "list_integrations", "read_delivery_raw",
		"replay_delivery", "rotate_integration_secret", "send_test_event", "update_integration",
	}
	snapshotPrompts   = []string{"decompose_into_subtasks", "pick_up_next_task", "summarize_for_handoff", "triage_inbox"}
	snapshotTemplates = []string{"board", "board-feed", "board-feed-cursor", "task", "task-context"}
)

// TestSurfaceSnapshot: a workspace admin's and a member's tools/list, prompts/list and
// resources/templates/list names are exactly the snapshot: the admin's tools are snapshotTools and
// snapshotAdminOnly, the member's snapshotTools alone.
func TestSurfaceSnapshot(t *testing.T) {
	onJoined(t, func(t *testing.T, j *joined) {
		for _, a := range []*agent{j.account("root", true), j.user("ada")} {
			wantTools := snapshotTools
			if a.login == "root" {
				wantTools = slices.Sorted(slices.Values(append(slices.Clone(snapshotTools), snapshotAdminOnly...)))
			}
			live := a.surface()
			tools := slices.Sorted(maps.Keys(live.tools.Tools))
			var prompts, templates []string
			for _, p := range live.prompts {
				prompts = append(prompts, p.Name)
			}
			for _, r := range live.templates {
				templates = append(templates, r.Name)
			}
			slices.Sort(prompts)
			slices.Sort(templates)
			for _, c := range []struct {
				what      string
				got, want []string
			}{{"tools", tools, wantTools}, {"prompts", prompts, snapshotPrompts}, {"resource templates", templates, snapshotTemplates}} {
				if !slices.Equal(c.got, c.want) {
					t.Errorf("%s's %s: served but not in the snapshot %v; in the snapshot but not served %v",
						a.login, c.what, minus(c.got, c.want), minus(c.want, c.got))
				}
			}
			t.Logf("%s as %s: %d tools, %d prompts, %d resource templates served, compared with the snapshot's %d, %d, %d", j.engine, a.login, len(tools), len(prompts), len(templates), len(wantTools), len(snapshotPrompts), len(snapshotTemplates))
		}
	})
}

// minus is the names of a not in b.
func minus(a, b []string) []string {
	var out []string
	for _, s := range a {
		if !slices.Contains(b, s) {
			out = append(out, s)
		}
	}
	return out
}
