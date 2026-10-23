package service

import (
	"context"
	"encoding/json"
	"log"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/memstore"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// The R1.17 tests run on memstore and on both engines: the Postgres leg skips without
// BATONDECK_TEST_POSTGRES_DSN.

type engineCase struct {
	name string
	open func(t *testing.T) store.Store
}

var engineCases = []engineCase{
	{"memstore", func(t *testing.T) store.Store {
		st := memstore.New()
		if _, err := st.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		return st
	}},
	{"sqlite", func(t *testing.T) store.Store { return storetest.SQLite(t) }},
	{"postgres", func(t *testing.T) store.Store { return storetest.Postgres(t) }},
}

func onEngines(t *testing.T, fn func(t *testing.T, e *env)) {
	for _, ec := range engineCases {
		t.Run(ec.name, func(t *testing.T) { fn(t, newEnv(t, ec.open(t))) })
	}
}

// env is one project (alice its admin, a default board) and a Core over st.
type env struct {
	ctx     context.Context
	st      store.Store
	c       *Core
	project string
	board   store.Board
	alice   domain.Caller
}

func newEnv(t *testing.T, st store.Store) *env {
	t.Helper()
	ctx := context.Background()
	e := &env{ctx: ctx, st: st, alice: domain.Caller{Identity: "alice", Agent: "claude", TokenScope: "alice"}}
	e.c = New(st, Options{Logger: log.New(&logBuf{}, "", 0), ToolCallFlush: -1})
	t.Cleanup(func() { _ = e.c.Close(context.Background()) })
	p, err := st.CreateProject(ctx, store.CreateProjectInput{Name: "P", CreatedBy: "alice"}, store.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	e.project = p.ID
	if e.board, err = st.CreateBoard(ctx, store.CreateBoardInput{ProjectID: p.ID, Name: "B", CreatedBy: "alice"}, store.WriteOptions{}); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) column(t *testing.T, s domain.Status) string {
	t.Helper()
	for _, c := range e.board.Columns {
		if c.Status == s {
			return c.ID
		}
	}
	t.Fatalf("no %s column", s)
	return ""
}

func (e *env) task(t *testing.T, s domain.Status) store.Task {
	t.Helper()
	task, err := e.st.CreateTask(e.ctx, store.CreateTaskInput{
		ProjectID: e.project, BoardID: e.board.ID, ColumnID: e.column(t, s), Title: "t", Status: s,
		OrderKey: "a0", CreatedBy: "alice", CreatedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// blocked seeds a BLOCKED task with reason ("" none, domain.BlockedReasonPoison a poisoned one).
func (e *env) blocked(t *testing.T, reason string) store.Task {
	t.Helper()
	task := e.task(t, domain.InProgress)
	got, err := e.st.SetStatus(e.ctx, e.project, task.ID, task.Version, store.StatusChange{
		Status: domain.Blocked, ColumnID: e.column(t, domain.Blocked), BlockedReason: reason, UpdatedBy: "alice", UpdatedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (e *env) get(t *testing.T, id string) store.Task {
	t.Helper()
	task, err := e.st.GetTask(e.ctx, e.project, id)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func (e *env) events(t *testing.T, id string) []store.Event {
	t.Helper()
	page, err := e.st.ListTaskEvents(e.ctx, e.project, id, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	return page.Events
}

func (e *env) member(t *testing.T, projectID, identity string, role domain.Role) domain.Caller {
	t.Helper()
	if _, err := e.st.AddMember(e.ctx, projectID, identity, role, "alice"); err != nil {
		t.Fatal(err)
	}
	return domain.Caller{Identity: identity, TokenScope: identity}
}

func (e *env) edges(t *testing.T) []store.DependencyEdge {
	t.Helper()
	es, err := e.st.ListEdges(e.ctx, e.project)
	if err != nil {
		t.Fatal(err)
	}
	return es
}

func ofType(events []store.Event, typ domain.EventType) []store.Event {
	var out []store.Event
	for _, ev := range events {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

// AC1: the briefing carries the items, the caller's task memory, the summary, both sides' edges,
// the fields and workflow.allowedMoves equal to the fixed table's row; no id, title or description.
func TestGetTaskContextComposesTheBriefing(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		task, blocker, dependent := e.task(t, domain.InProgress), e.task(t, domain.Todo), e.task(t, domain.Todo)
		cx := e.c.Context()
		sum, err := cx.SetSummary(e.ctx, e.alice, SetSummaryReq{ProjectID: e.project, TaskID: task.ID, Version: task.Version, Summary: "so far"})
		if err != nil {
			t.Fatal(err)
		}
		if sum.Task.ContextSummary != "so far" || sum.Task.Version != float64(task.Version+1) {
			t.Fatalf("set_summary task summary %q version %v, want so far and %d", sum.Task.ContextSummary, sum.Task.Version, task.Version+1)
		}
		item, err := cx.AddItem(e.ctx, e.alice, AddContextItemReq{ProjectID: e.project, TaskID: task.ID, Kind: "decision", Body: "use Go"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := cx.WriteMemory(e.ctx, e.alice, WriteMemoryReq{ProjectID: e.project, TaskID: task.ID, Scope: domain.MemoryScopeTask, Key: "k", Value: map[string]any{"n": 1.0}}); err != nil {
			t.Fatal(err)
		}
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		if _, err := cx.WriteMemory(e.ctx, bob, WriteMemoryReq{ProjectID: e.project, TaskID: task.ID, Scope: domain.MemoryScopeAgent, Key: "bob-private", Value: "x"}); err != nil {
			t.Fatal(err)
		}
		in, err := cx.AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: blocker.ID, ToTaskID: task.ID})
		if err != nil {
			t.Fatal(err)
		}
		outEdge, err := cx.AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: task.ID, ToTaskID: dependent.ID})
		if err != nil {
			t.Fatal(err)
		}
		got, err := cx.GetTaskContext(e.ctx, e.alice, GetTaskContextReq{ProjectID: e.project, TaskID: task.ID})
		if err != nil {
			t.Fatal(err)
		}
		if got.Summary != "so far" {
			t.Errorf("summary %q", got.Summary)
		}
		if len(got.Items) != 1 || got.Items[0].ID != item.Item.ID || got.Items[0].Kind != "decision" {
			t.Errorf("items %+v, want the one decision", got.Items)
		}
		if len(got.Memory) != 1 || got.Memory[0].Key != "k" {
			t.Errorf("memory %+v, want only alice's task entry (bob's agent entry is his)", got.Memory)
		}
		if !slices.Equal(got.Deps.BlockedBy, []string{blocker.ID}) || !slices.Equal(got.Deps.Blocks, []string{dependent.ID}) {
			t.Errorf("deps blockedBy %v blocks %v", got.Deps.BlockedBy, got.Deps.Blocks)
		}
		var ids []string
		for _, ed := range got.Deps.Edges {
			ids = append(ids, ed.ID)
		}
		slices.Sort(ids)
		want := []string{in.Edge.ID, outEdge.Edge.ID}
		slices.Sort(want)
		if !slices.Equal(ids, want) {
			t.Errorf("edges %v, want both sides %v", ids, want)
		}
		if got.Workflow.Status != domain.InProgress || !slices.Equal(got.Workflow.AllowedMoves, domain.AllowedNext(domain.InProgress)) {
			t.Errorf("workflow %s %v, want IN_PROGRESS %v", got.Workflow.Status, got.Workflow.AllowedMoves, domain.AllowedNext(domain.InProgress))
		}
		if !slices.Equal(got.Workflow.AllowedMoves, []domain.Status{domain.Review, domain.Blocked, domain.Todo}) {
			t.Errorf("allowedMoves %v, want the fixed table's IN_PROGRESS row [REVIEW BLOCKED TODO]", got.Workflow.AllowedMoves)
		}
		if len(got.Workflow.Lanes) != len(e.board.Columns) {
			t.Errorf("lanes %d, want the board's %d columns", len(got.Workflow.Lanes), len(e.board.Columns))
		}
		raw, err := json.Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		var top map[string]json.RawMessage
		if err := json.Unmarshal(raw, &top); err != nil {
			t.Fatal(err)
		}
		for _, k := range []string{"id", "title", "description", "contextItems"} {
			if _, ok := top[k]; ok {
				t.Errorf("the briefing carries %q", k)
			}
		}
		for _, k := range []string{"artifacts", "attachments", "openFollowUps"} {
			if string(top[k]) != "[]" {
				t.Errorf("%s is %s, want []", k, top[k])
			}
		}
		if got.Upstream != nil || got.ProjectMemory != nil {
			t.Errorf("upstream %v projectMemory %v without their flags", got.Upstream, got.ProjectMemory)
		}
		if _, err := cx.SetSummary(e.ctx, e.alice, SetSummaryReq{ProjectID: e.project, TaskID: blocker.ID, Version: e.get(t, blocker.ID).Version, Summary: "blocker so far"}); err != nil {
			t.Fatal(err)
		}
		if _, err := cx.WriteMemory(e.ctx, e.alice, WriteMemoryReq{ProjectID: e.project, Scope: domain.MemoryScopeProject, Key: "fact", Value: "shared"}); err != nil {
			t.Fatal(err)
		}
		opt, err := cx.GetTaskContext(e.ctx, e.alice, GetTaskContextReq{ProjectID: e.project, TaskID: task.ID, IncludeUpstream: true, IncludeProjectMemory: true, Include: []ContextSection{"summary"}})
		if err != nil {
			t.Fatal(err)
		}
		if len(opt.Upstream) != 1 || opt.Upstream[0].TaskID != blocker.ID || opt.Upstream[0].Summary != "blocker so far" || opt.Upstream[0].Status != string(domain.Todo) {
			t.Errorf("upstream %+v, want the blocker with its summary", opt.Upstream)
		}
		if len(opt.ProjectMemory) != 1 || opt.ProjectMemory[0].Key != "fact" {
			t.Errorf("projectMemory %+v, want the project fact", opt.ProjectMemory)
		}
		if len(opt.Items) != 0 || len(opt.Deps.Edges) != 0 || opt.Summary != "so far" {
			t.Errorf("include [summary] returned items %d edges %d summary %q", len(opt.Items), len(opt.Deps.Edges), opt.Summary)
		}
		t.Logf("briefing keys: %d, allowedMoves %v, edges %d", len(top), got.Workflow.AllowedMoves, len(got.Deps.Edges))
	})
}

// ctxCounting counts the store reads get_task_context makes; each is the statements its store
// contract states (store TestTaskContextStatements measures the six on both engines: one each).
type ctxCounting struct{ countingStore }

func (s *ctxCounting) GetTask(ctx context.Context, p, id string) (store.Task, error) {
	s.n.hit("GetTask")
	return s.Store.GetTask(ctx, p, id)
}

func (s *ctxCounting) GetSummary(ctx context.Context, p, id string) (store.Summary, error) {
	s.n.hit("GetSummary")
	return s.Store.GetSummary(ctx, p, id)
}

func (s *ctxCounting) ListContextItems(ctx context.Context, p, id string, q store.ContextItemsQuery) (store.ContextItemsPage, error) {
	s.n.hit("ListContextItems")
	return s.Store.ListContextItems(ctx, p, id, q)
}

func (s *ctxCounting) ReadMemory(ctx context.Context, p, id string, q store.ReadMemoryQuery) ([]store.MemoryEntry, error) {
	s.n.hit("ReadMemory")
	return s.Store.ReadMemory(ctx, p, id, q)
}

func (s *ctxCounting) ListBlockers(ctx context.Context, p, id string) ([]store.DependencyEdge, error) {
	s.n.hit("ListBlockers")
	return s.Store.ListBlockers(ctx, p, id)
}

func (s *ctxCounting) ListDependents(ctx context.Context, p, id string) ([]store.DependencyEdge, error) {
	s.n.hit("ListDependents")
	return s.Store.ListDependents(ctx, p, id)
}

// statements is the counted calls in statements: GetBoard is two (the board and one grouped count).
func statements(m map[string]int) int {
	n := 0
	for k, v := range m {
		if k == "GetBoard" {
			v *= 2
		}
		n += v
	}
	return n
}

// AC1 counter: the default call is at most six statements once the membership and the board
// snapshot are cached; cold, the snapshot adds store.GetBoard's two.
func TestGetTaskContextCostsAtMostSixStatements(t *testing.T) {
	f := newFixture(t, Options{})
	n := &calls{m: map[string]int{}}
	old := f.c
	f.c = New(&ctxCounting{countingStore{Store: f.st, n: n}}, Options{Logger: log.New(f.logs, "", 0), Now: f.clock.now, ToolCallFlush: -1})
	if err := old.Close(f.ctx); err != nil {
		t.Fatal(err)
	}
	task := f.task(t, domain.InProgress, nil)
	blocker := f.task(t, domain.Todo, nil)
	cx := f.c.Context()
	if _, err := cx.AddItem(f.ctx, f.alice, AddContextItemReq{ProjectID: f.project.ID, TaskID: task.ID, Kind: "note", Body: "n"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cx.AddDependency(f.ctx, f.alice, AddDependencyReq{ProjectID: f.project.ID, FromTaskID: blocker.ID, ToTaskID: task.ID}); err != nil {
		t.Fatal(err)
	}
	f.c.ObserveChange(store.Change{ID: 1, Payload: json.RawMessage(`{}`)}) // the bus is live: the snapshot cache serves
	req := GetTaskContextReq{ProjectID: f.project.ID, TaskID: task.ID}
	n.take()
	if _, err := cx.GetTaskContext(f.ctx, f.alice, req); err != nil {
		t.Fatal(err)
	}
	cold := n.take()
	if _, err := cx.GetTaskContext(f.ctx, f.alice, req); err != nil {
		t.Fatal(err)
	}
	warm := n.take()
	t.Logf("cold %d statements %v; warm %d statements %v", statements(cold), cold, statements(warm), warm)
	if got := statements(warm); got > 6 {
		t.Fatalf("get_task_context issued %d statements %v, the pin is 6", got, warm)
	}
	for _, m := range []string{"GetTask", "GetSummary", "ListContextItems", "ReadMemory", "ListBlockers", "ListDependents"} {
		if warm[m] != 1 {
			t.Errorf("%s ran %d times, want 1", m, warm[m])
		}
	}
	if got := statements(cold); got != 8 {
		t.Errorf("cold call %d statements %v, want 8 (6 + GetBoard's 2 + GetMember 0 after the writes cached it)", got, cold)
	}
}

// AC2: a member of project B recalls nothing written in project A — naming A is FORBIDDEN, naming
// B returns B's alone — while the same recall in A returns it.
func TestRecallMemoryNeverCrossesProjects(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		cx := e.c.Context()
		if _, err := cx.WriteMemory(e.ctx, e.alice, WriteMemoryReq{ProjectID: e.project, Scope: domain.MemoryScopeProject, Key: "secret", Value: "in A"}); err != nil {
			t.Fatal(err)
		}
		pb, err := e.st.CreateProject(e.ctx, store.CreateProjectInput{Name: "B", CreatedBy: "bob"}, store.WriteOptions{})
		if err != nil {
			t.Fatal(err)
		}
		bob := domain.Caller{Identity: "bob", TokenScope: "bob"}
		if _, err := cx.WriteMemory(e.ctx, bob, WriteMemoryReq{ProjectID: pb.ID, Scope: domain.MemoryScopeProject, Key: "mine", Value: "in B"}); err != nil {
			t.Fatal(err)
		}
		// refusal
		_, err = cx.RecallMemory(e.ctx, bob, RecallMemoryReq{ProjectID: e.project})
		wantCode(t, err, domain.CodeForbidden)
		_, err = cx.RecallMemory(e.ctx, bob, RecallMemoryReq{ProjectID: e.project, Scope: "agent_global"})
		wantCode(t, err, domain.CodeForbidden)
		for _, req := range []RecallMemoryReq{{ProjectID: pb.ID}, {ProjectID: pb.ID, Scope: "project"}, {ProjectID: pb.ID, Key: "secret"}, {Scope: "agent_global"}} {
			got, err := cx.RecallMemory(e.ctx, bob, req)
			if err != nil {
				t.Fatal(err)
			}
			for _, m := range got.Entries {
				if m.Key == "secret" {
					t.Fatalf("recall %+v from B returned A's entry %+v", req, m)
				}
			}
		}
		inB, err := cx.RecallMemory(e.ctx, bob, RecallMemoryReq{ProjectID: pb.ID})
		if err != nil || len(inB.Entries) != 1 || inB.Entries[0].Key != "mine" {
			t.Fatalf("recall in B %+v %v, want B's one entry", inB.Entries, err)
		}
		// control
		inA, err := cx.RecallMemory(e.ctx, e.alice, RecallMemoryReq{ProjectID: e.project, Key: "secret"})
		if err != nil || len(inA.Entries) != 1 || inA.Entries[0].Value != "in A" {
			t.Fatalf("recall in A %+v %v, want the entry", inA.Entries, err)
		}
		// agent_global is the caller's: alice's is never bob's. Newest first is (ts, id) with random
		// ids, so two writes in one millisecond tie by design: the 2 ms keeps the order asserted below.
		time.Sleep(2 * time.Millisecond)
		if _, err := cx.WriteMemory(e.ctx, e.alice, WriteMemoryReq{ProjectID: e.project, Scope: domain.MemoryScopeAgentGlobal, Key: "alice-own", Value: 1.0}); err != nil {
			t.Fatal(err)
		}
		own, err := cx.RecallMemory(e.ctx, e.alice, RecallMemoryReq{Scope: "agent_global"})
		if err != nil || len(own.Entries) != 1 {
			t.Fatalf("alice's agent_global %+v %v", own.Entries, err)
		}
		theirs, err := cx.RecallMemory(e.ctx, bob, RecallMemoryReq{Scope: "agent_global"})
		if err != nil || len(theirs.Entries) != 0 {
			t.Fatalf("bob's agent_global %+v %v, want none", theirs.Entries, err)
		}
		both, err := cx.RecallMemory(e.ctx, e.alice, RecallMemoryReq{ProjectID: e.project})
		if err != nil || len(both.Entries) != 2 || both.Entries[0].Key != "alice-own" {
			t.Fatalf("both scopes %+v %v, want alice-own then secret", both.Entries, err)
		}
		_, err = cx.RecallMemory(e.ctx, e.alice, RecallMemoryReq{Scope: "project"})
		wantCode(t, err, domain.CodeValidation)
		_, err = cx.RecallMemory(e.ctx, e.alice, RecallMemoryReq{ProjectID: e.project, K: 101})
		wantCode(t, err, domain.CodeValidation)
	})
}

// AC3: a cycle-closing add_dependency is CYCLE_DETECTED and writes nothing; removing a BLOCKED
// dependent's last blocker moves it to TODO.
func TestAddDependencyCycleWritesNothingAndRemoveUnblocks(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		cx := e.c.Context()
		a, b, c := e.task(t, domain.Todo), e.task(t, domain.Todo), e.task(t, domain.Todo)
		for _, p := range [][2]string{{a.ID, b.ID}, {b.ID, c.ID}} {
			if _, err := cx.AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: p[0], ToTaskID: p[1]}); err != nil {
				t.Fatal(err)
			}
		}
		before := []store.Task{e.get(t, a.ID), e.get(t, b.ID), e.get(t, c.ID)}
		edges, events := e.edges(t), len(e.events(t, a.ID))
		_, err := cx.AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: c.ID, ToTaskID: a.ID})
		wantCode(t, err, domain.CodeCycleDetected)
		_, err = cx.AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: a.ID, ToTaskID: a.ID})
		wantCode(t, err, domain.CodeCycleDetected)
		if got := e.edges(t); len(got) != len(edges) {
			t.Fatalf("a refused cycle left %d edges, want %d", len(got), len(edges))
		}
		for i, id := range []string{a.ID, b.ID, c.ID} {
			now := e.get(t, id)
			if now.Version != before[i].Version || !slices.Equal(now.BlockedBy, before[i].BlockedBy) || !slices.Equal(now.Blocks, before[i].Blocks) {
				t.Fatalf("%s moved: version %d->%d blockedBy %v->%v blocks %v->%v", id, before[i].Version, now.Version, before[i].BlockedBy, now.BlockedBy, before[i].Blocks, now.Blocks)
			}
		}
		if got := len(e.events(t, a.ID)); got != events {
			t.Fatalf("a refused cycle appended %d events", got-events)
		}

		dep, blocker := e.blocked(t, ""), e.task(t, domain.Todo)
		edge, err := cx.AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: blocker.ID, ToTaskID: dep.ID})
		if err != nil {
			t.Fatal(err)
		}
		if got := e.get(t, dep.ID); got.Status != domain.Blocked || got.Version != dep.Version+1 || !slices.Equal(got.BlockedBy, []string{blocker.ID}) {
			t.Fatalf("after add_dependency the dependent is %s v%d blockedBy %v: add_dependency changes no status and bumps once", got.Status, got.Version, got.BlockedBy)
		}
		if got := e.get(t, blocker.ID); got.Version != blocker.Version+1 {
			t.Fatalf("blocker version %d, want %d", got.Version, blocker.Version+1)
		}
		if _, err := cx.RemoveDependency(e.ctx, e.alice, RemoveDependencyReq{ProjectID: e.project, EdgeID: edge.Edge.ID}); err != nil {
			t.Fatal(err)
		}
		got := e.get(t, dep.ID)
		if got.Status != domain.Todo || got.ColumnID != e.column(t, domain.Todo) || len(got.BlockedBy) != 0 {
			t.Fatalf("after its last blocker is removed the dependent is %s in %s blockedBy %v, want TODO in the TODO column", got.Status, got.ColumnID, got.BlockedBy)
		}
		_, err = cx.RemoveDependency(e.ctx, e.alice, RemoveDependencyReq{ProjectID: e.project, EdgeID: edge.Edge.ID})
		wantCode(t, err, domain.CodeNotFound)
	})
}

