package wake

import (
	"context"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
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

// core-cli-ops#12 (tested): a session re-registers its pid with a new token while the sweep is
// rechecking its old entry, whose revoked bearer no longer resolves. The sweep's drop is the old
// entry's: the fresh one, and its row, stay (the drop went by pid, and removed the fresh entry).
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
