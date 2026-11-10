package service

import (
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// The R1.3 tests run on memstore and on both engines (onEngines); the Postgres leg skips without
// BATONDECK_TEST_POSTGRES_DSN. R1.18 runs the same set at the composition root.

// tkSeed is a task in status s straight through the store, its column the board's for s; BLOCKED with
// reason (poison, or "" for a dependency block), claimAttempts and labels as given.
func (e *env) tkSeed(t *testing.T, s domain.Status, reason string, attempts int64, labels []string) store.Task {
	t.Helper()
	task, err := e.st.CreateTask(e.ctx, store.CreateTaskInput{
		ProjectID: e.project, BoardID: e.board.ID, ColumnID: e.column(t, s), Title: "seed " + string(s), Status: s,
		Labels: labels, OrderKey: "a0", CreatedBy: "alice", CreatedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if reason == "" && attempts == 0 {
		return task
	}
	got, err := e.st.SetStatus(e.ctx, e.project, task.ID, task.Version, store.StatusChange{
		Status: s, BlockedReason: reason, ClaimAttempts: &attempts, UpdatedBy: "alice", UpdatedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (e *env) tkMove(t *testing.T, c domain.Caller, task store.Task, to domain.Status) (MoveTaskResp, error) {
	t.Helper()
	return e.c.Tasks().Move(e.ctx, c, MoveTaskReq{ProjectID: e.project, TaskID: task.ID, Version: task.Version, ToStatus: to})
}

func tkCode(err error) domain.Code {
	if e, ok := domain.AsBatonDeckError(err); ok {
		return e.Code
	}
	return ""
}

// ADR-0005 for add_subtask, and the no-TODO refusal of both creating tools: the child lands TODO in
// its board's first TODO column whatever the first column is, and a board with no TODO column refuses
// create_task and add_subtask with VALIDATION, writing nothing.
func TestCreatedTasksLandTodo(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		doneFirst, err := e.c.Boards().Create(e.ctx, e.alice, CreateBoardReq{ProjectID: e.project, Name: "done first", Columns: []ColumnSpec{{Name: "Done", Status: domain.Done}, {Name: "To do", Status: domain.Todo}}})
		if err != nil {
			t.Fatal(err)
		}
		parent, err := e.c.Tasks().Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: doneFirst.Board.ID, Title: "parent"})
		if err != nil {
			t.Fatal(err)
		}
		child, err := e.c.Context().AddSubtask(e.ctx, e.alice, AddSubtaskReq{ProjectID: e.project, ParentTaskID: parent.Task.ID, Title: "child"})
		if err != nil || child.Task.Status != domain.Todo || child.Task.ColumnID != doneFirst.Board.Columns[1].ID {
			t.Fatalf("add_subtask on a board whose first column is DONE: %+v, %v; want TODO in its To do column", child.Task, err)
		}

		noTodo, err := e.c.Boards().Create(e.ctx, e.alice, CreateBoardReq{ProjectID: e.project, Name: "no todo", Columns: []ColumnSpec{{Name: "In progress", Status: domain.InProgress}, {Name: "Done", Status: domain.Done}}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.c.Tasks().Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: noTodo.Board.ID, Title: "x"})
		wantCode(t, err, domain.CodeValidation)
		held, err := e.st.CreateTask(e.ctx, store.CreateTaskInput{
			ProjectID: e.project, BoardID: noTodo.Board.ID, ColumnID: noTodo.Board.Columns[1].ID, Title: "held", Status: domain.Done,
			OrderKey: "a0", CreatedBy: "alice", CreatedAt: time.Now().UnixMilli(),
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.c.Context().AddSubtask(e.ctx, e.alice, AddSubtaskReq{ProjectID: e.project, ParentTaskID: held.ID, Title: "x"})
		wantCode(t, err, domain.CodeValidation)
		page, err := e.st.ListTasks(e.ctx, e.project, store.TaskFilter{BoardID: noTodo.Board.ID}, "", 50)
		if err != nil || len(page.Tasks) != 1 {
			t.Fatalf("the no-TODO board holds %d tasks (%v), want only the seeded parent", len(page.Tasks), err)
		}
	})
}

// AC1: all 25 (from, to) pairs through Move: the 14 legal ones (9 table edges + 5 same-status)
// land on to, the 11 illegal ones are INVALID_TRANSITION carrying allowed and the shortest path
// (ADR-0005's table: 1+3+2+2+1 edges).
func TestMoveTheFiveByFiveTable(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		var legal, same, illegal int
		for _, from := range domain.AllStatuses() {
			for _, to := range domain.AllStatuses() {
				task := e.tkSeed(t, from, "", 0, nil)
				got, err := e.tkMove(t, e.alice, task, to)
				if domain.CanTransition(from, to) {
					if err != nil || got.Task.Status != to {
						t.Fatalf("%s → %s: %v (status %s), want a legal move", from, to, err, got.Task.Status)
					}
					legal++
					if from == to {
						same++
					}
					continue
				}
				be := wantCode(t, err, domain.CodeInvalidTransition)
				allowed, _ := be.Details["allowed"].([]domain.Status)
				path, _ := be.Details["path"].([]domain.Status)
				if !slices.Equal(allowed, domain.AllowedNext(from)) || !slices.Equal(path, domain.ShortestPath(from, to)) || len(path) < 3 {
					t.Fatalf("%s → %s: details %v, want allowed %v and path %v", from, to, be.Details, domain.AllowedNext(from), domain.ShortestPath(from, to))
				}
				illegal++
			}
		}
		t.Logf("25 moves: %d legal (%d same-status), %d INVALID_TRANSITION with allowed and path", legal, same, illegal)
		if legal != 14 || same != 5 || illegal != 11 {
			t.Fatalf("%d legal (%d same), %d illegal; want 14 (5), 11", legal, same, illegal)
		}
	})
}

// AC2: CANCELLED and DEAD_LETTER are not statuses: VALIDATION, and nothing moved.
func TestMoveRefusesCancelledAndDeadLetter(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		task := e.tkSeed(t, domain.Todo, "", 0, nil)
		for _, s := range []domain.Status{"CANCELLED", "DEAD_LETTER"} {
			_, err := e.tkMove(t, e.alice, task, s)
			wantCode(t, err, domain.CodeValidation)
		}
		if got := e.get(t, task.ID); got.Version != task.Version || got.Status != domain.Todo {
			t.Fatalf("after the refusals the task is %s v%d, want TODO v%d", got.Status, got.Version, task.Version)
		}
	})
}

