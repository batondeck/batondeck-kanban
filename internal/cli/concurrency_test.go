package cli

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/auth"
)

// The audit of 2026-10-24 (A2-A6; glitches G1, G2, G3, G6, G7) as tests through the served /mcp on
// every engine: its repro scripts (skip.py, pgstorm.py, nul.py, conc.py) turned into Go.

// session opens another MCP session of a's identity, on a token of its own: one person running two
// Claude Code sessions.
func (j *joined) session(a *agent) *agent {
	j.t.Helper()
	ctx := context.Background()
	token, _, err := (&auth.TokenService{Store: j.st}).Create(ctx, a.id, "", "second session", time.Hour)
	if err != nil {
		j.t.Fatal(err)
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "join-" + a.login + "-2", Version: "1"}, nil).Connect(cctx,
		&sdk.StreamableClientTransport{Endpoint: j.url + "/mcp", HTTPClient: &http.Client{Transport: bearerRT{token}}, MaxRetries: -1}, nil)
	if err != nil {
		j.t.Fatalf("connect %s again: %v", a.login, err)
	}
	j.t.Cleanup(func() { _ = cs.Close() })
	return &agent{t: j.t, login: a.login, id: a.id, cs: cs}
}

// G1 (skip.py): per round a fresh board with ONE TODO task that three callers keep writing beside
// (comments, context items, task memory); 50 ms in, claim_next on the board must claim it. On
// Postgres the frontier's SKIP LOCKED passed over the row: a comment's foreign key check held it FOR
// KEY SHARE (2 of 20 rounds claimed), and a writer that bumps the task holds it locked from its load
// (rev68 B1: 1 of 20 under write_memory), so a miss is looked at again without SKIP LOCKED.
func TestClaimNextClaimsATaskBeingWritten(t *testing.T) {
	writers := []struct {
		tool string
		args map[string]any
	}{
		{"add_comment", map[string]any{"body": "x"}},
		{"add_context_item", map[string]any{"kind": "note", "body": "x"}},
		{"write_memory", map[string]any{"scope": "task", "key": "k", "value": 1}},
	}
	onJoined(t, func(t *testing.T, j *joined) {
		owner := j.user("ada")
		spammers := []*agent{j.session(owner), j.session(owner), j.session(owner)}
		const rounds = 20
		for _, w := range writers {
			claimed, writes := 0, 0
			var mu sync.Mutex
			for r := range rounds {
				b := j.board(owner, nil, nil)
				id := asStr(b.task(fmt.Sprintf("only %d", r))["id"])
				stop := make(chan struct{})
				var wg sync.WaitGroup
				for _, s := range spammers {
					wg.Go(func() {
						for {
							select {
							case <-stop:
								return
							default:
							}
							if _, e := s.try(w.tool, with(map[string]any{"projectId": b.project, "taskId": id}, w.args)); e != nil {
								t.Errorf("%s: %+v", w.tool, e)
								return
							}
							mu.Lock()
							writes++
							mu.Unlock()
						}
					})
				}
				time.Sleep(50 * time.Millisecond)
				out, e := owner.try("claim_next", map[string]any{"projectId": b.project, "boardId": b.bid})
				close(stop)
				wg.Wait()
				switch got := asStr(asMap(out["task"])["id"]); {
				case e != nil:
					t.Errorf("round %d: claim_next: %+v", r, e)
				case got == id:
					claimed++
				default:
					t.Logf("round %d: claim_next answered task %q while %s was TODO", r, got, id)
				}
			}
			t.Logf("%s: claim_next claimed the task under %s in %d/%d rounds (%d writes landed beside it)", j.engine, w.tool, claimed, rounds, writes)
			if claimed != rounds {
				t.Errorf("under %s: claimed %d/%d rounds, want every one", w.tool, claimed, rounds)
			}
		}
	})
}

