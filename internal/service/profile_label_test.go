package service

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// capTask seeds a TODO task that requires caps.
func (e *lcEnv) capTask(t *testing.T, caps ...string) store.Task {
	t.Helper()
	task, err := e.st.CreateTask(e.ctx, store.CreateTaskInput{
		ProjectID: e.project, BoardID: e.board.ID, ColumnID: e.column(t, domain.Todo), Title: "t", Status: domain.Todo,
		OrderKey: "a0", CreatedBy: "alice", CreatedAt: time.Now().UnixMilli(), RequiredCapabilities: caps,
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// P-340: a profile and its stats are keyed by the agent name alone, so a name the client chose (an
// OAuth client_name, a clientInfo.name) can equal another member's label. On every engine:
// register_agent_profile and rank_tasks {useProfile} refuse such a key FORBIDDEN (details.label),
// whether the label's token is live, revoked or expired, and leave the owner's profile as it was;
// complete_task and block_task still transition, and the outcome lands under the caller's identity,
// not on the owner's stats. The owner on its own token, a caller with no agent, a free name and the
// caller's own label are not refused.
func TestAProfileKeyIsOnlyItsLabelsOwn(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		carol := e.member(t, e.project, "carol", domain.RoleWorker)
		now := e.clock.now().UnixMilli()
		hour := time.Hour.Milliseconds()
		for _, tok := range []store.Token{
			{ID: "tok-carol-b", IdentityID: "carol", Subject: "carol", SecretHash: "h-1", Label: "agent-b", CreatedAt: now, ExpiresAt: now + hour},
			{ID: "tok-carol-r", IdentityID: "carol", Subject: "carol", SecretHash: "h-2", Label: "agent-r", CreatedAt: now, ExpiresAt: now + hour},
			{ID: "tok-carol-x", IdentityID: "carol", Subject: "carol", SecretHash: "h-3", Label: "agent-x", CreatedAt: now - 2*hour, ExpiresAt: now - hour},
			{ID: "tok-bob-own", IdentityID: "bob", Subject: "bob", SecretHash: "h-4", Label: "bob-own", CreatedAt: now, ExpiresAt: now + hour},
		} {
			if err := e.st.CreateToken(e.ctx, tok); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.st.RevokeToken(e.ctx, "tok-carol-r", now); err != nil {
			t.Fatal(err)
		}
		as := func(c domain.Caller, agent string, labelled bool, via string) domain.Caller {
			c.Agent, c.Labelled, c.Via = agent, labelled, via
			return c
		}
		carolB := as(carol, "agent-b", true, domain.ViaAPIToken)
		bobOAuth := as(bob, "agent-b", false, domain.ViaOAuth)      // client_name "agent-b"
		bobDefault := as(bob, "agent-b", false, domain.ViaAPIToken) // default-label token, clientInfo.name "agent-b"
		bobCLI := as(bob, "", false, domain.ViaAPIToken)            // default-label token, no agent
		bobFree := as(bob, "bob-free", false, domain.ViaOAuth)      // a name no token carries
		bobOwn := as(bob, "bob-own", true, domain.ViaAPIToken)      // his own token's label
		prof := e.c.Profiles()
		stats := func(key string) GetSkillStatsResp {
			t.Helper()
			r, err := prof.SkillStats(e.ctx, carol, GetSkillStatsReq{ProjectID: e.project, AgentKey: key})
			if err != nil {
				t.Fatalf("stats %s: %v", key, err)
			}
			return r
		}
		forbiddenLabel := func(t *testing.T, step string, err error, label string) {
			t.Helper()
			be := wantCode(t, err, domain.CodeForbidden)
			if be.Details["field"] != "agentKey" || be.Details["label"] != label {
				t.Fatalf("%s: details %v, want field agentKey label %s", step, be.Details, label)
			}
		}

		// AC1: register_agent_profile.
		if r, err := prof.Register(e.ctx, carolB, RegisterAgentProfileReq{ProjectID: e.project, Capabilities: []string{"go"}}); err != nil || r.Profile.AgentKey != "agent-b" {
			t.Fatalf("AC1 carol on her agent-b token: %+v %v", r, err)
		}
		carolBefore := stats("agent-b")
		for _, c := range []struct {
			name string
			who  domain.Caller
		}{
			{"bob OAuth agent-b (live)", bobOAuth},
			{"bob default token agent-b (live)", bobDefault},
			{"bob OAuth agent-r (revoked)", as(bob, "agent-r", false, domain.ViaOAuth)},
			{"bob OAuth agent-x (expired)", as(bob, "agent-x", false, domain.ViaOAuth)},
		} {
			_, err := prof.Register(e.ctx, c.who, RegisterAgentProfileReq{ProjectID: e.project, Capabilities: []string{"evil"}})
			t.Logf("AC1 register %s: %v", c.name, err)
			forbiddenLabel(t, "AC1 register "+c.name, err, c.who.Agent)
		}
		for _, key := range []string{"agent-r", "agent-x"} {
			if p := stats(key).Profile; p != nil {
				t.Fatalf("AC1: a refused register wrote a %s profile: %+v", key, p)
			}
		}
		if got := stats("agent-b"); !reflect.DeepEqual(got, carolBefore) || !slices.Equal(got.Profile.Capabilities, []string{"go"}) {
			t.Fatalf("AC1: carol's profile changed: %+v, was %+v", got, carolBefore)
		}

		// AC3: rank_tasks {useProfile}, before bob has any profile.
		goTask, rustTask := e.capTask(t, "go"), e.capTask(t, "rust")
		rk := e.c.Ranking()
		ids := func(r RankTasksResp) []string {
			var out []string
			for _, x := range r.Ranked {
				out = append(out, x.Task.ID)
			}
			slices.Sort(out)
			return out
		}
		for _, who := range []domain.Caller{bobOAuth, bobDefault} {
			_, err := rk.Rank(e.ctx, who, RankTasksReq{ProjectID: e.project, BoardID: e.board.ID, UseProfile: true})
			t.Logf("AC3 rank useProfile as bob %s: %v", who.Via, err)
			forbiddenLabel(t, "AC3 rank bob "+who.Via, err, "agent-b")
		}
		r, err := rk.Rank(e.ctx, carolB, RankTasksReq{ProjectID: e.project, BoardID: e.board.ID, UseProfile: true})
		if err != nil || len(r.Ranked) != 1 || r.Ranked[0].Task.ID != goTask.ID || r.Ranked[0].Tier == "" {
			t.Fatalf("AC3 carol ranked by her profile (go only, rust %s excluded): %+v %v", rustTask.ID, r, err)
		}
		plain := []string{goTask.ID, rustTask.ID}
		slices.Sort(plain)
		r, err = rk.Rank(e.ctx, bobCLI, RankTasksReq{ProjectID: e.project, BoardID: e.board.ID, UseProfile: true})
		if err != nil || !slices.Equal(ids(r), plain) || r.Ranked[0].Tier != "" {
			t.Fatalf("AC3 bob with no agent and no profile: %+v %v, want the plain pool %v", r, err, plain)
		}
		t.Logf("AC3 carol ranked %s only; bob (no agent) ranked %v", goTask.ID, ids(r))

		// AC1 controls, after the rank: bob with no agent, a free name, his own label.
		for _, c := range []struct {
			who domain.Caller
			key string
		}{{bobCLI, "bob"}, {bobFree, "bob-free"}, {bobOwn, "bob-own"}} {
			r, err := prof.Register(e.ctx, c.who, RegisterAgentProfileReq{ProjectID: e.project, Capabilities: []string{"go"}})
			if err != nil || r.Profile.AgentKey != c.key {
				t.Fatalf("AC1 control %q: %+v %v, want key %s", c.who.Agent, r, err, c.key)
			}
		}

		// AC2: complete_task and block_task outcomes.
		lc := e.c.Lifecycle()
		for _, who := range []domain.Caller{bobOAuth, bobDefault} {
			done := e.capTask(t, "go")
			c := e.claim(t, who, done.ID, 3600)
			if _, err := lc.Complete(e.ctx, who, CompleteTaskReq{ProjectID: e.project, TaskID: done.ID, LeaseID: c.LeaseID}); err != nil {
				t.Fatalf("AC2 complete as bob %s: %v", who.Via, err)
			}
			blocked := e.capTask(t, "go")
			c = e.claim(t, who, blocked.ID, 3600)
			if _, err := lc.Block(e.ctx, who, BlockTaskReq{ProjectID: e.project, TaskID: blocked.ID, LeaseID: c.LeaseID, Reason: "stuck"}); err != nil {
				t.Fatalf("AC2 block as bob %s: %v", who.Via, err)
			}
			if got := e.get(t, done.ID).Status; got == domain.Todo || got == domain.InProgress {
				t.Fatalf("AC2: complete as bob %s left %s", who.Via, got)
			}
			if got := e.get(t, blocked.ID).Status; got != domain.Blocked {
				t.Fatalf("AC2: block as bob %s left %s", who.Via, got)
			}
		}
		if got := stats("agent-b"); !reflect.DeepEqual(got, carolBefore) {
			t.Fatalf("AC2: bob's outcomes landed on carol's agent-b: %+v, was %+v", got.Stats, carolBefore.Stats)
		}
		bs := stats("bob").Stats
		if len(bs) != 1 || bs[0].Capability != "go" || bs[0].Completed != 2 || bs[0].Blocked != 2 {
			t.Fatalf("AC2: bob's outcomes under his identity: %+v, want go completed 2 blocked 2", bs)
		}
		mine := e.capTask(t, "go")
		c := e.claim(t, carolB, mine.ID, 3600)
		if _, err := lc.Complete(e.ctx, carolB, CompleteTaskReq{ProjectID: e.project, TaskID: mine.ID, LeaseID: c.LeaseID}); err != nil {
			t.Fatalf("AC2 carol completes: %v", err)
		}
		cs := stats("agent-b").Stats
		if len(cs) != 1 || cs[0].AgentKey != "agent-b" || cs[0].Completed != 1 || cs[0].Blocked != 0 {
			t.Fatalf("AC2: carol's own completion under agent-b: %+v, want go completed 1", cs)
		}
		t.Logf("AC2 bob %+v; carol %+v", bs, cs)
	})
}