// AC5: through the unblock path a poisoned dependent stays BLOCKED (refusal) and a non-poisoned one
// goes TODO with one task.dependency_cleared {autoReady: true} (control).
func TestRemoveDependencyUnblockIsPoisonAware(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		cx := e.c.Context()
		run := func(t *testing.T, reason string) store.Task {
			dep, blocker := e.blocked(t, reason), e.task(t, domain.Todo)
			edge, err := cx.AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: blocker.ID, ToTaskID: dep.ID})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := cx.RemoveDependency(e.ctx, e.alice, RemoveDependencyReq{ProjectID: e.project, EdgeID: edge.Edge.ID}); err != nil {
				t.Fatal(err)
			}
			return e.get(t, dep.ID)
		}
		t.Run("PoisonedStaysBlocked", func(t *testing.T) {
			got := run(t, domain.BlockedReasonPoison)
			if got.Status != domain.Blocked || reasonOf(got) != domain.BlockedReasonPoison {
				t.Fatalf("a poisoned dependent whose last blocker went is %s (%q): it must stay BLOCKED", got.Status, reasonOf(got))
			}
			if n := len(ofType(e.events(t, got.ID), domain.EventTaskDependencyCleared)); n != 0 {
				t.Fatalf("%d dependency_cleared events on a dependent that stayed blocked", n)
			}
		})
		t.Run("NonPoisonedGoesReady", func(t *testing.T) {
			got := run(t, "")
			if got.Status != domain.Todo {
				t.Fatalf("the dependent is %s, want TODO", got.Status)
			}
			cleared := ofType(e.events(t, got.ID), domain.EventTaskDependencyCleared)
			if len(cleared) != 1 {
				t.Fatalf("%d dependency_cleared events, want 1", len(cleared))
			}
			var data struct {
				ClearedBy string `json:"clearedBy"`
				AutoReady bool   `json:"autoReady"`
			}
			if err := json.Unmarshal(cleared[0].Data, &data); err != nil || !data.AutoReady || data.ClearedBy == "" {
				t.Fatalf("dependency_cleared data %s (%v), want autoReady true and clearedBy", cleared[0].Data, err)
			}
		})
	})
}

