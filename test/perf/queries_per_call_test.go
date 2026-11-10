// Package perf pins the statements each hot tool call issues (plan §Performance "Pinned DB ops per
// tool call", conductor's fsReads pin in test/integration/firestore-ops.test.ts:74 ported as SQL
// statement counts). Each call runs through the service method its MCP tool is registered with
// (internal/mcp/tools: mcp.Tool(s, area, name, <method>)), on each SQL engine, counted by
// store.CountingStatements. The transport, the bearer's resolution and the buffered tool_calls row
// are outside the count.
package perf_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The pins. Each is measured with the caller's membership cached (the service's 60 s access
// cache, a working session's steady state) and the board snapshot in the state its comment names.
// A pin is raised only with a reason in the PR that raises it.
const (
	// get_task: the task, its summary and its artifacts. Raised from 2 by R2.1 for the artifacts
	// statement: the drawer's evidence list reads task.artifacts from get_task.
	pinGetTask = 3
	// list_tasks: one page (no count statement is issued).
	pinListTasks = 2
	// list_tasks with missingArtifacts: the page and one statement for its tasks' live artifacts
	// (R2.1), never one per task.
	pinListTasksMissingArtifacts = 2
	// get_board, snapshot dropped: the board and one GROUP BY status count. A cached snapshot
	// answers with none, which would pin nothing.
	pinGetBoard = 2
	// claim_next, snapshot cached: the IN_PROGRESS column's first order key, the frontier claim, the
	// summary, the event and the change row, then resume's artifacts and one page of context items,
	// read after the commit. Raised from 4 by P-117 for those two: resume was always null without
	// them; from 6 for the column's first key: the claimed task lands at the head of IN_PROGRESS with
	// a key of its own, instead of carrying its TODO key there, where equal keys left no room to drop
	// a card between two of them.
	pinClaimNext = 7
	// claim_next on Postgres: the same seven and NotifyChanges, the pg_notify every change row sends
	// the other processes' WatchChanges (internal/store/watch.go). Raised from the plan's 4 for that
	// statement, by P-117 for resume's two reads, and for the column's first key; SQLite holds 7.
	pinClaimNextPostgres = 8
	// get_task_context, snapshot cached: the task, its summary, a page of items, its memory, the
	// edges on each side, its artifacts, its attachments and its open follow-ups. Raised from 6 by
	// P-236 for the last three: one statement each, never one per row.
	pinGetTaskContext = 9
	// next_task: the frontier preview and the summary of the task it names.
	pinNextTask = 3
	// GET /api/analytics/operations (service.Analytics, no tool): the one GROUP BY over tool_calls.
	pinAnalyticsOperations = 1
)

// snapshot is the board snapshot cache's state when a call is measured.
type snapshot int

const (
	cached  snapshot = iota // the board's snapshot is held
	dropped                 // a change dropped it: the call reads the board
)

func (s snapshot) String() string {
	if s == cached {
		return "snapshot cached"
	}
	return "snapshot dropped"
}

