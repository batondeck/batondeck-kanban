package service

import (
	"reflect"
	"slices"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// P-351: an agentless caller's profile and stats are keyed by its identity id, so an agent name
// equal to another identity's id (a member's, or any account's) would write that identity's
// agentless profile and stats. On every engine: register_agent_profile and useProfile on
// rank_tasks, next_task and claim_next refuse such a key FORBIDDEN (details.agentKey) and leave the
// owner's profile as it was; complete_task and block_task still transition, and the outcome lands
// under the caller's own identity. A caller with no agent, one named its own id and a free name are
// not refused.
func TestAProfileKeyIsNeverAnotherIdentitysID(t *testing.T) {
	onLC(t, func(t *testing.T, e *lcEnv) {
		bob := e.member(t, e.project, "bob", domain.RoleWorker)
		carol := e.member(t, e.project, "carol", domain.RoleWorker)
		// dave is an account, not a member of this project.
		if err := e.st.CreateUser(e.ctx, store.User{ID: "U-dave", Email: "dave@example.com", DisplayName: "dave", PasswordHash: "x", CreatedAt: e.clock.now().UnixMilli()}); err != nil {
			t.Fatal(err)
		}
		as := func(c domain.Caller, agent string, labelled bool, via string) domain.Caller {
			c.Agent, c.Labelled, c.Via = agent, labelled, via
			return c
		}
		bobOAuth := as(bob, "carol", false, domain.ViaOAuth)      // client_name "carol"
		bobDefault := as(bob, "carol", false, domain.ViaAPIToken) // default-label token, clientInfo.name "carol"
		bobLabel := as(bob, "carol", true, domain.ViaAPIToken)    // a token labelled "carol"
		bobDave := as(bob, "U-dave", false, domain.ViaOAuth)
		prof := e.c.Profiles()
		lc := e.c.Lifecycle()
		stats := func(key string) GetSkillStatsResp {
			t.Helper()
			r, err := prof.SkillStats(e.ctx, carol, GetSkillStatsReq{ProjectID: e.project, AgentKey: key})
			if err != nil {
				t.Fatalf("stats %s: %v", key, err)
			}
			return r
		}
		forbiddenKey := func(t *testing.T, step string, err error, key string) {
			t.Helper()
			be := wantCode(t, err, domain.CodeForbidden)
			if be.Details["field"] != "agentKey" || be.Details["agentKey"] != key {
				t.Fatalf("%s: details %v, want field agentKey agentKey %s", step, be.Details, key)
			}
		}

		// carol, with no agent, keeps a profile and one outcome under her id.
		if r, err := prof.Register(e.ctx, carol, RegisterAgentProfileReq{ProjectID: e.project, Capabilities: []string{"go"}}); err != nil || r.Profile.AgentKey != "carol" {
			t.Fatalf("carol's agentless profile: %+v %v", r, err)
		}
		own := e.capTask(t, "go")
		c := e.claim(t, carol, own.ID, 3600)
		if _, err := lc.Complete(e.ctx, carol, CompleteTaskReq{ProjectID: e.project, TaskID: own.ID, LeaseID: c.LeaseID}); err != nil {
			t.Fatal(err)
		}
		carolBefore := stats("carol")
		if len(carolBefore.Stats) != 1 || carolBefore.Stats[0].Completed != 1 {
			t.Fatalf("carol's own outcome: %+v", carolBefore.Stats)
		}

		// AC1: register_agent_profile.
		for _, c := range []struct {
			name string
			who  domain.Caller
		}{{"bob OAuth carol", bobOAuth}, {"bob default token carol", bobDefault}, {"bob labelled carol", bobLabel}, {"bob OAuth U-dave", bobDave}} {
			_, err := prof.Register(e.ctx, c.who, RegisterAgentProfileReq{ProjectID: e.project, Capabilities: []string{"evil"}})
			t.Logf("AC1 register %s: %v", c.name, err)
			forbiddenKey(t, "AC1 register "+c.name, err, c.who.Agent)
		}
		if got := stats("carol"); !reflect.DeepEqual(got, carolBefore) || !slices.Equal(got.Profile.Capabilities, []string{"go"}) {
			t.Fatalf("AC1: carol's profile changed: %+v, was %+v", got, carolBefore)
		}
		if p := stats("U-dave").Profile; p != nil {
			t.Fatalf("AC1: a refused register wrote dave's profile: %+v", p)
		}

		// AC3: rank_tasks {useProfile}.
		goTask, rustTask := e.capTask(t, "go"), e.capTask(t, "rust")
		rk := e.c.Ranking()
		for _, who := range []domain.Caller{bobOAuth, bobDefault} {
			_, err := rk.Rank(e.ctx, who, RankTasksReq{ProjectID: e.project, BoardID: e.board.ID, UseProfile: true})
			t.Logf("AC3 rank useProfile as bob %s named carol: %v", who.Via, err)
			forbiddenKey(t, "AC3 rank bob "+who.Via, err, "carol")
		}
		r, err := rk.Rank(e.ctx, carol, RankTasksReq{ProjectID: e.project, BoardID: e.board.ID, UseProfile: true})
		if err != nil || len(r.Ranked) != 1 || r.Ranked[0].Task.ID != goTask.ID || r.Ranked[0].Tier == "" {
			t.Fatalf("AC3 carol ranked by her profile (go only, rust %s excluded): %+v %v", rustTask.ID, r, err)
		}

		// AC3 on the scorer's other callers (P-258 through withProfile): next_task {useProfile}, fifo
		// and score, and claim_next {useProfile} refuse the same keys, and the refused claim_next
		// claims nothing. carol, with no agent, is judged by her own profile.
		for _, c := range []struct {
			name string
			who  domain.Caller
		}{{"bob OAuth carol", bobOAuth}, {"bob default token carol", bobDefault}, {"bob labelled carol", bobLabel}, {"bob OAuth U-dave", bobDave}} {
			for _, strategy := range []NextStrategy{"", "score"} {
				_, err := lc.Next(e.ctx, c.who, NextTaskReq{ProjectID: e.project, BoardID: e.board.ID, UseProfile: true, Strategy: strategy})
				t.Logf("AC3 next_task useProfile strategy %q as %s: %v", strategy, c.name, err)
				forbiddenKey(t, "AC3 next_task "+string(strategy)+" "+c.name, err, c.who.Agent)
			}
			_, err := lc.ClaimNext(e.ctx, c.who, ClaimNextReq{ProjectID: e.project, BoardID: e.board.ID, UseProfile: true})
			t.Logf("AC3 claim_next useProfile as %s: %v", c.name, err)
			forbiddenKey(t, "AC3 claim_next "+c.name, err, c.who.Agent)
		}
		for _, id := range []string{goTask.ID, rustTask.ID} {
			if got := e.get(t, id); got.Status != domain.Todo || got.Claim != nil {
				t.Fatalf("AC3: a refused claim_next claimed %s: %s %+v", id, got.Status, got.Claim)
			}
		}
		if n, err := lc.Next(e.ctx, carol, NextTaskReq{ProjectID: e.project, BoardID: e.board.ID, UseProfile: true}); err != nil || taskID(n.Task) != goTask.ID {
			t.Fatalf("AC3 carol next_task {useProfile}: %s %v, want %s", taskID(n.Task), err, goTask.ID)
		}

		// AC1 controls: bob with no agent, bob named his own id, a free name.
		for _, c := range []struct {
			who domain.Caller
			key string
		}{{as(bob, "", false, domain.ViaAPIToken), "bob"}, {as(bob, "bob", false, domain.ViaOAuth), "bob"}, {as(bob, "bob-free", false, domain.ViaOAuth), "bob-free"}} {
			r, err := prof.Register(e.ctx, c.who, RegisterAgentProfileReq{ProjectID: e.project, Capabilities: []string{"go"}})
			if err != nil || r.Profile.AgentKey != c.key {
				t.Fatalf("AC1 control %q: %+v %v, want key %s", c.who.Agent, r, err, c.key)
			}
		}

		// AC2: complete_task and block_task outcomes under carol's id, and dave's.
		for _, who := range []domain.Caller{bobOAuth, bobDefault, bobDave} {
			done := e.capTask(t, "go")
			c := e.claim(t, who, done.ID, 3600)
			if _, err := lc.Complete(e.ctx, who, CompleteTaskReq{ProjectID: e.project, TaskID: done.ID, LeaseID: c.LeaseID}); err != nil {
				t.Fatalf("AC2 complete as bob named %s (%s): %v", who.Agent, who.Via, err)
			}
			blocked := e.capTask(t, "go")
			c = e.claim(t, who, blocked.ID, 3600)
			if _, err := lc.Block(e.ctx, who, BlockTaskReq{ProjectID: e.project, TaskID: blocked.ID, LeaseID: c.LeaseID, Reason: "stuck"}); err != nil {
				t.Fatalf("AC2 block as bob named %s (%s): %v", who.Agent, who.Via, err)
			}
			if got := e.get(t, done.ID).Status; got == domain.Todo || got == domain.InProgress {
				t.Fatalf("AC2: complete left %s", got)
			}
			if got := e.get(t, blocked.ID).Status; got != domain.Blocked {
				t.Fatalf("AC2: block left %s", got)
			}
		}
		if got := stats("carol"); !reflect.DeepEqual(got, carolBefore) {
			t.Fatalf("AC2: bob's outcomes landed on carol's id: %+v, was %+v", got.Stats, carolBefore.Stats)
		}
		if got := stats("U-dave").Stats; len(got) != 0 {
			t.Fatalf("AC2: bob's outcomes landed on dave's id: %+v", got)
		}
		bs := stats("bob").Stats
		if len(bs) != 1 || bs[0].Capability != "go" || bs[0].Completed != 3 || bs[0].Blocked != 3 {
			t.Fatalf("AC2: bob's outcomes under his identity: %+v, want go completed 3 blocked 3", bs)
		}
		t.Logf("AC2 bob %+v; carol %+v", bs, carolBefore.Stats)
	})
}