// AC5, the completion path's entry (R1.5 calls it): a dependent whose blockedBy names the cleared
// blocker gets its cache written, and moves to TODO unless it is poisoned; the event says which.
func TestUnblockDependentOnTheCompletionPath(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		for _, c := range []struct {
			reason string
			want   domain.Status
		}{{domain.BlockedReasonPoison, domain.Blocked}, {"", domain.Todo}} {
			t.Run("reason="+c.reason, func(t *testing.T) { completeOne(t, e, c.reason, c.want) })
		}
	})
}

// completeOne completes a blocker the way R1.5 will: DecideUnblock over the dependent's cache, then
// the entry, inside the blocker's mutation.
func completeOne(t *testing.T, e *env, reason string, want domain.Status) {
	dep, blocker := e.blocked(t, reason), e.task(t, domain.Todo)
	if _, err := e.c.Context().AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: blocker.ID, ToTaskID: dep.ID}); err != nil {
		t.Fatal(err)
	}
	_, err := mutate(e.ctx, e.c, e.alice, mutation{Tool: "complete_task", ProjectID: e.project, TaskID: blocker.ID}, func(tx *txn) (struct{}, error) {
		d, err := tx.Tx.GetTask(e.ctx, e.project, dep.ID)
		if err != nil {
			return struct{}{}, err
		}
		_, err = tx.unblockDependent(d, blocker.ID, domain.DecideUnblock(d.BlockedBy, d.Status, reasonOf(d), blocker.ID))
		return struct{}{}, err
	})
	if err != nil {
		t.Fatal(err)
	}
	got := e.get(t, dep.ID)
	if got.Status != want || len(got.BlockedBy) != 0 {
		t.Fatalf("reason %q: dependent %s blockedBy %v, want %s and an empty cache", reason, got.Status, got.BlockedBy, want)
	}
	if n := len(ofType(e.events(t, dep.ID), domain.EventTaskDependencyCleared)); n != 1 {
		t.Fatalf("reason %q: %d dependency_cleared events, want 1", reason, n)
	}
}

