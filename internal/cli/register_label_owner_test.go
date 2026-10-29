package cli

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/wake"
)

// registerRefused registers c as a worker for p under agent over the Unix socket and wants it
// refused FORBIDDEN, naming the label, with no entry for c's pid left in the directory.
func (s *dirServer) registerRefused(t *testing.T, p person, c *client, agent string) {
	t.Helper()
	body := fmt.Sprintf(`{"pid":%d,"role":"worker","agent":%q,"socket":%q,"sessionId":%q}`, c.pid, agent, c.sock, c.session)
	code, got := httpDo(t, unixHTTP(s.sock), http.MethodPost, "http://x/internal/wake-session", p.token, body)
	if code != http.StatusForbidden || !strings.Contains(got, `"FORBIDDEN"`) || !strings.Contains(got, "another agent's label") {
		t.Fatalf("register pid %d as %q: %d %s, want 403 FORBIDDEN (another agent's label)", c.pid, agent, code, got)
	}
	if slices.Contains(s.pids(), c.pid) {
		t.Fatalf("refused pid %d is in the directory: %v", c.pid, s.pids())
	}
}

// P-322 and P-343 (Fable B1 on #78, its probe adopted), over the real serve composition and socket:
// carol, a project member on the server host, cannot register a worker under bob's token label
// "test-bob" (seedDirectory), so neither a task a master assigns to test-bob (AgentScope) nor
// alice's "@test-bob" rings her socket or notifies her. bob's own worker under the label is the
// positive control: it is rung for both. A case variant of the label is a different agent to a
// claim, so its register passes, and the mention alias still does not read it as bob's (P-343).
func TestARegisterUnderAnothersLabelIsRefusedAndRingsNobody(t *testing.T) {
	s := startDirectory(t, nil)
	ctx := context.Background()
	if _, err := s.st.AddMember(ctx, s.project, s.carol.id, domain.RoleWorker, s.alice.id); err != nil {
		t.Fatal(err)
	}
	label := "test-bob"
	c, variant, b := s.client(t, "s-carol-impostor", ""), s.client(t, "s-carol-variant", ""), s.client(t, "s-bob-worker", "")
	s.registerRefused(t, s.carol, c, label)
	s.registerAgent(t, s.carol, variant, strings.ToUpper(label))
	s.registerAgent(t, s.bob, b, label)
	quiet(c, variant, b)

	// A master assigns work to the label: bob's worker is rung on AgentScope(test-bob), carol's not.
	s.task(t, s.alice, label)
	if f := content(t, b.frame(t, time.Second)); f.Kind != wake.KindAssigned {
		t.Fatalf("a task assigned to %s rang bob's worker %+v, want assigned", label, f)
	}
	c.none(t, s.win.AssignedMaxHold+time.Second)
	variant.none(t, 0)
	quiet(c, variant, b)

	task := s.task(t, s.alice, "")
	got, err := s.a.core.Comments().AddComment(ctx, s.alice.caller(), service.AddCommentReq{ProjectID: s.project, TaskID: task.ID, Body: "@" + label + " over to you"})
	if err != nil {
		t.Fatal(err)
	}
	if f := content(t, b.frame(t, time.Second)); f.Kind != wake.KindAssigned {
		t.Fatalf("alice's @%s rang bob's worker %+v, want assigned", label, f)
	}
	select {
	case raw := <-c.got:
		t.Fatalf("alice's @%s (bob's token label) rang carol's refused pid %d: %+v", label, c.pid, content(t, raw))
	case raw := <-variant.got:
		t.Fatalf("alice's @%s rang carol's %s worker pid %d: %+v; mentions %v", label, strings.ToUpper(label), variant.pid, content(t, raw), got.Comment.Mentions)
	case <-time.After(3 * time.Second):
	}
	if n := s.mentionsFor(t, s.carol, got.Comment.ID); n != 0 || !slices.Equal(got.Comment.Mentions, []string{s.bob.id}) {
		t.Fatalf("@%s: %d mention notifications for carol, mentions %v; want 0 and [%s]", label, n, got.Comment.Mentions, s.bob.id)
	}
}

// P-343 requirement 5, with no entry injected: a free name carol registered before bob minted it is
// bob's from his mint on, so "@<name>" rings bob's worker under it and notifies bob alone, while
// carol's earlier entry under the name is neither rung nor notified (the alias reads ownership at
// comment time, not at register time).
func TestALabelMintedAfterAnotherRegisteredItMentionsOnlyItsMinter(t *testing.T) {
	s := startDirectory(t, nil)
	ctx := context.Background()
	if _, err := s.st.AddMember(ctx, s.project, s.carol.id, domain.RoleWorker, s.alice.id); err != nil {
		t.Fatal(err)
	}
	name := "shared-x"
	c := s.client(t, "s-carol-early", "")
	s.registerAgent(t, s.carol, c, name) // no token carries it yet: accepted
	tok, _, err := (&auth.TokenService{Store: s.st}).Create(ctx, s.bob.id, "", name, time.Hour)
	if err != nil {
		t.Fatalf("bob mints %s: %v", name, err)
	}
	b := s.client(t, "s-bob-shared", "")
	s.registerAgent(t, person{id: s.bob.id, token: tok}, b, name)
	task := s.task(t, s.alice, "") // its open-work broadcast is drained by quiet
	quiet(c, b)

	got, err := s.a.core.Comments().AddComment(ctx, s.alice.caller(), service.AddCommentReq{ProjectID: s.project, TaskID: task.ID, Body: "@" + name + " please look"})
	if err != nil {
		t.Fatal(err)
	}
	if f := content(t, b.frame(t, time.Second)); f.Kind != wake.KindAssigned {
		t.Fatalf("alice's @%s rang bob's worker %+v, want assigned", name, f)
	}
	b.none(t, s.win.Slow+500*time.Millisecond) // once
	c.none(t, 0)
	if !slices.Equal(got.Comment.Mentions, []string{s.bob.id}) || s.mentionsFor(t, s.bob, got.Comment.ID) != 1 || s.mentionsFor(t, s.carol, got.Comment.ID) != 0 {
		t.Fatalf("@%s: mentions %v, bob %d and carol %d notifications; want [%s], 1 and 0", name, got.Comment.Mentions,
			s.mentionsFor(t, s.bob, got.Comment.ID), s.mentionsFor(t, s.carol, got.Comment.ID), s.bob.id)
	}
}

