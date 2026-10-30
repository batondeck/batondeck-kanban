package cli

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/wake"
)

// P-348 and P-349: REVIEW-343-322-fable's probes 1, 2 and 4, adopted (probe 3 is P-344's, in
// wake_label_minted_test.go). A body token names a label as ResolveMentions reads it
// (domain.MentionNames), and a free name never takes another member's own name.

// P-348 AC1 (Fable A1), over the real serve socket: bob's email-form label "pbob@x.example" is named
// by "@pbob" through its local part, so carol's worker under the free name "pbob" loses its alias:
// alice's "@pbob" rings bob alone and notifies carol nothing.
func TestALocalPartOfAnEmailFormLabelRingsOnlyItsOwner(t *testing.T) {
	s := startDirectory(t, nil)
	ctx := context.Background()
	if _, err := s.st.AddMember(ctx, s.project, s.carol.id, domain.RoleWorker, s.alice.id); err != nil {
		t.Fatal(err)
	}
	owned := "pbob@x.example"
	tok, _, err := (&auth.TokenService{Store: s.st}).Create(ctx, s.bob.id, "", owned, time.Hour)
	if err != nil {
		t.Fatalf("bob mints %s: %v", owned, err)
	}
	b := s.client(t, "s-bob-email", "")
	s.registerAgent(t, person{id: s.bob.id, token: tok}, b, owned)
	c := s.client(t, "s-carol-local", "")
	s.registerAgent(t, s.carol, c, "pbob") // free by the exact rule: no token carries "pbob"
	task := s.task(t, s.alice, "")
	quiet(c, b)
	got, err := s.a.core.Comments().AddComment(ctx, s.alice.caller(), service.AddCommentReq{ProjectID: s.project, TaskID: task.ID, Body: "@pbob please look"})
	if err != nil {
		t.Fatal(err)
	}
	bobRung, carolRung := false, false
	deadline := time.After(3 * time.Second)
loop:
	for {
		select {
		case raw := <-b.got:
			bobRung = bobRung || content(t, raw).Kind == wake.KindAssigned
		case raw := <-c.got:
			carolRung = carolRung || content(t, raw).Kind == wake.KindAssigned
		case <-deadline:
			break loop
		}
	}
	t.Logf("@pbob with bob's label %q: mentions %v, bob rung %v, carol rung %v, carol notified %d", owned, got.Comment.Mentions, bobRung, carolRung, s.mentionsFor(t, s.carol, got.Comment.ID))
	if carolRung || s.mentionsFor(t, s.carol, got.Comment.ID) != 0 {
		t.Fatalf("@pbob resolves to bob's label %q by its local part (bob rung %v) and also to carol's free name \"pbob\" (rung %v, notified %d)",
			owned, bobRung, carolRung, s.mentionsFor(t, s.carol, got.Comment.ID))
	}
	if !bobRung || !slices.Equal(got.Comment.Mentions, []string{s.bob.id}) {
		t.Fatalf("control: @pbob rang bob %v with mentions %v, want bob rung and mentioned alone", bobRung, got.Comment.Mentions)
	}
}