// AC4: an entry with a one-second ttl is read at once and gone two seconds later.
func TestWriteMemoryTTLExpiresOnRead(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		t.Parallel()
		cx := e.c.Context()
		task := e.task(t, domain.Todo)
		ttl := time.Now().Add(time.Second).UTC().Format(time.RFC3339Nano)
		w, err := cx.WriteMemory(e.ctx, e.alice, WriteMemoryReq{ProjectID: e.project, TaskID: task.ID, Scope: domain.MemoryScopeTask, Key: "short", Value: "v", TTL: ttl})
		if err != nil {
			t.Fatal(err)
		}
		read := func() []MemoryEntry {
			got, err := cx.ReadMemory(e.ctx, e.alice, ReadMemoryReq{ProjectID: e.project, TaskID: task.ID})
			if err != nil {
				t.Fatal(err)
			}
			return got.Entries
		}
		at := read()
		if len(at) != 1 || at[0].ID != w.EntryID || at[0].TTL == nil {
			t.Fatalf("at once: %+v, want the entry with its ttl", at)
		}
		time.Sleep(2 * time.Second)
		if later := read(); len(later) != 0 {
			t.Fatalf("after 2 s: %+v, want none", later)
		}
		_, err = cx.WriteMemory(e.ctx, e.alice, WriteMemoryReq{ProjectID: e.project, TaskID: task.ID, Scope: domain.MemoryScopeTask, Key: "k", TTL: "1000"})
		wantCode(t, err, domain.CodeValidation)
	})
}