// P-322 requirement 4, the start-up rebuild: a row whose agent is another identity's label, as a
// server before the register check wrote it, is dropped when serve restarts, with its reason
// logged, while a live row under the registrant's own label is restored. Both processes stay alive
// across the restart, so the label is the only thing that differs.
func TestARestartDropsARowUnderAnothersLabel(t *testing.T) {
	s := startDirectory(t, nil)
	ctx := context.Background()
	own, impostor := s.client(t, "s-alice-own", ""), s.client(t, "s-carol-pre-fix", "")
	s.register(t, s.alice, own, "worker") // under her token's label, test-alice
	s.registerAgent(t, s.carol, impostor, "pre-fix-free")
	s.stop()
	rows, err := s.st.ListWakeSessions(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows %+v, %v; want two", rows, err)
	}
	for _, r := range rows {
		if r.PID == impostor.pid {
			r.AgentID = "test-bob" // what an unchecked register stored for --agent test-bob
			if err := s.st.PutWakeSession(ctx, r); err != nil {
				t.Fatal(err)
			}
		}
	}
	again := startDirectory(t, s)
	if got := again.pids(); !slices.Equal(got, []int64{own.pid}) {
		t.Fatalf("restored pids %v, want only alice's %d", got, own.pid)
	}
	want := fmt.Sprintf(`dropped the row of pid %d at start-up: its agent "test-bob" is another agent's label`, impostor.pid)
	if !strings.Contains(again.logs.String(), want) {
		t.Fatalf("the drop is not logged as %q:\n%s", want, again.logs.String())
	}
	if rows, _ := s.st.ListWakeSessions(ctx); len(rows) != 1 || rows[0].PID != own.pid {
		t.Fatalf("rows after the restart %+v, want alice's alone", rows)
	}
}

// P-345, over the real serve composition and socket: carol cannot register a worker as bob's
// derived agent id, "id:<bob's identity>": FORBIDDEN, naming the identity rule, and no entry for her
// pid, so no presence frame lists her process as bob's agent. Her own "id:<carol's identity>" is the
// positive control and registers.
func TestARegisterAsAnotherIdentitysIDIsRefused(t *testing.T) {
	s := startDirectory(t, nil)
	c, own := s.client(t, "s-carol-as-bob", ""), s.client(t, "s-carol-own-id", "")
	agent := "id:" + s.bob.id
	body := fmt.Sprintf(`{"pid":%d,"role":"worker","agent":%q,"socket":%q,"sessionId":%q}`, c.pid, agent, c.sock, c.session)
	code, got := httpDo(t, unixHTTP(s.sock), http.MethodPost, "http://x/internal/wake-session", s.carol.token, body)
	if code != http.StatusForbidden || !strings.Contains(got, `"FORBIDDEN"`) || !strings.Contains(got, "agent names another identity") {
		t.Fatalf("register pid %d as %q: %d %s, want 403 FORBIDDEN (agent names another identity)", c.pid, agent, code, got)
	}
	if slices.Contains(s.pids(), c.pid) {
		t.Fatalf("refused pid %d is in the directory: %v", c.pid, s.pids())
	}
	s.registerAgent(t, s.carol, own, "id:"+s.carol.id)
	for _, e := range s.a.core.DeviceSessions().Entries() {
		if e.AgentID == agent {
			t.Fatalf("a presence entry names %q: %+v", agent, e)
		}
	}
}

// P-345 requirement 5, the start-up rebuild: a row whose agent is another identity's derived id, as
// a server before the identity check wrote it, is dropped when serve restarts with a logged reason
// that says so, while the registrant's own row is restored.
func TestARestartDropsARowUnderAnotherIdentitysID(t *testing.T) {
	s := startDirectory(t, nil)
	ctx := context.Background()
	own, impostor := s.client(t, "s-alice-own", ""), s.client(t, "s-carol-pre-fix-id", "")
	s.register(t, s.alice, own, "worker")
	s.registerAgent(t, s.carol, impostor, "pre-fix-free")
	s.stop()
	rows, err := s.st.ListWakeSessions(ctx)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows %+v, %v; want two", rows, err)
	}
	agent := "id:" + s.bob.id
	for _, r := range rows {
		if r.PID == impostor.pid {
			r.AgentID = agent // what an unchecked register stored for --agent id:<bob>
			if err := s.st.PutWakeSession(ctx, r); err != nil {
				t.Fatal(err)
			}
		}
	}
	again := startDirectory(t, s)
	if got := again.pids(); !slices.Equal(got, []int64{own.pid}) {
		t.Fatalf("restored pids %v, want only alice's %d", got, own.pid)
	}
	want := fmt.Sprintf(`dropped the row of pid %d at start-up: its agent %q names another identity`, impostor.pid, agent)
	if !strings.Contains(again.logs.String(), want) {
		t.Fatalf("the drop is not logged as %q:\n%s", want, again.logs.String())
	}
}