// AC3: un-poisoning is master-only. A worker's move of a poisoned task is FORBIDDEN; a master's lands
// TODO with claimAttempts 0, the reason and the poisoned label gone; with an open blocker the same
// move lands BLOCKED, un-poisoned (P-42). The control on a dependency-blocked task: a master's move
// to TODO keeps claimAttempts. DEVIATION: a worker's move of a dependency-blocked task is FORBIDDEN
// too, not the brief's writer-tier success: move_task is master tier (domain grants.go, pinned by
// grants_test.go), so authorize refuses every worker move before the poison is read.
func TestUnpoisonIsMasterOnly(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		master := e.member(t, e.project, "mia", domain.RoleMaster)
		worker := e.member(t, e.project, "will", domain.RoleWorker)
		poisoned := func(t *testing.T) store.Task {
			return e.tkSeed(t, domain.Blocked, domain.BlockedReasonPoison, 3, []string{"x", domain.LabelPoisoned})
		}
		t.Run("WorkerIsForbidden", func(t *testing.T) {
			task := poisoned(t)
			_, err := e.tkMove(t, worker, task, domain.Todo)
			wantCode(t, err, domain.CodeForbidden)
			if got := e.get(t, task.ID); got.Status != domain.Blocked || reasonOf(got) != domain.BlockedReasonPoison || got.ClaimAttempts != 3 {
				t.Fatalf("after the refusal %s %q attempts %d, want it still poisoned", got.Status, reasonOf(got), got.ClaimAttempts)
			}
		})
		t.Run("MasterUnpoisons", func(t *testing.T) {
			task := poisoned(t)
			got, err := e.tkMove(t, master, task, domain.Todo)
			if err != nil {
				t.Fatal(err)
			}
			st := e.get(t, task.ID)
			if got.Task.Status != domain.Todo || st.ClaimAttempts != 0 || st.BlockedReason != nil || slices.Contains(st.Labels, domain.LabelPoisoned) || !slices.Contains(st.Labels, "x") {
				t.Fatalf("un-poisoned task %s attempts %d reason %v labels %v, want TODO, 0, nil, [x]", st.Status, st.ClaimAttempts, st.BlockedReason, st.Labels)
			}
		})
		t.Run("MasterUnpoisonWithABlockerLandsBlocked", func(t *testing.T) {
			task, blocker := poisoned(t), e.tkSeed(t, domain.Todo, "", 0, nil)
			if _, err := e.c.Context().AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: blocker.ID, ToTaskID: task.ID}); err != nil {
				t.Fatal(err)
			}
			got, err := e.tkMove(t, master, e.get(t, task.ID), domain.Todo)
			if err != nil {
				t.Fatal(err)
			}
			st := e.get(t, task.ID)
			if got.Task.Status != domain.Blocked || st.BlockedReason != nil || st.ClaimAttempts != 0 || slices.Contains(st.Labels, domain.LabelPoisoned) {
				t.Fatalf("got %s reason %v attempts %d labels %v, want BLOCKED by its blocker, un-poisoned", st.Status, st.BlockedReason, st.ClaimAttempts, st.Labels)
			}
		})
		t.Run("ControlDependencyBlocked", func(t *testing.T) {
			task := e.tkSeed(t, domain.Blocked, "", 2, nil)
			_, err := e.tkMove(t, worker, task, domain.Todo)
			wantCode(t, err, domain.CodeForbidden) // DEVIATION above: the tier, not the poison
			got, err := e.tkMove(t, master, task, domain.Todo)
			if err != nil {
				t.Fatal(err)
			}
			if got.Task.Status != domain.Todo || got.Task.ClaimAttempts != 2 {
				t.Fatalf("dependency-blocked → %s attempts %v, want TODO keeping its 2 attempts", got.Task.Status, got.Task.ClaimAttempts)
			}
		})
		t.Run("ReorderInTheLaneKeepsPoison", func(t *testing.T) {
			task := poisoned(t)
			got, err := e.tkMove(t, master, task, domain.Blocked)
			if err != nil {
				t.Fatal(err)
			}
			if st := e.get(t, got.Task.ID); reasonOf(st) != domain.BlockedReasonPoison || st.ClaimAttempts != 3 {
				t.Fatalf("a same-lane move left reason %q attempts %d, want poison and 3", reasonOf(st), st.ClaimAttempts)
			}
		})
	})
}

// AC4: wipLimit 1 on In progress with one task there: a second move in is WIP_EXCEEDED; once the
// first leaves, it succeeds.
func TestMoveWIPExceeded(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		limit := int64(1)
		if _, err := e.st.UpdateColumn(e.ctx, e.project, e.board.ID, e.column(t, domain.InProgress), store.ColumnPatch{SetWIPLimit: true, WIPLimit: &limit}); err != nil {
			t.Fatal(err)
		}
		first, second := e.tkSeed(t, domain.InProgress, "", 0, nil), e.tkSeed(t, domain.Todo, "", 0, nil)
		_, err := e.tkMove(t, e.alice, second, domain.InProgress)
		be := wantCode(t, err, domain.CodeWIPExceeded)
		t.Logf("second move in: %s %v", be.Code, be.Details)
		if _, err := e.tkMove(t, e.alice, first, domain.Review); err != nil {
			t.Fatal(err)
		}
		if got, err := e.tkMove(t, e.alice, second, domain.InProgress); err != nil || got.Task.Status != domain.InProgress {
			t.Fatalf("after the first left: %v %s, want IN_PROGRESS", err, got.Task.Status)
		}
	})
}

