package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/bus"
	"github.com/batondeck/batondeck-kanban/internal/service/servicetest"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The join's proofs (R1.18; plan §Verification "State machine", "delete_task", "Race"): the serve
// composition (compose, then serveUntil) over each engine servicetest selects, driven by
// real go-sdk clients holding real API tokens. They live in the composition root because only it may
// reach the store a run is seeded through and the change log a doorbell is read from (ADR-0003 §C
// row 6), and because only here is /mcp beside /api/stream on one mux.

// joined is compose over one engine, served by serveUntil (the session directory, the board
// snapshot cache's tap and the HTTP server, as serve starts them) on a loopback listener.
type joined struct {
	t      *testing.T
	engine string
	st     store.Store
	a      *app
	url    string
}

func join(t *testing.T, engine string) *joined {
	t.Helper()
	return joinStore(t, engine, servicetest.OpenStore(t, engine))
}

// joinStore is join over a store the caller opened (to read its rows back on its own connection).
func joinStore(t *testing.T, engine string, st store.Store) *joined {
	t.Helper()
	a := mustCompose(t, st, "", log.New(io.Discard, "", 0))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveUntil(ctx, []net.Listener{ln}, a, "x", log.New(io.Discard, "", 0)) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serveUntil: %v", err)
		}
	})
	url := "http://" + ln.Addr().String()
	for deadline := time.Now().Add(5 * time.Second); ; {
		if resp, err := http.Get(url + "/healthz"); err == nil {
			resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("serveUntil never answered /healthz")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return &joined{t: t, engine: engine, st: st, a: a, url: url}
}

// onJoined runs fn once per selected engine over a fresh composition.
func onJoined(t *testing.T, fn func(t *testing.T, j *joined)) {
	for _, e := range servicetest.Engines(t) {
		t.Run(e, func(t *testing.T) { fn(t, join(t, e)) })
	}
}

const joinPassword = "join password"

// agent is one signed-up user: its identity, an API token and an MCP session with it.
type agent struct {
	t     *testing.T
	login string
	id    string
	cs    *sdk.ClientSession
}

// user adds login with a password and an API token, and opens its MCP session.
func (j *joined) user(login string) *agent { j.t.Helper(); return j.account(login, false) }

// account is user, a workspace admin when admin is set.
func (j *joined) account(login string, admin bool) *agent {
	j.t.Helper()
	ctx := context.Background()
	u, err := (&auth.Accounts{Store: j.st, Params: auth.Argon2Params{Time: 1, Memory: 64, Threads: 1}}).Add(ctx, login, login, joinPassword, admin)
	if err != nil {
		j.t.Fatal(err)
	}
	token, _, err := (&auth.TokenService{Store: j.st}).Create(ctx, u.ID, "", "join", time.Hour)
	if err != nil {
		j.t.Fatal(err)
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "join-" + login, Version: "1"}, nil)
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cs, err := client.Connect(cctx, &sdk.StreamableClientTransport{Endpoint: j.url + "/mcp",
		HTTPClient: &http.Client{Transport: bearerRT{token}}, MaxRetries: -1}, nil)
	if err != nil {
		j.t.Fatalf("connect %s: %v", login, err)
	}
	j.t.Cleanup(func() { _ = cs.Close() })
	return &agent{t: j.t, login: login, id: u.ID, cs: cs}
}

// toolErr is a tool's structured error (contract/errors.json's shape).
type toolErr struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details"`
	Retryable bool           `json:"retryable"`
}

// try calls tool and answers its structured content, or its error.
func (a *agent) try(tool string, args map[string]any) (map[string]any, *toolErr) {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := a.cs.CallTool(ctx, &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		a.t.Fatalf("%s as %s: %v", tool, a.login, err)
	}
	if res.IsError {
		var e toolErr
		for _, c := range res.Content {
			if tc, ok := c.(*sdk.TextContent); ok {
				_ = json.Unmarshal([]byte(tc.Text), &e)
			}
		}
		if e.Code == "" {
			a.t.Fatalf("%s as %s: an error with no code: %+v", tool, a.login, res.Content)
		}
		return nil, &e
	}
	out, _ := res.StructuredContent.(map[string]any)
	return out, nil
}

func (a *agent) call(tool string, args map[string]any) map[string]any {
	a.t.Helper()
	out, e := a.try(tool, args)
	if e != nil {
		a.t.Fatalf("%s %v as %s: %s %s %v", tool, args, a.login, e.Code, e.Message, e.Details)
	}
	return out
}