// AC6: add_subtask on an IN_PROGRESS parent puts the child in the first column with parentTaskId and
// one task.created event, and leaves the parent's status, blockedBy and version as they were.
func TestAddSubtaskLeavesTheParentAlone(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		cx := e.c.Context()
		parent, other := e.task(t, domain.InProgress), e.task(t, domain.Todo)
		if _, err := cx.AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: other.ID, ToTaskID: parent.ID}); err != nil {
			t.Fatal(err)
		}
		before := e.get(t, parent.ID)
		feed := func() int {
			page, err := e.st.ListBoardFeed(e.ctx, e.project, e.board.ID, "", 200)
			if err != nil {
				t.Fatal(err)
			}
			return len(page.Events)
		}
		events, parentEvents := feed(), len(e.events(t, parent.ID))
		got, err := cx.AddSubtask(e.ctx, e.alice, AddSubtaskReq{ProjectID: e.project, ParentTaskID: parent.ID, Title: "child"})
		if err != nil {
			t.Fatal(err)
		}
		first := e.board.Columns[0]
		if got.Task.ColumnID != first.ID || got.Task.Status != first.Status || got.Task.ParentTaskID == nil || *got.Task.ParentTaskID != parent.ID {
			t.Fatalf("child in %s %s parent %v, want %s %s %s", got.Task.ColumnID, got.Task.Status, got.Task.ParentTaskID, first.ID, first.Status, parent.ID)
		}
		if len(got.Task.BlockedBy) != 0 {
			t.Fatalf("child blockedBy %v: add_subtask adds no edge", got.Task.BlockedBy)
		}
		after := e.get(t, parent.ID)
		if after.Version != before.Version || after.Status != before.Status || !slices.Equal(after.BlockedBy, before.BlockedBy) {
			t.Fatalf("parent moved: %s v%d %v -> %s v%d %v", before.Status, before.Version, before.BlockedBy, after.Status, after.Version, after.BlockedBy)
		}
		if n := feed() - events; n != 1 {
			t.Fatalf("add_subtask appended %d events, want 1", n)
		}
		if n := len(e.events(t, parent.ID)); n != parentEvents {
			t.Fatalf("the parent gained %d events", n-parentEvents)
		}
		evs := e.events(t, got.Task.ID)
		if len(evs) != 1 || evs[0].Type != domain.EventTaskCreated {
			t.Fatalf("child events %+v, want one task.created", evs)
		}
		list, err := cx.ListSubtasks(e.ctx, e.alice, ListSubtasksReq{ProjectID: e.project, ParentTaskID: parent.ID})
		if err != nil || len(list.Tasks) != 1 || list.Tasks[0].ID != got.Task.ID {
			t.Fatalf("list_subtasks %+v %v", list.Tasks, err)
		}
		_, err = cx.AddSubtask(e.ctx, e.alice, AddSubtaskReq{ProjectID: e.project, ParentTaskID: "T-404", Title: "x"})
		wantCode(t, err, domain.CodeNotFound)
		_, err = cx.ListSubtasks(e.ctx, e.alice, ListSubtasksReq{ProjectID: e.project, ParentTaskID: "T-404"})
		wantCode(t, err, domain.CodeNotFound)
	})
}