// Promote-with-dependencies: a task with an open blocker moved to TODO (or IN_PROGRESS) lands in
// the Blocked column; dropping the blocker's edge is then what frees it.
func TestMovePromoteWithDependenciesLandsBlocked(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		task, blocker := e.tkSeed(t, domain.InProgress, "", 0, nil), e.tkSeed(t, domain.Todo, "", 0, nil)
		if _, err := e.c.Context().AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: blocker.ID, ToTaskID: task.ID}); err != nil {
			t.Fatal(err)
		}
		got, err := e.tkMove(t, e.alice, e.get(t, task.ID), domain.Todo)
		if err != nil {
			t.Fatal(err)
		}
		if got.Task.Status != domain.Blocked || got.Task.ColumnID != e.column(t, domain.Blocked) {
			t.Fatalf("promoted with a blocker → %s in %s, want BLOCKED in the Blocked column", got.Task.Status, got.Task.ColumnID)
		}
	})
}

// A move to DONE clears its id from each dependent: the non-poison one goes TODO with one
// task.dependency_cleared and a broadcast; the poisoned one keeps BLOCKED and its reason.
func TestMoveToDoneUnblocksDependents(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		blocker := e.tkSeed(t, domain.Review, "", 0, nil)
		plain, poison := e.tkSeed(t, domain.Blocked, "", 0, nil), e.tkSeed(t, domain.Blocked, domain.BlockedReasonPoison, 3, []string{domain.LabelPoisoned})
		for _, d := range []store.Task{plain, poison} {
			if _, err := e.c.Context().AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: blocker.ID, ToTaskID: d.ID}); err != nil {
				t.Fatal(err)
			}
		}
		before := e.changeCount(t)
		if _, err := e.tkMove(t, e.alice, e.get(t, blocker.ID), domain.Done); err != nil {
			t.Fatal(err)
		}
		if got := e.get(t, plain.ID); got.Status != domain.Todo || len(got.BlockedBy) != 0 || len(ofType(e.events(t, plain.ID), domain.EventTaskDependencyCleared)) != 1 {
			t.Fatalf("plain dependent %s blockedBy %v, want TODO, [] and one dependency_cleared", got.Status, got.BlockedBy)
		}
		if got := e.get(t, poison.ID); got.Status != domain.Blocked || reasonOf(got) != domain.BlockedReasonPoison || len(got.BlockedBy) != 0 {
			t.Fatalf("poisoned dependent %s %q blockedBy %v, want BLOCKED poison with the cache cleared", got.Status, reasonOf(got), got.BlockedBy)
		}
		kinds := e.changeKinds(t, before)
		if !slices.Contains(kinds, DoorbellBroadcast+":"+plain.ID) {
			t.Fatalf("change rows %v, want a broadcast for %s", kinds, plain.ID)
		}
	})
}

