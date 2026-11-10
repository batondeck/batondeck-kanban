package wake

import (
	"context"
	"log"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/bus"
	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store/memstore"
)

// hookResolver resolves the bearers in callers and refuses every other one UNAUTHENTICATED; before,
// when set, runs ahead of each resolve with the bearer.
type hookResolver struct {
	mu      sync.Mutex
	callers map[string]domain.Caller
	before  func(bearer string)
}

func (r *hookResolver) ResolveCaller(_ context.Context, bearer string) (domain.Caller, error) {
	r.mu.Lock()
	hook := r.before
	r.mu.Unlock()
	if hook != nil {
		hook(bearer)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.callers[bearer]; ok {
		return c, nil
	}
	return domain.Caller{}, domain.NewError(domain.CodeUnauthenticated, "the bearer does not resolve", nil)
}

// memWakeStore is a WakeStore over a map, recording the rows it was asked to drop.
type memWakeStore struct {
	mu      sync.Mutex
	rows    map[string]service.WakeSessionRow
	dropped []string
	n       int
}

func (s *memWakeStore) RegisterWakeSession(_ context.Context, caller domain.Caller, req service.RegisterWakeSessionReq) (service.RegisterWakeSessionResp, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := req.ID
	if id == "" {
		s.n++
		id = "W-" + string(rune('a'+s.n))
	}
	agent := req.Agent
	if agent == "" {
		agent = "id:" + caller.Identity
	}
	s.rows[id] = service.WakeSessionRow{ID: id, IdentityID: caller.Identity, AgentID: agent, PID: req.PID, Role: req.Role, Bearer: req.Bearer, Sealed: true}
	return service.RegisterWakeSessionResp{ID: id, AgentID: agent}, nil
}

func (s *memWakeStore) CheckWakeAgent(context.Context, domain.Caller, string) error { return nil }

func (s *memWakeStore) ListWakeSessions(context.Context) ([]service.WakeSessionRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []service.WakeSessionRow
	for _, r := range s.rows {
		out = append(out, r)
	}
	return out, nil
}

func (s *memWakeStore) SetWakeSessionCursor(context.Context, string, int64) error { return nil }

func (s *memWakeStore) DropWakeSession(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, id)
	s.dropped = append(s.dropped, id)
	return nil
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

// sleeper is a live process of this uid that the test kills at its end.
func sleeper(t *testing.T) int64 {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return int64(cmd.Process.Pid)
}

// A re-register during a recheck (tested): a session re-registers its pid with a new token while
// the sweep is rechecking its old entry, whose revoked bearer no longer resolves. The sweep's drop
// is the old entry's: the fresh one, and its row, stay (the drop went by pid, and removed the fresh
// entry).
func TestASweepDropsTheEntryItRecheckedNotItsReplacement(t *testing.T) {
	alice := domain.Caller{Identity: "U-alice", TokenScope: "U-alice"}
	res := &hookResolver{callers: map[string]domain.Caller{"bd_new": alice}} // bd_old is revoked
	rows := &memWakeStore{rows: map[string]service.WakeSessionRow{}}
	logs := &strings.Builder{}
	var logMu sync.Mutex
	logger := log.New(writerFunc(func(p []byte) (int, error) { logMu.Lock(); defer logMu.Unlock(); return logs.Write(p) }), "", 0)
	log := memstore.New()
	if _, err := log.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	b := bus.New(log, bus.Options{})
	t.Cleanup(b.Close)
	d, err := NewDirectory(Options{Bus: b, Resolver: res, Sessions: rows, Registry: t.TempDir(), Logger: logger,
		Sweep:      20 * time.Millisecond,
		Scopes:     func(context.Context, domain.Caller) ([]string, error) { return []string{"U-alice"}, nil },
		ProcessUID: func(int) (int, error) { return os.Getuid(), nil }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := d.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); <-d.Done() })
	pid := sleeper(t)
	peer := core.Peer{UID: os.Getuid(), PID: os.Getpid()}
	req := RegisterReq{PID: pid, Role: service.RoleWorker}
	reregistered := make(chan error, 1)
	var once sync.Once
	res.mu.Lock()
	res.before = func(bearer string) {
		if bearer == "bd_old" {
			once.Do(func() { // SessionStart again, with a new token, while the sweep reads the old one
				_, err := d.Register(ctx, peer, alice, "bd_new", req)
				reregistered <- err
			})
		}
	}
	res.mu.Unlock()
	if _, err := d.Register(ctx, peer, alice, "bd_old", req); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-reregistered:
		if err != nil {
			t.Fatalf("the re-register: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the sweep never rechecked the old entry")
	}
	time.Sleep(200 * time.Millisecond) // that recheck's verdict, and several sweeps of the fresh entry
	if got := d.Entries(); len(got) != 1 || got[0].PID != pid {
		logMu.Lock()
		defer logMu.Unlock()
		t.Fatalf("entries after the sweep %+v, want the re-registered pid %d; the log:\n%s", got, pid, logs.String())
	}
	rows.mu.Lock()
	defer rows.mu.Unlock()
	if len(rows.rows) != 1 || len(rows.dropped) != 0 {
		t.Fatalf("rows %v, dropped %v; want the one row kept", rows.rows, rows.dropped)
	}
}