// P-348 AC2 (Fable A2): a label is compared whole, so bob's "pt bob" is not named by "@pt", and
// carol's worker under the free name "pt" (no token carries it, claim_next {assignee: "pt"} passes
// for her) keeps its alias: alice's "@pt" rings it (P-310 decision 4, unowned names alias).
func TestAFreeNameIsNotDroppedForALabelItOnlyStarts(t *testing.T) {
	s := startDirectory(t, nil)
	ctx := context.Background()
	if _, err := s.st.AddMember(ctx, s.project, s.carol.id, domain.RoleWorker, s.alice.id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (&auth.TokenService{Store: s.st}).Create(ctx, s.bob.id, "", "pt bob", time.Hour); err != nil {
		t.Fatalf("bob mints %q: %v", "pt bob", err)
	}
	c := s.client(t, "s-carol-pt", "")
	s.registerAgent(t, s.carol, c, "pt")
	task := s.task(t, s.alice, "")
	quiet(c)
	got, err := s.a.core.Comments().AddComment(ctx, s.alice.caller(), service.AddCommentReq{ProjectID: s.project, TaskID: task.ID, Body: "@pt please look"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-c.got:
		if f := content(t, raw); f.Kind != wake.KindAssigned {
			t.Fatalf("@pt rang carol's free-name worker %+v, want assigned", f)
		}
		t.Logf("@pt rang carol's free-name worker; mentions %v", got.Comment.Mentions)
	case <-time.After(3 * time.Second):
		t.Fatalf("@pt (a free name, claimable by carol) rang nobody because bob's label %q has the mention token \"pt\"; mentions %v", "pt bob", got.Comment.Mentions)
	}
}

// P-349 (Fable F1, probe 4 adopted): a free name equal to another member's own name, its login
// (an email's local part included) or its display name, aliases nobody, on every engine. carol's
// workers under "will" (will's login) and "dee" (dora's display name) are not mentions of "@will"
// or "@dee", which ring and notify neither carol; carol's "@carol" still rings her worker, and her
// free "claude-rev", no member's name, still aliases her.
func TestAFreeNameEqualToAMembersOwnNameAliasesNobody(t *testing.T) {
	onJoined(t, func(t *testing.T, j *joined) {
		ctx := context.Background()
		ada, will, carol := j.user("ada"), j.user("will"), j.user("carol")
		dora, err := (&auth.Accounts{Store: j.st, Params: auth.Argon2Params{Time: 1, Memory: 64, Threads: 1}}).Add(ctx, "dora@x.example", "Dee", joinPassword, false)
		if err != nil {
			t.Fatal(err)
		}
		j.a.core.SetDeviceSessions(fixedDevices{
			{PID: 401, Role: service.RoleWorker, IdentityID: carol.id, AgentID: "will"},
			{PID: 402, Role: service.RoleWorker, IdentityID: carol.id, AgentID: "dee"},
			{PID: 403, Role: service.RoleWorker, IdentityID: carol.id, AgentID: "claude-rev"},
		})
		p := asStr(ada.call("create_project", map[string]any{"name": "Ada's"})["projectId"])
		b := asStr(asMap(ada.call("create_board", map[string]any{"projectId": p, "name": "Main"})["board"])["id"])
		for _, id := range []string{will.id, carol.id, dora.ID} {
			ada.call("add_member", map[string]any{"projectId": p, "identityId": id, "role": "worker"})
		}
		task := asStr(asMap(ada.call("create_task", map[string]any{"projectId": p, "boardId": b, "title": "retry"})["task"])["id"])
		for _, c := range []struct {
			body, who string
			want      []string
			carolRung bool
		}{
			{"@will please look", "will's login", []string{will.id}, false},
			{"@dee please look", "dora's display name", []string{dora.ID}, false},
			{"@carol and you", "control: carol's own login", []string{carol.id}, true},
			{"@claude-rev please look", "control: a free name no member has", []string{carol.id}, true},
		} {
			m, rung, notified := commentOutcome(t, j, ada, p, task, c.body, carol.id)
			t.Logf("%s: %s (%s) mentions %v, carol rung %v, notified %v", j.engine, c.body, c.who, m, rung, notified)
			if !slices.Equal(m, c.want) {
				t.Errorf("%s: %q (%s) mentions %v, want %v (will %s, carol %s, dora %s)", j.engine, c.body, c.who, m, c.want, will.id, carol.id, dora.ID)
			}
			if rung != c.carolRung || notified != c.carolRung {
				t.Errorf("%s: %q (%s) rang carol %v and notified her %v, want %v", j.engine, c.body, c.who, rung, notified, c.carolRung)
			}
		}
	})
}

// P-348 AC3, on every engine: bob's email-form label is named by its local part, so carol's free
// "pbob" aliases nothing and "@pbob" mentions bob alone; bob's "pt bob" is compared whole, so
// carol's free "pt" keeps aliasing her and "@pt" mentions carol alone. A label only its own
// identity's rows carry aliases as before (requirement 2), even where a body token also names
// another identity's label: bob's own "qcarol" and carol's own "qcarol@x.example" both alias, so
// "@qcarol" mentions both (which of the two a mint should refuse is lane L4's rule).
func TestAMentionNamesALabelAsItResolvesOnEveryEngine(t *testing.T) {
	onJoined(t, func(t *testing.T, j *joined) {
		ctx := context.Background()
		ada, bob, carol := j.user("ada"), j.user("bob"), j.user("carol")
		for _, m := range []struct{ id, label string }{{bob.id, "pbob@x.example"}, {bob.id, "pt bob"}, {bob.id, "qcarol"}, {carol.id, "qcarol@x.example"}} {
			if _, _, err := (&auth.TokenService{Store: j.st}).Create(ctx, m.id, "", m.label, time.Hour); err != nil {
				t.Fatalf("%s mints %q: %v", m.id, m.label, err)
			}
		}
		j.a.core.SetDeviceSessions(fixedDevices{
			{PID: 501, Role: service.RoleWorker, IdentityID: bob.id, AgentID: "pbob@x.example"},
			{PID: 502, Role: service.RoleWorker, IdentityID: carol.id, AgentID: "pbob"},
			{PID: 503, Role: service.RoleWorker, IdentityID: carol.id, AgentID: "pt"},
			{PID: 504, Role: service.RoleWorker, IdentityID: bob.id, AgentID: "qcarol"},
			{PID: 505, Role: service.RoleWorker, IdentityID: carol.id, AgentID: "qcarol@x.example"},
		})
		p := asStr(ada.call("create_project", map[string]any{"name": "Ada's"})["projectId"])
		b := asStr(asMap(ada.call("create_board", map[string]any{"projectId": p, "name": "Main"})["board"])["id"])
		for _, m := range []*agent{bob, carol} {
			ada.call("add_member", map[string]any{"projectId": p, "identityId": m.id, "role": "worker"})
		}
		task := asStr(asMap(ada.call("create_task", map[string]any{"projectId": p, "boardId": b, "title": "retry"})["task"])["id"])
		for _, c := range []struct {
			body      string
			want      []string
			carolRung bool
		}{
			{"@pbob please look", []string{bob.id}, false},
			{"@pt please look", []string{carol.id}, true},
			{"@qcarol please look", []string{bob.id, carol.id}, true},
		} {
			m, rung, notified := commentOutcome(t, j, ada, p, task, c.body, carol.id)
			t.Logf("%s: %s mentions %v, carol rung %v, notified %v", j.engine, c.body, m, rung, notified)
			slices.Sort(m)
			slices.Sort(c.want)
			if !slices.Equal(m, c.want) || rung != c.carolRung || notified != c.carolRung {
				t.Errorf("%s: %q mentions %v, rang carol %v, notified her %v; want %v and %v (bob %s, carol %s)",
					j.engine, c.body, m, rung, notified, c.want, c.carolRung, bob.id, carol.id)
			}
		}
	})
}

// commentOutcome is by's comment body on task: its mentions, and whether it rang (an assigned
// change scoped to who) and notified (a mention notification of that comment) who.
func commentOutcome(t *testing.T, j *joined, by *agent, project, task, body, who string) (mentions []string, rung, notified bool) {
	t.Helper()
	ctx := context.Background()
	before, err := j.st.ReadChanges(ctx, 0, 100000)
	if err != nil {
		t.Fatal(err)
	}
	cm := asMap(by.call("add_comment", map[string]any{"projectId": project, "taskId": task, "body": body})["comment"])
	rows, err := j.st.ReadChanges(ctx, before[len(before)-1].ID, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		rung = rung || (r.Kind != service.ChangeKindNotification && r.Kind+"@"+r.Scope == "assigned@"+who)
	}
	n, err := j.st.ListNotifications(ctx, who, store.NotificationsQuery{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range n.Items {
		var d struct{ CommentID string }
		_ = json.Unmarshal(r.Data, &d)
		notified = notified || (r.Type == domain.NotificationMention && d.CommentID == asStr(cm["id"]))
	}
	m, _ := cm["mentions"].([]any)
	mentions = []string{}
	for _, x := range m {
		mentions = append(mentions, asStr(x))
	}
	return mentions, rung, notified
}
