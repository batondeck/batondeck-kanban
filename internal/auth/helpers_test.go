package auth

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// testArgon keeps the tests fast; the hash format and the verification path are the real ones.
var testArgon = Argon2Params{Time: 1, Memory: 64, Threads: 1}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type fixture struct {
	st    store.Store
	clk   *clock
	svc   *Service
	audit []AuditEvent
	mu    sync.Mutex
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "auth.db"), "FULL")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	f := &fixture{st: st, clk: &clock{t: time.UnixMilli(1_800_000_000_000)}}
	f.svc = &Service{
		Accounts: &Accounts{Store: st, Now: f.clk.now, Params: testArgon, Limits: &LoginLimits{}},
		Tokens:   &TokenService{Store: st, Now: f.clk.now},
		Sessions: st,
		Now:      f.clk.now,
		Audit: func(_ context.Context, e AuditEvent) {
			f.mu.Lock()
			f.audit = append(f.audit, e)
			f.mu.Unlock()
		},
	}
	return f
}

func (f *fixture) audited() []AuditEvent {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]AuditEvent(nil), f.audit...)
}

func (f *fixture) addUser(t *testing.T, login, password string) store.User {
	t.Helper()
	u, err := f.svc.Accounts.Add(context.Background(), login, "", password, false)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func wantCode(t *testing.T, err error, code domain.Code) {
	t.Helper()
	e, ok := domain.AsBatonDeckError(err)
	if !ok || e.Code != code {
		t.Fatalf("error %v, want %s", err, code)
	}
}