// holdingStore holds the register of bearer hold after it has written its row: until a drop has
// run, or for at most wait, or (wait 0) until release is closed.
type holdingStore struct {
	*memWakeStore
	hold            string
	wait            time.Duration
	written         chan struct{}
	release         chan struct{}
	dropRan         chan struct{}
	writeOnce, drop sync.Once
}

func newHoldingStore(hold string, wait time.Duration) *holdingStore {
	return &holdingStore{memWakeStore: &memWakeStore{rows: map[string]service.WakeSessionRow{}}, hold: hold, wait: wait,
		written: make(chan struct{}), release: make(chan struct{}), dropRan: make(chan struct{})}
}

func (s *holdingStore) RegisterWakeSession(ctx context.Context, c domain.Caller, req service.RegisterWakeSessionReq) (service.RegisterWakeSessionResp, error) {
	resp, err := s.memWakeStore.RegisterWakeSession(ctx, c, req)
	if req.Bearer == s.hold {
		s.writeOnce.Do(func() { close(s.written) })
		if s.wait > 0 {
			select {
			case <-s.dropRan:
			case <-time.After(s.wait):
			}
		} else {
			<-s.release
		}
	}
	return resp, err
}

func (s *holdingStore) DropWakeSession(ctx context.Context, id string) error {
	err := s.memWakeStore.DropWakeSession(ctx, id)
	s.drop.Do(func() { close(s.dropRan) })
	return err
}

// testDirectory starts a directory over rows and res whose sweep runs every 20 ms, and stops it
// at the test's end.
func testDirectory(t *testing.T, res *hookResolver, rows service.WakeStore) *Directory {
	t.Helper()
	log := memstore.New()
	if _, err := log.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	b := bus.New(log, bus.Options{})
	t.Cleanup(b.Close)
	d, err := NewDirectory(Options{Bus: b, Resolver: res, Sessions: rows, Registry: t.TempDir(), Logger: testLogger(t),
		Sweep:      20 * time.Millisecond,
		Scopes:     func(context.Context, domain.Caller) ([]string, error) { return []string{"U-alice"}, nil },
		ProcessUID: func(int) (int, error) { return os.Getuid(), nil }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := d.Start(ctx); err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); <-d.Done() })
	return d
}

// testLogger logs into the test's own log, shown when it fails.
func testLogger(t *testing.T) *log.Logger {
	return log.New(writerFunc(func(p []byte) (int, error) { t.Log(strings.TrimSpace(string(p))); return len(p), nil }), "", 0)
}