func (e *env) changeCount(t *testing.T) int {
	t.Helper()
	ch, err := e.st.ReadChanges(e.ctx, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return len(ch)
}

// changeKinds is "kind:taskId" of each change row after the first skip.
func (e *env) changeKinds(t *testing.T, skip int) []string {
	t.Helper()
	ch, err := e.st.ReadChanges(e.ctx, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, c := range ch[skip:] {
		var p changePayload
		if err := json.Unmarshal(c.Payload, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p.Kind+":"+p.TaskID)
	}
	return out
}

// Every declared error of move_task besides the table's and WIP's.
func TestMoveRefusals(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		task := e.tkSeed(t, domain.Todo, "", 0, nil)
		mv := func(req MoveTaskReq) error {
			req.ProjectID = cmpOr(req.ProjectID, e.project)
			_, err := e.c.Tasks().Move(e.ctx, e.alice, req)
			return err
		}
		wantCode(t, mv(MoveTaskReq{TaskID: task.ID, Version: task.Version + 1, ToStatus: domain.Todo}), domain.CodeStale)
		wantCode(t, mv(MoveTaskReq{TaskID: "T-404", Version: 1, ToStatus: domain.Todo}), domain.CodeNotFound)
		wantCode(t, mv(MoveTaskReq{TaskID: task.ID, Version: task.Version, ToColumnID: "C-404"}), domain.CodeNotFound)
		wantCode(t, mv(MoveTaskReq{TaskID: task.ID, Version: task.Version, Order: "!"}), domain.CodeValidation)
		wantCode(t, mv(MoveTaskReq{TaskID: task.ID, Version: task.Version}), domain.CodeValidation)
		wantCode(t, mv(MoveTaskReq{ProjectID: "P-other", TaskID: task.ID, Version: task.Version, ToStatus: domain.Todo}), domain.CodeForbidden)

		now := time.Now()
		held, err := e.st.ClaimTask(e.ctx, e.project, task.ID, task.Version, store.ClaimInput{
			Owner: "bob", LeaseID: "L-1", ExpiresAt: now.Add(time.Minute).UnixMilli(), Now: now.UnixMilli(), ColumnID: e.column(t, domain.InProgress),
		})
		if err != nil {
			t.Fatal(err)
		}
		wantCode(t, mv(MoveTaskReq{TaskID: held.ID, Version: held.Version, ToStatus: domain.Review}), domain.CodeConflictLocked)

		racing := e.tkSeed(t, domain.Todo, "", 0, nil)
		on := true
		racing, err = e.st.UpdateTask(e.ctx, e.project, racing.ID, racing.Version, store.TaskPatch{Racing: &on, UpdatedBy: "alice", UpdatedAt: now.UnixMilli()})
		if err != nil {
			t.Fatal(err)
		}
		be := wantCode(t, mv(MoveTaskReq{TaskID: racing.ID, Version: racing.Version, ToStatus: domain.Todo}), domain.CodeConflictLocked)
		if be.Details["reason"] != "race" {
			t.Fatalf("racing refusal details %v, want reason race", be.Details)
		}
		// The control: the lease holder itself moves its task out, and the lease goes with it.
		bob := e.member(t, e.project, "bob", domain.RoleMaster)
		got, err := e.c.Tasks().Move(e.ctx, bob, MoveTaskReq{ProjectID: e.project, TaskID: held.ID, Version: held.Version, ToStatus: domain.Review})
		if err != nil || got.Task.Claim != nil {
			t.Fatalf("the holder's own move: %v claim %v, want REVIEW with the lease dropped", err, got.Task.Claim)
		}
	})
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// create_task: the first column's status, T-n from the counter, the idempotency replay, columnId,
// the event and the TODO broadcast; and its refusals.
func TestCreateTask(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		tk := e.c.Tasks()
		first := CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "first", Labels: []string{"l"}, CustomFields: map[string]FieldValue{"n": 1.0, "s": "v", "b": true}, IdempotencyKey: "k"}
		a, err := tk.Create(e.ctx, e.alice, first)
		if err != nil {
			t.Fatal(err)
		}
		if a.Task.Status != domain.Todo || a.Task.ColumnID != e.column(t, domain.Todo) || a.Task.Priority != domain.PriorityNormal || a.Task.CustomFields["n"] != 1.0 {
			t.Fatalf("create landed %+v, want TODO in the To do column, normal, its fields", a.Task)
		}
		// The same call again replays (a key is bound to the call's arguments: other arguments under
		// the same key are another call).
		again, err := tk.Create(e.ctx, e.alice, first)
		if err != nil || again.Task.ID != a.Task.ID {
			t.Fatalf("replay %v %s, want %s", err, again.Task.ID, a.Task.ID)
		}
		before := e.changeCount(t)
		b, err := tk.Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "second", ColumnID: e.column(t, domain.Todo)})
		if err != nil {
			t.Fatal(err)
		}
		if b.Task.Status != domain.Todo || tkNum(t, b.Task.ID) != tkNum(t, a.Task.ID)+1 {
			t.Fatalf("second %s %s after %s, want TODO and the next T-n", b.Task.ID, b.Task.Status, a.Task.ID)
		}
		if kinds := e.changeKinds(t, before); !slices.Equal(kinds, []string{DoorbellBroadcast + ":" + b.Task.ID}) {
			t.Fatalf("change rows %v, want one broadcast", kinds)
		}
		if n := len(ofType(e.events(t, b.Task.ID), domain.EventTaskCreated)); n != 1 {
			t.Fatalf("%d task.created events", n)
		}
		// ADR-0005: no created task is a draft. It lands TODO, in the board's first TODO column when
		// no column is named, and a named column of any other status is VALIDATION.
		doneFirst, err := e.c.Boards().Create(e.ctx, e.alice, CreateBoardReq{ProjectID: e.project, Name: "done first", Columns: []ColumnSpec{{Name: "Done", Status: domain.Done}, {Name: "To do", Status: domain.Todo}}})
		if err != nil {
			t.Fatal(err)
		}
		c, err := tk.Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: doneFirst.Board.ID, Title: "lands todo"})
		if err != nil || c.Task.Status != domain.Todo || c.Task.ColumnID != doneFirst.Board.Columns[1].ID {
			t.Fatalf("create on a board whose first column is DONE: %+v, %v; want TODO in its To do column", c.Task, err)
		}
		for _, st := range domain.AllStatuses() {
			if st == domain.Todo {
				continue
			}
			_, err := tk.Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "x", ColumnID: e.column(t, st)})
			if tkCode(err) != domain.CodeValidation {
				t.Errorf("create into the %s column: %v, want VALIDATION", st, err)
			}
		}
		worker := e.member(t, e.project, "will", domain.RoleWorker)
		_, err = tk.Create(e.ctx, worker, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "w"})
		wantCode(t, err, domain.CodeForbidden)
		parent := "T-404"
		for name, c := range map[string]struct {
			req  CreateTaskReq
			code domain.Code
		}{
			"empty title":      {CreateTaskReq{Title: ""}, domain.CodeValidation},
			"long title":       {CreateTaskReq{Title: strings.Repeat("x", 301)}, domain.CodeValidation},
			"system label":     {CreateTaskReq{Title: "x", Labels: []string{domain.LabelPoisoned}}, domain.CodeValidation},
			"33 labels":        {CreateTaskReq{Title: "x", Labels: slices.Repeat([]string{"l"}, 33)}, domain.CodeValidation},
			"long label":       {CreateTaskReq{Title: "x", Labels: []string{strings.Repeat("l", 65)}}, domain.CodeValidation},
			"field type":       {CreateTaskReq{Title: "x", CustomFields: map[string]FieldValue{"o": []any{}}}, domain.CodeValidation},
			"long field":       {CreateTaskReq{Title: "x", CustomFields: map[string]FieldValue{"o": strings.Repeat("v", 501)}}, domain.CodeValidation},
			"priority":         {CreateTaskReq{Title: "x", Priority: "huge"}, domain.CodeValidation},
			"model hint":       {CreateTaskReq{Title: "x", ModelHint: ModelHintInput{Model: "m", Effort: "max"}}, domain.CodeValidation},
			"unknown column":   {CreateTaskReq{Title: "x", ColumnID: "C-404"}, domain.CodeValidation},
			"missing parent":   {CreateTaskReq{Title: "x", ParentTaskID: &parent}, domain.CodeNotFound},
			"unknown sprint":   {CreateTaskReq{Title: "x", SprintID: "S-1"}, domain.CodeNotFound},
			"unknown board":    {CreateTaskReq{Title: "x", BoardID: "B-404"}, domain.CodeNotFound},
			"other project":    {CreateTaskReq{Title: "x", ProjectID: "P-other"}, domain.CodeForbidden},
			"long idempotency": {CreateTaskReq{Title: "x", IdempotencyKey: strings.Repeat("k", 129)}, domain.CodeValidation},
		} {
			c.req.ProjectID, c.req.BoardID = cmpOr(c.req.ProjectID, e.project), cmpOr(c.req.BoardID, e.board.ID)
			_, err := tk.Create(e.ctx, e.alice, c.req)
			if tkCode(err) != c.code {
				t.Errorf("%s: %v, want %s", name, err, c.code)
			}
		}
	})
}

