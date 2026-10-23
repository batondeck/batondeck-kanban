package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"slices"
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
// after GapTimeout the tail passes it — with no further write (the poll's tick moves a stalled
// frontier), counted and reported — and later changes still arrive.
func TestPostgresPassesAnIDARolledBackTransactionTook(t *testing.T) {
	b := postgresBackend(t)
	var mu sync.Mutex
	var reported []error
	bus, _ := newBus(t, b, Options{GapTimeout: 300 * time.Millisecond, OnError: func(err error) {
		mu.Lock()
		reported = append(reported, err)
		mu.Unlock()
	}})
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
	time.Sleep(300*time.Millisecond + 3*PollInterval) // no write: only the tick can move it
	if f := bus.frontier.Load(); f != after {
		t.Fatalf("frontier %d after GapTimeout with no write, want %d: the rolled-back id %d was never passed", f, after, lost)
	}
	mu.Lock()
	got := slices.Clone(reported)
	mu.Unlock()
	if bus.GapsPassed() != 1 || len(got) != 1 || !errors.Is(got[0], ErrGapPassed) {
		t.Fatalf("gaps passed %d, reported %v; want 1 and one ErrGapPassed", bus.GapsPassed(), got)
	}
	last := write(t, b.store, change("project:P-1", 3))[0]
	if c := next(t, sub, 2*time.Second); c.ID != last {
		t.Fatalf("delivered %d, want %d", c.ID, last)
	}
	t.Logf("id %d rolled back; the frontier held at %d, then passed it to %d with no write (%v)", lost, lost-1, after, got[0])
}

// R0.11 review B1: the tail's start is no watermark. On Postgres a transaction that took its id
// before the first subscriber's tail started, and commits after Subscribe returned, is delivered.
func TestPostgresDeliversAChangeInFlightWhenTheTailStarts(t *testing.T) {
	b := postgresBackend(t)
	ctx := context.Background()
	write(t, b.store, change("project:P-1", 0))
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
	idB := write(t, b.store, change("project:P-1", 2))[0] // visible above A before the tail starts
	bus, _ := newBus(t, b, Options{})
	sub := subscribe(t, bus, 0, "project:P-1")
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if c := next(t, sub, 2*time.Second); c.ID != idA {
		t.Fatalf("delivered %d, want A (%d), in flight when the tail started", c.ID, idA)
	}
	quiet(t, sub, 3*PollInterval) // and not B, committed before Subscribe
	t.Logf("A took id %d before the tail started and committed after Subscribe (B %d already visible): delivered", idA, idB)
}

// hiding is a change log that does not show one id until shown: an in-flight transaction, over
// memstore.
type hiding struct {
	store.ChangeLog
	id    int64
	shown atomic.Bool
}

func (h *hiding) ReadChanges(ctx context.Context, since int64, limit int) ([]store.Change, error) {
	rows, err := h.ChangeLog.ReadChanges(ctx, since, limit)
	if h.shown.Load() {
		return rows, err
	}
	return slices.DeleteFunc(rows, func(c store.Change) bool { return c.ID == h.id }), err
}

// B1 over memstore: ids 1 (an hour old), 2 (in flight) and 3 (just committed) when the tail starts;
// 2 commits after Subscribe and is delivered, 3 is not.
func TestAChangeInFlightWhenTheTailStartsIsDelivered(t *testing.T) {
	b := memBackend(t)
	old := change("project:P-1", 0)
	old.Ts = time.Now().Add(-time.Hour).UnixMilli()
	ids := write(t, b.store, old, change("project:P-1", 1), change("project:P-1", 2))
	log := &hiding{ChangeLog: b.store, id: ids[1]}
	bus := New(log, Options{OnError: func(err error) { t.Errorf("bus: %v", err) }})
	t.Cleanup(bus.Close)
	sub := subscribe(t, bus, 0, "project:P-1")
	log.shown.Store(true)
	if c := next(t, sub, 2*time.Second); c.ID != ids[1] {
		t.Fatalf("delivered %d, want the in-flight %d", c.ID, ids[1])
	}
	quiet(t, sub, 3*PollInterval)
	if f := bus.frontier.Load(); f != ids[2] {
		t.Fatalf("frontier %d, want %d", f, ids[2])
	}
}

