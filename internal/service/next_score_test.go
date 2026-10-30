package service

import (
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The P-258 tests: next_task {strategy: "score", explain} ranks with rank_tasks's scorer, and
// useProfile on next_task and claim_next judges eligibility by the caller's own registered profile.
// They run on memstore and on both engines (the Postgres leg skips without
// BATONDECK_TEST_POSTGRES_DSN).

// pTask seeds a TODO task of priority p requiring caps, due at due (nil for none), created now.
func (e *lcEnv) pTask(t *testing.T, title string, p domain.Priority, due *int64, caps ...string) store.Task {
	t.Helper()
	task, err := e.st.CreateTask(e.ctx, store.CreateTaskInput{
		ProjectID: e.project, BoardID: e.board.ID, ColumnID: e.column(t, domain.Todo), Title: title, Status: domain.Todo,
		Priority: p, RequiredCapabilities: caps, OrderKey: "a0", CreatedBy: "alice", CreatedAt: e.clock.now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if due != nil {
		if task, err = e.st.UpdateTask(e.ctx, e.project, task.ID, task.Version, store.TaskPatch{DueAt: &store.Nullable[int64]{Value: due}, UpdatedBy: "alice", UpdatedAt: task.CreatedAt}); err != nil {
			t.Fatal(err)
		}
	}
	return task
}

func taskID(task *Task) string {
	if task == nil {
		return "null"
	}
	return task.ID
}

// AC1: strategy score takes a normal task due now that unblocks five over an urgent one, which
// fifo takes; explain carries the factors and the runner-up (the urgent task); explain without
// strategy score is VALIDATION.
func TestNextTaskScores(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		urgent := e.pTask(t, "urgent", domain.PriorityUrgent, nil)
		now := e.clock.now().UnixMilli()
		due := e.pTask(t, "due now, unblocks five", domain.PriorityNormal, &now)
		for range 5 {
			e.dependOn(t, due.ID, e.pTask(t, "waits", domain.PriorityLow, nil).ID)
		}
		lc := e.c.Lifecycle()
		fifo, err := lc.Next(e.ctx, e.alice, NextTaskReq{ProjectID: e.project, BoardID: e.board.ID})
		if err != nil || taskID(fifo.Task) != urgent.ID {
			t.Fatalf("fifo: %s %v, want %s", taskID(fifo.Task), err, urgent.ID)
		}
		plain, err := lc.Next(e.ctx, e.alice, NextTaskReq{ProjectID: e.project, BoardID: e.board.ID, Strategy: "score"})
		if err != nil || taskID(plain.Task) != due.ID || plain.Explanation.Reason != "" || plain.Explanation.RunnerUp != nil {
			t.Fatalf("score: %s %+v %v, want %s and no explanation", taskID(plain.Task), plain.Explanation, err, due.ID)
		}
		got, err := lc.Next(e.ctx, e.alice, NextTaskReq{ProjectID: e.project, BoardID: e.board.ID, Strategy: "score", Explain: true})
		if err != nil {
			t.Fatal(err)
		}
		ex := got.Explanation
		t.Logf("fifo %s; score %s; explanation score %.4f reason %q factors %+v runnerUp %+v tier %q", taskID(fifo.Task), taskID(got.Task), ex.Score, ex.Reason, ex.Factors, ex.RunnerUp, ex.Tier)
		if taskID(got.Task) != due.ID || ex.Factors.Deadline.Value != 1 || ex.Factors.Readiness.Value != 0.5 || ex.Reason != "normal · unblocks 5 · due soon" {
			t.Fatalf("explanation %+v", ex)
		}
		if ex.RunnerUp == nil || ex.RunnerUp.TaskID != urgent.ID || ex.RunnerUp.Reason != "urgent" || ex.RunnerUp.Score >= ex.Score || ex.Tier != "" {
			t.Fatalf("runnerUp %+v (top %.4f), tier %q", ex.RunnerUp, ex.Score, ex.Tier)
		}
		_, err = lc.Next(e.ctx, e.alice, NextTaskReq{ProjectID: e.project, BoardID: e.board.ID, Explain: true})
		be := wantCode(t, err, domain.CodeValidation)
		_, err = lc.Next(e.ctx, e.alice, NextTaskReq{ProjectID: e.project, BoardID: e.board.ID, Strategy: "fifo", Explain: true})
		wantCode(t, err, domain.CodeValidation)
		t.Logf("explain without score: %s %q %v", be.Code, be.Message, be.Details)
	})
}

// AC2: with a registered profile (capabilities [go], tags [backend], the alias golang -> go),
// next_task and claim_next take the tasks it covers by alias and by tag and skip the urgent rust one
// fifo would take; under score, explain carries tier and reliability. A caller with no profile
// falls back to capabilities; a caller whose agent is another member's label is FORBIDDEN.
func TestUseProfile(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		e.settings(t, map[string]any{"capabilityAliases": map[string]string{"golang": "go"}})
		rust := e.pTask(t, "rust", domain.PriorityUrgent, nil, "rust")
		alias := e.pTask(t, "golang", domain.PriorityHigh, nil, "golang")
		tagged := e.pTask(t, "backend-api", domain.PriorityNormal, nil, "backend-api")
		if _, err := e.c.Profiles().Register(e.ctx, e.alice, RegisterAgentProfileReq{ProjectID: e.project, Capabilities: []string{"go"}, Tags: []string{"backend"}}); err != nil {
			t.Fatal(err)
		}
		lc := e.c.Lifecycle()
		q := NextTaskReq{ProjectID: e.project, BoardID: e.board.ID}
		fifo, err := lc.Next(e.ctx, e.alice, q)
		if err != nil || taskID(fifo.Task) != rust.ID {
			t.Fatalf("fifo: %s %v, want the urgent %s", taskID(fifo.Task), err, rust.ID)
		}
		q.UseProfile = true
		prof, err := lc.Next(e.ctx, e.alice, q)
		if err != nil || taskID(prof.Task) != alias.ID {
			t.Fatalf("next_task {useProfile}: %s %v, want %s", taskID(prof.Task), err, alias.ID)
		}
		// capabilities are the fallback only: a profile overrides them.
		q.Capabilities = []string{"rust"}
		if over, err := lc.Next(e.ctx, e.alice, q); err != nil || taskID(over.Task) != alias.ID {
			t.Fatalf("next_task {useProfile, capabilities [rust]}: %s %v, want the profile's %s", taskID(over.Task), err, alias.ID)
		}
		q.Capabilities, q.Strategy, q.Explain = nil, "score", true
		sc, err := lc.Next(e.ctx, e.alice, q)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("fifo %s; useProfile %s; score+profile %s tier %q reliability %v runnerUp %+v", taskID(fifo.Task), taskID(prof.Task), taskID(sc.Task), sc.Explanation.Tier, sc.Explanation.Reliability, sc.Explanation.RunnerUp)
		if taskID(sc.Task) != alias.ID || sc.Explanation.Tier != "synonym" || sc.Explanation.Reliability != 0.8 || sc.Explanation.RunnerUp == nil || sc.Explanation.RunnerUp.TaskID != tagged.ID {
			t.Fatalf("score with a profile: %s %+v", taskID(sc.Task), sc.Explanation)
		}

		// The profile, not capabilities [rust], judges claim_next too.
		cq := ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, UseProfile: true, Capabilities: []string{"rust"}}
		var won []string
		for range 3 {
			r, err := lc.ClaimNext(e.ctx, e.alice, cq)
			if err != nil {
				t.Fatal(err)
			}
			won = append(won, taskID(r.Task))
		}
		t.Logf("claim_next {useProfile} x3: %v", won)
		if won[0] != alias.ID || won[1] != tagged.ID || won[2] != "null" || e.get(t, rust.ID).Claim != nil {
			t.Fatalf("claim_next {useProfile}: %v, want [%s %s null] and %s unclaimed", won, alias.ID, tagged.ID, rust.ID)
		}

		// bob has no profile: useProfile falls back to capabilities.
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		if r, err := lc.Next(e.ctx, bob, NextTaskReq{ProjectID: e.project, BoardID: e.board.ID, UseProfile: true, Capabilities: []string{"go"}}); err != nil || r.Task != nil {
			t.Fatalf("no profile, capabilities [go]: %s %v, want null", taskID(r.Task), err)
		}
		r, err := lc.ClaimNext(e.ctx, bob, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, UseProfile: true, Capabilities: []string{"rust"}})
		if err != nil || taskID(r.Task) != rust.ID {
			t.Fatalf("no profile, capabilities [rust]: %s %v, want %s", taskID(r.Task), err, rust.ID)
		}

		// carol's token carries the label agent-b: bob's OAuth client named agent-b may not use it.
		e.member(t, e.project, "carol", domain.RoleWorker)
		now := e.clock.now().UnixMilli()
		if err := e.st.CreateToken(e.ctx, store.Token{ID: "tok-carol-b", IdentityID: "carol", Subject: "carol", SecretHash: "h-1", Label: "agent-b", CreatedAt: now, ExpiresAt: now + time.Hour.Milliseconds()}); err != nil {
			t.Fatal(err)
		}
		posing := bob
		posing.Agent, posing.Via = "agent-b", domain.ViaOAuth
		_, err = lc.Next(e.ctx, posing, NextTaskReq{ProjectID: e.project, BoardID: e.board.ID, UseProfile: true})
		be := wantCode(t, err, domain.CodeForbidden)
		_, err = lc.ClaimNext(e.ctx, posing, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, UseProfile: true})
		wantCode(t, err, domain.CodeForbidden)
		t.Logf("useProfile under another's label: %s %q", be.Code, be.Message)
	})
}