// G2 (pgstorm.py): four writers loop add_comment, add_context_item, write_memory and add_follow_up
// on one task while three loop get_task + update_task on it. None of the first four carries a
// version, so each must land; update_task may lose to them (STALE) and nothing may be INTERNAL. On
// Postgres two writers that each inserted a child row and then locked the task deadlocked, and a
// bump that lost a version race was answered INTERNAL.
func TestUnversionedWritesToOneTaskNeverDeadlockOrAnswerInternal(t *testing.T) {
	onJoined(t, func(t *testing.T, j *joined) {
		owner := j.user("ada")
		b := j.board(owner, nil, nil)
		id := asStr(b.task("hot")["id"])
		var mu sync.Mutex
		res := map[string]int{}
		note := func(tool string, e *toolErr) {
			code := "OK"
			if e != nil {
				code = e.Code
			}
			mu.Lock()
			res[tool+" "+code]++
			mu.Unlock()
		}
		calls := [][2]any{
			{"add_comment", map[string]any{"body": "x"}},
			{"add_context_item", map[string]any{"kind": "note", "body": "x"}},
			{"write_memory", map[string]any{"scope": "task", "key": "k", "value": 1}},
			{"add_follow_up", map[string]any{"body": "f"}},
		}
		stop := time.Now().Add(4 * time.Second)
		var wg sync.WaitGroup
		for range 4 {
			s := j.session(owner)
			wg.Go(func() {
				for time.Now().Before(stop) {
					for _, c := range calls {
						_, e := s.try(c[0].(string), with(map[string]any{"projectId": b.project, "taskId": id}, c[1].(map[string]any)))
						note(c[0].(string), e)
					}
				}
			})
		}
		for range 3 {
			s := j.session(owner)
			wg.Go(func() {
				for n := 0; time.Now().Before(stop); n++ {
					got, e := s.try("get_task", map[string]any{"projectId": b.project, "taskId": id})
					if e != nil {
						note("get_task", e)
						continue
					}
					_, e = s.try("update_task", map[string]any{"projectId": b.project, "taskId": id, "version": asMap(got["task"])["version"],
						"patch": map[string]any{"title": fmt.Sprintf("hot %d", n)}})
					note("update_task", e)
				}
			})
		}
		wg.Wait()
		t.Logf("%s: %v", j.engine, res)
		for k, n := range res {
			tool, code, _ := strings.Cut(k, " ")
			if code != "OK" && (tool != "update_task" || code != "STALE") {
				t.Errorf("%s answered %s %d times; want OK (update_task: OK or STALE)", tool, code, n)
			}
		}
		for _, c := range calls {
			if res[c[0].(string)+" OK"] == 0 {
				t.Errorf("%s never landed: the storm proves nothing about it", c[0])
			}
		}
	})
}

// G2's other half: add_follow_up {reopen} carries no version and writes the task after its own row
// (the follow-up, whose foreign key check holds the task FOR KEY SHARE). Several at once on a task in
// REVIEW: each lands, exactly one reopens it, and the others, having lost the version race to it,
// are judged again rather than answered STALE or INTERNAL (a deadlock, under FOR UPDATE).
func TestConcurrentReopeningFollowUpsAllLand(t *testing.T) {
	onJoined(t, func(t *testing.T, j *joined) {
		owner := j.user("ada")
		b := j.board(owner, nil, nil)
		sessions := []*agent{owner, j.session(owner), j.session(owner), j.session(owner)}
		const rounds = 10
		for r := range rounds {
			id := asStr(b.task(fmt.Sprintf("r%d", r))["id"])
			owner.call("complete_task", map[string]any{"projectId": b.project, "taskId": id, "leaseId": b.claim(owner, id), "deliverable": "d"})
			reopened := make([]bool, len(sessions))
			errs := make([]*toolErr, len(sessions))
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i, s := range sessions {
				wg.Go(func() {
					<-start
					out, e := s.try("add_follow_up", map[string]any{"projectId": b.project, "taskId": id, "body": "again", "reopen": true})
					errs[i] = e
					reopened[i] = e == nil && asMap(out["followUp"])["reopened"] == true
				})
			}
			close(start)
			wg.Wait()
			n := 0
			for i := range sessions {
				if errs[i] != nil {
					t.Errorf("round %d session %d: add_follow_up: %+v", r, i, errs[i])
				}
				if reopened[i] {
					n++
				}
			}
			if got := b.get(id)["status"]; n != 1 || got != "TODO" {
				t.Errorf("round %d: %d follow-ups reopened the task, now %v; want exactly 1 and TODO", r, n, got)
			}
		}
		t.Logf("%s: %d rounds of %d concurrent reopening follow-ups", j.engine, rounds, len(sessions))
	})
}

