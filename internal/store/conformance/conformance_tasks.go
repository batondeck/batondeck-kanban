package conformance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

func init() { register("tasks", tasks) }

// taskClock is the time the task batteries write at: a fixed Unix millisecond, so every engine
// records the same values and expiry is judged against a known now.
const taskClock int64 = 1_700_000_000_000

// taskBoard is a migrated store holding one project (alice's) with one board of the default columns.
type taskBoard struct {
	s       store.Store
	project string
	board   string
	col     map[domain.Status]string
}

func newTaskBoard(t *testing.T, newStore Factory) taskBoard {
	t.Helper()
	s := migrated(t, newStore)
	return taskBoardOn(t, s)
}

// taskBoardOn is a new project and board on s.
func taskBoardOn(t *testing.T, s store.Store) taskBoard {
	t.Helper()
	p := newProject(t, s, "alice")
	b := newBoard(t, s, p.ID, nil)
	col := map[domain.Status]string{}
	for _, c := range b.Columns {
		col[c.Status] = c.ID
	}
	return taskBoard{s: s, project: p.ID, board: b.ID, col: col}
}

// input is a TODO task titled title, at order key "a0", created by alice at taskClock.
func (f taskBoard) input(title string) store.CreateTaskInput {
	return store.CreateTaskInput{
		ProjectID: f.project, BoardID: f.board, ColumnID: f.col[domain.Todo], Title: title,
		Status: domain.Todo, OrderKey: "a0", CreatedBy: "alice", CreatedAt: taskClock,
	}
}

// add creates a task from input(title) after edit.
func (f taskBoard) add(t *testing.T, title string, edit func(*store.CreateTaskInput)) store.Task {
	t.Helper()
	in := f.input(title)
	if edit != nil {
		edit(&in)
	}
	task, err := f.s.CreateTask(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// requireBuilt skips the whole area when one of probes answers that its method is unbuilt on this
// engine (store.UnbuiltTask): the contract can land before its statements do.
func requireBuilt(t *testing.T, newStore Factory, probes ...func(store.Store) error) {
	t.Helper()
	s := migrated(t, newStore)
	for _, probe := range probes {
		if task, unbuilt := store.UnbuiltTask(probe(s)); unbuilt {
			t.Skipf("not built on this engine yet: %s builds it", task)
		}
	}
}

func probeTasks(s store.Store) error {
	_, err := s.GetTask(context.Background(), "P-probe", "T-1")
	return err
}

// mustOK fails on err.
func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// wantStale fails unless err is domain.Stale(expected, actual).
func wantStale(t *testing.T, err error, expected, actual int64) {
	t.Helper()
	wantCode(t, err, domain.CodeStale, "")
	e, _ := domain.AsBatonDeckError(err)
	if fmt.Sprint(e.Details["expected"]) != fmt.Sprint(expected) || fmt.Sprint(e.Details["actual"]) != fmt.Sprint(actual) {
		t.Fatalf("STALE details %v, want expected %d actual %d", e.Details, expected, actual)
	}
}

// sameTask fails unless got and want are equal in every field.
func sameTask(t *testing.T, got, want store.Task) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("task\n got %+v\nwant %+v", got, want)
	}
}

func taskIDs(ts []store.Task) []string {
	out := make([]string, len(ts))
	for i, task := range ts {
		out[i] = task.ID
	}
	return out
}