// waitGone waits up to 5 s for the directory to list no entry.
func waitGone(d *Directory) []service.DeviceSession {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(d.Entries()) == 0 {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return d.Entries()
}

// A re-register still in flight during a recheck (tested): the session re-registers its pid with a
// new token while the sweep rechecks its old entry, and the re-register has written its row (the
// old entry's id: the same process) but not yet put its entry when the old entry's drop runs. The
// drop waits for that register: the fresh row and the pid's exit watch stay, so the entry leaves
// when its process exits (the drop deleted the fresh row and the watch, and the entry stayed
// listed after its process was killed).
func TestADropWaitsForARegisterOfItsPidInFlight(t *testing.T) {
	alice := domain.Caller{Identity: "U-alice", TokenScope: "U-alice"}
	res := &hookResolver{callers: map[string]domain.Caller{"bd_new": alice}} // bd_old is revoked
	rows := newHoldingStore("bd_new", time.Second)
	d := testDirectory(t, res, rows)
	pid := sleeper(t)
	peer := core.Peer{UID: os.Getuid(), PID: os.Getpid()}
	req := RegisterReq{PID: pid, Role: service.RoleWorker}
	reregistered := make(chan error, 1)
	var once sync.Once
	res.mu.Lock()
	res.before = func(bearer string) {
		if bearer == "bd_old" {
			once.Do(func() { // the sweep reads the old bearer while the re-register writes its row
				go func() {
					_, err := d.Register(context.Background(), peer, alice, "bd_new", req)
					reregistered <- err
				}()
				<-rows.written
			})
		}
	}
	res.mu.Unlock()
	if _, err := d.Register(context.Background(), peer, alice, "bd_old", req); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-reregistered:
		if err != nil {
			t.Fatalf("the re-register: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the sweep never rechecked the old entry")
	}
	time.Sleep(100 * time.Millisecond) // the old entry's drop, and several sweeps of the fresh entry
	rows.mu.Lock()
	kept, dropped := len(rows.rows), slices.Clone(rows.dropped)
	rows.mu.Unlock()
	if got := d.Entries(); len(got) != 1 || got[0].PID != pid || kept != 1 || len(dropped) != 0 {
		t.Fatalf("after the old entry's drop: entries %+v, %d row(s), dropped %v; want the re-registered pid %d and its row", got, kept, dropped, pid)
	}
	if err := syscall.Kill(int(pid), syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	if got := waitGone(d); got != nil {
		t.Fatalf("entries %+v 5 s after the process was killed: the pid's exit watch is gone", got)
	}
}

// An exit during a register (tested): the process exits after its register has started watching it
// and before the register has put its entry. The exit's drop waits for that register and drops the
// entry it put (the exit found no entry, and the register then put one for a process that was
// gone, listed until serve stopped).
func TestAnExitDuringItsRegisterDropsTheEntry(t *testing.T) {
	alice := domain.Caller{Identity: "U-alice", TokenScope: "U-alice"}
	res := &hookResolver{callers: map[string]domain.Caller{"bd": alice}}
	rows := newHoldingStore("bd", 0)
	d := testDirectory(t, res, rows)
	pid := sleeper(t)
	registered := make(chan error, 1)
	go func() {
		_, err := d.Register(context.Background(), core.Peer{UID: os.Getuid(), PID: os.Getpid()}, alice, "bd", RegisterReq{PID: pid, Role: service.RoleWorker})
		registered <- err
	}()
	<-rows.written
	if err := syscall.Kill(int(pid), syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // the dispatcher reads the exit
	close(rows.release)
	if err := <-registered; err != nil {
		t.Fatalf("the register: %v", err)
	}
	if got := waitGone(d); got != nil {
		t.Fatalf("entries %+v 5 s after the process exited during its register", got)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		rows.mu.Lock()
		left := len(rows.rows)
		rows.mu.Unlock()
		if left == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d row(s) 5 s after the process exited; want none", left)
		}
	}
}