// G3 (nul.py): a NUL character (U+0000) in any text argument is VALIDATION on every engine. Postgres
// cannot store it and answered INTERNAL (retryable) on each of these; SQLite stored it.
func TestNULInTextIsValidationOnEveryEngine(t *testing.T) {
	onJoined(t, func(t *testing.T, j *joined) {
		const nul = "a\u0000b"
		owner := j.user("ada")
		b := j.board(owner, nil, nil)
		id := asStr(b.task("t")["id"])
		lease := b.claim(owner, id)
		cases := []struct {
			tool, field string
			args        map[string]any
		}{
			{"create_project", "name", map[string]any{}},
			{"create_board", "name", map[string]any{"projectId": b.project}},
			{"create_task", "title", map[string]any{"projectId": b.project, "boardId": b.bid}},
			{"create_task", "description", map[string]any{"projectId": b.project, "boardId": b.bid, "title": "t"}},
			{"add_comment", "body", map[string]any{"projectId": b.project, "taskId": id}},
			{"add_context_item", "body", map[string]any{"projectId": b.project, "taskId": id, "kind": "note"}},
			{"write_memory", "key", map[string]any{"projectId": b.project, "taskId": id, "scope": "task", "value": 1}},
			{"add_follow_up", "body", map[string]any{"projectId": b.project, "taskId": id}},
			{"complete_task", "deliverable", map[string]any{"projectId": b.project, "taskId": id, "leaseId": lease}},
		}
		for _, c := range cases {
			_, e := owner.try(c.tool, with(c.args, map[string]any{c.field: nul}))
			if e == nil || e.Code != "VALIDATION" || e.Details["field"] != c.field || e.Retryable {
				t.Errorf("%s.%s with a NUL: %+v, want VALIDATION naming the field, not retryable", c.tool, c.field, e)
				continue
			}
			t.Logf("%s: %s.%s = %q → %s %q", j.engine, c.tool, c.field, nul, e.Code, e.Message)
		}
		// Nested: a label inside the patch, and a key of customFields.
		e := owner.refused("update_task", map[string]any{"projectId": b.project, "taskId": id, "version": b.get(id)["version"],
			"patch": map[string]any{"labels": []any{"ok", nul}}}, "VALIDATION")
		if e.Details["field"] != "patch.labels[1]" {
			t.Errorf("a NUL label: %+v, want the field patch.labels[1]", e)
		}
		// Positive control: each call with the NUL taken out lands.
		for _, c := range cases {
			owner.call(c.tool, with(c.args, map[string]any{c.field: "ab"}))
		}
		// The REST BFF routes through the same check.
		status, body := j.api(j.login("ada"), "POST", "/api/projects/"+b.project+"/tasks/"+id+"/comments", `{"body":"a\u0000b"}`)
		if status != http.StatusBadRequest || !strings.Contains(body, `"VALIDATION"`) {
			t.Fatalf("REST comment with a NUL: %d %s, want 400 VALIDATION", status, body)
		}
	})
}

