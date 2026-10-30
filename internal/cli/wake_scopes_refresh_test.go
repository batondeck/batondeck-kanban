package cli

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/wake"
)

// These run with the production 60 s sweep (dirOpts.sweep zero), so a doorbell heard within a few
// seconds of a membership change was routed by the re-read that change triggered, not by the sweep.

// P-229 AC1 (tested): carol, a registered worker of nothing, is added to P and hears the broadcast
// of bob's next unassigned task within the slow window; removed, she hears none, and her entry stays.
func TestDirectoryRescopesAWorkerOnAMembershipChange(t *testing.T) {
	t.Parallel()
	s := startDirectoryWith(t, nil, dirOpts{windows: shortSlow})
	c := s.client(t, "s-carol", "")
	s.register(t, s.carol, c, "worker")
	ctx := context.Background()
	if _, err := s.a.core.Projects().AddMember(ctx, s.alice.caller(), service.AddMemberReq{ProjectID: s.project, IdentityID: s.carol.id, Role: domain.RoleWorker}); err != nil {
		t.Fatal(err)
	}
	added := time.Now()
	s.task(t, s.bob, "")
	if f := content(t, c.frame(t, s.win.Slow+time.Second)); f.Kind != wake.KindBroadcast || f.ProjectID != s.project {
		t.Fatalf("after the add: %+v, want P's broadcast", f)
	}
	t.Logf("P's broadcast reached carol %v after she was added", time.Since(added).Round(time.Millisecond))
	if _, err := s.a.core.Projects().RemoveMember(ctx, s.alice.caller(), service.RemoveMemberReq{ProjectID: s.project, IdentityID: s.carol.id}); err != nil {
		t.Fatal(err)
	}
	s.waitLog(t, fmt.Sprintf("the scopes of pid %d changed", c.pid), 2)
	s.task(t, s.bob, "")
	c.none(t, s.win.Slow+500*time.Millisecond)
	if got := s.pids(); len(got) != 1 || got[0] != c.pid {
		t.Fatalf("the re-read dropped the entry: %v", got)
	}
}

// P-229 AC2 (tested): carol, a registered master of nothing, is added to P as a master and hears a
// changed doorbell for P when alice files a task there.
func TestDirectoryRescopesAMasterOnAMembershipChange(t *testing.T) {
	t.Parallel()
	s := startDirectoryWith(t, nil, dirOpts{windows: shortSlow})
	c := s.client(t, "s-carol", "")
	s.register(t, s.carol, c, "master")
	if _, err := s.a.core.Projects().AddMember(context.Background(), s.alice.caller(), service.AddMemberReq{ProjectID: s.project, IdentityID: s.carol.id, Role: domain.RoleMaster}); err != nil {
		t.Fatal(err)
	}
	s.task(t, s.alice, "")
	if f := content(t, c.frame(t, s.win.Slow+time.Second)); f.Kind != wake.KindChanged || f.ProjectID != s.project {
		t.Fatalf("after the add: %+v, want P's changed", f)
	}
}

