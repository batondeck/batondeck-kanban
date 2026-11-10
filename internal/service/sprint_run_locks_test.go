package service

import (
	"context"
	"log"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// The race-order tests: open_run, set_sprint and create_task {sprintId} read what they judge (the
// parent's racing flag, the sprint's status) and write after it, so under Postgres READ COMMITTED a
// pick_run, cancel_runs or update_sprint that commits in between must not slip past the read. Each
// holds the first call inside its transaction just after that read and runs the second beside it.
// Postgres only: SQLite's BEGIN IMMEDIATE already serialises the two writers.

// pauseKey marks the context of the call pausingStore holds.
type pauseKey struct{}

// pausingStore holds a marked call inside its transaction just before its ListRuns (open_run: the
// parent read as racing, its runs counted) or just after its GetSprint (set_sprint, create_task: the
// sprint read as open), reports it on reached and waits for release.
type pausingStore struct {
	store.Store
	reached chan struct{}
	release chan struct{}
}

func (p pausingStore) wrap(tx store.Store) store.Store {
	return pausingStore{Store: tx, reached: p.reached, release: p.release}
}

func (p pausingStore) Atomically(ctx context.Context, fn func(tx store.Store) error) error {
	return p.Store.Atomically(ctx, func(tx store.Store) error { return fn(p.wrap(tx)) })
}

func (p pausingStore) WithIdempotency(ctx context.Context, scope, identity, key string, ttl time.Duration, fn func(tx store.Store) ([]byte, error)) ([]byte, error) {
	return p.Store.WithIdempotency(ctx, scope, identity, key, ttl, func(tx store.Store) ([]byte, error) { return fn(p.wrap(tx)) })
}

func (p pausingStore) hold(ctx context.Context) {
	if ctx.Value(pauseKey{}) != nil {
		p.reached <- struct{}{}
		<-p.release
	}
}

func (p pausingStore) ListRuns(ctx context.Context, projectID, parentTaskID string) ([]store.Run, error) {
	p.hold(ctx)
	return p.Store.ListRuns(ctx, projectID, parentTaskID)
}

func (p pausingStore) GetSprint(ctx context.Context, projectID, sprintID string) (store.Sprint, error) {
	sp, err := p.Store.GetSprint(ctx, projectID, sprintID)
	p.hold(ctx)
	return sp, err
}

// pausedRace runs first under a Core over a pausingStore, holds it at its read, then starts second
// beside it on e's own Core and gives second up to settle to finish on its own before first goes on.
// It answers both errors and whether second finished while first was held.
func pausedRace(t *testing.T, e *lcEnv, first func(c *Core, ctx context.Context) error, second func() error) (firstErr, secondErr error, secondFirst bool) {
	t.Helper()
	ps := pausingStore{Store: e.st, reached: make(chan struct{}), release: make(chan struct{})}
	c := New(ps, Options{Logger: log.New(&logBuf{}, "", 0), Now: e.clock.now, ToolCallFlush: -1})
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- first(c, context.WithValue(e.ctx, pauseKey{}, true)) }()
	select {
	case <-ps.reached:
	case err := <-firstDone:
		t.Fatalf("the held call finished before its read: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the held call never reached its read")
	}
	go func() { secondDone <- second() }()
	select {
	case secondErr = <-secondDone:
		secondFirst = true
	case <-time.After(3 * time.Second):
	}
	close(ps.release)
	firstErr = <-firstDone
	if !secondFirst {
		secondErr = <-secondDone
	}
	return firstErr, secondErr, secondFirst
}

// open_run held after it read the parent as racing; pick_run beside it. The pick must not settle the
// race under open_run: either open_run lands first and pick_run deletes its run with the other
// losers, or open_run reads the settled parent and is refused. Before open_run took the project
// lock, pick_run committed in the gap and open_run then left a claimable TODO run under a DONE
// parent that no race tool could reach.
func TestOpenRunDoesNotOutliveAConcurrentPick(t *testing.T) {
	e := newLC(t, storetest.Postgres(t))
	e.settings(t, map[string]any{"reviewEnabled": false})
	parent := e.task(t, domain.Todo)
	started, err := e.c.Runs().Start(e.ctx, e.alice, StartRunsReq{ProjectID: e.project, TaskID: parent.ID, Version: parent.Version, Count: 2})
	if err != nil {
		t.Fatal(err)
	}
	winner := started.Runs[0].ID
	c := e.claim(t, e.alice, winner, 0)
	if _, err := e.c.Lifecycle().Complete(e.ctx, e.alice, CompleteTaskReq{ProjectID: e.project, TaskID: winner, LeaseID: c.LeaseID, Deliverable: "the answer"}); err != nil {
		t.Fatal(err)
	}
	openErr, pickErr, pickFirst := pausedRace(t, e,
		func(c *Core, ctx context.Context) error {
			_, err := c.Runs().Open(ctx, e.alice, OpenRunReq{ProjectID: e.project, TaskID: parent.ID})
			return err
		},
		func() error {
			_, err := e.c.Runs().Pick(e.ctx, e.alice, PickRunReq{ProjectID: e.project, TaskID: parent.ID, RunTaskID: winner, Version: int64(started.Task.Version)})
			return err
		})
	if pickErr != nil {
		t.Fatalf("pick_run: %v", pickErr)
	}
	after := e.get(t, parent.ID)
	left := e.runIDs(t, parent.ID)
	t.Logf("pick_run finished while open_run was held: %v; open_run: %v; parent %s racing=%v; runs left %v", pickFirst, openErr, after.Status, after.Racing, left)
	if openErr != nil {
		wantCode(t, openErr, domain.CodeInvalidTransition)
	}
	if after.Racing || after.Status != domain.Done || !slices.Equal(left, []string{winner}) {
		t.Fatalf("a run outlived the pick: parent %s racing=%v, runs %v, want only the winner %s", after.Status, after.Racing, left, winner)
	}
}