func tkNum(t *testing.T, id string) int {
	t.Helper()
	var n int
	if _, err := fmt.Sscanf(id, "T-%d", &n); err != nil {
		t.Fatalf("task id %q: %v", id, err)
	}
	return n
}

// get_task: the task with its contextSummary, at most two statements (the pin); NOT_FOUND and
// FORBIDDEN.
func TestGetTask(t *testing.T) {
	f := newFixture(t, Options{})
	n := &calls{m: map[string]int{}}
	old := f.c
	f.c = New(&ctxCounting{countingStore{Store: f.st, n: n}}, Options{Logger: log.New(f.logs, "", 0), Now: f.clock.now, ToolCallFlush: -1})
	if err := old.Close(f.ctx); err != nil {
		t.Fatal(err)
	}
	task := f.task(t, domain.Todo, nil)
	if _, err := f.c.Context().SetSummary(f.ctx, f.alice, SetSummaryReq{ProjectID: f.project.ID, TaskID: task.ID, Version: task.Version, Summary: "so far"}); err != nil {
		t.Fatal(err)
	}
	req := GetTaskReq{ProjectID: f.project.ID, TaskID: task.ID}
	if _, err := f.c.Tasks().Get(f.ctx, f.alice, req); err != nil {
		t.Fatal(err)
	}
	n.take()
	got, err := f.c.Tasks().Get(f.ctx, f.alice, req)
	if err != nil {
		t.Fatal(err)
	}
	warm := n.take()
	t.Logf("get_task: %d statements %v", statements(warm), warm)
	if statements(warm) > 2 || got.Task.ContextSummary != "so far" || got.Task.ID != task.ID {
		t.Fatalf("get_task %v statements, summary %q; want ≤ 2 and the summary", warm, got.Task.ContextSummary)
	}
	_, err = f.c.Tasks().Get(f.ctx, f.alice, GetTaskReq{ProjectID: f.project.ID, TaskID: "T-404"})
	wantCode(t, err, domain.CodeNotFound)
	_, err = f.c.Tasks().Get(f.ctx, domain.Caller{Identity: "stranger"}, req)
	wantCode(t, err, domain.CodeForbidden)
}

// update_task's tri-state fields: absent changes nothing, null clears, a value sets — for assignee,
// dueAt, reviewer, estimate and modelHint; description is absent or a value ("" included).
func TestUpdateTaskTriState(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		task := e.tkSeed(t, domain.Todo, "", 0, nil)
		up := func(t *testing.T, p TaskPatch) store.Task {
			t.Helper()
			cur := e.get(t, task.ID)
			if _, err := e.c.Tasks().Update(e.ctx, e.alice, UpdateTaskReq{ProjectID: e.project, TaskID: task.ID, Version: cur.Version, Patch: p}); err != nil {
				t.Fatal(err)
			}
			return e.get(t, task.ID)
		}
		due := "2026-11-01T10:00:00.000Z"
		set := up(t, TaskPatch{
			Assignee: Value("ann"), DueAt: Value(due), Reviewer: Value("rev"), Estimate: Value(3.5),
			ModelHint: Value(ModelHintInput{Model: "opus", Effort: "high"}), Description: Some("d"), Title: "renamed",
		})
		if set.Assignee == nil || *set.Assignee != "ann" || set.DueAt == nil || isoMs(*set.DueAt) != due || set.Reviewer == nil || set.Estimate == nil || *set.Estimate != 3.5 || len(set.ModelHint) == 0 || set.Description != "d" || set.Title != "renamed" {
			t.Fatalf("a value did not set: %+v", set)
		}
		kept := up(t, TaskPatch{})
		if kept.Assignee == nil || kept.DueAt == nil || kept.Reviewer == nil || kept.Estimate == nil || len(kept.ModelHint) == 0 || kept.Description != "d" {
			t.Fatalf("an absent field changed: %+v", kept)
		}
		cleared := up(t, TaskPatch{Assignee: Null[string](), DueAt: Null[string](), Reviewer: Null[string](), Estimate: Null[float64](), ModelHint: Null[ModelHintInput](), Description: Some("")})
		if cleared.Assignee != nil || cleared.DueAt != nil || cleared.Reviewer != nil || cleared.Estimate != nil || len(cleared.ModelHint) != 0 || cleared.Description != "" {
			t.Fatalf("a null did not clear: %+v", cleared)
		}
		updated := ofType(e.events(t, task.ID), domain.EventTaskUpdated)
		if len(updated) != 3 || !strings.Contains(string(updated[0].Data)+string(updated[1].Data)+string(updated[2].Data), `"assignee","estimate","dueAt"`) {
			t.Fatalf("task.updated events %d, want 3 naming their fields", len(updated))
		}
	})
}

