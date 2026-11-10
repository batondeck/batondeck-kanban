package cli

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/wake"
)

// core-cli-ops#1 (tested): serve restarts with more restored entries that each have a doorbell to
// catch up on than the dispatcher's catch-up channel holds (16). Start restored every row before
// it started the loop that reads that channel, so the 17th catch-up blocked Start, and with it
// serve, until SIGTERM: no /healthz, no /mcp, no UI. Now Start returns, serve answers, and every
// restored entry is rung once with what it missed.
func TestARestartWithMoreCatchUpsThanTheDispatcherBuffers(t *testing.T) {
	s := startDirectory(t, nil)
	ctx := context.Background()
	s.task(t, s.alice, "") // a change before the sessions start: the cursor they were last rung at
	rung := s.lastChange(t)
	const n = 20
	clients := make([]*client, n)
	for i := range clients {
		clients[i] = s.client(t, fmt.Sprintf("s-many-%d", i), "")
		s.register(t, s.alice, clients[i], "worker")
	}
	s.task(t, s.alice, "") // open work in P: a broadcast row every worker misses
	s.stop()               // inside the broadcast window, so nobody was rung
	rows, err := s.st.ListWakeSessions(ctx)
	if err != nil || len(rows) != n {
		t.Fatalf("rows %d, %v; want %d", len(rows), err, n)
	}
	for _, r := range rows {
		// Each was last rung before the broadcast row, as a session rung earlier would have been.
		if err := s.st.SetWakeSessionCursor(ctx, r.ID, rung, time.Now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	again := startDirectoryWith(t, s, dirOpts{windows: shortSlow}) // its /healthz answering is the check
	if got := len(again.pids()); got != n {
		t.Fatalf("restored %d entries, want %d", got, n)
	}
	for _, c := range clients {
		if f := content(t, c.frame(t, again.win.Slow+2*time.Second)); f.Kind != wake.KindBroadcast || f.ProjectID != s.project {
			t.Fatalf("pid %d caught up with %+v, want P's broadcast", c.pid, f)
		}
	}
}