func (a *agent) refused(tool string, args map[string]any, code string) *toolErr {
	a.t.Helper()
	_, e := a.try(tool, args)
	if e == nil || e.Code != code {
		a.t.Fatalf("%s %v as %s: %+v, want %s", tool, args, a.login, e, code)
	}
	return e
}

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func asStr(v any) string         { s, _ := v.(string); return s }

// board is a project the owner created (so its admin) with one board, and its members.
type board struct {
	j            *joined
	owner        *agent
	project, bid string
}

func (j *joined) board(owner *agent, settings map[string]any, members map[*agent]string) *board {
	j.t.Helper()
	p := asStr(owner.call("create_project", map[string]any{"name": "Join"})["projectId"])
	b := asStr(asMap(owner.call("create_board", map[string]any{"projectId": p, "name": "Main"})["board"])["id"])
	if settings != nil {
		owner.call("update_settings", with(map[string]any{"projectId": p}, settings))
	}
	for m, role := range members {
		owner.call("add_member", map[string]any{"projectId": p, "identityId": m.id, "role": role})
	}
	return &board{j: j, owner: owner, project: p, bid: b}
}

func with(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func (b *board) task(title string) map[string]any {
	b.j.t.Helper()
	return asMap(b.owner.call("create_task", map[string]any{"projectId": b.project, "boardId": b.bid, "title": title})["task"])
}

func (b *board) get(id string) map[string]any {
	b.j.t.Helper()
	return asMap(b.owner.call("get_task", map[string]any{"projectId": b.project, "taskId": id})["task"])
}

func (b *board) tryGet(id string) *toolErr {
	b.j.t.Helper()
	_, e := b.owner.try("get_task", map[string]any{"projectId": b.project, "taskId": id})
	return e
}

func (b *board) move(who *agent, id, to string) (map[string]any, *toolErr) {
	b.j.t.Helper()
	return who.try("move_task", map[string]any{"projectId": b.project, "taskId": id, "version": b.get(id)["version"], "toStatus": to})
}

// claim is who's lease on id.
func (b *board) claim(who *agent, id string) string {
	b.j.t.Helper()
	return asStr(who.call("claim_task", map[string]any{"projectId": b.project, "taskId": id})["leaseId"])
}

// The fixed table (ADR-0005), written out rather than read from internal/domain, so an edge added
// to the code's table fails here: the legal targets of each status, the same status first.
var joinLegal = map[string][]string{
	"TODO":        {"TODO", "IN_PROGRESS"},
	"IN_PROGRESS": {"IN_PROGRESS", "REVIEW", "BLOCKED", "TODO"},
	"BLOCKED":     {"BLOCKED", "IN_PROGRESS", "TODO"},
	"REVIEW":      {"REVIEW", "DONE", "IN_PROGRESS"},
	"DONE":        {"DONE", "IN_PROGRESS"},
}

// joinPath is the shortest legal path of each illegal pair.
var joinPath = map[[2]string][]string{
	{"TODO", "REVIEW"}:      {"TODO", "IN_PROGRESS", "REVIEW"},
	{"TODO", "BLOCKED"}:     {"TODO", "IN_PROGRESS", "BLOCKED"},
	{"TODO", "DONE"}:        {"TODO", "IN_PROGRESS", "REVIEW", "DONE"},
	{"IN_PROGRESS", "DONE"}: {"IN_PROGRESS", "REVIEW", "DONE"},
	{"BLOCKED", "REVIEW"}:   {"BLOCKED", "IN_PROGRESS", "REVIEW"},
	{"BLOCKED", "DONE"}:     {"BLOCKED", "IN_PROGRESS", "REVIEW", "DONE"},
	{"REVIEW", "TODO"}:      {"REVIEW", "IN_PROGRESS", "TODO"},
	{"REVIEW", "BLOCKED"}:   {"REVIEW", "IN_PROGRESS", "BLOCKED"},
	{"DONE", "TODO"}:        {"DONE", "IN_PROGRESS", "TODO"},
	{"DONE", "BLOCKED"}:     {"DONE", "IN_PROGRESS", "BLOCKED"},
	{"DONE", "REVIEW"}:      {"DONE", "IN_PROGRESS", "REVIEW"},
}

// joinReach is the legal moves that take a new (TODO) task to each status.
var joinReach = map[string][]string{
	"TODO":        nil,
	"IN_PROGRESS": {"IN_PROGRESS"},
	"BLOCKED":     {"IN_PROGRESS", "BLOCKED"},
	"REVIEW":      {"IN_PROGRESS", "REVIEW"},
	"DONE":        {"IN_PROGRESS", "REVIEW", "DONE"},
}

var joinStatuses = []string{"TODO", "IN_PROGRESS", "BLOCKED", "REVIEW", "DONE"}

func strs(v any) []string {
	var out []string
	for _, x := range asSlice(v) {
		out = append(out, asStr(x))
	}
	return out
}

func asSlice(v any) []any { s, _ := v.([]any); return s }

// TestJoinMoveVerdicts: every (from, to) pair of the five statuses through a real client's
// move_task: 14 legal (5 same-status) land on to; 11 are INVALID_TRANSITION carrying the allowed
// next statuses and the shortest path; CANCELLED and DEAD_LETTER are VALIDATION. The table is
// logged, one line per pair.
func TestJoinMoveVerdicts(t *testing.T) {
	onJoined(t, func(t *testing.T, j *joined) {
		b := j.board(j.user("ada"), nil, nil)
		var legal, same, illegal int
		var table []string
		for _, from := range joinStatuses {
			for _, to := range joinStatuses {
				id := asStr(b.task(from + "→" + to)["id"])
				for _, step := range joinReach[from] {
					if _, e := b.move(b.owner, id, step); e != nil {
						t.Fatalf("reaching %s: move to %s: %+v", from, step, e)
					}
				}
				got, e := b.move(b.owner, id, to)
				if slices.Contains(joinLegal[from], to) {
					if e != nil || asStr(asMap(got["task"])["status"]) != to {
						t.Fatalf("%s → %s: %+v %v, want a legal move", from, to, e, got)
					}
					legal++
					if from == to {
						same++
					}
					table = append(table, fmt.Sprintf("%s → %s: legal", from, to))
					continue
				}
				if e == nil || e.Code != "INVALID_TRANSITION" {
					t.Fatalf("%s → %s: %+v %v, want INVALID_TRANSITION", from, to, e, got)
				}
				allowed, path := strs(e.Details["allowed"]), strs(e.Details["path"])
				want := slices.DeleteFunc(slices.Clone(joinLegal[from]), func(s string) bool { return s == from })
				if !sameSet(allowed, want) || !slices.Equal(path, joinPath[[2]string{from, to}]) {
					t.Fatalf("%s → %s: allowed %v path %v, want %v and %v", from, to, allowed, path, want, joinPath[[2]string{from, to}])
				}
				if after := b.get(id); asStr(after["status"]) != from {
					t.Fatalf("%s → %s refused, yet the task is %s", from, to, after["status"])
				}
				illegal++
				table = append(table, fmt.Sprintf("%s → %s: INVALID_TRANSITION allowed=%v path=%v", from, to, allowed, path))
			}
		}
		id := asStr(b.task("gone states")["id"])
		for _, s := range []string{"CANCELLED", "DEAD_LETTER"} {
			if _, e := b.move(b.owner, id, s); e == nil || e.Code != "VALIDATION" {
				t.Fatalf("move to %s: %+v, want VALIDATION", s, e)
			}
		}
		t.Logf("%s: 25 move_task verdicts through a real client:\n  %s", j.engine, strings.Join(table, "\n  "))
		t.Logf("%s: %d legal (%d same-status), %d INVALID_TRANSITION with allowed and path; CANCELLED and DEAD_LETTER VALIDATION", j.engine, legal, same, illegal)
		if legal != 14 || same != 5 || illegal != 11 {
			t.Fatalf("%d legal (%d same), %d illegal; want 14 (5), 11", legal, same, illegal)
		}
	})
}

func sameSet(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	slices.Sort(x)
	slices.Sort(y)
	return slices.Equal(x, y)
}

// retry3 poisons at the third failure; the 1 ms backoff lets the next claim follow at once.
var retry3 = map[string]any{"retryPolicy": map[string]any{"baseDelayMs": 1, "maxDelayMs": 1, "jitter": 0, "poisonAfterAttempts": 3}, "reviewEnabled": false}

// poison fails id three times as who: BLOCKED, reason poison, claimAttempts 3.
func (b *board) poison(who *agent, id string) {
	b.j.t.Helper()
	for i := 1; i <= 3; i++ {
		time.Sleep(5 * time.Millisecond) // past the 1 ms backoff
		lease := b.claim(who, id)
		who.call("fail_task", map[string]any{"projectId": b.project, "taskId": id, "leaseId": lease, "reason": fmt.Sprintf("failure %d", i)})
	}
	if got := b.get(id); !poisoned(got) || got["claimAttempts"] != 3.0 {
		b.j.t.Fatalf("after three failures %s is %v labels %v attempts %v, want BLOCKED poisoned 3", id, got["status"], got["labels"], got["claimAttempts"])
	}
}

// poisoned reports a task BLOCKED with the poisoned label: the wire form of blockedReason poison,
// which the Task contract does not carry.
func poisoned(task map[string]any) bool {
	return task["status"] == "BLOCKED" && slices.Contains(strs(task["labels"]), "poisoned")
}

// TestJoinPoison: fail_task ×3 poisons the task; claim_next skips it for the other TODO task; a
// worker's move_task → TODO is FORBIDDEN, a master's lands TODO with claimAttempts 0. Then the
// last-dependency refusal and its control: when a blocker completes, its poisoned dependent stays
// BLOCKED poison while its plain blocked dependent goes TODO.
func TestJoinPoison(t *testing.T) {
	onJoined(t, func(t *testing.T, j *joined) {
		owner, mia, will := j.user("ada"), j.user("mia"), j.user("will")
		b := j.board(owner, retry3, map[*agent]string{mia: "master", will: "worker"})
		p, other := asStr(b.task("poisoned")["id"]), asStr(b.task("other")["id"])
		b.poison(will, p)
		next := will.call("claim_next", map[string]any{"projectId": b.project, "boardId": b.bid})
		if got := asStr(asMap(next["task"])["id"]); got != other {
			t.Fatalf("claim_next took %q, want %s (the poisoned %s is not claimable)", got, other, p)
		}
		if e := will.refused("move_task", map[string]any{"projectId": b.project, "taskId": p, "version": b.get(p)["version"], "toStatus": "TODO"}, "FORBIDDEN"); e != nil {
			t.Logf("%s: a worker's move_task of poisoned %s → TODO: %s %q", j.engine, p, e.Code, e.Message)
		}
		if got, e := b.move(mia, p, "TODO"); e != nil || asMap(got["task"])["status"] != "TODO" {
			t.Fatalf("master's un-poison: %+v %v", e, got)
		}
		if got := b.get(p); got["claimAttempts"] != nil || slices.Contains(strs(got["labels"]), "poisoned") {
			t.Fatalf("un-poisoned %s: attempts %v labels %v, want 0 (omitted on the wire) and no poisoned label", p, got["claimAttempts"], got["labels"])
		}
		t.Logf("%s: fail_task ×3 → BLOCKED poison attempts 3; claim_next took %s, not %s; worker FORBIDDEN; master → TODO attempts 0", j.engine, other, p)

		// The last dependency: blocker B; plain D blocked on B (block_task writes the edge); poisoned
		// P depending on B. B completes (reviewEnabled off: DONE).
		blocker, plain, victim := asStr(b.task("B")["id"]), asStr(b.task("D")["id"]), asStr(b.task("P")["id"])
		lease := b.claim(will, plain)
		will.call("block_task", map[string]any{"projectId": b.project, "taskId": plain, "leaseId": lease, "reason": "needs B", "blockedBy": []any{blocker}})
		b.poison(will, victim)
		owner.call("add_dependency", map[string]any{"projectId": b.project, "fromTaskId": blocker, "toTaskId": victim})
		lease = b.claim(will, blocker)
		if done := will.call("complete_task", map[string]any{"projectId": b.project, "taskId": blocker, "leaseId": lease}); asMap(done["task"])["status"] != "DONE" {
			t.Fatalf("complete B: %v, want DONE", done)
		}
		d, pp := b.get(plain), b.get(victim)
		if d["status"] != "TODO" || len(asSlice(d["blockedBy"])) != 0 {
			t.Fatalf("control: plain dependent %v blockedBy %v, want TODO []", d["status"], d["blockedBy"])
		}
		if !poisoned(pp) {
			t.Fatalf("refusal: poisoned dependent %v labels %v, want BLOCKED poisoned", pp["status"], pp["labels"])
		}
		t.Logf("%s: B done → plain %s TODO blockedBy %v (control); poisoned %s stays %v labels %v blockedBy %v (refusal)", j.engine, plain, d["blockedBy"], victim, pp["status"], pp["labels"], pp["blockedBy"])
	})
}

// feedDeleted counts the board feed's task.deleted events naming id, read as a client reads the
// board-feed resource.
func (b *board) feedDeleted(id string) int {
	b.j.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := b.owner.cs.ReadResource(ctx, &sdk.ReadResourceParams{URI: "batondeck://" + b.project + "/board/" + b.bid + "/feed"})
	if err != nil || len(res.Contents) != 1 {
		b.j.t.Fatalf("read the feed: %+v %v", res, err)
	}
	var feed struct {
		Events []struct {
			Type string         `json:"type"`
			Data map[string]any `json:"data"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(res.Contents[0].Text), &feed); err != nil {
		b.j.t.Fatal(err)
	}
	n := 0
	for _, e := range feed.Events {
		if e.Type == "task.deleted" && e.Data["id"] == id {
			n++
		}
	}
	return n
}

func (b *board) del(who *agent, id string) (map[string]any, *toolErr) {
	b.j.t.Helper()
	return who.try("delete_task", map[string]any{"projectId": b.project, "taskId": id, "version": b.get(id)["version"]})
}

// TestJoinDeleteTask: the eight delete_task cases of plan §Verification through a real client.
func TestJoinDeleteTask(t *testing.T) {
	onJoined(t, func(t *testing.T, j *joined) {
		owner, will := j.user("ada"), j.user("will")
		b := j.board(owner, nil, map[*agent]string{will: "worker"})
		var cases []string

		// 1. Deleting a blocker auto-unblocks its dependent.
		blocker, dep := asStr(b.task("blocker")["id"]), asStr(b.task("dependent")["id"])
		lease := b.claim(will, dep)
		will.call("block_task", map[string]any{"projectId": b.project, "taskId": dep, "leaseId": lease, "reason": "waits", "blockedBy": []any{blocker}})
		if _, e := b.del(owner, blocker); e != nil {
			t.Fatalf("delete blocker: %+v", e)
		}
		if d := b.get(dep); d["status"] != "TODO" || len(asSlice(d["blockedBy"])) != 0 {
			t.Fatalf("1: dependent %v blockedBy %v, want TODO []", d["status"], d["blockedBy"])
		}
		cases = append(cases, "1 blocker deleted → dependent "+dep+" TODO, blockedBy []")

		// 2. Another identity's live lease: CONFLICT_LOCKED, the task kept.
		leased := asStr(b.task("leased")["id"])
		b.claim(will, leased)
		if _, e := b.del(owner, leased); e == nil || e.Code != "CONFLICT_LOCKED" {
			t.Fatalf("2: delete under will's lease: %+v, want CONFLICT_LOCKED", e)
		}
		cases = append(cases, "2 will's live lease on "+leased+" → CONFLICT_LOCKED")

		// 3. A worker: FORBIDDEN.
		mine := asStr(b.task("worker's")["id"])
		if _, e := b.del(will, mine); e == nil || e.Code != "FORBIDDEN" {
			t.Fatalf("3: a worker's delete: %+v, want FORBIDDEN", e)
		}
		cases = append(cases, "3 worker → FORBIDDEN")

		// 4. Subtasks go with their parent.
		parent := asStr(b.task("parent")["id"])
		var subs []string
		for _, title := range []string{"s1", "s2"} {
			subs = append(subs, asStr(asMap(owner.call("add_subtask", map[string]any{"projectId": b.project, "parentTaskId": parent, "title": title})["task"])["id"]))
		}
		if _, e := b.del(owner, parent); e != nil {
			t.Fatalf("4: %+v", e)
		}
		for _, id := range append([]string{parent}, subs...) {
			if e := b.tryGet(id); e == nil || e.Code != "NOT_FOUND" {
				t.Fatalf("4: %s after its parent's delete: %+v, want NOT_FOUND", id, e)
			}
		}
		cases = append(cases, fmt.Sprintf("4 parent %s deleted → subtasks %v NOT_FOUND", parent, subs))

		// 5. A racing parent's last run deleted: the run is gone, the parent TODO with racing false.
		rp := asStr(b.task("racing parent")["id"])
		run := asStr(asMap(owner.call("start_runs", map[string]any{"projectId": b.project, "taskId": rp, "version": b.get(rp)["version"]})["runs"].([]any)[0])["id"])
		if p := b.get(rp); p["racing"] != true {
			t.Fatalf("5: the seeded parent is not racing: %v", p)
		}
		if _, e := b.del(owner, run); e != nil {
			t.Fatalf("5: %+v", e)
		}
		if e := b.tryGet(run); e == nil || e.Code != "NOT_FOUND" {
			t.Fatalf("5: the run %s: %+v, want NOT_FOUND", run, e)
		}
		if p := b.get(rp); p["status"] != "TODO" || p["racing"] == true {
			t.Fatalf("5: parent %v racing %v, want TODO and not racing (racing is omitted when false)", p["status"], p["racing"])
		}
		cases = append(cases, "5 last run "+run+" deleted → gone; parent "+rp+" racing → TODO, not racing")

		// 6. One task.deleted on the board feed; 7. a second delete is NOT_FOUND.
		victim := b.task("victim")
		vid := asStr(victim["id"])
		if _, e := owner.try("delete_task", map[string]any{"projectId": b.project, "taskId": vid, "version": victim["version"]}); e != nil {
			t.Fatalf("6: %+v", e)
		}
		if n := b.feedDeleted(vid); n != 1 {
			t.Fatalf("6: %d task.deleted events for %s, want 1", n, vid)
		}
		cases = append(cases, "6 one task.deleted for "+vid+" on the board feed")
		if _, e := owner.try("delete_task", map[string]any{"projectId": b.project, "taskId": vid, "version": victim["version"]}); e == nil || e.Code != "NOT_FOUND" {
			t.Fatalf("7: second delete: %+v, want NOT_FOUND", e)
		}
		cases = append(cases, "7 second delete of "+vid+" → NOT_FOUND")

		// 8. The T-n counter never reuses the id: the newest task deleted, the next create is past it.
		last := b.task("last")
		lid := asStr(last["id"])
		if _, e := owner.try("delete_task", map[string]any{"projectId": b.project, "taskId": lid, "version": last["version"]}); e != nil {
			t.Fatalf("8: %+v", e)
		}
		next := asStr(b.task("after")["id"])
		if n, m := taskNum(t, lid), taskNum(t, next); m != n+1 {
			t.Fatalf("8: after deleting %s the next create is %s, want T-%d", lid, next, n+1)
		}
		cases = append(cases, "8 "+lid+" deleted → next create "+next)
		t.Logf("%s: the eight delete_task cases:\n  %s", j.engine, strings.Join(cases, "\n  "))
	})
}

func taskNum(t *testing.T, id string) int {
	t.Helper()
	var n int
	if _, err := fmt.Sscanf(id, "T-%d", &n); err != nil {
		t.Fatalf("task id %q: %v", id, err)
	}
	return n
}

// TestJoinTwoClaimsOneWinner: two agents claim_task the same TODO task at once, 20 rounds: each round
// exactly one lease, the other CONFLICT_LOCKED.
func TestJoinTwoClaimsOneWinner(t *testing.T) {
	onJoined(t, func(t *testing.T, j *joined) {
		owner, will, mia := j.user("ada"), j.user("will"), j.user("mia")
		b := j.board(owner, nil, map[*agent]string{will: "worker", mia: "worker"})
		const rounds = 20
		for r := range rounds {
			id := asStr(b.task(fmt.Sprintf("contested %d", r))["id"])
			errs := make([]*toolErr, 2)
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i, who := range []*agent{will, mia} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					_, errs[i] = who.try("claim_task", map[string]any{"projectId": b.project, "taskId": id})
				}()
			}
			close(start)
			wg.Wait()
			wins, locked := 0, 0
			for _, e := range errs {
				switch {
				case e == nil:
					wins++
				case e.Code == "CONFLICT_LOCKED":
					locked++
				default:
					t.Fatalf("round %d: %+v", r, e)
				}
			}
			if wins != 1 || locked != 1 {
				t.Fatalf("round %d on %s: %d winners, %d CONFLICT_LOCKED (%+v %+v), want 1 and 1", r, id, wins, locked, errs[0], errs[1])
			}
		}
		t.Logf("%s: %d rounds of two concurrent claim_task: one winner and one CONFLICT_LOCKED each", j.engine, rounds)
	})
}

// TestJoinFiftyClaimers: 50 TODO tasks and 50 workers, each with its own session, call claim_next at
// once: 50 distinct winners, 0 errors; the latency of each call is logged as p50, p95 and max.
func TestJoinFiftyClaimers(t *testing.T) {
	onJoined(t, func(t *testing.T, j *joined) {
		const n = 50
		r := j.claimRace(n)
		t.Logf("%s: %d concurrent claim_next: %d distinct winners, %d errors; latency p50 %v p95 %v max %v",
			j.engine, n, r.winners, r.failed, r.p(0.50).Round(time.Millisecond), r.p(0.95).Round(time.Millisecond), r.p(1).Round(time.Millisecond))
		if r.winners != n || r.failed != 0 {
			t.Fatalf("%d distinct winners, %d errors; want %d and 0", r.winners, r.failed, n)
		}
	})
}

// race is one claimRace: the distinct winners, the calls that failed, each call's latency in
// ascending order, and the wall time from the start to the last answer.
type race struct {
	winners, failed int
	sorted          []time.Duration
	wall            time.Duration
}

// p is the q-quantile of the latencies (nearest rank); p(1) is the slowest.
func (r race) p(q float64) time.Duration { return r.sorted[int(q*float64(len(r.sorted)-1)+0.5)] }

// claimRace makes n workers, each a member with its own session, and a board of n TODO tasks, then
// has every worker call claim_next at once; a duplicate or empty win and every error is reported on
// j.t.
func (j *joined) claimRace(n int) race {
	t := j.t
	t.Helper()
	owner := j.user("ada")
	workers := map[*agent]string{}
	var order []*agent
	for i := range n {
		w := j.user(fmt.Sprintf("w%02d", i))
		workers[w] = "worker"
		order = append(order, w)
	}
	b := j.board(owner, nil, workers)
	for i := range n {
		b.task(fmt.Sprintf("job %d", i))
	}
	ids := make([]string, n)
	errs := make([]*toolErr, n)
	took := make([]time.Duration, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, w := range order {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			t0 := time.Now()
			out, e := w.try("claim_next", map[string]any{"projectId": b.project, "boardId": b.bid})
			took[i], errs[i] = time.Since(t0), e
			ids[i] = asStr(asMap(out["task"])["id"])
		}()
	}
	t0 := time.Now()
	close(start)
	wg.Wait()
	r := race{wall: time.Since(t0)}
	seen := map[string]bool{}
	for i := range n {
		if errs[i] != nil {
			r.failed++
			t.Errorf("claimer %d: %+v", i, errs[i])
			continue
		}
		if ids[i] == "" || seen[ids[i]] {
			t.Errorf("claimer %d won %q (empty or a duplicate)", i, ids[i])
		}
		seen[ids[i]] = true
	}
	r.winners = len(seen)
	r.sorted = slices.Clone(took)
	sort.Slice(r.sorted, func(a, b int) bool { return r.sorted[a] < r.sorted[b] })
	return r
}

// changes is the change log past cursor after: each row's kind, scope and tool.
func (j *joined) changes(after int64) (rows []string, last int64) {
	j.t.Helper()
	ch, err := j.st.ReadChanges(context.Background(), after, 1000)
	if err != nil {
		j.t.Fatal(err)
	}
	last = after
	for _, c := range ch {
		var p struct {
			Kind string `json:"kind"`
			Tool string `json:"tool"`
		}
		_ = json.Unmarshal(c.Payload, &p)
		rows = append(rows, p.Kind+"@"+c.Scope+":"+p.Tool)
		last = c.ID
	}
	return rows, last
}

func rang(rows []string) []string {
	var out []string
	for _, r := range rows {
		if strings.HasPrefix(r, "assigned@") || strings.HasPrefix(r, "broadcast@") {
			out = append(out, r)
		}
	}
	return out
}

// TestJoinDoorbellRows: a claim writes its change row and rings nobody (no assigned or broadcast
// row); a handoff_task to another member writes exactly one assigned row, scoped to that member.
func TestJoinDoorbellRows(t *testing.T) {
	onJoined(t, func(t *testing.T, j *joined) {
		owner, will, mia := j.user("ada"), j.user("will"), j.user("mia")
		b := j.board(owner, nil, map[*agent]string{will: "worker", mia: "worker"})
		id := asStr(b.task("rung")["id"])
		_, cur := j.changes(0)
		lease := b.claim(will, id)
		rows, cur := j.changes(cur)
		if len(rows) == 0 || len(rang(rows)) != 0 {
			t.Fatalf("claim_task wrote %v, want its change row and no doorbell", rows)
		}
		t.Logf("%s: claim_task wrote %v (no doorbell)", j.engine, rows)
		will.call("handoff_task", map[string]any{"projectId": b.project, "taskId": id, "leaseId": lease, "toAgent": mia.id, "memoryNote": "yours"})
		rows, _ = j.changes(cur)
		if got := rang(rows); !slices.Equal(got, []string{"assigned@" + mia.id + ":handoff_task"}) {
			t.Fatalf("handoff_task to %s wrote %v, want one assigned row scoped to %s", mia.id, rows, mia.id)
		}
		t.Logf("%s: handoff_task to %s wrote %v", j.engine, mia.id, rows)
	})
}

// TestJoinStreamFrameAfterAnMCPMove: an /api/stream subscriber of the board, signed in through POST
// /api/login on the same mux, gets a board frame within 200 ms of an agent's move_task over /mcp.
func TestJoinStreamFrameAfterAnMCPMove(t *testing.T) {
	onJoined(t, func(t *testing.T, j *joined) {
		owner := j.user("ada")
		b := j.board(owner, nil, nil)
		id := asStr(b.task("watched")["id"])
		cookies := j.login("ada")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "GET", j.url+"/api/stream?projectId="+b.project+"&boardId="+b.bid, nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("stream: %d %s", resp.StatusCode, b)
		}
		events := make(chan string, 16)
		go func() {
			defer close(events)
			buf := make([]byte, 1<<16)
			var pending string
			for {
				n, err := resp.Body.Read(buf)
				pending += string(buf[:n])
				for {
					i := strings.Index(pending, "\n\n")
					if i < 0 {
						break
					}
					for _, line := range strings.Split(pending[:i], "\n") {
						if ev, ok := strings.CutPrefix(line, "event: "); ok {
							events <- ev
						}
					}
					pending = pending[i+2:]
				}
				if err != nil {
					return
				}
			}
		}()
		wait := func(want string, d time.Duration) time.Duration {
			t0 := time.Now()
			deadline := time.After(d)
			for {
				select {
				case ev, ok := <-events:
					if !ok {
						t.Fatalf("the stream closed waiting for %s", want)
					}
					if ev == want {
						return time.Since(t0)
					}
				case <-deadline:
					t.Fatalf("no %s frame within %v", want, d)
				}
			}
		}
		wait("ready", 2*time.Second)
		var writes []time.Duration // each move_task's own round trip, inside its frame's time
		move := func(to string) time.Duration {
			t.Helper()
			t0 := time.Now()
			if _, e := b.move(owner, id, to); e != nil {
				t.Fatal(e)
			}
			writes = append(writes, time.Since(t0))
			wait("board", time.Second)
			return time.Since(t0)
		}
		// Busy: each move follows the last write within one bus poll (250 ms), so its wake lands
		// while the previous read is fresh; quiet: no write for longer than a poll.
		busy := make([]time.Duration, 5)
		for i := range busy {
			busy[i] = move([]string{"IN_PROGRESS", "TODO"}[i%2])
			wait("updated", time.Second)
		}
		time.Sleep(bus.PollInterval + 50*time.Millisecond)
		quiet := move("IN_PROGRESS")
		t.Logf("%s: board frame after an MCP move_task: %v quiet (no write in the last %v), %v busy (writes just before); the move_task calls took %v", j.engine, quiet.Round(time.Millisecond), bus.PollInterval, busy, writes)
		// memstore has no commit wake (store.ChangeWatcher): its bus polls, so the bound is the engines'.
		if j.engine == servicetest.Memstore {
			return
		}
		for i, d := range append(busy, quiet) {
			if d > 200*time.Millisecond {
				t.Fatalf("board frame %d of %d took %v, want <= 200ms (busy %v, quiet %v; the move_task calls took %v)", i+1, len(busy)+1, d, busy, quiet, writes)
			}
		}
	})
}

// login signs login in through POST /api/login and answers its cookies.
func (j *joined) login(login string) []*http.Cookie {
	j.t.Helper()
	form := url.Values{"login": {login}, "password": {joinPassword}}
	req, _ := http.NewRequest("POST", j.url+"/api/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		j.t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		j.t.Fatalf("login: %d", resp.StatusCode)
	}
	return resp.Cookies()
}