// update_task: the doorbell on a new assignee, the broadcast when a TODO task loses it; STALE; the
// system-label and autoAssign refusals.
func TestUpdateTaskDoorbellsAndRefusals(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		task := e.tkSeed(t, domain.Todo, "", 0, []string{domain.LabelPoisoned})
		before := e.changeCount(t)
		got, err := e.c.Tasks().Update(e.ctx, e.alice, UpdateTaskReq{ProjectID: e.project, TaskID: task.ID, Version: task.Version, Patch: TaskPatch{Assignee: Value("ann")}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.c.Tasks().Update(e.ctx, e.alice, UpdateTaskReq{ProjectID: e.project, TaskID: task.ID, Version: int64(got.Task.Version), Patch: TaskPatch{Assignee: Null[string]()}}); err != nil {
			t.Fatal(err)
		}
		if kinds := e.changeKinds(t, before); !slices.Equal(kinds, []string{DoorbellAssigned + ":" + task.ID, DoorbellBroadcast + ":" + task.ID}) {
			t.Fatalf("change rows %v, want assigned then broadcast", kinds)
		}
		cur := e.get(t, task.ID)
		upd := func(req UpdateTaskReq) error {
			req.ProjectID, req.TaskID = e.project, task.ID
			_, err := e.c.Tasks().Update(e.ctx, e.alice, req)
			return err
		}
		wantCode(t, upd(UpdateTaskReq{Version: cur.Version - 1, Patch: TaskPatch{Title: "x"}}), domain.CodeStale)
		wantCode(t, upd(UpdateTaskReq{Version: cur.Version, Patch: TaskPatch{Labels: []string{"other"}}}), domain.CodeValidation)
		wantCode(t, upd(UpdateTaskReq{Version: cur.Version, AutoAssign: true, Patch: TaskPatch{Assignee: Value("ann")}}), domain.CodeValidation)
		wantCode(t, upd(UpdateTaskReq{Version: cur.Version, AutoAssign: true}), domain.CodeValidation)
		wantCode(t, upd(UpdateTaskReq{Version: cur.Version, Patch: TaskPatch{DueAt: Value("tomorrow")}}), domain.CodeValidation)
		wantCode(t, upd(UpdateTaskReq{Version: cur.Version, Patch: TaskPatch{Reviewer: Value("")}}), domain.CodeValidation)
		// The control: keeping the system label it carries is allowed.
		if err := upd(UpdateTaskReq{Version: cur.Version, Patch: TaskPatch{Labels: []string{domain.LabelPoisoned, "other"}}}); err != nil {
			t.Fatalf("a label list keeping the system label: %v", err)
		}
		worker := e.member(t, e.project, "will", domain.RoleWorker)
		_, err = e.c.Tasks().Update(e.ctx, worker, UpdateTaskReq{ProjectID: e.project, TaskID: task.ID, Version: cur.Version + 1})
		wantCode(t, err, domain.CodeForbidden)
	})
}

// list_tasks: the filters, cursor paging over every task once, one statement, and the refusals.
func TestListTasks(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		ann := "ann"
		var ids []string
		for i := range 5 {
			task, err := e.c.Tasks().Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: fmt.Sprint("t", i), ColumnID: e.column(t, domain.Todo), Labels: []string{fmt.Sprint("l", i%2)}})
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, task.Task.ID)
		}
		cur := e.get(t, ids[0])
		if _, err := e.c.Tasks().Update(e.ctx, e.alice, UpdateTaskReq{ProjectID: e.project, TaskID: ids[0], Version: cur.Version, Patch: TaskPatch{Assignee: Value(ann)}}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.c.Tasks().Move(e.ctx, e.alice, MoveTaskReq{ProjectID: e.project, TaskID: ids[1], Version: e.get(t, ids[1]).Version, ToStatus: domain.InProgress}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.c.Context().AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: ids[3], ToTaskID: ids[2]}); err != nil {
			t.Fatal(err)
		}
		list := func(t *testing.T, req ListTasksReq) []string {
			t.Helper()
			req.ProjectID, req.BoardID = e.project, e.board.ID
			page, err := e.c.Tasks().List(e.ctx, e.alice, req)
			if err != nil {
				t.Fatal(err)
			}
			var out []string
			for _, x := range page.Tasks {
				out = append(out, x.ID)
			}
			slices.Sort(out)
			return out
		}
		sorted := func(ss ...string) []string { slices.Sort(ss); return ss }
		for name, c := range map[string]struct {
			req  ListTasksReq
			want []string
		}{
			"all":              {ListTasksReq{}, sorted(ids...)},
			"status":           {ListTasksReq{Status: domain.InProgress}, []string{ids[1]}},
			"assignee value":   {ListTasksReq{Assignee: Value(ann)}, []string{ids[0]}},
			"assignee null":    {ListTasksReq{Assignee: Null[string]()}, sorted(ids[1:]...)},
			"label":            {ListTasksReq{Label: "l1"}, sorted(ids[1], ids[3])},
			"unblockedOnly":    {ListTasksReq{UnblockedOnly: true}, sorted(ids[0], ids[1], ids[3], ids[4])},
			"missingArtifacts": {ListTasksReq{MissingArtifacts: true}, sorted(ids...)},
			"updatedSince":     {ListTasksReq{UpdatedSince: isoMs(time.Now().Add(time.Hour).UnixMilli())}, nil},
		} {
			if got := list(t, c.req); !slices.Equal(got, c.want) {
				t.Errorf("%s: %v, want %v", name, got, c.want)
			}
		}
		var paged []string
		cursor := ""
		for range 10 {
			page, err := e.c.Tasks().List(e.ctx, e.alice, ListTasksReq{ProjectID: e.project, BoardID: e.board.ID, Limit: 2, Cursor: cursor})
			if err != nil {
				t.Fatal(err)
			}
			for _, x := range page.Tasks {
				paged = append(paged, x.ID)
			}
			if cursor = page.NextCursor; cursor == "" {
				break
			}
		}
		if slices.Sort(paged); !slices.Equal(paged, sorted(ids...)) {
			t.Fatalf("paging by 2 read %v, want each of %v once", paged, ids)
		}
		lst := func(req ListTasksReq) error {
			req.ProjectID, req.BoardID = cmpOr(req.ProjectID, e.project), cmpOr(req.BoardID, e.board.ID)
			_, err := e.c.Tasks().List(e.ctx, e.alice, req)
			return err
		}
		wantCode(t, lst(ListTasksReq{Status: "CANCELLED"}), domain.CodeValidation)
		wantCode(t, lst(ListTasksReq{Limit: 201}), domain.CodeValidation)
		wantCode(t, lst(ListTasksReq{Label: "l", SprintID: "S-1"}), domain.CodeValidation)
		wantCode(t, lst(ListTasksReq{Cursor: "!!"}), domain.CodeValidation)
		wantCode(t, lst(ListTasksReq{UpdatedSince: "yesterday"}), domain.CodeValidation)
		wantCode(t, lst(ListTasksReq{BoardID: "B-404"}), domain.CodeNotFound)
		wantCode(t, lst(ListTasksReq{ProjectID: "P-other"}), domain.CodeForbidden)
	})
}

