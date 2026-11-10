package cli

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/wake"
)

// A restart with many catch-ups (tested): serve restarts with more restored entries that each have
// a doorbell to catch up on than the dispatcher's catch-up channel holds (16). Start restored every
// row before it started the loop that reads that channel, so the 17th catch-up blocked Start, and
// with it serve, until SIGTERM: no /healthz, no /mcp, no UI. Now Start returns, serve answers, and
// every restored entry is rung once with what it missed.
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

// P-167 (tested): serves on two devices share one database (Postgres). A
// directory row names the device whose serve registered it, and a serve starts from its own
// device's rows alone: it neither restores another device's row whose pid happens to be live here
// (a phantom entry) nor drops one whose pid is not a process here (a session that device still
// rings), and a session here registers a pid number another device's row holds. Before, every
// serve restored or dropped every row by pid, and one pid number was the database's once.
func TestADirectoryLeavesAnotherDevicesRows(t *testing.T) {
	a := startDirectoryWith(t, nil, dirOpts{device: "dev-a"})
	here, gone := a.client(t, "s-alice-a", ""), a.client(t, "s-alice-a-gone", "")
	a.register(t, a.alice, here, "worker")
	a.register(t, a.alice, gone, "worker")
	a.stop()
	_ = gone.cmd.Process.Kill() // exits while no serve of dev-a runs: its row stays until dev-a starts
	_ = gone.cmd.Wait()
	b := startDirectoryWith(t, a, dirOpts{device: "dev-b"})
	if got := b.pids(); len(got) != 0 {
		t.Fatalf("dev-b restored %v from dev-a's rows, want nothing", got)
	}
	if rows := b.rows(t); len(rows) != 2 {
		t.Fatalf("after dev-b's start-up: rows %+v, want dev-a's two kept", rows)
	}
	b.register(t, b.alice, here, "worker") // the same pid number as dev-a's row
	if rows := b.rows(t); len(rows) != 3 {
		t.Fatalf("after dev-b's register: rows %+v, want dev-a's two and dev-b's", rows)
	}
	again := startDirectoryWith(t, a, dirOpts{device: "dev-a"})
	if got := again.pids(); !slices.Equal(got, []int64{here.pid}) {
		t.Fatalf("dev-a restored %v, want its live %d", got, here.pid)
	}
	var pids []int64
	for _, r := range again.rows(t) {
		pids = append(pids, r.PID)
	}
	if !slices.Equal(pids, []int64{here.pid, here.pid}) {
		t.Fatalf("rows' pids %v after dev-a's restart: want dev-a's and dev-b's rows of %d, the exited %d's dropped", pids, here.pid, gone.pid)
	}
}