// P-331 AC1 (tested): alice, a registered worker, creates project Q and its board and adds bob, and
// hears the broadcast of the unassigned task bob files in Q; deleting Q re-reads her scopes again.
func TestDirectoryRescopesOnAProjectCreatedAfterRegister(t *testing.T) {
	t.Parallel()
	s := startDirectoryWith(t, nil, dirOpts{windows: shortSlow})
	c := s.client(t, "s-alice", "")
	s.register(t, s.alice, c, "worker")
	ctx := context.Background()
	q, err := s.a.core.Projects().Create(ctx, s.alice.caller(), service.CreateProjectReq{Name: "Q"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.a.core.Boards().Create(ctx, s.alice.caller(), service.CreateBoardReq{ProjectID: q.ProjectID, Name: "QB"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.a.core.Projects().AddMember(ctx, s.alice.caller(), service.AddMemberReq{ProjectID: q.ProjectID, IdentityID: s.bob.id, Role: domain.RoleMaster}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.a.core.Tasks().Create(ctx, s.bob.caller(), service.CreateTaskReq{ProjectID: q.ProjectID, BoardID: b.Board.ID, Title: "in Q"}); err != nil {
		t.Fatal(err)
	}
	if f := content(t, c.frame(t, s.win.Slow+time.Second)); f.Kind != wake.KindBroadcast || f.ProjectID != q.ProjectID {
		t.Fatalf("after Q's task: %+v, want Q's broadcast", f)
	}
	if _, err := s.a.core.Projects().Delete(ctx, s.alice.caller(), service.DeleteProjectReq{ProjectID: q.ProjectID}); err != nil {
		t.Fatal(err)
	}
	s.waitLog(t, fmt.Sprintf("the scopes of pid %d changed", c.pid), 2) // gained Q, then lost it
}

// P-229 requirement 3 (tested): a Register whose scopes were read before a membership commit, and
// whose entry is put after the re-read that commit triggered has listed the entries, is re-read
// too, so carol hears P at once and not at the next sweep. carol's register is held between her
// ListProjects and its return while alice adds her to P and the loop's re-read reaches bob's entry.
func TestDirectoryRescopesARegisterThatRacedTheReRead(t *testing.T) {
	t.Parallel()
	s := startDirectoryWith(t, nil, dirOpts{windows: shortSlow})
	other := s.client(t, "s-bob", "")
	s.register(t, s.bob, other, "worker")
	c := s.client(t, "s-carol", "")
	held, release, swept := make(chan struct{}), make(chan struct{}), make(chan struct{}, 1)
	var first atomic.Bool
	hook := func(identity string) {
		switch identity {
		case s.carol.id:
			if first.CompareAndSwap(false, true) { // only Register's read; the loop's re-read comes after release
				close(held)
				<-release
			}
		case s.bob.id:
			select {
			case swept <- struct{}{}:
			default:
			}
		}
	}
	s.st.listed.Store(&hook)
	t.Cleanup(func() { s.st.listed.Store(nil) })
	registered := make(chan error, 1)
	go func() {
		body := fmt.Sprintf(`{"pid":%d,"role":"worker","socket":%q,"sessionId":%q}`, c.pid, c.sock, c.session)
		req, _ := http.NewRequest(http.MethodPost, "http://x/internal/wake-session", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+s.carol.token)
		resp, err := unixHTTP(s.sock).Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode != http.StatusCreated {
				err = fmt.Errorf("register: %d", resp.StatusCode)
			}
		}
		registered <- err
	}()
	select {
	case <-held:
	case <-time.After(5 * time.Second):
		t.Fatal("carol's register never read her scopes")
	}
	select { // drain anything bob's own register left
	case <-swept:
	default:
	}
	if _, err := s.a.core.Projects().AddMember(context.Background(), s.alice.caller(), service.AddMemberReq{ProjectID: s.project, IdentityID: s.carol.id, Role: domain.RoleWorker}); err != nil {
		close(release)
		t.Fatal(err)
	}
	select {
	case <-swept: // the add_member row's re-read listed the entries without carol's
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("the add_member row triggered no re-read")
	}
	close(release)
	if err := <-registered; err != nil {
		t.Fatal(err)
	}
	s.waitLog(t, fmt.Sprintf("the scopes of pid %d changed", c.pid), 1)
	s.task(t, s.alice, "")
	if f := content(t, c.frame(t, s.win.Slow+time.Second)); f.Kind != wake.KindBroadcast || f.ProjectID != s.project {
		t.Fatalf("after the raced register: %+v, want P's broadcast", f)
	}
}

// P-229 AC4 (tested): with the production sweep an idle directory of one entry makes none of the
// recheck's reads (GetToken, GetUser, ListProjects) for 2 s: the re-read runs on a membership
// change, not on a timer.
func TestDirectoryIdleIssuesNoRecheck(t *testing.T) {
	t.Parallel()
	s := startDirectoryWith(t, nil, dirOpts{windows: shortSlow})
	c := s.client(t, "s-carol", "")
	s.register(t, s.carol, c, "worker")
	n0 := s.st.rechecks.Load()
	time.Sleep(2 * time.Second)
	if n := s.st.rechecks.Load() - n0; n != 0 {
		t.Fatalf("an idle directory of one entry made %d recheck reads in 2 s, want 0", n)
	}
}