// The declared refusals of the methods this file covers that the tests above do not.
func TestContextRefusals(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		cx := e.c.Context()
		task := e.task(t, domain.Todo)
		worker := e.member(t, e.project, "wanda", domain.RoleWorker)
		stranger := domain.Caller{Identity: "zed", TokenScope: "zed"}
		for _, c := range []struct {
			name string
			code domain.Code
			call func() error
		}{
			{"set_summary stale", domain.CodeStale, func() error {
				_, err := cx.SetSummary(e.ctx, e.alice, SetSummaryReq{ProjectID: e.project, TaskID: task.ID, Version: task.Version + 5, Summary: "s"})
				return err
			}},
			{"add_context_item link not a URL", domain.CodeValidation, func() error {
				_, err := cx.AddItem(e.ctx, e.alice, AddContextItemReq{ProjectID: e.project, TaskID: task.ID, Kind: "link", Body: "not a url"})
				return err
			}},
			{"add_context_item unknown kind", domain.CodeValidation, func() error {
				_, err := cx.AddItem(e.ctx, e.alice, AddContextItemReq{ProjectID: e.project, TaskID: task.ID, Kind: "rumour", Body: "b"})
				return err
			}},
			{"add_context_item missing task", domain.CodeNotFound, func() error {
				_, err := cx.AddItem(e.ctx, e.alice, AddContextItemReq{ProjectID: e.project, TaskID: "T-404", Kind: "note", Body: "b"})
				return err
			}},
			{"get_task_context non-member", domain.CodeForbidden, func() error {
				_, err := cx.GetTaskContext(e.ctx, stranger, GetTaskContextReq{ProjectID: e.project, TaskID: task.ID})
				return err
			}},
			{"get_task_context missing task", domain.CodeNotFound, func() error {
				_, err := cx.GetTaskContext(e.ctx, e.alice, GetTaskContextReq{ProjectID: e.project, TaskID: "T-404"})
				return err
			}},
			{"write_memory task scope without task", domain.CodeValidation, func() error {
				_, err := cx.WriteMemory(e.ctx, e.alice, WriteMemoryReq{ProjectID: e.project, Scope: domain.MemoryScopeShared, Key: "k"})
				return err
			}},
			{"write_memory provenance task missing", domain.CodeNotFound, func() error {
				_, err := cx.WriteMemory(e.ctx, e.alice, WriteMemoryReq{ProjectID: e.project, TaskID: "T-404", Scope: domain.MemoryScopeProject, Key: "k"})
				return err
			}},
			{"read_memory durable scope", domain.CodeValidation, func() error {
				_, err := cx.ReadMemory(e.ctx, e.alice, ReadMemoryReq{ProjectID: e.project, TaskID: task.ID, Scope: "project"})
				return err
			}},
			{"add_dependency worker", domain.CodeForbidden, func() error {
				_, err := cx.AddDependency(e.ctx, worker, AddDependencyReq{ProjectID: e.project, FromTaskID: task.ID, ToTaskID: task.ID})
				return err
			}},
			{"add_dependency missing blocker", domain.CodeNotFound, func() error {
				_, err := cx.AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: "T-404", ToTaskID: task.ID})
				return err
			}},
			{"add_subtask worker", domain.CodeForbidden, func() error {
				_, err := cx.AddSubtask(e.ctx, worker, AddSubtaskReq{ProjectID: e.project, ParentTaskID: task.ID, Title: "x"})
				return err
			}},
		} {
			if err := c.call(); err == nil {
				t.Errorf("%s: no error, want %s", c.name, c.code)
			} else if be, ok := domain.AsBatonDeckError(err); !ok || be.Code != c.code {
				t.Errorf("%s: %v, want %s", c.name, err, c.code)
			}
		}
	})
}

