package cli

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/wake"
)

// R2.1 AC2 (ADR-0004): a comment rings nobody, neither the assignee's worker nor a master of the
// project; a follow-up rings its target's worker assigned within a second of the call (the
// positive control, and the measurement).
func TestDirectoryFollowUpRingsItsTargetAndACommentNobody(t *testing.T) {
	t.Parallel()
	s := startDirectoryWith(t, nil, dirOpts{windows: shortSlow})
	w, m := s.client(t, "s-alice-worker", ""), s.client(t, "s-alice-master", "")
	s.register(t, s.alice, w, "worker")
	s.register(t, s.alice, m, "master")
	ctx := context.Background()
	task := s.task(t, s.bob, s.alice.id)
	// The create's broadcast and the assignment ring the worker, and bob's changes the master; let
	// both fall quiet before the comment.
	var kinds []string
	for _, c := range []*client{w, m} {
		for quiet := false; !quiet; {
			select {
			case b := <-c.got:
				kinds = append(kinds, content(t, b).Kind)
			case <-time.After(s.win.Slow + 500*time.Millisecond):
				quiet = true
			}
		}
	}
	if !slices.Contains(kinds, wake.KindAssigned) {
		t.Fatalf("the assignment rang %v, want an assigned among them", kinds)
	}

	before := s.writesAfter(t, len(kinds))
	if _, err := s.a.core.Comments().AddComment(ctx, s.bob.caller(), service.AddCommentReq{ProjectID: s.project, TaskID: task.ID, Body: "@alice looks good"}); err != nil {
		t.Fatal(err)
	}
	if !s.changeOf(t, "add_comment") {
		t.Fatal("add_comment wrote no change row: the silence would prove nothing")
	}
	w.none(t, s.win.Slow+500*time.Millisecond)
	m.none(t, 0)
	if n := s.a.dir.Writes() - before; n != 0 {
		t.Fatalf("the comment wrote %d doorbells, want 0", n)
	}

	start := time.Now()
	if _, err := s.a.core.FollowUps().AddFollowUp(ctx, s.bob.caller(), service.AddFollowUpReq{ProjectID: s.project, TaskID: task.ID, Body: "please add a test"}); err != nil {
		t.Fatal(err)
	}
	f := content(t, w.frame(t, time.Second))
	took := time.Since(start)
	if f.Kind != wake.KindAssigned || f.ProjectID != s.project {
		t.Fatalf("the follow-up rang alice's worker %+v, want assigned on %s", f, s.project)
	}
	t.Logf("add_follow_up to alice rang her worker pid %d assigned in %v", w.pid, took)
	// The dispatcher's own record of both rings of the worker: the assignment and the follow-up.
	s.waitLog(t, fmt.Sprintf("wake: rang pid %d (worker) assigned", w.pid), 2)
}
