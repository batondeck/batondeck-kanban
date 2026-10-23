package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/memstore"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// counted is a change log whose reads are counted: every ChangeLog call is one statement on the
// engines (changelog.go), so the count is the tail's statement count.
type counted struct {
	store.ChangeLog
	reads atomic.Int64
}

func (c *counted) ReadChanges(ctx context.Context, since int64, limit int) ([]store.Change, error) {
	c.reads.Add(1)
	return c.ChangeLog.ReadChanges(ctx, since, limit)
}

func (c *counted) ReadChangesForScope(ctx context.Context, scope string, since int64, limit int) ([]store.Change, error) {
	c.reads.Add(1)
	return c.ChangeLog.ReadChangesForScope(ctx, scope, since, limit)
}

// backend is a store the bus is judged over, with the wake it gives the bus (nil: memstore polls).
type backend struct {
	name  string
	store store.Store
	watch store.ChangeWatcher
}

func memBackend(t *testing.T) backend {
	s := memstore.New()
	t.Cleanup(func() { s.Close() })
	if _, err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return backend{"memstore", s, nil}
}

func sqliteBackend(t *testing.T) backend {
	s := storetest.SQLite(t)
	return backend{"sqlite", s, s}
}

func postgresBackend(t *testing.T) backend {
	s := storetest.Postgres(t)
	return backend{"postgres", s, s}
}

type opener struct {
	name string
	open func(*testing.T) backend
}

var (
	engines  = []opener{{"sqlite", sqliteBackend}, {"postgres", postgresBackend}}
	backends = append([]opener{{"memstore", memBackend}}, engines...)
)

// each runs f over memstore and both engines; the Postgres leg skips without its DSN.
func each(t *testing.T, f func(t *testing.T, b backend)) {
	for _, o := range backends {
		t.Run(o.name, func(t *testing.T) { f(t, o.open(t)) })
	}
}

func newBus(t *testing.T, b backend, opts Options) (*Bus, *counted) {
	t.Helper()
	log := &counted{ChangeLog: b.store}
	opts.Watcher = b.watch
	if opts.OnError == nil {
		opts.OnError = func(err error) { t.Errorf("bus: %v", err) }
	}
	bus := New(log, opts)
	t.Cleanup(bus.Close)
	return bus, log
}

func change(scope string, n int) store.ChangeInput {
	return store.ChangeInput{Scope: scope, ProjectID: "P-1", Kind: "changed", Payload: json.RawMessage(fmt.Sprintf(`{"n":%d}`, n)), Ts: time.Now().UnixMilli()}
}