// G6 (conc.py): one identity in two sessions claiming one task at once never holds two leases: the
// second claim answers the lease the first minted, and that lease stays good. Another identity is
// still refused CONFLICT_LOCKED (the control that exclusivity holds), and a lease that has expired
// is replaced by a fresh one.
func TestOneIdentityTwoSessionsHoldOneLease(t *testing.T) {
	onJoined(t, func(t *testing.T, j *joined) {
		owner, bob := j.user("ada"), j.user("bob")
		b := j.board(owner, nil, map[*agent]string{bob: "worker"})
		sessions := []*agent{owner, j.session(owner), j.session(owner), j.session(owner)}
		const rounds = 20
		twoLeases := 0
		for r := range rounds {
			id := asStr(b.task(fmt.Sprintf("c%d", r))["id"])
			leases := make([]string, len(sessions))
			errs := make([]*toolErr, len(sessions))
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i, s := range sessions {
				wg.Go(func() {
					<-start
					out, e := s.try("claim_task", map[string]any{"projectId": b.project, "taskId": id, "leaseSeconds": 3600})
					leases[i], errs[i] = asStr(out["leaseId"]), e
				})
			}
			close(start)
			wg.Wait()
			distinct := map[string]bool{}
			for i, l := range leases {
				if errs[i] != nil {
					t.Errorf("round %d session %d: %+v", r, i, errs[i])
					continue
				}
				distinct[l] = true
			}
			if len(distinct) > 1 {
				twoLeases++
				continue
			}
			for l := range distinct {
				owner.call("heartbeat_task", map[string]any{"projectId": b.project, "taskId": id, "leaseId": l})
			}
			e := bob.refused("claim_task", map[string]any{"projectId": b.project, "taskId": id}, "CONFLICT_LOCKED")
			if e.Details["owner"] != owner.id {
				t.Errorf("bob's refusal names %v, want %s", e.Details["owner"], owner.id)
			}
		}
		t.Logf("%s: %d sessions of one identity x %d rounds: %d rounds held more than one lease", j.engine, len(sessions), rounds, twoLeases)
		if twoLeases != 0 {
			t.Fatalf("%d of %d rounds minted more than one lease", twoLeases, rounds)
		}
		// rev68 S4: the second claim's leaseSeconds lengthens the shared lease and never shortens it.
		long := asStr(b.task("long")["id"])
		short := owner.call("claim_task", map[string]any{"projectId": b.project, "taskId": long, "leaseSeconds": 60})
		joinedLease := sessions[1].call("claim_task", map[string]any{"projectId": b.project, "taskId": long, "leaseSeconds": 3600})
		again := sessions[2].call("claim_task", map[string]any{"projectId": b.project, "taskId": long, "leaseSeconds": 60})
		exp := func(m map[string]any) time.Time {
			at, _ := time.Parse(time.RFC3339Nano, asStr(asMap(asMap(m["task"])["claim"])["expiresAt"]))
			return at
		}
		if joinedLease["leaseId"] != short["leaseId"] || again["leaseId"] != short["leaseId"] ||
			time.Until(exp(joinedLease)) < 3500*time.Second || !exp(again).Equal(exp(joinedLease)) {
			t.Fatalf("claims of 60, 3600, 60 s: expiries %v, %v, %v, leases %v %v %v; want one lease, lengthened to 3600 s and kept",
				exp(short), exp(joinedLease), exp(again), short["leaseId"], joinedLease["leaseId"], again["leaseId"])
		}
		// Control: a lease that has expired is not handed back; the claim mints a new one.
		id := asStr(b.task("short")["id"])
		first := asStr(owner.call("claim_task", map[string]any{"projectId": b.project, "taskId": id, "leaseSeconds": 1})["leaseId"])
		time.Sleep(1100 * time.Millisecond)
		second := asStr(owner.call("claim_task", map[string]any{"projectId": b.project, "taskId": id})["leaseId"])
		if second == "" || second == first {
			t.Fatalf("re-claim after expiry: %q, want a lease other than %q", second, first)
		}
	})
}

// G7: heartbeat_task with no leaseSeconds keeps a longer lease (it cut a 3600 s claim to 300 s);
// an explicit leaseSeconds still sets the expiry, shorter included.
func TestHeartbeatWithoutLeaseSecondsKeepsTheLease(t *testing.T) {
	onJoined(t, func(t *testing.T, j *joined) {
		owner := j.user("ada")
		b := j.board(owner, nil, nil)
		id := asStr(b.task("long")["id"])
		claim := owner.call("claim_task", map[string]any{"projectId": b.project, "taskId": id, "leaseSeconds": 3600})
		lease := asStr(claim["leaseId"])
		claimed := asStr(asMap(asMap(claim["task"])["claim"])["expiresAt"])
		hb := asStr(owner.call("heartbeat_task", map[string]any{"projectId": b.project, "taskId": id, "leaseId": lease})["expiresAt"])
		if hb != claimed {
			t.Fatalf("heartbeat without leaseSeconds moved a 3600 s lease from %s to %s", claimed, hb)
		}
		short := asStr(owner.call("heartbeat_task", map[string]any{"projectId": b.project, "taskId": id, "leaseId": lease, "leaseSeconds": 60})["expiresAt"])
		at, err := time.Parse(time.RFC3339Nano, short)
		if err != nil || time.Until(at) > 61*time.Second || time.Until(at) < 30*time.Second {
			t.Fatalf("heartbeat leaseSeconds 60: %s (%v), want about now + 60 s", short, err)
		}
		// From a 60 s lease, the default 300 s lengthens it.
		def := asStr(owner.call("heartbeat_task", map[string]any{"projectId": b.project, "taskId": id, "leaseId": lease})["expiresAt"])
		if at2, err := time.Parse(time.RFC3339Nano, def); err != nil || time.Until(at2) < 290*time.Second {
			t.Fatalf("heartbeat without leaseSeconds from a 60 s lease: %s (%v), want about now + 300 s", def, err)
		}
		t.Logf("%s: claim 3600 s → %s; heartbeat {} → %s; leaseSeconds 60 → %s; then {} → %s", j.engine, claimed, hb, short, def)
	})
}