// manual is a watcher woken by the test alone.
type manual struct{ wake chan func() }

func (m *manual) WatchChanges(ctx context.Context, wake func()) error {
	wake()
	m.wake <- wake
	<-ctx.Done()
	return nil
}

// A permanent gap (an id that never shows) under a watcher: once GapTimeout passes, the poll's tick
// moves the frontier past it with no write and no wake, counts it and reports ErrGapPassed.
func TestAGapIsPassedOnTheTickWithNoWrite(t *testing.T) {
	b := memBackend(t)
	log := &hiding{ChangeLog: b.store}
	w := &manual{wake: make(chan func(), 1)}
	var mu sync.Mutex
	var reported []error
	bus := New(log, Options{Watcher: w, GapTimeout: 200 * time.Millisecond, OnError: func(err error) {
		mu.Lock()
		reported = append(reported, err)
		mu.Unlock()
	}})
	t.Cleanup(bus.Close)
	sub := subscribe(t, bus, 0, "project:P-1")
	wake := <-w.wake
	time.Sleep(2 * PollInterval) // past the read the first wake allowed
	ids := write(t, b.store, change("project:P-1", 1), change("project:P-1", 2), change("project:P-1", 3))
	log.id = ids[1] // never shown
	wake()
	for _, want := range []int64{ids[0], ids[2]} {
		if c := next(t, sub, 2*time.Second); c.ID != want {
			t.Fatalf("delivered %d, want %d", c.ID, want)
		}
	}
	if f := bus.frontier.Load(); f != ids[0] {
		t.Fatalf("frontier %d, want it held at %d below the gap", f, ids[0])
	}
	time.Sleep(200*time.Millisecond + 3*PollInterval)
	mu.Lock()
	got := slices.Clone(reported)
	mu.Unlock()
	if f := bus.frontier.Load(); f != ids[2] || bus.GapsPassed() != 1 || len(got) != 1 || !errors.Is(got[0], ErrGapPassed) {
		t.Fatalf("after GapTimeout with no write: frontier %d (want %d), gaps %d, reported %v", f, ids[2], bus.GapsPassed(), got)
	}
	t.Logf("gap %d passed on the tick: frontier %d, %v", ids[1], ids[2], got[0])
}

// blocking is a change log whose reads wait until released.
type blocking struct {
	store.ChangeLog
	entered chan struct{}
	release chan struct{}
}

func (b *blocking) ReadChanges(ctx context.Context, since int64, limit int) ([]store.Change, error) {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-b.release
	return b.ChangeLog.ReadChanges(context.WithoutCancel(ctx), since, limit) // the read succeeds after Close
}

// A Bus closed while the first Subscribe waits for its tail fails that Subscribe: it never hands back
// a subscription whose channel is already closed.
func TestSubscribeRacingCloseFails(t *testing.T) {
	b := memBackend(t)
	log := &blocking{ChangeLog: b.store, entered: make(chan struct{}, 1), release: make(chan struct{})}
	bus := New(log, Options{})
	got := make(chan error, 1)
	go func() {
		_, err := bus.Subscribe(context.Background(), []string{"project:P-1"}, 0)
		got <- err
	}()
	<-log.entered
	closed := make(chan struct{})
	go func() { bus.Close(); close(closed) }()
	for {
		bus.mu.RLock()
		n := bus.count
		bus.mu.RUnlock()
		if n == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	close(log.release)
	if err := <-got; !errors.Is(err, ErrClosed) {
		t.Fatalf("Subscribe racing Close returned %v, want ErrClosed", err)
	}
	<-closed
}

// Subscribers come and go while changes flow: no panic, no send on a closed channel (-race).
func TestChurn(t *testing.T) {
	b := sqliteBackend(t)
	bus, _ := newBus(t, b, Options{PollInterval: 5 * time.Millisecond})
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for i := range 20 {
		wg.Go(func() {
			for range 10 {
				s, err := bus.Subscribe(context.Background(), []string{"project:P-1", fmt.Sprintf("agent:%d", i)}, 1)
				if err != nil {
					errs <- err
					return
				}
				time.Sleep(time.Millisecond)
				s.Close()
			}
		})
	}
	for i := range 50 {
		write(t, b.store, change("project:P-1", i))
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
