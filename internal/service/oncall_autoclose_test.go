package service

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/blob"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/memstore"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// autocloseEnv is one engine's core with an autoclose integration and alice, its project's admin.
type autocloseEnv struct {
	t       *testing.T
	ctx     context.Context
	st      store.Store
	c       *Core
	alice   domain.Caller
	project string
	board   string
	iid     string
	secret  string
	n       int
	clock   *clock
}

func newAutocloseEnv(t *testing.T, st store.Store) *autocloseEnv {
	t.Helper()
	ctx := context.Background()
	files, err := blob.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	signer, err := blob.NewSigner(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatal(err)
	}
	clk := &clock{t: time.Now()}
	c := New(st, Options{ToolCallFlush: -1, Now: clk.now, Sealer: oncallKeyring(t), Media: MediaOptions{Files: files, Signer: signer, BaseURL: "http://x"}})
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	e := &autocloseEnv{t: t, ctx: ctx, st: st, c: c, clock: clk, alice: domain.Caller{Identity: "alice", Agent: "claude", TokenScope: "alice", WorkspaceAdmin: true}}
	p, err := c.Projects().Create(ctx, e.alice, CreateProjectReq{Name: "P"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Boards().Create(ctx, e.alice, CreateBoardReq{ProjectID: p.ProjectID, Name: "B"})
	if err != nil {
		t.Fatal(err)
	}
	in, err := c.Oncall().CreateIntegration(ctx, e.alice, CreateIntegrationReq{ProjectID: p.ProjectID, BoardID: b.Board.ID, Name: "n", Source: "generic",
		Lifecycle: "autoclose", Auth: IntegrationAuthInput{Mode: "hmac"}, Routing: IntegrationRouting{Mode: "broadcast"}, Limits: IntegrationLimitsIn{RatePerMin: 6000}})
	if err != nil {
		t.Fatal(err)
	}
	e.project, e.board, e.iid, e.secret = p.ProjectID, b.Board.ID, in.Integration.ID, in.Secret
	return e
}

// deliver sends one signed generic delivery and answers its one result.
func (e *autocloseEnv) deliver(fp, status string) HookResult {
	e.t.Helper()
	e.n++
	// A second apart, so the per-id pre-check (30 a burst) never refuses the table's deliveries.
	e.clock.advance(time.Second)
	out, err := e.c.Hook(e.ctx, e.iid, signedHook(e.secret, e.clock.now(), genericBody(fp, status, "incident "+fp, "deliveryId", fp+"-"+status+"-"+string(rune('a'+e.n)))))
	if err != nil || out.Refusal != "" || len(out.Results) != 1 {
		e.t.Fatalf("%s %s: %+v, %v", fp, status, out, err)
	}
	return out.Results[0]
}

func (e *autocloseEnv) version(id string) int64 {
	e.t.Helper()
	task, err := e.st.GetTask(e.ctx, e.project, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return task.Version
}

func (e *autocloseEnv) claim(id string) string {
	e.t.Helper()
	r, err := e.c.Lifecycle().Claim(e.ctx, e.alice, ClaimTaskReq{ProjectID: e.project, TaskID: id})
	if err != nil {
		e.t.Fatal(err)
	}
	return r.LeaseID
}

// race starts n runs of id and answers their ids.
func (e *autocloseEnv) race(id string, n int64) []string {
	e.t.Helper()
	r, err := e.c.Runs().Start(e.ctx, e.alice, StartRunsReq{ProjectID: e.project, TaskID: id, Version: e.version(id), Count: n})
	must(e.t, err)
	var ids []string
	for _, run := range r.Runs {
		ids = append(ids, run.ID)
	}
	return ids
}

func (e *autocloseEnv) other() string {
	e.t.Helper()
	r, err := e.c.Tasks().Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board, Title: "other"})
	if err != nil {
		e.t.Fatal(err)
	}
	return r.Task.ID
}

// taskWrite is one human write to the incident ticket. It answers the ids of tasks it made, which
// must outlive the resolve. want is the resolve's result; gone that the write itself removed the
// ticket.
type taskWrite struct {
	name  string
	apply func(e *autocloseEnv, incident string) []string
	want  string
	gone  bool
	// after checks what the write made, once the resolve has run.
	after func(e *autocloseEnv, incident string)
}

// taskWrites is every task-taking write tool of the surface (contract/expected.json, not a read):
// each is a write a human or an agent makes to an incident ticket, which autoclose must then not
// delete. A tool still in contract/pending.json is listed by pendingWrites until it lands.
var taskWrites = map[string][]taskWrite{
	"add_context_item": {{name: "a note", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
		_, err := e.c.Context().AddItem(e.ctx, e.alice, AddContextItemReq{ProjectID: e.project, TaskID: id, Kind: "note", Body: "mine"})
		must(e.t, err)
		return nil
	}}},
	"add_dependency": {
		{name: "relates from the incident", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
			o := e.other()
			_, err := e.c.Context().AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: id, ToTaskID: o, Type: "relates"})
			must(e.t, err)
			return []string{o}
		}},
		{name: "relates to the incident", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
			o := e.other()
			_, err := e.c.Context().AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: o, ToTaskID: id, Type: "relates"})
			must(e.t, err)
			return []string{o}
		}},
		{name: "the incident blocks another", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
			o := e.other()
			_, err := e.c.Context().AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: id, ToTaskID: o, Type: "blocks"})
			must(e.t, err)
			return []string{o}
		}},
	},
	"add_subtask": {{name: "a subtask", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
		r, err := e.c.Context().AddSubtask(e.ctx, e.alice, AddSubtaskReq{ProjectID: e.project, ParentTaskID: id, Title: "check replica lag"})
		must(e.t, err)
		return []string{r.Task.ID}
	}}},
	"attach_file": {{name: "a pending attachment", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
		_, err := e.c.Media().AttachFile(e.ctx, e.alice, AttachFileReq{ProjectID: e.project, TaskID: id, FileName: "graph.png", MimeType: "image/png", Kind: "image", Bytes: 10})
		must(e.t, err)
		return nil
	}}},
	"block_task": {{name: "claimed and blocked", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
		_, err := e.c.Lifecycle().Block(e.ctx, e.alice, BlockTaskReq{ProjectID: e.project, TaskID: id, LeaseID: e.claim(id), Reason: "waiting"})
		must(e.t, err)
		return nil
	}}},
	"cancel_task": {{name: "cancelled (lands DONE)", want: "recorded", apply: func(e *autocloseEnv, id string) []string {
		_, err := e.c.Lifecycle().Cancel(e.ctx, e.alice, CancelTaskReq{ProjectID: e.project, TaskID: id, Version: e.version(id), Reason: "dup"})
		must(e.t, err)
		return nil
	}}},
	"claim_task": {{name: "claimed", want: "annotated", apply: func(e *autocloseEnv, id string) []string { e.claim(id); return nil }}},
	"complete_task": {{name: "claimed and completed", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
		_, err := e.c.Lifecycle().Complete(e.ctx, e.alice, CompleteTaskReq{ProjectID: e.project, TaskID: id, LeaseID: e.claim(id), Deliverable: "fixed"})
		must(e.t, err)
		return nil
	}}},
	"create_task": {{name: "a child by parentTaskId", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
		r, err := e.c.Tasks().Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board, Title: "child", ParentTaskID: &id})
		must(e.t, err)
		return []string{r.Task.ID}
	}}},
	"delete_task": {{name: "deleted by hand", want: "recorded", gone: true, apply: func(e *autocloseEnv, id string) []string {
		_, err := e.c.Tasks().Delete(e.ctx, e.alice, DeleteTaskReq{ProjectID: e.project, TaskID: id, Version: e.version(id)})
		must(e.t, err)
		return nil
	}}},
	"fail_task": {{name: "claimed and failed", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
		_, err := e.c.Lifecycle().Fail(e.ctx, e.alice, FailTaskReq{ProjectID: e.project, TaskID: id, LeaseID: e.claim(id), Reason: "flaky"})
		must(e.t, err)
		return nil
	}}},
	"handoff_task": {{name: "claimed and handed off", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
		_, err := e.c.Lifecycle().Handoff(e.ctx, e.alice, HandoffTaskReq{ProjectID: e.project, TaskID: id, LeaseID: e.claim(id), ToAgent: "bob", MemoryNote: "yours"})
		must(e.t, err)
		return nil
	}}},
	"heartbeat_task": {{name: "claimed and heartbeat", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
		_, err := e.c.Lifecycle().Heartbeat(e.ctx, e.alice, HeartbeatTaskReq{ProjectID: e.project, TaskID: id, LeaseID: e.claim(id)})
		must(e.t, err)
		return nil
	}}},
	"move_task": {{name: "moved to IN_PROGRESS", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
		_, err := e.c.Tasks().Move(e.ctx, e.alice, MoveTaskReq{ProjectID: e.project, TaskID: id, Version: e.version(id), ToStatus: domain.InProgress})
		must(e.t, err)
		return nil
	}}},
	"release_task": {{name: "claimed and released", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
		_, err := e.c.Lifecycle().Release(e.ctx, e.alice, ReleaseTaskReq{ProjectID: e.project, TaskID: id, LeaseID: e.claim(id)})
		must(e.t, err)
		return nil
	}}},
	// The race tools (R2.2): each moves the incident's version, so the resolve annotates it, and the
	// runs they made (or the winner pick_run keeps) outlive it.
	"start_runs": {{name: "raced", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
		return e.race(id, 2)
	}}},
	"open_run": {{name: "raced and a run opened", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
		runs := e.race(id, 1)
		r, err := e.c.Runs().Open(e.ctx, e.alice, OpenRunReq{ProjectID: e.project, TaskID: id})
		must(e.t, err)
		return append(runs, r.Run.ID)
	}}},
	"pick_run": {{name: "raced and a winner picked", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
		runs := e.race(id, 2)
		_, err := e.c.Lifecycle().Complete(e.ctx, e.alice, CompleteTaskReq{ProjectID: e.project, TaskID: runs[0], LeaseID: e.claim(runs[0]), Deliverable: "the fix"})
		must(e.t, err)
		_, err = e.c.Runs().Pick(e.ctx, e.alice, PickRunReq{ProjectID: e.project, TaskID: id, RunTaskID: runs[0], Version: e.version(id)})
		must(e.t, err)
		return runs[:1]
	}}},
	"cancel_runs": {{name: "raced and the race cancelled (back TODO)", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
		e.race(id, 2)
		_, err := e.c.Runs().Cancel(e.ctx, e.alice, CancelRunsReq{ProjectID: e.project, TaskID: id, Version: e.version(id)})
		must(e.t, err)
		return nil
	}}},
	"set_sprint": {{name: "added to a sprint", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
		sp, err := e.c.Sprints().Create(e.ctx, e.alice, CreateSprintReq{ProjectID: e.project, BoardID: e.board, Name: "s", Goal: "g", Acceptance: "a"})
		must(e.t, err)
		_, err = e.c.Sprints().Set(e.ctx, e.alice, SetSprintReq{ProjectID: e.project, TaskID: id, Version: e.version(id), SprintID: sp.Sprint.ID, Action: "add"})
		must(e.t, err)
		return nil
	}}},
	"set_summary": {{name: "a summary", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
		_, err := e.c.Context().SetSummary(e.ctx, e.alice, SetSummaryReq{ProjectID: e.project, TaskID: id, Version: e.version(id), Summary: "on it"})
		must(e.t, err)
		return nil
	}}},
	"update_task": {{name: "retitled", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
		_, err := e.c.Tasks().Update(e.ctx, e.alice, UpdateTaskReq{ProjectID: e.project, TaskID: id, Version: e.version(id), Patch: TaskPatch{Title: "mine now"}})
		must(e.t, err)
		return nil
	}}},
	"write_memory": {
		{name: "task scope", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
			_, err := e.c.Context().WriteMemory(e.ctx, e.alice, WriteMemoryReq{ProjectID: e.project, TaskID: id, Scope: "task", Key: "k", Value: 1})
			must(e.t, err)
			return nil
		}},
		{name: "shared scope", want: "annotated", apply: func(e *autocloseEnv, id string) []string {
			_, err := e.c.Context().WriteMemory(e.ctx, e.alice, WriteMemoryReq{ProjectID: e.project, TaskID: id, Scope: "shared", Key: "k", Value: 1})
			must(e.t, err)
			return nil
		}},
		// DEVIATION (decided in R2.9's review round): a project entry only names the ticket as its
		// provenance, and delete_task's cascade keeps it, so it does not hold the ticket open.
		{name: "project scope naming the incident (kept by the cascade)", want: "closed", apply: func(e *autocloseEnv, id string) []string {
			_, err := e.c.Context().WriteMemory(e.ctx, e.alice, WriteMemoryReq{ProjectID: e.project, TaskID: id, Scope: "project", Key: "postmortem", Value: "x"})
			must(e.t, err)
			return nil
		}, after: func(e *autocloseEnv, id string) {
			got, err := e.st.RecallMemory(e.ctx, store.RecallMemoryQuery{Scope: store.MemoryProject, ProjectID: e.project, Key: "postmortem", Limit: 10})
			if err != nil || len(got) != 1 {
				e.t.Errorf("the project memory naming %s did not outlive the close: %+v, %v", id, got, err)
			}
		}},
	},
}

