package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// barrierStore parks each transaction's ListMembers at a two-party barrier: two member mutations
// that both read the member list before either writes meet there and go on together, the
// interleaving that loses the last-admin guard. A transaction that waits alone (the other is held
// on the project lock) gives up after a short wait and breaks the barrier for the rest.
type barrierStore struct {
	store.Store
	b *barrier
}

type barrier struct {
	mu      sync.Mutex
	arrived int
	open    chan struct{}
	broken  bool
}

func (b *barrier) wait() {
	b.mu.Lock()
	if b.broken {
		b.mu.Unlock()
		return
	}
	b.arrived++
	if b.arrived == 2 {
		close(b.open)
		b.mu.Unlock()
		return
	}
	b.mu.Unlock()
	select {
	case <-b.open:
	case <-time.After(3 * time.Second):
		b.mu.Lock()
		b.broken = true
		b.mu.Unlock()
	}
}

func (s *barrierStore) Atomically(ctx context.Context, fn func(tx store.Store) error) error {
	return s.Store.Atomically(ctx, func(tx store.Store) error { return fn(&barrierStore{Store: tx, b: s.b}) })
}

func (s *barrierStore) ListMembers(ctx context.Context, projectID string) ([]store.Member, error) {
	if s.b != nil {
		s.b.wait()
	}
	return s.Store.ListMembers(ctx, projectID)
}

// Two admins removing (or demoting) each other at once leave the project one admin: the last-admin
// guard reads the member list and writes one row, so the two mutations are serialised on the
// project lock. Without it, on Postgres (READ COMMITTED, two different rows) both counted two admins
// and both committed: a project with no admin.
func TestLastAdminGuardIsSerialised(t *testing.T) {
	for _, e := range engines {
		for _, demote := range []bool{false, true} {
			name := e.name + "/remove"
			if demote {
				name = e.name + "/demote"
			}
			t.Run(name, func(t *testing.T) {
				ctx := context.Background()
				st := e.open(t)
				carol := domain.Caller{Identity: "carol", TokenScope: "carol"}
				dave := domain.Caller{Identity: "dave", TokenScope: "dave"}
				setup := New(st, Options{ToolCallFlush: -1})
				p, err := setup.Projects().Create(ctx, carol, CreateProjectReq{Name: "Q"})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := setup.Projects().AddMember(ctx, carol, AddMemberReq{ProjectID: p.ProjectID, IdentityID: "dave", Role: domain.RoleAdmin}); err != nil {
					t.Fatal(err)
				}
				_ = setup.Close(ctx)
				c := New(&barrierStore{Store: st, b: &barrier{open: make(chan struct{})}}, Options{ToolCallFlush: -1})
				t.Cleanup(func() { _ = c.Close(context.Background()) })
				var wg sync.WaitGroup
				errs := make([]error, 2)
				for i, pair := range [][2]domain.Caller{{carol, dave}, {dave, carol}} {
					wg.Add(1)
					go func() {
						defer wg.Done()
						if demote {
							_, errs[i] = c.Projects().AddMember(ctx, pair[0], AddMemberReq{ProjectID: p.ProjectID, IdentityID: pair[1].Identity, Role: domain.RoleMember})
						} else {
							_, errs[i] = c.Projects().RemoveMember(ctx, pair[0], RemoveMemberReq{ProjectID: p.ProjectID, IdentityID: pair[1].Identity})
						}
					}()
				}
				wg.Wait()
				members, err := st.ListMembers(ctx, p.ProjectID)
				if err != nil {
					t.Fatal(err)
				}
				admins := 0
				for _, m := range members {
					if m.Role == domain.RoleAdmin {
						admins++
					}
				}
				t.Logf("errors %v; members %+v", errs, members)
				if admins != 1 {
					t.Fatalf("%d admins after the two calls, want 1", admins)
				}
				ok := 0
				for _, err := range errs {
					if err == nil {
						ok++
						continue
					}
					if e, isBD := domain.AsBatonDeckError(err); !isBD || e.Code != domain.CodeValidation {
						t.Fatalf("error %v, want VALIDATION", err)
					}
				}
				if ok != 1 {
					t.Fatalf("%d calls succeeded, want exactly one", ok)
				}
			})
		}
	}
}
