package service

import (
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
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

// AC1: all 36 (from, to) pairs through Move: the 15 legal ones (6 same-status) land on to, the 21
// illegal ones are INVALID_TRANSITION carrying allowed and the shortest path.
func TestMoveTheSixBySixTable(t *testing.T) {
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
		t.Logf("36 moves: %d legal (%d same-status), %d INVALID_TRANSITION with allowed and path", legal, same, illegal)
		if legal != 15 || same != 6 || illegal != 21 {
			t.Fatalf("%d legal (%d same), %d illegal; want 15 (6), 21", legal, same, illegal)
		}
	})
}

// AC2: CANCELLED and DEAD_LETTER are not statuses: VALIDATION, and nothing moved.
func TestMoveRefusesCancelledAndDeadLetter(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		task := e.tkSeed(t, domain.Ready, "", 0, nil)
		for _, s := range []domain.Status{"CANCELLED", "DEAD_LETTER"} {
			_, err := e.tkMove(t, e.alice, task, s)
			wantCode(t, err, domain.CodeValidation)
		}
		if got := e.get(t, task.ID); got.Version != task.Version || got.Status != domain.Ready {
			t.Fatalf("after the refusals the task is %s v%d, want READY v%d", got.Status, got.Version, task.Version)
		}
	})
}

// AC3: un-poisoning is master-only. A worker's move of a poisoned task is FORBIDDEN; a master's lands
// READY with claimAttempts 0, the reason and the poisoned label gone; with an open blocker the same
// move lands BLOCKED, un-poisoned (P-42). The control on a dependency-blocked task: a master's move
// to READY keeps claimAttempts. DEVIATION: a worker's move of a dependency-blocked task is FORBIDDEN
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
			_, err := e.tkMove(t, worker, task, domain.Ready)
			wantCode(t, err, domain.CodeForbidden)
			if got := e.get(t, task.ID); got.Status != domain.Blocked || reasonOf(got) != domain.BlockedReasonPoison || got.ClaimAttempts != 3 {
				t.Fatalf("after the refusal %s %q attempts %d, want it still poisoned", got.Status, reasonOf(got), got.ClaimAttempts)
			}
		})
		t.Run("MasterUnpoisons", func(t *testing.T) {
			task := poisoned(t)
			got, err := e.tkMove(t, master, task, domain.Ready)
			if err != nil {
				t.Fatal(err)
			}
			st := e.get(t, task.ID)
			if got.Task.Status != domain.Ready || st.ClaimAttempts != 0 || st.BlockedReason != nil || slices.Contains(st.Labels, domain.LabelPoisoned) || !slices.Contains(st.Labels, "x") {
				t.Fatalf("un-poisoned task %s attempts %d reason %v labels %v, want READY, 0, nil, [x]", st.Status, st.ClaimAttempts, st.BlockedReason, st.Labels)
			}
		})
		t.Run("MasterUnpoisonWithABlockerLandsBlocked", func(t *testing.T) {
			task, blocker := poisoned(t), e.tkSeed(t, domain.Ready, "", 0, nil)
			if _, err := e.c.Context().AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: blocker.ID, ToTaskID: task.ID}); err != nil {
				t.Fatal(err)
			}
			got, err := e.tkMove(t, master, e.get(t, task.ID), domain.Ready)
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
			_, err := e.tkMove(t, worker, task, domain.Ready)
			wantCode(t, err, domain.CodeForbidden) // DEVIATION above: the tier, not the poison
			got, err := e.tkMove(t, master, task, domain.Ready)
			if err != nil {
				t.Fatal(err)
			}
			if got.Task.Status != domain.Ready || got.Task.ClaimAttempts != 2 {
				t.Fatalf("dependency-blocked → %s attempts %v, want READY keeping its 2 attempts", got.Task.Status, got.Task.ClaimAttempts)
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
		first, second := e.tkSeed(t, domain.InProgress, "", 0, nil), e.tkSeed(t, domain.Ready, "", 0, nil)
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

// Promote-with-dependencies: a task with an open blocker moved to READY (or IN_PROGRESS) lands in
// the Blocked column; dropping the blocker's edge is then what frees it.
func TestMovePromoteWithDependenciesLandsBlocked(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		task, blocker := e.tkSeed(t, domain.Backlog, "", 0, nil), e.tkSeed(t, domain.Ready, "", 0, nil)
		if _, err := e.c.Context().AddDependency(e.ctx, e.alice, AddDependencyReq{ProjectID: e.project, FromTaskID: blocker.ID, ToTaskID: task.ID}); err != nil {
			t.Fatal(err)
		}
		got, err := e.tkMove(t, e.alice, e.get(t, task.ID), domain.Ready)
		if err != nil {
			t.Fatal(err)
		}
		if got.Task.Status != domain.Blocked || got.Task.ColumnID != e.column(t, domain.Blocked) {
			t.Fatalf("promoted with a blocker → %s in %s, want BLOCKED in the Blocked column", got.Task.Status, got.Task.ColumnID)
		}
	})
}