// write appends the changes in one transaction, the way service.mutate writes them.
func write(t *testing.T, s store.Store, in ...store.ChangeInput) []int64 {
	t.Helper()
	var ids []int64
	err := s.Atomically(context.Background(), func(tx store.Store) error {
		for _, c := range in {
			id, err := tx.AppendChange(context.Background(), c)
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return ids
}

func subscribe(t *testing.T, bus *Bus, buffer int, scopes ...string) *Subscription {
	t.Helper()
	s, err := bus.Subscribe(context.Background(), scopes, buffer)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// next is the subscription's next change, or a failure after within.
func next(t *testing.T, s *Subscription, within time.Duration) store.Change {
	t.Helper()
	select {
	case c, ok := <-s.C():
		if !ok {
			t.Fatal("the subscription closed")
		}
		return c
	case <-time.After(within):
		t.Fatalf("no change within %v", within)
	}
	return store.Change{}
}

func quiet(t *testing.T, s *Subscription, within time.Duration) {
	t.Helper()
	select {
	case c := <-s.C():
		t.Fatalf("an unexpected change: %+v", c)
	case <-time.After(within):
	}
}

// AC1: 200 subscribers held for 10 s with no write cost 0 statements after the tail finds the end
// of the log; one write, through a transaction as service.mutate writes it, reaches all 200 within
// 500 ms — on SQLite by its in-process commit wake, on Postgres by NOTIFY.
func TestIdle(t *testing.T) {
	for _, o := range engines {
		t.Run(o.name, func(t *testing.T) {
			b := o.open(t)
			write(t, b.store, change("project:P-1", 0), change("project:P-1", 1)) // a log that is not empty
			bus, log := newBus(t, b, Options{})
			subs := make([]*Subscription, 200)
			for i := range subs {
				subs[i] = subscribe(t, bus, 0, "project:P-1", fmt.Sprintf("agent:a%d", i))
			}
			tail := log.reads.Load()
			time.Sleep(10 * time.Second)
			idle := log.reads.Load() - tail
			t.Logf("200 subscribers: %d statement(s) to find the end of the log, %d in 10 s idle", tail, idle)
			if idle != 0 {
				t.Fatalf("an idle tail issued %d statements in 10 s, want 0", idle)
			}
			began := time.Now()
			id := write(t, b.store, change("project:P-1", 2))[0]
			var worst time.Duration
			for _, s := range subs {
				if c := next(t, s, 500*time.Millisecond-time.Since(began)); c.ID != id {
					t.Fatalf("got change %d, want %d", c.ID, id)
				}
				worst = max(worst, time.Since(began))
			}
			t.Logf("one write: all 200 received it within %v (%d statement(s) to read it)", worst, log.reads.Load()-tail)
		})
	}
}

// AC2: a subscriber that never reads does not delay one that does: the fast one receives all
// 1,000 changes while the slow one's buffer overflows and its drop counter counts the rest.
func TestASlowSubscriberDoesNotDelayAFastOne(t *testing.T) {
	each(t, func(t *testing.T, b backend) {
		bus, _ := newBus(t, b, Options{})
		slow := subscribe(t, bus, 8, "project:P-1")
		fast := subscribe(t, bus, 1024, "project:P-1")
		in := make([]store.ChangeInput, 1000)
		for i := range in {
			in[i] = change("project:P-1", i)
		}
		ids := write(t, b.store, in...)
		for i := range ids {
			if c := next(t, fast, 5*time.Second); c.ID != ids[i] {
				t.Fatalf("fast: change %d is %d, want %d", i, c.ID, ids[i])
			}
		}
		deadline := time.Now().Add(time.Second)
		for slow.Dropped() != 992 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		t.Logf("fast received %d; slow holds %d, dropped %d; fast dropped %d", len(ids), len(slow.C()), slow.Dropped(), fast.Dropped())
		if slow.Dropped() != 992 || len(slow.C()) != 8 || fast.Dropped() != 0 {
			t.Fatalf("slow dropped %d holding %d, fast dropped %d; want 992, 8, 0", slow.Dropped(), len(slow.C()), fast.Dropped())
		}
	})
}

// AC5: the tail runs only while a subscriber exists: no goroutine before the first Subscribe, and
// none once the last subscription closes.
func TestTheTailRunsOnlyWhileASubscriberExists(t *testing.T) {
	each(t, func(t *testing.T, b backend) {
		bus, _ := newBus(t, b, Options{})
		settle := func() int {
			time.Sleep(50 * time.Millisecond)
			return runtime.NumGoroutine()
		}
		before := settle()
		if bus.Running() {
			t.Fatal("a tail runs before any subscriber")
		}
		one := subscribe(t, bus, 0, "project:P-1")
		two := subscribe(t, bus, 0, "agent:x")
		during := runtime.NumGoroutine()
		if !bus.Running() {
			t.Fatal("no tail runs with two subscribers")
		}
		one.Close()
		if !bus.Running() {
			t.Fatal("the tail stopped while a subscriber remained")
		}
		two.Close()
		deadline := time.Now().Add(2 * time.Second)
		for bus.Running() && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		after := settle()
		t.Logf("goroutines: %d before, %d with subscribers, %d after the last closed; running %v", before, during, after, bus.Running())
		if bus.Running() || after > before {
			t.Fatalf("after the last subscriber: running %v, %d goroutines (was %d)", bus.Running(), after, before)
		}
		// And a later subscriber starts it again, from the end of the log.
		write(t, b.store, change("project:P-1", 1))
		again := subscribe(t, bus, 0, "project:P-1")
		quiet(t, again, 3*PollInterval)
		id := write(t, b.store, change("project:P-1", 2))[0]
		if c := next(t, again, 2*time.Second); c.ID != id {
			t.Fatalf("restarted tail delivered %d, want %d", c.ID, id)
		}
	})
}

// A subscription receives its scopes' changes after it subscribed, and nothing else; one holding
// several scopes receives each; Replay merges its scopes' changes by id.
func TestScopesAndReplay(t *testing.T) {
	each(t, func(t *testing.T, b backend) {
		before := write(t, b.store, change("project:P-1", 0))[0]
		bus, _ := newBus(t, b, Options{})
		proj := subscribe(t, bus, 0, "project:P-1")
		both := subscribe(t, bus, 0, "alice", "project:P-1", "alice")
		if got := both.Scopes(); len(got) != 2 {
			t.Fatalf("a scope listed twice: %v", got)
		}
		ids := write(t, b.store, change("alice", 1), change("bob", 2), change("project:P-1", 3))
		if c := next(t, both, 2*time.Second); c.ID != ids[0] || c.Scope != "alice" {
			t.Fatalf("both: %+v, want alice's %d", c, ids[0])
		}
		if c := next(t, both, 2*time.Second); c.ID != ids[2] {
			t.Fatalf("both: %+v, want %d", c, ids[2])
		}
		if c := next(t, proj, 2*time.Second); c.ID != ids[2] {
			t.Fatalf("project: %+v, want %d (and nothing written before it subscribed)", c, ids[2])
		}
		quiet(t, proj, 3*PollInterval)
		got, err := bus.Replay(context.Background(), []string{"project:P-1", "alice"}, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		var gotIDs []int64
		for _, c := range got {
			gotIDs = append(gotIDs, c.ID)
		}
		if want := []int64{before, ids[0], ids[2]}; fmt.Sprint(gotIDs) != fmt.Sprint(want) {
			t.Fatalf("Replay = %v, want %v", gotIDs, want)
		}
		if _, err := bus.Subscribe(context.Background(), nil, 0); err == nil {
			t.Fatal("a subscription with no scope was accepted")
		}
		if _, err := bus.Subscribe(context.Background(), []string{""}, 0); err == nil {
			t.Fatal("an empty scope was accepted")
		}
	})
}

// The amended requirement 1 (contract_changelog.go:30-32): on Postgres an id is taken at the INSERT
// and seen at the commit, so two transactions committing out of id order must both be delivered —
// the tail never treats the highest id it has read as a watermark.
func TestPostgresDeliversChangesCommittedOutOfIDOrder(t *testing.T) {
	b := postgresBackend(t)
	bus, _ := newBus(t, b, Options{})
	sub := subscribe(t, bus, 0, "project:P-1")
	ctx := context.Background()
	appended, release := make(chan int64), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- b.store.Atomically(ctx, func(tx store.Store) error {
			id, err := tx.AppendChange(ctx, change("project:P-1", 1))
			if err != nil {
				return err
			}
			appended <- id
			<-release
			return nil
		})
	}()
	idA := <-appended
	idB, err := b.store.AppendChange(ctx, change("project:P-1", 2)) // its own transaction, committed now
	if err != nil {
		t.Fatal(err)
	}
	if idB <= idA {
		t.Fatalf("ids %d then %d: the test needs B's id above A's", idA, idB)
	}
	if c := next(t, sub, 2*time.Second); c.ID != idB {
		t.Fatalf("first delivered %d, want B (%d), committed first", c.ID, idB)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if c := next(t, sub, 2*time.Second); c.ID != idA {
		t.Fatalf("then delivered %d, want A (%d), committed second with the lower id", c.ID, idA)
	}
	t.Logf("A took id %d and committed after B (id %d): both delivered, B then A", idA, idB)
	quiet(t, sub, 3*PollInterval)
}

// The positive control of the frontier: an id a rolled-back transaction took never appears, and
// after GapTimeout the tail passes it — later changes still arrive and the frontier moves past it.
func TestPostgresPassesAnIDARolledBackTransactionTook(t *testing.T) {
	b := postgresBackend(t)
	bus, _ := newBus(t, b, Options{GapTimeout: 300 * time.Millisecond})
	sub := subscribe(t, bus, 0, "project:P-1")
	ctx := context.Background()
	var lost int64
	boom := errors.New("rolled back")
	if err := b.store.Atomically(ctx, func(tx store.Store) error {
		var err error
		lost, err = tx.AppendChange(ctx, change("project:P-1", 1))
		if err != nil {
			return err
		}
		return boom
	}); !errors.Is(err, boom) {
		t.Fatal(err)
	}
	after := write(t, b.store, change("project:P-1", 2))[0]
	if c := next(t, sub, 2*time.Second); c.ID != after {
		t.Fatalf("delivered %d, want %d", c.ID, after)
	}
	if f := bus.frontier.Load(); f != lost-1 {
		t.Fatalf("frontier %d right after the gap, want it held at %d", f, lost-1)
	}
	time.Sleep(400 * time.Millisecond)
	last := write(t, b.store, change("project:P-1", 3))[0]
	if c := next(t, sub, 2*time.Second); c.ID != last {
		t.Fatalf("delivered %d, want %d", c.ID, last)
	}
	if f := bus.frontier.Load(); f != last {
		t.Fatalf("frontier %d after GapTimeout, want %d: the rolled-back id %d was never passed", f, last, lost)
	}
	t.Logf("id %d rolled back; the frontier held at %d, then passed it to %d", lost, lost-1, last)
}

// Subscribers come and go while changes flow: no panic, no send on a closed channel (-race).
func TestChurn(t *testing.T) {
	b := sqliteBackend(t)
	bus, _ := newBus(t, b, Options{PollInterval: 5 * time.Millisecond})
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			for range 10 {
				s := subscribe(t, bus, 1, "project:P-1", fmt.Sprintf("agent:%d", i))
				time.Sleep(time.Millisecond)
				s.Close()
			}
		})
	}
	for i := range 50 {
		write(t, b.store, change("project:P-1", i))
	}
	wg.Wait()
}