// list_tasks is one statement once the membership is cached (the pin is two).
func TestListTasksCostsOneStatement(t *testing.T) {
	f, n := counted(t)
	f.task(t, domain.Todo, nil)
	req := ListTasksReq{ProjectID: f.project.ID, BoardID: f.board.ID}
	if _, err := f.c.Tasks().List(f.ctx, f.alice, req); err != nil {
		t.Fatal(err)
	}
	n.take()
	if _, err := f.c.Tasks().List(f.ctx, f.alice, req); err != nil {
		t.Fatal(err)
	}
	warm := n.take()
	t.Logf("list_tasks: %d statements %v", statements(warm), warm)
	if statements(warm) > 2 || warm["ListTasks"] != 1 {
		t.Fatalf("list_tasks made %v, want one ListTasks (pin 2)", warm)
	}
}

// pgOnly is a fresh env over Postgres; it skips without BATONDECK_TEST_POSTGRES_DSN. memstore and
// SQLite serialise every transaction whole, so a race needs Postgres's READ COMMITTED to show.
func pgOnly(t *testing.T) *env {
	t.Helper()
	return newEnv(t, storetest.Postgres(t))
}

// P-146 (review round 1, the reviewer's TestProbeMoveWIPRace): two concurrent moves of two TODO
// tasks into a wipLimit-1 In progress admit exactly one, 40 runs out of 40; without the project lock
// both were admitted in 40 of 40.
func TestMoveWIPRaceAdmitsOne(t *testing.T) {
	e := pgOnly(t)
	limit := int64(1)
	if _, err := e.st.UpdateColumn(e.ctx, e.project, e.board.ID, e.column(t, domain.InProgress), store.ColumnPatch{SetWIPLimit: true, WIPLimit: &limit}); err != nil {
		t.Fatal(err)
	}
	over, runs := 0, 40
	for i := range runs {
		pair := []store.Task{e.tkSeed(t, domain.Todo, "", 0, nil), e.tkSeed(t, domain.Todo, "", 0, nil)}
		errs := make([]error, 2)
		var wg sync.WaitGroup
		start := make(chan struct{})
		for j, task := range pair {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, errs[j] = e.c.Tasks().Move(e.ctx, e.alice, MoveTaskReq{ProjectID: e.project, TaskID: task.ID, Version: task.Version, ToStatus: domain.InProgress})
			}()
		}
		close(start)
		wg.Wait()
		ip := domain.InProgress
		n, err := e.st.CountTasks(e.ctx, e.project, store.TaskFilter{BoardID: e.board.ID, Status: &ip})
		if err != nil {
			t.Fatal(err)
		}
		if n > 1 {
			over++
			t.Logf("run %d: IN_PROGRESS=%d under wipLimit 1; errors %v / %v", i, n, errs[0], errs[1])
		} else if (errs[0] == nil) == (errs[1] == nil) || tkCode(cmpErr(errs)) != domain.CodeWIPExceeded {
			t.Fatalf("run %d: errors %v / %v, want one success and one WIP_EXCEEDED", i, errs[0], errs[1])
		}
		for _, task := range pair {
			if cur := e.get(t, task.ID); cur.Status == domain.InProgress {
				if _, err := e.tkMove(t, e.alice, cur, domain.Review); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	t.Logf("WIP over-admitted in %d of %d runs", over, runs)
	if over > 0 {
		t.Fatalf("P-146: %d of %d runs left 2 tasks IN_PROGRESS under wipLimit 1", over, runs)
	}
}

// cmpErr is the one non-nil error of two, or nil.
func cmpErr(errs []error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// Review round 1: a move landing on DONE takes the project lock first, so it cannot deadlock against
// add/remove_dependency's lockPair (dependent first when its id sorts lower). The reviewer saw one
// INTERNAL in about 800 unlocked runs; 200 runs here must show none. Not mutation-provable at that
// rate: it is the regression sentry, the lock order is the argument.
func TestMoveToDoneVersusDependenciesNeverInternal(t *testing.T) {
	e := pgOnly(t)
	internal := 0
	for i := range 200 {
		dep, blocker := e.tkSeed(t, domain.Blocked, "", 0, nil), e.tkSeed(t, domain.Review, "", 0, nil)
		edge, err := e.c.Context().AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: blocker.ID, ToTaskID: dep.ID})
		if err != nil {
			t.Fatal(err)
		}
		blocker = e.get(t, blocker.ID)
		errs := make([]error, 2)
		var wg sync.WaitGroup
		start := make(chan struct{})
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, errs[0] = e.tkMove(t, e.alice, blocker, domain.Done)
		}()
		go func() {
			defer wg.Done()
			<-start
			if i%2 == 0 {
				_, errs[1] = e.c.Context().RemoveDependency(e.ctx, e.alice, RemoveDependencyReq{ProjectID: e.project, EdgeID: edge.Edge.ID})
			} else {
				_, errs[1] = e.c.Context().AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: dep.ID, ToTaskID: blocker.ID})
			}
		}()
		close(start)
		wg.Wait()
		for _, err := range errs {
			if tkCode(err) == domain.CodeInternal {
				internal++
				t.Logf("run %d: %v", i, err)
			}
		}
	}
	if internal > 0 {
		t.Fatalf("%d INTERNAL in 200 runs of move→DONE against add/remove_dependency", internal)
	}
}