// A move to DONE clears its id from each dependent: the non-poison one goes READY with one
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
		if got := e.get(t, plain.ID); got.Status != domain.Ready || len(got.BlockedBy) != 0 || len(ofType(e.events(t, plain.ID), domain.EventTaskDependencyCleared)) != 1 {
			t.Fatalf("plain dependent %s blockedBy %v, want READY, [] and one dependency_cleared", got.Status, got.BlockedBy)
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
		task := e.tkSeed(t, domain.Ready, "", 0, nil)
		mv := func(req MoveTaskReq) error {
			req.ProjectID = cmpOr(req.ProjectID, e.project)
			_, err := e.c.Tasks().Move(e.ctx, e.alice, req)
			return err
		}
		wantCode(t, mv(MoveTaskReq{TaskID: task.ID, Version: task.Version + 1, ToStatus: domain.Backlog}), domain.CodeStale)
		wantCode(t, mv(MoveTaskReq{TaskID: "T-404", Version: 1, ToStatus: domain.Backlog}), domain.CodeNotFound)
		wantCode(t, mv(MoveTaskReq{TaskID: task.ID, Version: task.Version, ToColumnID: "C-404"}), domain.CodeNotFound)
		wantCode(t, mv(MoveTaskReq{TaskID: task.ID, Version: task.Version, Order: "!"}), domain.CodeValidation)
		wantCode(t, mv(MoveTaskReq{TaskID: task.ID, Version: task.Version}), domain.CodeValidation)
		wantCode(t, mv(MoveTaskReq{ProjectID: "P-other", TaskID: task.ID, Version: task.Version, ToStatus: domain.Backlog}), domain.CodeForbidden)

		now := time.Now()
		held, err := e.st.ClaimTask(e.ctx, e.project, task.ID, task.Version, store.ClaimInput{
			Owner: "bob", LeaseID: "L-1", ExpiresAt: now.Add(time.Minute).UnixMilli(), Now: now.UnixMilli(), ColumnID: e.column(t, domain.InProgress),
		})
		if err != nil {
			t.Fatal(err)
		}
		wantCode(t, mv(MoveTaskReq{TaskID: held.ID, Version: held.Version, ToStatus: domain.Review}), domain.CodeConflictLocked)

		racing := e.tkSeed(t, domain.Ready, "", 0, nil)
		on := true
		racing, err = e.st.UpdateTask(e.ctx, e.project, racing.ID, racing.Version, store.TaskPatch{Racing: &on, UpdatedBy: "alice", UpdatedAt: now.UnixMilli()})
		if err != nil {
			t.Fatal(err)
		}
		be := wantCode(t, mv(MoveTaskReq{TaskID: racing.ID, Version: racing.Version, ToStatus: domain.Backlog}), domain.CodeConflictLocked)
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
// the event and the READY broadcast; and its refusals.
func TestCreateTask(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		tk := e.c.Tasks()
		a, err := tk.Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "first", Labels: []string{"l"}, CustomFields: map[string]FieldValue{"n": 1.0, "s": "v", "b": true}, IdempotencyKey: "k"})
		if err != nil {
			t.Fatal(err)
		}
		if a.Task.Status != domain.Backlog || a.Task.ColumnID != e.column(t, domain.Backlog) || a.Task.Priority != domain.PriorityNormal || a.Task.CustomFields["n"] != 1.0 {
			t.Fatalf("create landed %+v, want BACKLOG in the first column, normal, its fields", a.Task)
		}
		again, err := tk.Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "first", IdempotencyKey: "k"})
		if err != nil || again.Task.ID != a.Task.ID {
			t.Fatalf("replay %v %s, want %s", err, again.Task.ID, a.Task.ID)
		}
		before := e.changeCount(t)
		b, err := tk.Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: "second", ColumnID: e.column(t, domain.Ready)})
		if err != nil {
			t.Fatal(err)
		}
		if b.Task.Status != domain.Ready || tkNum(t, b.Task.ID) != tkNum(t, a.Task.ID)+1 {
			t.Fatalf("second %s %s after %s, want READY and the next T-n", b.Task.ID, b.Task.Status, a.Task.ID)
		}
		if kinds := e.changeKinds(t, before); !slices.Equal(kinds, []string{DoorbellBroadcast + ":" + b.Task.ID}) {
			t.Fatalf("change rows %v, want one broadcast", kinds)
		}
		if n := len(ofType(e.events(t, b.Task.ID), domain.EventTaskCreated)); n != 1 {
			t.Fatalf("%d task.created events", n)
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
			"no sprint store":  {CreateTaskReq{Title: "x", SprintID: "S-1"}, domain.CodeNotFound},
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
	task := f.task(t, domain.Ready, nil)
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
		task := e.tkSeed(t, domain.Ready, "", 0, nil)
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

// update_task: the doorbell on a new assignee, the broadcast when a READY task loses it; STALE; the
// system-label and autoAssign refusals.
func TestUpdateTaskDoorbellsAndRefusals(t *testing.T) {
	onEngines(t, func(t *testing.T, e *env) {
		task := e.tkSeed(t, domain.Ready, "", 0, []string{domain.LabelPoisoned})
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
			task, err := e.c.Tasks().Create(e.ctx, e.alice, CreateTaskReq{ProjectID: e.project, BoardID: e.board.ID, Title: fmt.Sprint("t", i), ColumnID: e.column(t, domain.Ready), Labels: []string{fmt.Sprint("l", i%2)}})
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, task.Task.ID)
		}
		cur := e.get(t, ids[0])
		if _, err := e.c.Tasks().Update(e.ctx, e.alice, UpdateTaskReq{ProjectID: e.project, TaskID: ids[0], Version: cur.Version, Patch: TaskPatch{Assignee: Value(ann)}}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.c.Tasks().Move(e.ctx, e.alice, MoveTaskReq{ProjectID: e.project, TaskID: ids[1], Version: e.get(t, ids[1]).Version, ToStatus: domain.Backlog}); err != nil {
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
			"status":           {ListTasksReq{Status: domain.Backlog}, []string{ids[1]}},
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
	f.task(t, domain.Ready, nil)
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