// tasks is the task area: ids per project, create and read back, the patch, status and order
// writes at a version (STALE when it is not the task's), pages, counts, lookups by id and delete,
// each with its refusals; and the board's counts and column removal over real tasks.
func tasks(t *testing.T, newStore Factory) {
	ctx := context.Background()
	requireBuilt(t, newStore, probeTasks)

	t.Run("CreateTaskMintsIdsPerProjectAtVersionOneAndReadsBack", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		in := f.input("first")
		in.Description = "the description"
		in.Priority = domain.PriorityHigh
		in.Assignee = ptr("bob")
		in.Labels = []string{"x", "y"}
		in.RequiredCapabilities = []string{"go"}
		in.CustomFields = json.RawMessage(`{"points":3}`)
		in.ModelHint = json.RawMessage(`{"model":"m"}`)
		in.SprintIDs = []string{"S-1"}
		in.CreatedByAgent = ptr("agent-1")
		first, err := f.s.CreateTask(ctx, in)
		mustOK(t, err)
		want := store.Task{
			ProjectID: f.project, ID: "T-1", BoardID: f.board, ColumnID: f.col[domain.Todo], Title: "first",
			Description: "the description", Status: domain.Todo, Priority: domain.PriorityHigh, Assignee: ptr("bob"),
			Labels: []string{"x", "y"}, RequiredCapabilities: []string{"go"}, OrderKey: "a0",
			CustomFields: json.RawMessage(`{"points":3}`), BlockedBy: []string{}, Blocks: []string{},
			ModelHint: json.RawMessage(`{"model":"m"}`), SprintIDs: []string{"S-1"}, Version: 1,
			CreatedBy: "alice", CreatedByAgent: ptr("agent-1"), CreatedAt: taskClock, UpdatedBy: "alice", UpdatedAt: taskClock,
		}
		sameTask(t, first, want)
		got, err := f.s.GetTask(ctx, f.project, "T-1")
		mustOK(t, err)
		sameTask(t, got, want)

		// The defaults: priority normal, empty lists and custom fields, every nullable NULL.
		second := f.add(t, "second", nil)
		if second.ID != "T-2" || second.Priority != domain.PriorityNormal || second.Assignee != nil ||
			len(second.Labels) != 0 || second.Labels == nil || string(second.CustomFields) != `{}` ||
			second.ModelHint != nil || second.Claim != nil || second.SprintIDs == nil {
			t.Fatalf("defaults: %+v", second)
		}
		// Another project's counter is its own: it starts at T-1 too.
		other := taskBoardOn(t, f.s)
		if task := other.add(t, "elsewhere", nil); task.ID != "T-1" {
			t.Fatalf("another project's first task is %s, want T-1", task.ID)
		}
	})

	t.Run("CreateTaskRefusesAndTakesNoNumber", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		otherBoard := newBoard(t, f.s, f.project, nil)
		for name, c := range map[string]struct {
			edit   func(*store.CreateTaskInput)
			code   domain.Code
			entity string
		}{
			"empty title":            {func(in *store.CreateTaskInput) { in.Title = " " }, domain.CodeValidation, ""},
			"empty creator":          {func(in *store.CreateTaskInput) { in.CreatedBy = "" }, domain.CodeValidation, ""},
			"empty order key":        {func(in *store.CreateTaskInput) { in.OrderKey = "" }, domain.CodeValidation, ""},
			"no time":                {func(in *store.CreateTaskInput) { in.CreatedAt = 0 }, domain.CodeValidation, ""},
			"a retired status":       {func(in *store.CreateTaskInput) { in.Status = "CANCELLED" }, domain.CodeValidation, ""},
			"an unknown priority":    {func(in *store.CreateTaskInput) { in.Priority = "critical" }, domain.CodeValidation, ""},
			"custom fields a list":   {func(in *store.CreateTaskInput) { in.CustomFields = json.RawMessage(`[1]`) }, domain.CodeValidation, ""},
			"a model hint not JSON":  {func(in *store.CreateTaskInput) { in.ModelHint = json.RawMessage(`{`) }, domain.CodeValidation, ""},
			"a missing project":      {func(in *store.CreateTaskInput) { in.ProjectID = "P-missing" }, domain.CodeNotFound, "Project"},
			"a missing board":        {func(in *store.CreateTaskInput) { in.BoardID = "B-missing" }, domain.CodeNotFound, "Board"},
			"another board's column": {func(in *store.CreateTaskInput) { in.ColumnID = otherBoard.Columns[0].ID }, domain.CodeNotFound, "Column"},
		} {
			in := f.input("t")
			c.edit(&in)
			_, err := f.s.CreateTask(ctx, in)
			if err == nil {
				t.Fatalf("%s: created", name)
			}
			wantCode(t, err, c.code, c.entity)
		}
		// Positive control, and no refusal took a number.
		if task := f.add(t, "ok", nil); task.ID != "T-1" {
			t.Fatalf("after the refusals the first task is %s, want T-1", task.ID)
		}
	})

	t.Run("CreateTaskInARolledBackAtomicallyLeavesNothing", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		boom := errors.New("boom")
		err := f.s.Atomically(ctx, func(tx store.Store) error {
			if _, err := tx.CreateTask(ctx, f.input("gone")); err != nil {
				return err
			}
			if _, err := tx.GetTask(ctx, f.project, "T-1"); err != nil {
				return fmt.Errorf("the transaction does not see its own task: %w", err)
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("Atomically = %v, want the callback's error", err)
		}
		_, err = f.s.GetTask(ctx, f.project, "T-1")
		wantCode(t, err, domain.CodeNotFound, "Task")
		if task := f.add(t, "kept", nil); task.ID != "T-1" {
			t.Fatalf("after the rollback the first task is %s, want T-1", task.ID)
		}
	})

	t.Run("LockTaskReadsAsGetTaskInsideATransaction", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		want := f.add(t, "locked", nil)
		mustOK(t, f.s.Atomically(ctx, func(tx store.Store) error {
			got, err := tx.LockTask(ctx, f.project, want.ID)
			if err != nil {
				return err
			}
			sameTask(t, got, want)
			_, err = tx.LockTask(ctx, f.project, "T-99")
			wantCode(t, err, domain.CodeNotFound, "Task")
			return nil
		}))
	})

	t.Run("GetTaskIsKeyedByProject", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		g := taskBoardOn(t, f.s)
		f.add(t, "in f", nil)
		g.add(t, "in g", nil)
		a, err := f.s.GetTask(ctx, f.project, "T-1")
		mustOK(t, err)
		b, err := f.s.GetTask(ctx, g.project, "T-1")
		mustOK(t, err)
		if a.Title != "in f" || b.Title != "in g" {
			t.Fatalf("T-1 by project: %q and %q", a.Title, b.Title)
		}
		_, err = f.s.GetTask(ctx, f.project, "T-2")
		wantCode(t, err, domain.CodeNotFound, "Task")
		_, err = f.s.GetTask(ctx, "P-missing", "T-1")
		wantCode(t, err, domain.CodeNotFound, "Task")
	})

	t.Run("UpdateTaskAppliesThePatchAndBumpsTheVersion", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		before := f.add(t, "t", func(in *store.CreateTaskInput) { in.Assignee = ptr("bob") })
		after, err := f.s.UpdateTask(ctx, f.project, before.ID, 1, store.TaskPatch{
			Title: ptr("renamed"), Priority: ptr(domain.PriorityUrgent), Assignee: &store.Nullable[string]{},
			Labels: ptr([]string{"l"}), Estimate: &store.Nullable[float64]{Value: ptr(2.5)},
			DueAt: &store.Nullable[int64]{Value: ptr(taskClock + 5)}, CustomFields: ptr(json.RawMessage(`{"k":"v"}`)),
			Review: &store.Nullable[json.RawMessage]{Value: ptr(json.RawMessage(`{"requestedBy":"alice"}`))},
			Racing: ptr(true), Score: ptr(1.5), SearchTokens: ptr("tok"),
			UpdatedBy: "carol", UpdatedAt: taskClock + 10,
		})
		mustOK(t, err)
		want := before
		want.Title, want.Priority, want.Assignee, want.Labels = "renamed", domain.PriorityUrgent, nil, []string{"l"}
		want.Estimate, want.DueAt, want.CustomFields = ptr(2.5), ptr(taskClock+5), json.RawMessage(`{"k":"v"}`)
		want.Review, want.Racing, want.Score, want.SearchTokens = json.RawMessage(`{"requestedBy":"alice"}`), true, 1.5, "tok"
		want.Version, want.UpdatedBy, want.UpdatedAt = 2, "carol", taskClock+10
		sameTask(t, after, want)
		got, err := f.s.GetTask(ctx, f.project, before.ID)
		mustOK(t, err)
		sameTask(t, got, want)
	})

	t.Run("UpdateTaskAtAnotherVersionIsStaleAndChangesNothing", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		before := f.add(t, "t", nil)
		_, err := f.s.UpdateTask(ctx, f.project, before.ID, 7, store.TaskPatch{Title: ptr("no"), UpdatedBy: "bob", UpdatedAt: taskClock + 1})
		wantStale(t, err, 7, 1)
		got, err := f.s.GetTask(ctx, f.project, before.ID)
		mustOK(t, err)
		sameTask(t, got, before)
	})

	t.Run("UpdateTaskConcurrentAtOneVersionHasOneWinnerAndTheRestAreStale", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		const n = 10
		errs := make([]error, n)
		var wg sync.WaitGroup
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = f.s.UpdateTask(ctx, f.project, task.ID, 1, store.TaskPatch{Title: ptr(fmt.Sprint("by ", i)), UpdatedBy: "bob", UpdatedAt: taskClock + 1})
			}()
		}
		wg.Wait()
		won := 0
		for _, err := range errs {
			if err == nil {
				won++
				continue
			}
			wantStale(t, err, 1, 2)
		}
		if won != 1 {
			t.Fatalf("%d writers won at version 1, want 1", won)
		}
		if got, _ := f.s.GetTask(ctx, f.project, task.ID); got.Version != 2 {
			t.Fatalf("version %d after one win, want 2", got.Version)
		}
	})

	t.Run("UpdateTaskRefusals", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		_, err := f.s.UpdateTask(ctx, f.project, "T-9", 1, store.TaskPatch{Title: ptr("x"), UpdatedBy: "bob", UpdatedAt: taskClock})
		wantCode(t, err, domain.CodeNotFound, "Task")
		for name, p := range map[string]store.TaskPatch{
			"an empty title":       {Title: ptr(""), UpdatedBy: "bob", UpdatedAt: taskClock},
			"an unknown priority":  {Priority: ptr(domain.Priority("critical")), UpdatedBy: "bob", UpdatedAt: taskClock},
			"no updater":           {Title: ptr("x"), UpdatedAt: taskClock},
			"custom fields a list": {CustomFields: ptr(json.RawMessage(`[1]`)), UpdatedBy: "bob", UpdatedAt: taskClock},
			"a review not JSON":    {Review: &store.Nullable[json.RawMessage]{Value: ptr(json.RawMessage(`{`))}, UpdatedBy: "bob", UpdatedAt: taskClock},
			"no time":              {Title: ptr("x"), UpdatedBy: "bob"},
		} {
			_, err := f.s.UpdateTask(ctx, f.project, task.ID, 1, p)
			if err == nil {
				t.Fatalf("%s: updated", name)
			}
			wantCode(t, err, domain.CodeValidation, "")
		}
		if got, _ := f.s.GetTask(ctx, f.project, task.ID); got.Version != 1 {
			t.Fatalf("a refused update moved the version to %d", got.Version)
		}
	})

	t.Run("SetStatusMovesWritesTheRetryStateAndBumps", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		blocked, err := f.s.SetStatus(ctx, f.project, task.ID, 1, store.StatusChange{
			Status: domain.Blocked, ColumnID: f.col[domain.Blocked], OrderKey: ptr("b0"), BlockedReason: "waiting",
			ClaimAttempts: ptr(int64(2)), NextEligibleAt: &store.Nullable[int64]{Value: ptr(taskClock + 60_000)},
			UpdatedBy: "bob", UpdatedAt: taskClock + 1,
		})
		mustOK(t, err)
		if blocked.Status != domain.Blocked || blocked.ColumnID != f.col[domain.Blocked] || blocked.OrderKey != "b0" ||
			blocked.BlockedReason == nil || *blocked.BlockedReason != "waiting" || blocked.ClaimAttempts != 2 ||
			blocked.NextEligibleAt == nil || *blocked.NextEligibleAt != taskClock+60_000 || blocked.Version != 2 ||
			blocked.UpdatedBy != "bob" || blocked.UpdatedAt != taskClock+1 {
			t.Fatalf("blocked: %+v", blocked)
		}
		// Leaving BLOCKED clears the reason; ColumnID "" keeps the column; nil keeps the retry state.
		ready, err := f.s.SetStatus(ctx, f.project, task.ID, 2, store.StatusChange{Status: domain.Todo, BlockedReason: "ignored", UpdatedBy: "bob", UpdatedAt: taskClock + 2})
		mustOK(t, err)
		if ready.Status != domain.Todo || ready.BlockedReason != nil || ready.ColumnID != f.col[domain.Blocked] ||
			ready.ClaimAttempts != 2 || ready.NextEligibleAt == nil || ready.Version != 3 {
			t.Fatalf("ready: %+v", ready)
		}
		got, err := f.s.GetTask(ctx, f.project, task.ID)
		mustOK(t, err)
		sameTask(t, got, ready)
	})

	t.Run("SetStatusUnpoisonResetsTheRetryState", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", func(in *store.CreateTaskInput) { in.Labels = []string{"keep"} })
		poisoned, err := f.s.SetStatus(ctx, f.project, task.ID, 1, store.StatusChange{
			Status: domain.Blocked, BlockedReason: domain.BlockedReasonPoison, ClaimAttempts: ptr(int64(3)),
			Labels: domain.WithLabel(task.Labels, domain.LabelPoisoned), UpdatedBy: "bob", UpdatedAt: taskClock + 1,
		})
		mustOK(t, err)
		if !domain.IsPoisoned(poisoned.Status, *poisoned.BlockedReason) {
			t.Fatalf("not poisoned: %+v", poisoned)
		}
		back, err := f.s.SetStatus(ctx, f.project, task.ID, 2, store.StatusChange{Status: domain.Todo, ColumnID: f.col[domain.Todo], Unpoison: true, UpdatedBy: "master", UpdatedAt: taskClock + 2})
		mustOK(t, err)
		if back.Status != domain.Todo || back.BlockedReason != nil || back.ClaimAttempts != 0 || back.NextEligibleAt != nil ||
			!reflect.DeepEqual(back.Labels, []string{"keep"}) || back.Version != 3 {
			t.Fatalf("un-poisoned: %+v", back)
		}
		// Refusal: Unpoison on a task that is not poisoned.
		_, err = f.s.SetStatus(ctx, f.project, task.ID, 3, store.StatusChange{Status: domain.Todo, Unpoison: true, UpdatedBy: "master", UpdatedAt: taskClock + 3})
		wantCode(t, err, domain.CodeValidation, "")
	})

	t.Run("SetStatusWritesTheHandoverFields", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", func(in *store.CreateTaskInput) { in.Assignee = ptr("ann") })
		review := json.RawMessage(`{"reviewer":"rex"}`)
		got, err := f.s.SetStatus(ctx, f.project, task.ID, 1, store.StatusChange{
			Status: domain.Review, Assignee: &store.Nullable[string]{Value: ptr("rex")}, Deliverable: &store.Nullable[string]{Value: ptr("shipped")},
			Review: &store.Nullable[json.RawMessage]{Value: &review}, UpdatedBy: "bob", UpdatedAt: taskClock + 1,
		})
		mustOK(t, err)
		if got.Assignee == nil || *got.Assignee != "rex" || got.Deliverable == nil || *got.Deliverable != "shipped" ||
			!json.Valid(got.Review) || !strings.Contains(string(got.Review), `"rex"`) || got.Version != 2 {
			t.Fatalf("handover: %+v", got)
		}
		// nil keeps each field; a nil Value sets it NULL.
		kept, err := f.s.SetStatus(ctx, f.project, task.ID, 2, store.StatusChange{Status: domain.Done, UpdatedBy: "bob", UpdatedAt: taskClock + 2})
		mustOK(t, err)
		if kept.Assignee == nil || *kept.Assignee != "rex" || kept.Deliverable == nil || kept.Review == nil {
			t.Fatalf("nil fields were not kept: %+v", kept)
		}
		cleared, err := f.s.SetStatus(ctx, f.project, task.ID, 3, store.StatusChange{Status: domain.InProgress, Assignee: &store.Nullable[string]{}, Review: &store.Nullable[json.RawMessage]{}, UpdatedBy: "bob", UpdatedAt: taskClock + 3})
		mustOK(t, err)
		if cleared.Assignee != nil || cleared.Review != nil || cleared.Deliverable == nil {
			t.Fatalf("nil Values were not written NULL: %+v", cleared)
		}
		again, err := f.s.GetTask(ctx, f.project, task.ID)
		mustOK(t, err)
		sameTask(t, again, cleared)
		// Refusal: a Review that is not JSON writes nothing.
		bad := json.RawMessage(`{`)
		_, err = f.s.SetStatus(ctx, f.project, task.ID, 4, store.StatusChange{Status: domain.Done, Review: &store.Nullable[json.RawMessage]{Value: &bad}, UpdatedBy: "bob", UpdatedAt: taskClock + 4})
		wantCode(t, err, domain.CodeValidation, "")
		after, _ := f.s.GetTask(ctx, f.project, task.ID)
		sameTask(t, after, cleared)
	})

	t.Run("SetStatusAtAnotherVersionIsStale", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		_, err := f.s.SetStatus(ctx, f.project, task.ID, 2, store.StatusChange{Status: domain.Blocked, UpdatedBy: "bob", UpdatedAt: taskClock})
		wantStale(t, err, 2, 1)
		got, _ := f.s.GetTask(ctx, f.project, task.ID)
		sameTask(t, got, task)
	})

	t.Run("SetStatusRefusals", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		other := newBoard(t, f.s, f.project, nil)
		_, err := f.s.SetStatus(ctx, f.project, "T-9", 1, store.StatusChange{Status: domain.Blocked, UpdatedBy: "bob", UpdatedAt: taskClock})
		wantCode(t, err, domain.CodeNotFound, "Task")
		_, err = f.s.SetStatus(ctx, f.project, task.ID, 1, store.StatusChange{Status: "DEAD_LETTER", UpdatedBy: "bob", UpdatedAt: taskClock})
		wantCode(t, err, domain.CodeValidation, "")
		_, err = f.s.SetStatus(ctx, f.project, task.ID, 1, store.StatusChange{Status: domain.Blocked, UpdatedAt: taskClock})
		wantCode(t, err, domain.CodeValidation, "")
		_, err = f.s.SetStatus(ctx, f.project, task.ID, 1, store.StatusChange{Status: domain.Blocked, ColumnID: other.Columns[0].ID, UpdatedBy: "bob", UpdatedAt: taskClock})
		wantCode(t, err, domain.CodeNotFound, "Column")
		if got, _ := f.s.GetTask(ctx, f.project, task.ID); got.Version != 1 {
			t.Fatalf("a refused status write moved the version to %d", got.Version)
		}
	})

	t.Run("SetOrderKeyReordersAndMovesColumn", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		got, err := f.s.SetOrderKey(ctx, f.project, task.ID, 1, "a5", "", "bob", taskClock+1)
		mustOK(t, err)
		if got.OrderKey != "a5" || got.ColumnID != task.ColumnID || got.Version != 2 || got.UpdatedBy != "bob" {
			t.Fatalf("reordered: %+v", got)
		}
		got, err = f.s.SetOrderKey(ctx, f.project, task.ID, 2, "a6", f.col[domain.Blocked], "bob", taskClock+2)
		mustOK(t, err)
		if got.OrderKey != "a6" || got.ColumnID != f.col[domain.Blocked] || got.Version != 3 {
			t.Fatalf("moved: %+v", got)
		}
	})

	t.Run("SetOrderKeyAtAnotherVersionIsStale", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		_, err := f.s.SetOrderKey(ctx, f.project, task.ID, 5, "a5", "", "bob", taskClock)
		wantStale(t, err, 5, 1)
	})

	t.Run("SetOrderKeyRefusals", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		other := newBoard(t, f.s, f.project, nil)
		_, err := f.s.SetOrderKey(ctx, f.project, "T-9", 1, "a5", "", "bob", taskClock)
		wantCode(t, err, domain.CodeNotFound, "Task")
		_, err = f.s.SetOrderKey(ctx, f.project, task.ID, 1, "", "", "bob", taskClock)
		wantCode(t, err, domain.CodeValidation, "")
		_, err = f.s.SetOrderKey(ctx, f.project, task.ID, 1, "a5", other.Columns[0].ID, "bob", taskClock)
		wantCode(t, err, domain.CodeNotFound, "Column")
	})

	t.Run("ListTasksPagesInOrderKeyOrderAndFilters", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		// Created out of order: the list is (order key, id), never creation order.
		f.add(t, "c", func(in *store.CreateTaskInput) { in.OrderKey = "a2"; in.Labels = []string{"red"} })
		f.add(t, "a", func(in *store.CreateTaskInput) { in.OrderKey = "a0"; in.Assignee = ptr("bob") })
		f.add(t, "b", func(in *store.CreateTaskInput) {
			in.OrderKey = "a1"
			in.Status = domain.Blocked
			in.ColumnID = f.col[domain.Blocked]
		})
		f.add(t, "b2", func(in *store.CreateTaskInput) { in.OrderKey = "a1"; in.RunOf = ptr("T-1") })
		f.add(t, "d", func(in *store.CreateTaskInput) {
			in.OrderKey = "a3"
			in.Assignee = ptr("bob")
			in.Labels = []string{"red"}
		})
		other := taskBoardOn(t, f.s)
		other.add(t, "elsewhere", nil)

		var pages [][]string
		cursor := ""
		for {
			page, err := f.s.ListTasks(ctx, f.project, store.TaskFilter{BoardID: f.board}, cursor, 2)
			mustOK(t, err)
			pages = append(pages, taskIDs(page.Tasks))
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		if want := [][]string{{"T-2", "T-3"}, {"T-4", "T-1"}, {"T-5"}}; !reflect.DeepEqual(pages, want) {
			t.Fatalf("pages %v, want %v", pages, want)
		}
		all, err := f.s.ListTasks(ctx, f.project, store.TaskFilter{BoardID: f.board}, "", 0)
		mustOK(t, err)
		if len(all.Tasks) != 5 || all.NextCursor != "" {
			t.Fatalf("limit 0 (50): %v next %q", taskIDs(all.Tasks), all.NextCursor)
		}
		for name, c := range map[string]struct {
			f    store.TaskFilter
			want []string
		}{
			"status":     {store.TaskFilter{BoardID: f.board, Status: ptr(domain.Blocked)}, []string{"T-3"}},
			"column":     {store.TaskFilter{BoardID: f.board, ColumnID: f.col[domain.Todo]}, []string{"T-2", "T-4", "T-1", "T-5"}},
			"assignee":   {store.TaskFilter{BoardID: f.board, Assignee: ptr("bob")}, []string{"T-2", "T-5"}},
			"unassigned": {store.TaskFilter{BoardID: f.board, Unassigned: true}, []string{"T-3", "T-4", "T-1"}},
			"label":      {store.TaskFilter{BoardID: f.board, Label: "red"}, []string{"T-1", "T-5"}},
			"run of":     {store.TaskFilter{BoardID: f.board, RunOf: ptr("T-1")}, []string{"T-4"}},
			"two":        {store.TaskFilter{BoardID: f.board, Assignee: ptr("bob"), Label: "red"}, []string{"T-5"}},
		} {
			page, err := f.s.ListTasks(ctx, f.project, c.f, "", 50)
			mustOK(t, err)
			if got := taskIDs(page.Tasks); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("%s: %v, want %v", name, got, c.want)
			}
			n, err := f.s.CountTasks(ctx, f.project, c.f)
			mustOK(t, err)
			if n != int64(len(c.want)) {
				t.Fatalf("%s: CountTasks %d, want %d", name, n, len(c.want))
			}
		}
	})

	t.Run("ListTasksComparesOrderKeysAndIdsAsBytes", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		for i := range 10 {
			f.add(t, fmt.Sprint("tie ", i+1), nil) // T-1..T-10, all at "a0"
		}
		f.add(t, "upper", func(in *store.CreateTaskInput) { in.OrderKey = "Zz" }) // T-11: 'Z' < 'a' as bytes
		want := []string{"T-11", "T-1", "T-10", "T-2", "T-3", "T-4", "T-5", "T-6", "T-7", "T-8", "T-9"}
		var got []string
		cursor := ""
		for {
			page, err := f.s.ListTasks(ctx, f.project, store.TaskFilter{BoardID: f.board}, cursor, 3)
			mustOK(t, err)
			got = append(got, taskIDs(page.Tasks)...)
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("order %v, want %v", got, want)
		}
	})

	// LastOrderKey is the column's last key in ListTasks' (bytes) order: what an appended task goes
	// after. Another column's keys, and an empty column, do not count.
	t.Run("LastOrderKeyIsTheColumnsLastKeyAsBytes", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		last, err := f.s.LastOrderKey(ctx, f.project, f.board, f.col[domain.Todo])
		mustOK(t, err)
		if last != "" {
			t.Fatalf("an empty column's last key %q, want \"\"", last)
		}
		f.add(t, "a0", nil)
		f.add(t, "Zz", func(in *store.CreateTaskInput) { in.OrderKey = "Zz" }) // 'Z' < 'a' as bytes
		f.add(t, "a1", func(in *store.CreateTaskInput) { in.OrderKey = "a1" })
		f.add(t, "elsewhere", func(in *store.CreateTaskInput) {
			in.OrderKey, in.ColumnID, in.Status = "b0", f.col[domain.InProgress], domain.InProgress
		})
		last, err = f.s.LastOrderKey(ctx, f.project, f.board, f.col[domain.Todo])
		mustOK(t, err)
		if last != "a1" {
			t.Fatalf("the TODO column's last key %q, want a1", last)
		}
	})

	t.Run("ListTasksAndCountTasksRefusals", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		_, err := f.s.ListTasks(ctx, f.project, store.TaskFilter{BoardID: "B-missing"}, "", 10)
		wantCode(t, err, domain.CodeNotFound, "Board")
		_, err = f.s.CountTasks(ctx, f.project, store.TaskFilter{BoardID: "B-missing"})
		wantCode(t, err, domain.CodeNotFound, "Board")
		// A board of another project is not this project's.
		other := taskBoardOn(t, f.s)
		_, err = f.s.ListTasks(ctx, f.project, store.TaskFilter{BoardID: other.board}, "", 10)
		wantCode(t, err, domain.CodeNotFound, "Board")
		_, err = f.s.ListTasks(ctx, f.project, store.TaskFilter{BoardID: f.board, Assignee: ptr("bob"), Unassigned: true}, "", 10)
		wantCode(t, err, domain.CodeValidation, "")
		_, err = f.s.CountTasks(ctx, f.project, store.TaskFilter{BoardID: f.board, Assignee: ptr("bob"), Unassigned: true})
		wantCode(t, err, domain.CodeValidation, "")
		for _, bad := range []string{"%%%", "bnVsbA", store.EncodeCursor("a", "T-1")[:5]} {
			_, err = f.s.ListTasks(ctx, f.project, store.TaskFilter{BoardID: f.board}, bad, 10)
			wantCode(t, err, domain.CodeValidation, "")
		}
		// Positive control: an empty board is an empty page and a zero count.
		page, err := f.s.ListTasks(ctx, f.project, store.TaskFilter{BoardID: f.board}, "", 10)
		mustOK(t, err)
		n, err := f.s.CountTasks(ctx, f.project, store.TaskFilter{BoardID: f.board})
		mustOK(t, err)
		if len(page.Tasks) != 0 || page.NextCursor != "" || n != 0 {
			t.Fatalf("an empty board: %+v, %d", page, n)
		}
	})

	t.Run("ListTasksByIDsKeepsTheOrderAndSkipsTheMissing", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		for _, title := range []string{"1", "2", "3"} {
			f.add(t, title, nil)
		}
		other := taskBoardOn(t, f.s)
		other.add(t, "theirs", nil)
		got, err := f.s.ListTasksByIDs(ctx, f.project, []string{"T-3", "T-9", "T-1"})
		mustOK(t, err)
		if ids := taskIDs(got); !reflect.DeepEqual(ids, []string{"T-3", "T-1"}) || got[0].Title != "3" {
			t.Fatalf("by ids: %v", ids)
		}
		none, err := f.s.ListTasksByIDs(ctx, f.project, nil)
		mustOK(t, err)
		if len(none) != 0 {
			t.Fatalf("no ids: %v", taskIDs(none))
		}
		many := make([]string, 201)
		for i := range many {
			many[i] = fmt.Sprint("T-", i+1)
		}
		_, err = f.s.ListTasksByIDs(ctx, f.project, many)
		wantCode(t, err, domain.CodeValidation, "")
		_, err = f.s.ListTasksByIDs(ctx, f.project, many[:200])
		mustOK(t, err)
	})

	t.Run("DeleteTaskDeletesAndTheNumberIsNotReused", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		mustOK(t, f.s.DeleteTask(ctx, f.project, task.ID, 1))
		_, err := f.s.GetTask(ctx, f.project, task.ID)
		wantCode(t, err, domain.CodeNotFound, "Task")
		wantCode(t, f.s.DeleteTask(ctx, f.project, task.ID, 1), domain.CodeNotFound, "Task")
		if next := f.add(t, "next", nil); next.ID != "T-2" {
			t.Fatalf("after a delete the next task is %s, want T-2", next.ID)
		}
	})

	t.Run("DeleteTaskAtAnotherVersionIsStaleAndKeepsIt", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		task := f.add(t, "t", nil)
		wantStale(t, f.s.DeleteTask(ctx, f.project, task.ID, 3), 3, 1)
		_, err := f.s.GetTask(ctx, f.project, task.ID)
		mustOK(t, err)
	})

	t.Run("BoardCountsAndColumnRemovalSeeTasks", func(t *testing.T) {
		f := newTaskBoard(t, newStore)
		f.add(t, "1", nil)
		f.add(t, "2", nil)
		f.add(t, "3", func(in *store.CreateTaskInput) { in.Status = domain.Done; in.ColumnID = f.col[domain.Done] })
		_, counts, err := f.s.GetBoard(ctx, f.project, f.board)
		mustOK(t, err)
		if counts[f.col[domain.Todo]] != 2 || counts[f.col[domain.Done]] != 1 || counts[f.col[domain.Blocked]] != 0 {
			t.Fatalf("counts %v", counts)
		}
		_, err = f.s.RemoveColumn(ctx, f.project, f.board, f.col[domain.Done])
		wantCode(t, err, domain.CodeValidation, "")
		// Positive control: an empty column goes.
		_, err = f.s.RemoveColumn(ctx, f.project, f.board, f.col[domain.Blocked])
		mustOK(t, err)
	})
}
