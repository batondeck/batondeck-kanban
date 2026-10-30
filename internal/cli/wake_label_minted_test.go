package cli

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/wake"
)

// P-344 (REVIEW-343-322-fable A5, its probe 3 adopted), over the real serve composition and socket:
// carol's worker registered under the free name late-x, which bob then mints. Her re-register is
// refused, and the delivery's recheck judges the live entry as a restart would: a task assigned to
// late-x writes nothing to her socket, and her entry and its row are dropped with restore's reason.
func TestAWorkerUnderANameAnotherIdentityMintedIsDroppedAtDelivery(t *testing.T) {
	s := startDirectory(t, nil)
	ctx := context.Background()
	if _, err := s.st.AddMember(ctx, s.project, s.carol.id, domain.RoleWorker, s.alice.id); err != nil {
		t.Fatal(err)
	}
	name := "late-x"
	c := s.client(t, "s-carol-late", "")
	s.registerAgent(t, s.carol, c, name)
	if _, _, err := (&auth.TokenService{Store: s.st}).Create(ctx, s.bob.id, "", name, time.Hour); err != nil {
		t.Fatalf("bob mints %s: %v", name, err)
	}
	body := fmt.Sprintf(`{"pid":%d,"role":"worker","agent":%q,"socket":%q,"sessionId":%q}`, c.pid, name, c.sock, c.session)
	code, got := httpDo(t, unixHTTP(s.sock), http.MethodPost, "http://x/internal/wake-session", s.carol.token, body)
	if code != http.StatusForbidden {
		t.Fatalf("re-register under the now-minted %s: %d %s, want 403", name, code, got)
	}
	if !slices.Contains(s.pids(), c.pid) {
		t.Fatalf("pid %d left the directory at the refused re-register: %v; the delivery is what this checks", c.pid, s.pids())
	}
	quiet(c)
	s.task(t, s.alice, name)
	select {
	case raw := <-c.got:
		t.Fatalf("a task assigned to bob's %s rang carol's pid %d: %+v", name, c.pid, content(t, raw))
	case <-time.After(s.win.AssignedMaxHold + time.Second):
	}
	if slices.Contains(s.pids(), c.pid) {
		t.Fatalf("carol's pid %d is still in the directory under bob's %s: %v", c.pid, name, s.pids())
	}
	s.waitLog(t, fmt.Sprintf("wake: dropped pid %d (worker, %s): its agent %q is another agent's label", c.pid, s.carol.id, name), 1)
	rows, err := s.st.ListWakeSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.PID == c.pid {
			t.Fatalf("carol's row %+v outlived the drop", r)
		}
	}
}

// P-344 requirement 3, the control: bob's worker under his own token's label is rung for a task
// assigned to it, and that delivery reads no token row: the mint refuses a label another identity
// holds, so the own label is not judged again.
func TestAWorkerUnderItsOwnLabelIsRungWithNoTokenRead(t *testing.T) {
	s := startDirectory(t, nil)
	b := s.client(t, "s-bob-own", "")
	s.register(t, s.bob, b, "worker") // under his token's label, test-bob
	quiet(b)
	task := s.task(t, s.bob, "")
	quiet(b)
	n0 := s.st.tokenLists.Load()
	s.assign(t, s.alice, task, "test-bob")
	n1 := s.st.tokenLists.Load() // what the assign itself read, before the hold releases the frame
	if f := content(t, b.frame(t, s.win.AssignedMaxHold+time.Second)); f.Kind != wake.KindAssigned {
		t.Fatalf("a task assigned to test-bob rang bob's worker %+v, want assigned", f)
	}
	if n := s.st.tokenLists.Load() - n1; n != 0 {
		t.Fatalf("the delivery to bob's own label read the token rows %d times, want 0 (the assign read them %d times)", n, n1-n0)
	}
}