// set_sprint add held after it read the sprint as ACTIVE; update_sprint closing it beside it. The
// task must not join a sprint that closed under it: either the add lands first and the close's
// warning names it, or the add reads the CLOSED sprint and is refused. Before both took the project
// lock, the close committed in the gap (warning of nothing) and the task then joined the CLOSED
// sprint.
func TestSetSprintDoesNotJoinASprintClosedUnderIt(t *testing.T) {
	e := newLC(t, storetest.Postgres(t))
	sp := e.sprint(t, e.board.ID)
	active := domain.SprintActive
	sp, err := e.st.UpdateSprint(e.ctx, e.project, sp.ID, sp.Version, store.SprintPatch{Status: &active, UpdatedBy: "alice", UpdatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	task := e.task(t, domain.Todo)
	var warnings []string
	addErr, closeErr, closeFirst := pausedRace(t, e,
		func(c *Core, ctx context.Context) error {
			_, err := c.Sprints().Set(ctx, e.alice, SetSprintReq{ProjectID: e.project, SprintID: sp.ID, TaskID: task.ID, Action: "add", Version: task.Version})
			return err
		},
		func() error {
			r, err := e.c.Sprints().Update(e.ctx, e.alice, UpdateSprintReq{ProjectID: e.project, SprintID: sp.ID, Version: sp.Version,
				Patch: SprintPatchIn{Status: Some(SprintStatus("CLOSED"))}})
			warnings = r.Warnings
			return err
		})
	if closeErr != nil {
		t.Fatalf("update_sprint CLOSED: %v", closeErr)
	}
	got := e.get(t, task.ID)
	t.Logf("the close finished while set_sprint was held: %v; set_sprint: %v; sprintIds %v; close warnings %q", closeFirst, addErr, got.SprintIDs, warnings)
	if addErr != nil {
		wantCode(t, addErr, domain.CodeInvalidTransition)
		return
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], task.ID) {
		t.Fatalf("%s joined sprint %s (sprintIds %v) but the close warned %q: it joined after the close", task.ID, sp.ID, got.SprintIDs, warnings)
	}
}

// create_task {sprintId} held after it read the sprint as ACTIVE; update_sprint closing it beside it:
// the same order as set_sprint's.
func TestCreateTaskInSprintDoesNotJoinASprintClosedUnderIt(t *testing.T) {
	e := newLC(t, storetest.Postgres(t))
	sp := e.sprint(t, e.board.ID)
	active := domain.SprintActive
	sp, err := e.st.UpdateSprint(e.ctx, e.project, sp.ID, sp.Version, store.SprintPatch{Status: &active, UpdatedBy: "alice", UpdatedAt: 1})
	if err != nil {
		t.Fatal(err)
	}
	var created string
	var warnings []string
	createErr, closeErr, closeFirst := pausedRace(t, e,
		func(c *Core, ctx context.Context) error {
			r, err := c.Tasks().Create(ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "late member", SprintID: sp.ID})
			created = r.Task.ID
			return err
		},
		func() error {
			r, err := e.c.Sprints().Update(e.ctx, e.alice, UpdateSprintReq{ProjectID: e.project, SprintID: sp.ID, Version: sp.Version,
				Patch: SprintPatchIn{Status: Some(SprintStatus("CLOSED"))}})
			warnings = r.Warnings
			return err
		})
	if closeErr != nil {
		t.Fatalf("update_sprint CLOSED: %v", closeErr)
	}
	t.Logf("the close finished while create_task was held: %v; create_task: %v %s; close warnings %q", closeFirst, createErr, created, warnings)
	if createErr != nil {
		wantCode(t, createErr, domain.CodeInvalidTransition)
		return
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], created) {
		t.Fatalf("%s was created in sprint %s but the close warned %q: it joined after the close", created, sp.ID, warnings)
	}
}