// gateStore holds every AddEdge inside its transaction until release closes, and reports each
// arrival: the window the R0.10 review's P-110 repro opened.
type gateStore struct {
	store.Store
	arrived chan struct{}
	release chan struct{}
}

func (g *gateStore) Atomically(ctx context.Context, fn func(tx store.Store) error) error {
	return g.Store.Atomically(ctx, func(tx store.Store) error {
		return fn(&gateStore{Store: tx, arrived: g.arrived, release: g.release})
	})
}

func (g *gateStore) AddEdge(ctx context.Context, in store.AddEdgeInput) (store.DependencyEdge, error) {
	e, err := g.Store.AddEdge(ctx, in)
	if err == nil {
		g.arrived <- struct{}{}
		<-g.release
	}
	return e, err
}

// hasCycle: the blocks graph holds a cycle iff some edge closes one over the others.
func hasCycle(edges []store.DependencyEdge) bool {
	var es []domain.Edge
	for _, e := range edges {
		if e.Type == store.DependencyBlocks {
			es = append(es, domain.Edge{FromTaskID: e.FromTaskID, ToTaskID: e.ToTaskID})
		}
	}
	for i, e := range es {
		rest := append(slices.Clone(es[:i]), es[i+1:]...)
		if domain.CheckCycle(rest, e.FromTaskID, e.ToTaskID) != nil {
			return true
		}
	}
	return false
}