// sqlEngines is the engines servicetest selects that count statements: memstore issues none, so a
// zero there would prove nothing.
func sqlEngines(t *testing.T) []string {
	var out []string
	for _, e := range servicetest.Engines(t) {
		if e != servicetest.Memstore {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		t.Skipf("%s selects memstore only, which issues no statement to count", servicetest.EngineEnv)
	}
	return out
}

type fixture struct {
	st      store.Store
	core    *service.Core
	alice   domain.Caller
	project string
	board   string
	full    string // a task holding a summary, an item, a memory entry, an edge on each side, an artifact, an attachment and an open and an acked follow-up
}

// open seeds, through the service, a project, a board, a task with context of every kind (blocked
// by one task, blocking another) and four claimable TODO tasks.
func open(t *testing.T, engine string) *fixture {
	t.Helper()
	s := servicetest.Open(t, engine)
	ctx := context.Background()
	f := &fixture{st: s.Store, core: s.Core, alice: domain.Caller{Identity: "alice", Agent: "claude", TokenScope: "alice"}}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	p, err := f.core.Projects().Create(ctx, f.alice, service.CreateProjectReq{Name: "perf"})
	must(err)
	f.project = p.ProjectID
	b, err := f.core.Boards().Create(ctx, f.alice, service.CreateBoardReq{ProjectID: f.project, Name: "b"})
	must(err)
	f.board = b.Board.ID
	task := func(title string) service.Task {
		t.Helper()
		r, err := f.core.Tasks().Create(ctx, f.alice, service.CreateTaskReq{ProjectID: f.project, BoardID: f.board, Title: title})
		must(err)
		return r.Task
	}
	full, blocker, dependent := task("full"), task("blocker"), task("dependent")
	for _, title := range []string{"c1", "c2", "c3", "c4"} {
		task(title)
	}
	f.full = full.ID
	_, err = f.core.Context().SetSummary(ctx, f.alice, service.SetSummaryReq{ProjectID: f.project, TaskID: full.ID, Summary: "so far", Version: int64(full.Version)})
	must(err)
	_, err = f.core.Context().AddItem(ctx, f.alice, service.AddContextItemReq{ProjectID: f.project, TaskID: full.ID, Kind: "note", Body: "n"})
	must(err)
	_, err = f.core.Context().WriteMemory(ctx, f.alice, service.WriteMemoryReq{ProjectID: f.project, TaskID: full.ID, Scope: domain.MemoryScopeTask, Key: "k", Value: 1})
	must(err)
	_, err = f.core.Context().AddDependency(ctx, f.alice, service.AddDependencyReq{ProjectID: f.project, FromTaskID: blocker.ID, ToTaskID: full.ID, Type: "blocks"})
	must(err)
	_, err = f.core.Context().AddDependency(ctx, f.alice, service.AddDependencyReq{ProjectID: f.project, FromTaskID: full.ID, ToTaskID: dependent.ID, Type: "blocks"})
	must(err)
	_, err = f.core.Artifacts().AddArtifact(ctx, f.alice, service.AddArtifactReq{ProjectID: f.project, TaskID: full.ID, Artifacts: []service.ArtifactInput{{Kind: "branch", Ref: "feat/x"}}})
	must(err)
	_, err = f.core.Media().AttachFile(ctx, f.alice, service.AttachFileReq{ProjectID: f.project, TaskID: full.ID, FileName: "a.png", MimeType: "image/png", Kind: "image", Bytes: 1})
	must(err)
	for _, body := range []string{"open", "acked"} {
		r, err := f.core.FollowUps().AddFollowUp(ctx, f.alice, service.AddFollowUpReq{ProjectID: f.project, TaskID: full.ID, Body: body})
		must(err)
		if body == "acked" {
			_, err = f.core.FollowUps().AckFollowUp(ctx, f.alice, service.AckFollowUpReq{ProjectID: f.project, TaskID: full.ID, FollowUpID: r.FollowUp.ID})
			must(err)
		}
	}
	// serve's bus tap marks the snapshot cache live (cli.feedSnapshots); this stack has no tap, so
	// the test does. Every write below goes through this core, which drops its own snapshots.
	f.core.ResetSnapshots(true)
	return f
}

// call is one pinned tool: its name, the snapshot state its pin is measured in, and the service
// method its MCP tool runs.
type call struct {
	tool  string
	state snapshot
	run   func(ctx context.Context, c *service.Core, f *fixture) error
}

// pin is cl's pin on engine.
func (cl call) pin(engine string) int64 {
	switch cl.tool {
	case "get_task":
		return pinGetTask
	case "list_tasks":
		return pinListTasks
	case "list_tasks missingArtifacts":
		return pinListTasksMissingArtifacts
	case "get_board":
		return pinGetBoard
	case "claim_next":
		if engine == servicetest.Postgres {
			return pinClaimNextPostgres
		}
		return pinClaimNext
	case "get_task_context":
		return pinGetTaskContext
	case "next_task":
		return pinNextTask
	case "analytics operations":
		return pinAnalyticsOperations
	}
	panic("no pin for " + cl.tool)
}

var errNoClaim = errors.New("claim_next claimed nothing: the fixture's TODO tasks ran out")

var calls = []call{
	{"get_task", cached, func(ctx context.Context, c *service.Core, f *fixture) error {
		_, err := c.Tasks().Get(ctx, f.alice, service.GetTaskReq{ProjectID: f.project, TaskID: f.full})
		return err
	}},
	{"list_tasks", cached, func(ctx context.Context, c *service.Core, f *fixture) error {
		_, err := c.Tasks().List(ctx, f.alice, service.ListTasksReq{ProjectID: f.project, BoardID: f.board})
		return err
	}},
	{"list_tasks missingArtifacts", cached, func(ctx context.Context, c *service.Core, f *fixture) error {
		_, err := c.Tasks().List(ctx, f.alice, service.ListTasksReq{ProjectID: f.project, BoardID: f.board, MissingArtifacts: true})
		return err
	}},
	{"get_board", dropped, func(ctx context.Context, c *service.Core, f *fixture) error {
		_, err := c.Boards().Get(ctx, f.alice, service.GetBoardReq{ProjectID: f.project, BoardID: f.board})
		return err
	}},
	{"get_task_context", cached, func(ctx context.Context, c *service.Core, f *fixture) error {
		_, err := c.Context().GetTaskContext(ctx, f.alice, service.GetTaskContextReq{ProjectID: f.project, TaskID: f.full})
		return err
	}},
	{"next_task", cached, func(ctx context.Context, c *service.Core, f *fixture) error {
		_, err := c.Lifecycle().Next(ctx, f.alice, service.NextTaskReq{ProjectID: f.project, BoardID: f.board})
		return err
	}},
	{"analytics operations", cached, func(ctx context.Context, c *service.Core, f *fixture) error {
		_, err := c.Analytics().Operations(ctx, f.alice, service.OperationsReq{ProjectID: f.project, Days: 90})
		return err
	}},
	// Last, since it writes: each claim takes another TODO task.
	{"claim_next", cached, func(ctx context.Context, c *service.Core, f *fixture) error {
		r, err := c.Lifecycle().ClaimNext(ctx, f.alice, service.ClaimNextReq{ProjectID: f.project, BoardID: f.board})
		if err == nil && r.Task == nil {
			return errNoClaim
		}
		return err
	}},
}

// measure is the statements cl issues on c.
func measure(t *testing.T, f *fixture, c *service.Core, cl call) int64 {
	t.Helper()
	var n atomic.Int64
	if err := cl.run(store.CountingStatements(context.Background(), &n), c, f); err != nil {
		t.Fatalf("%s: %v", cl.tool, err)
	}
	return n.Load()
}

// at is cl measured on the fixture's core with the membership cached and the snapshot in state.
func at(t *testing.T, f *fixture, cl call, state snapshot) int64 {
	t.Helper()
	if _, err := f.core.Boards().Get(context.Background(), f.alice, service.GetBoardReq{ProjectID: f.project, BoardID: f.board}); err != nil {
		t.Fatal(err) // caches the membership and the board's snapshot
	}
	if state == dropped {
		f.core.ResetSnapshots(true)
	}
	return measure(t, f, f.core, cl)
}

// benchMetric is one count make bench records (internal/cli/bench_test.go's metric, which bench/
// compares with its baseline: "exact" fails on any rise).
type benchMetric struct {
	Engine string  `json:"engine"`
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
	Unit   string  `json:"unit"`
	Gate   string  `json:"gate"`
}

// TestQueriesPerCall: each pinned tool issues at most its pin, on each SQL engine, in the state
// its pin names, and prints the count beside the pin with the other two states for the record: the
// other snapshot state and a fresh core (no membership cached, no snapshot). A pinned count of zero
// fails, since the counter then saw nothing and the pin proved nothing. With BATONDECK_BENCH_DIR
// set (make bench), the three counts and the pin of each tool go to pins-<engine>.json there.
func TestQueriesPerCall(t *testing.T) {
	benchDir := os.Getenv("BATONDECK_BENCH_DIR")
	for _, engine := range sqlEngines(t) {
		t.Run(engine, func(t *testing.T) {
			var bench []benchMetric
			defer func() {
				// Written after a failure too, so bench/ names the count that rose and its rise.
				if benchDir == "" || t.Skipped() || len(bench) == 0 {
					return
				}
				b, _ := json.MarshalIndent(bench, "", "  ")
				if err := os.WriteFile(filepath.Join(benchDir, "pins-"+engine+".json"), append(b, '\n'), 0o644); err != nil {
					t.Error(err)
				}
			}()
			f := open(t, engine)
			for _, cl := range calls {
				fresh := service.New(f.st, service.Options{Logger: log.New(io.Discard, "", 0), ToolCallFlush: -1})
				cold := measure(t, f, fresh, cl)
				if err := fresh.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
				other := cached
				if cl.state == cached {
					other = dropped
				}
				otherN := at(t, f, cl, other)
				got, pin := at(t, f, cl, cl.state), cl.pin(engine)
				t.Logf("%-16s %-8s %d statements (pin %d, %s); %s %d; fresh core %d", cl.tool, engine, got, pin, cl.state, other, otherN, cold)
				bench = append(bench,
					benchMetric{engine, cl.tool + ": " + cl.state.String(), float64(got), "", "exact"},
					benchMetric{engine, cl.tool + ": " + other.String(), float64(otherN), "", "exact"},
					benchMetric{engine, cl.tool + ": fresh core", float64(cold), "", "exact"},
					benchMetric{engine, cl.tool + ": pin", float64(pin), "", ""})
				switch {
				case got == 0:
					t.Errorf("%s on %s issued 0 statements (%s): the counter saw nothing, so the pin proves nothing", cl.tool, engine, cl.state)
				case got > pin:
					t.Errorf("%s on %s issued %d statements (%s), over its pin of %d", cl.tool, engine, got, cl.state, pin)
				}
			}
		})
	}
}