// O4 (review round 1): a move into IN_PROGRESS consumes the retry backoff (conductor's clearBackoff,
// tasks.ts:418); a move elsewhere keeps it.
func TestMoveIntoInProgressClearsTheBackoff(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		later := time.Now().Add(time.Hour).UnixMilli()
		seed := func(t *testing.T) store.Task {
			task := e.tkSeed(t, domain.Todo, "", 0, nil)
			got, err := e.st.SetStatus(e.ctx, e.project, task.ID, task.Version, store.StatusChange{
				Status: domain.Todo, NextEligibleAt: &store.Nullable[int64]{Value: &later}, UpdatedBy: "alice", UpdatedAt: time.Now().UnixMilli(),
			})
			if err != nil {
				t.Fatal(err)
			}
			return got
		}
		in := seed(t)
		if _, err := e.tkMove(t, e.alice, in, domain.InProgress); err != nil {
			t.Fatal(err)
		}
		if got := e.get(t, in.ID); got.NextEligibleAt != nil {
			t.Fatalf("after the move into IN_PROGRESS nextEligibleAt is %d, want cleared", *got.NextEligibleAt)
		}
		kept := seed(t)
		if _, err := e.tkMove(t, e.alice, kept, domain.Todo); err != nil {
			t.Fatal(err)
		}
		if got := e.get(t, kept.ID); got.NextEligibleAt == nil || *got.NextEligibleAt != later {
			t.Fatalf("a reorder within TODO left nextEligibleAt %v, want %d kept", got.NextEligibleAt, later)
		}
	})
}

// move_task refuses an order key holding a byte outside the key alphabet, or longer than
// domain.MaxOrderKeyLen, with VALIDATION on every engine (a NUL used to be Postgres's INTERNAL), and
// stores nothing.
func TestMoveRefusesAMalformedOrderKey(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		task := e.tkSeed(t, domain.Todo, "", 0, nil)
		for _, order := range []string{"a0~", "a0\x00", "a0\n<b>", "a" + strings.Repeat("1", domain.MaxOrderKeyLen)} {
			_, err := e.c.Tasks().Move(e.ctx, e.alice, MoveTaskReq{ProjectID: e.project, TaskID: task.ID, Version: task.Version, Order: order})
			if tkCode(err) != domain.CodeValidation {
				t.Fatalf("move_task order %.20q: %v, want VALIDATION", order, err)
			}
		}
		if got := e.get(t, task.ID); got.Version != task.Version || got.OrderKey != task.OrderKey {
			t.Fatalf("a refused move wrote: %+v", got)
		}
	})
}

// Each card holds an order key of its own in its column, so a card can always be dropped between
// two others: create_task + claim_next rounds leave distinct keys in IN_PROGRESS (a claim used to
// carry its TODO key, the very key the next create took again), and concurrent creates into one
// TODO column mint distinct keys (on Postgres they read the column's head at once).
func TestLifecycleWritesKeepOrderKeysDistinct(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		distinct := func(t *testing.T, status domain.Status, want int) {
			t.Helper()
			page, err := e.st.ListTasks(e.ctx, e.project, store.TaskFilter{BoardID: e.board.ID, ColumnID: e.column(t, status)}, "", 200)
			if err != nil {
				t.Fatal(err)
			}
			keys := map[string]bool{}
			for i, task := range page.Tasks {
				keys[task.OrderKey] = true
				if i > 0 {
					if _, err := domain.OrderBetween(page.Tasks[i-1].OrderKey, task.OrderKey); err != nil {
						t.Fatalf("%s: no key fits between %s %q and %s %q: %v", status, page.Tasks[i-1].ID, page.Tasks[i-1].OrderKey, task.ID, task.OrderKey, err)
					}
				}
			}
			if len(page.Tasks) != want || len(keys) != want {
				t.Fatalf("%s holds %d tasks under %d distinct keys, want %d and %d", status, len(page.Tasks), len(keys), want, want)
			}
		}
		for range 3 {
			if _, err := e.c.Tasks().Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "t"}); err != nil {
				t.Fatal(err)
			}
			got, err := e.c.Lifecycle().ClaimNext(e.ctx, e.alice, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID})
			if err != nil || got.Task == nil {
				t.Fatalf("claim_next: %+v %v", got.Task, err)
			}
		}
		distinct(t, domain.InProgress, 3)

		const n = 16
		var wg sync.WaitGroup
		errs := make([]error, n)
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = e.c.Tasks().Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "c"})
			}()
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		distinct(t, domain.Todo, n)
	})
}