// Requirement 2a, the R0.10 review's TestReviewP110Postgres ported to the service: over B→C and
// D→A, two concurrent add_dependency calls on the disjoint pairs A→B and C→D give exactly one
// CYCLE_DETECTED and no cycle. Under Postgres READ COMMITTED both passed CheckCycle and committed a
// 4-edge cycle until mutate took the project lock first.
func TestConcurrentAddDependencyOnDisjointPairsClosesNoCycle(t *testing.T) {
	for _, ec := range engineCases {
		t.Run(ec.name, func(t *testing.T) {
			st := ec.open(t)
			e := newEnv(t, st)
			ids := []string{e.task(t, domain.Todo).ID, e.task(t, domain.Todo).ID, e.task(t, domain.Todo).ID, e.task(t, domain.Todo).ID}
			A, B, C, D := ids[0], ids[1], ids[2], ids[3]
			for _, p := range [][2]string{{B, C}, {D, A}} {
				if _, err := e.c.Context().AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: p[0], ToTaskID: p[1]}); err != nil {
					t.Fatal(err)
				}
			}
			g := &gateStore{Store: st, arrived: make(chan struct{}, 2), release: make(chan struct{})}
			c := New(g, Options{Logger: log.New(&logBuf{}, "", 0), ToolCallFlush: -1})
			t.Cleanup(func() { _ = c.Close(context.Background()) })
			errs := make([]error, 2)
			var wg sync.WaitGroup
			for i, p := range [][2]string{{A, B}, {C, D}} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					_, errs[i] = c.Context().AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: p[0], ToTaskID: p[1]})
				}()
			}
			both := 0
			timeout := time.After(2 * time.Second)
		wait:
			for both < 2 {
				select {
				case <-g.arrived:
					both++
				case <-timeout:
					break wait
				}
			}
			close(g.release)
			wg.Wait()
			edges := e.edges(t)
			var pairs [][2]string
			for _, ed := range edges {
				pairs = append(pairs, [2]string{ed.FromTaskID, ed.ToTaskID})
			}
			t.Logf("inside their transactions at once: %d/2; errs=%v; edges=%v; cycle=%v", both, errs, pairs, hasCycle(edges))
			if hasCycle(edges) {
				t.Fatalf("P-110: two concurrent add_dependency on disjoint pairs committed a cycle: %v", pairs)
			}
			cycles := 0
			for _, err := range errs {
				if be, ok := domain.AsBatonDeckError(err); ok && be.Code == domain.CodeCycleDetected {
					cycles++
				} else if err != nil {
					t.Fatalf("unexpected error %v", err)
				}
			}
			if cycles != 1 {
				t.Fatalf("%d CYCLE_DETECTED, want exactly 1 (errs %v)", cycles, errs)
			}
		})
	}
}