// pendingWrites are task-taking write tools still in contract/pending.json: each needs a case in
// taskWrites the day it leaves (add_comment is R2.1's), which TestAutocloseSparesEveryTaskWrite
// enforces.
var pendingWrites = []string{"ack_follow_up", "add_artifact", "add_comment", "add_follow_up", "detect_anomalies",
	"orchestrate_subtasks", "retract_artifact"}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// taskTakingWriteTools is every tool of contract/expected.json taking a task id that is not a read.
func taskTakingWriteTools(t *testing.T) []string {
	t.Helper()
	var doc struct {
		Tools map[string]struct {
			Input struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"input"`
		} `json:"tools"`
	}
	raw, err := os.ReadFile("../../contract/expected.json")
	must(t, err)
	must(t, json.Unmarshal(raw, &doc))
	var out []string
	for name, tool := range doc.Tools {
		p, ok := domain.ToolPosture(name)
		if !ok {
			t.Fatalf("%s is not classified", name)
		}
		if p.Tier == domain.TierRead {
			continue
		}
		for _, k := range []string{"taskId", "parentTaskId", "fromTaskId", "toTaskId"} {
			if _, ok := tool.Input.Properties[k]; ok {
				out = append(out, name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

func pendingTools(t *testing.T) map[string]bool {
	t.Helper()
	var doc struct {
		Tools map[string][]string `json:"tools"`
	}
	raw, err := os.ReadFile("../../contract/pending.json")
	must(t, err)
	must(t, json.Unmarshal(raw, &doc))
	out := map[string]bool{}
	for _, names := range doc.Tools {
		for _, n := range names {
			out[n] = true
		}
	}
	return out
}

// Review B1 of #56: every task-taking write tool, applied to an untouched incident under autoclose,
// keeps the resolve from deleting it (or, for the provenance-only project memory, is declared not
// to) and every task the write made outlives the resolve, on memstore, SQLite and Postgres. A
// task-taking write tool with no case here, or a pending one that has landed, fails the test. The
// positive control is the untouched incident the same resolve deletes.
func TestAutocloseSparesEveryTaskWrite(t *testing.T) {
	pending := pendingTools(t)
	for _, tool := range taskTakingWriteTools(t) {
		_, hasCase := taskWrites[tool]
		switch {
		case hasCase:
		case slices.Contains(pendingWrites, tool) && pending[tool]:
		case slices.Contains(pendingWrites, tool):
			t.Errorf("%s has left contract/pending.json: give it a case in taskWrites", tool)
		default:
			t.Errorf("%s writes to a task and has no case in taskWrites: say what it does to an incident autoclose may delete", tool)
		}
	}
	for tool := range taskWrites {
		if !slices.Contains(taskTakingWriteTools(t), tool) {
			t.Errorf("taskWrites has %s, which is not a task-taking write tool", tool)
		}
	}
	for _, eng := range []struct {
		name string
		open func(t *testing.T) store.Store
	}{
		{"memstore", func(t *testing.T) store.Store {
			s := memstore.New()
			t.Cleanup(func() { s.Close() })
			if _, err := s.Migrate(context.Background()); err != nil {
				t.Fatal(err)
			}
			return s
		}},
		{"sqlite", func(t *testing.T) store.Store { return storetest.SQLite(t) }},
		{"postgres", func(t *testing.T) store.Store { return storetest.Postgres(t) }},
	} {
		t.Run(eng.name, func(t *testing.T) {
			e := newAutocloseEnv(t, eng.open(t))
			control := e.deliver("control", "firing")
			if r := e.deliver("control", "resolved"); r.Result != "closed" {
				t.Fatalf("an untouched incident (positive control): %+v, want closed", r)
			}
			if _, err := e.st.GetTask(e.ctx, e.project, *control.TaskID); err == nil {
				t.Fatal("the closed control is still there")
			}
			tools := make([]string, 0, len(taskWrites))
			for tool := range taskWrites {
				tools = append(tools, tool)
			}
			sort.Strings(tools)
			for _, tool := range tools {
				for _, w := range taskWrites[tool] {
					fp := tool + "/" + w.name
					incident := *e.deliver(fp, "firing").TaskID
					made := w.apply(e, incident)
					got := e.deliver(fp, "resolved")
					_, err := e.st.GetTask(e.ctx, e.project, incident)
					t.Logf("%-16s %-52s resolve -> %s", tool, w.name, got.Result)
					switch {
					case got.Result != w.want:
						t.Errorf("%s (%s): the resolve was %s, want %s", tool, w.name, got.Result, w.want)
					case w.want != "closed" && !w.gone && err != nil:
						t.Errorf("%s (%s): the incident is gone: %v", tool, w.name, err)
					}
					if w.after != nil {
						w.after(e, incident)
					}
					for _, id := range made {
						if _, err := e.st.GetTask(e.ctx, e.project, id); err != nil {
							t.Errorf("%s (%s): the write's task %s went with the incident: %v", tool, w.name, id, err)
						}
					}
				}
			}
		})
	}
}
