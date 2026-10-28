package cli

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/wake"
)

// quiet drains each of cs, at once, until nothing has rung it for a slow window, so the next ring
// is the next call's.
func quiet(cs ...*client) {
	var wg sync.WaitGroup
	for _, c := range cs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-c.got:
				case <-time.After(wake.SlowWindow + 500*time.Millisecond):
					return
				}
			}
		}()
	}
	wg.Wait()
}

// registerAgent registers c as a worker for p under the agent label agent, over the Unix socket.
func (s *dirServer) registerAgent(t *testing.T, p person, c *client, agent string) {
	t.Helper()
	body := fmt.Sprintf(`{"pid":%d,"role":"worker","agent":%q,"socket":%q,"sessionId":%q}`, c.pid, agent, c.sock, c.session)
	if code, got := httpDo(t, unixHTTP(s.sock), http.MethodPost, "http://x/internal/wake-session", p.token, body); code != http.StatusCreated {
		t.Fatalf("register pid %d as %s: %d %s", c.pid, agent, code, got)
	}
}

// mentionsFor is how many mention notifications p's feed holds for the comment id.
func (s *dirServer) mentionsFor(t *testing.T, p person, commentID string) int {
	t.Helper()
	page, err := s.st.ListNotifications(context.Background(), p.id, store.NotificationsQuery{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, r := range page.Items {
		if r.Type == domain.NotificationMention && string(r.Data) == fmt.Sprintf(`{"commentId":%q}`, commentID) {
			n++
		}
	}
	return n
}

// P-310 (owner decision 5, the exception to ADR-0004's "comments ring nobody"): a comment that
// mentions a member with a live worker session on this device, by @login or by the worker's
// @label, rings that worker assigned once, and writes the member its mention notification; the
// member's master stays silent. A self-mention, a mention of a member with no worker (a human), a
// name that is no member and a non-member ring nobody and do not error. The positive control is the
// first ring; the dispatcher's write count says the silences are silences, not slow rings.
func TestDirectoryMentionRingsTheMentionedWorkerOnly(t *testing.T) {
	s := startDirectory(t, nil)
	ctx := context.Background()
	if _, err := s.st.AddMember(ctx, s.project, s.carol.id, domain.RoleMember, s.alice.id); err != nil {
		t.Fatal(err) // carol: a member with no session, a human at the web UI
	}
	w, m := s.client(t, "s-alice-worker", ""), s.client(t, "s-alice-master", "")
	s.registerAgent(t, s.alice, w, "claude-rev")
	s.register(t, s.alice, m, "master")
	task := s.task(t, s.bob, "") // unassigned: the mention is the only thing that names alice
	quiet(w, m)

	comment := func(by person, body string) service.Comment {
		t.Helper()
		got, err := s.a.core.Comments().AddComment(ctx, by.caller(), service.AddCommentReq{ProjectID: s.project, TaskID: task.ID, Body: body})
		if err != nil {
			t.Fatalf("%q: %v", body, err)
		}
		return got.Comment
	}

	// The positive control: bob mentions alice by login.
	before := s.a.dir.Writes()
	start := time.Now()
	c := comment(s.bob, "@alice please look at the retry")
	f := content(t, w.frame(t, time.Second))
	took := time.Since(start)
	if f.Kind != wake.KindAssigned || f.ProjectID != s.project {
		t.Fatalf("bob's @alice rang her worker %+v, want assigned on %s", f, s.project)
	}
	w.none(t, wake.SlowWindow+500*time.Millisecond)
	m.none(t, 0)
	if n := s.a.dir.Writes() - before; n != 1 {
		t.Fatalf("bob's @alice wrote %d doorbells, want 1 (her worker's)", n)
	}
	if len(c.Mentions) != 1 || c.Mentions[0] != s.alice.id || s.mentionsFor(t, s.alice, c.ID) != 1 {
		t.Fatalf("bob's @alice: mentions %v, %d notifications for alice; want [%s] and 1", c.Mentions, s.mentionsFor(t, s.alice, c.ID), s.alice.id)
	}
	t.Logf("bob's @alice rang her worker pid %d %s in %v (frame %+v) and wrote her a mention notification", w.pid, f.Kind, took, f)

	// By the worker's label: the same member, the same one ring.
	before = s.a.dir.Writes()
	c = comment(s.bob, "@claude-rev over to you")
	if f := content(t, w.frame(t, time.Second)); f.Kind != wake.KindAssigned {
		t.Fatalf("bob's @claude-rev rang %+v, want assigned", f)
	}
	w.none(t, wake.SlowWindow+500*time.Millisecond)
	m.none(t, 0)
	if n := s.a.dir.Writes() - before; n != 1 || len(c.Mentions) != 1 || c.Mentions[0] != s.alice.id {
		t.Fatalf("bob's @claude-rev: %d doorbells, mentions %v; want 1 and [%s]", n, c.Mentions, s.alice.id)
	}

	// The silences: a self-mention (by login and by label), a human member, a name that is no
	// member, and a non-member.
	before = s.a.dir.Writes()
	self := comment(s.alice, "@alice @claude-rev note to self")
	human := comment(s.bob, "@carol can you confirm")
	comment(s.bob, "@nobody @dave hello")
	if err := s.st.RemoveMember(ctx, s.project, s.carol.id); err != nil {
		t.Fatal(err)
	}
	gone := comment(s.bob, "@carol are you still here")
	w.none(t, wake.SlowWindow+500*time.Millisecond)
	m.none(t, 0)
	if n := s.a.dir.Writes() - before; n != 0 {
		t.Fatalf("the self-mention, the human, the unknown names and the non-member wrote %d doorbells, want 0", n)
	}
	if len(self.Mentions) != 1 || s.mentionsFor(t, s.alice, self.ID) != 0 {
		t.Fatalf("alice's self-mention: mentions %v, %d notifications for her; want her own and 0", self.Mentions, s.mentionsFor(t, s.alice, self.ID))
	}
	if len(human.Mentions) != 1 || human.Mentions[0] != s.carol.id || s.mentionsFor(t, s.carol, human.ID) != 1 {
		t.Fatalf("bob's @carol: mentions %v, %d notifications for carol; want [%s] and 1", human.Mentions, s.mentionsFor(t, s.carol, human.ID), s.carol.id)
	}
	if len(gone.Mentions) != 0 {
		t.Fatalf("@carol after she left: mentions %v, want none", gone.Mentions)
	}
	s.waitLog(t, fmt.Sprintf("wake: rang pid %d (worker) assigned", w.pid), 2)
}
