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
	old := change("project:P-1", 0) // older than GapTimeout: the seed's frontier stops at it, not below B
	old.Ts = time.Now().Add(-time.Hour).UnixMilli()
	idOld := write(t, b.store, old)[0]
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
	if f := bus.frontier.Load(); f != idOld {
		t.Fatalf("seed frontier %d, want %d (the old row; %d is in flight)", f, idOld, idA)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if c := next(t, sub, 2*time.Second); c.ID != idA {
		t.Fatalf("delivered %d, want A (%d), in flight when the tail started", c.ID, idA)
	}
	quiet(t, sub, 3*PollInterval) // and not B, committed before Subscribe
	t.Logf("A took id %d before the tail started and committed after Subscribe (old %d, B %d already visible): seed frontier %d, delivered", idA, idOld, idB, idOld)
}

// hidingSet is a change log that does not show the hidden ids until each is shown: in-flight
// transactions, over memstore. The LIMIT applies to the visible rows, as on an engine.
type hidingSet struct {
	store.ChangeLog
	mu     sync.Mutex
	hidden map[int64]bool
}

func (h *hidingSet) hide(ids ...int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.hidden == nil {
		h.hidden = map[int64]bool{}
	}
	for _, id := range ids {
		h.hidden[id] = true
	}
}

func (h *hidingSet) show(id int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.hidden, id)
}

func (h *hidingSet) ReadChanges(ctx context.Context, since int64, limit int) ([]store.Change, error) {
	h.mu.Lock()
	n := len(h.hidden)
	h.mu.Unlock()
	rows, err := h.ChangeLog.ReadChanges(ctx, since, limit+n)
	h.mu.Lock()
	defer h.mu.Unlock()
	rows = slices.DeleteFunc(rows, func(c store.Change) bool { return h.hidden[c.ID] })
	return rows[:min(len(rows), limit)], err
}

// reporter collects what OnError hears.
type reporter struct {
	mu  sync.Mutex
	got []error
}

func (r *reporter) on(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, err)
}

func (r *reporter) errs() []error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.got)
}

// B1 over memstore: ids 1 (an hour old), 2 (in flight) and 3 (just committed) when the tail starts;
// 2 commits after Subscribe and is delivered, 3 is not.
func TestAChangeInFlightWhenTheTailStartsIsDelivered(t *testing.T) {
	b := memBackend(t)
	old := change("project:P-1", 0)
	old.Ts = time.Now().Add(-time.Hour).UnixMilli()
	ids := write(t, b.store, old, change("project:P-1", 1), change("project:P-1", 2))
	log := &hidingSet{ChangeLog: b.store}
	log.hide(ids[1])
	bus := New(log, Options{OnError: func(err error) { t.Errorf("bus: %v", err) }})
	t.Cleanup(bus.Close)
	sub := subscribe(t, bus, 0, "project:P-1")
	log.show(ids[1])
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

// A permanent two-id gap (ids that never show) under a watcher: once GapTimeout passes, the poll's
// tick moves the frontier past it with no write and no wake, counts each id and reports the range.
func TestAGapIsPassedOnTheTickWithNoWrite(t *testing.T) {
	b := memBackend(t)
	log := &hidingSet{ChangeLog: b.store}
	w := &manual{wake: make(chan func(), 1)}
	rep := &reporter{}
	gap := 200 * time.Millisecond
	bus := New(log, Options{Watcher: w, GapTimeout: gap, OnError: rep.on})
	t.Cleanup(bus.Close)
	sub := subscribe(t, bus, 0, "project:P-1")
	wake := <-w.wake
	time.Sleep(2 * PollInterval) // past the read the first wake allowed
	ids := write(t, b.store, change("project:P-1", 1), change("project:P-1", 2), change("project:P-1", 3), change("project:P-1", 4))
	log.hide(ids[1], ids[2]) // never shown
	wake()
	for _, want := range []int64{ids[0], ids[3]} {
		if c := next(t, sub, 2*time.Second); c.ID != want {
			t.Fatalf("delivered %d, want %d", c.ID, want)
		}
	}
	if f := bus.frontier.Load(); f != ids[0] {
		t.Fatalf("frontier %d, want it held at %d below the gap", f, ids[0])
	}
	time.Sleep(gap + 3*PollInterval)
	got := rep.errs()
	want := fmt.Sprintf("%v: ids %d..%d not committed within %v", ErrGapPassed, ids[1], ids[2], gap)
	if f := bus.frontier.Load(); f != ids[3] || bus.GapsPassed() != 2 || len(got) != 1 || !errors.Is(got[0], ErrGapPassed) || got[0].Error() != want {
		t.Fatalf("after GapTimeout with no write: frontier %d (want %d), gaps %d (want 2), reported %v (want [%s])", f, ids[3], bus.GapsPassed(), got, want)
	}
	t.Logf("gap %d..%d passed on the tick: frontier %d, GapsPassed %d, %v", ids[1], ids[2], ids[3], bus.GapsPassed(), got[0])
}

// R0.11 review B2, unit: each hole gets its own GapTimeout. Hole 1 stalls at t0 and fills at t0+9s
// while hole 3 is open; at t0+10s hole 3 has waited 1 s and is not passed.
func TestEachHoleGetsItsOwnStallClock(t *testing.T) {
	t0 := time.Unix(1000, 0)
	gap := 10 * time.Second
	ts := &tailState{seen: map[int64]struct{}{2: {}}}
	ts.advance(t0, gap, nil)
	if ts.frontier != 0 || ts.stalled != t0 {
		t.Fatalf("frontier %d stalled %v, want 0 and %v", ts.frontier, ts.stalled, t0)
	}
	ts.seen[1], ts.seen[4] = struct{}{}, struct{}{}
	ts.advance(t0.Add(9*time.Second), gap, nil)
	if ts.frontier != 2 {
		t.Fatalf("frontier %d, want 2", ts.frontier)
	}
	var passed [][2]int64
	ts.advance(t0.Add(10*time.Second), gap, func(a, b int64) { passed = append(passed, [2]int64{a, b}) })
	if ts.frontier != 2 || len(passed) != 0 {
		t.Fatalf("id 3, missing for 1 s of a %v GapTimeout, was passed: frontier %d, passed %v", gap, ts.frontier, passed)
	}
	ts.advance(t0.Add(19*time.Second), gap, func(a, b int64) { passed = append(passed, [2]int64{a, b}) })
	if ts.frontier != 4 || !slices.Equal(passed, [][2]int64{{3, 3}}) {
		t.Fatalf("after its own GapTimeout: frontier %d (want 4), passed %v (want [[3 3]])", ts.frontier, passed)
	}
}

// hold appends in inside an Atomically that commits only when release is closed: an in-flight id.
func hold(t *testing.T, s store.Store, in store.ChangeInput) (int64, chan struct{}, chan error) {
	t.Helper()
	appended, release, done := make(chan int64), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- s.Atomically(context.Background(), func(tx store.Store) error {
			id, err := tx.AppendChange(context.Background(), in)
			if err != nil {
				return err
			}
			appended <- id
			<-release
			return nil
		})
	}()
	return <-appended, release, done
}

// R0.11 review B2 on Postgres: A (id 2) is in flight when the tail starts and commits ~0.6 s later;
// C (id 4) is taken before A commits and held 0.8 s, under a 1 s GapTimeout. C is delivered: its
// hole is not judged by the clock A's hole started.
func TestPostgresDeliversAnInFlightIDAfterAnEarlierHoleFills(t *testing.T) {
	b := postgresBackend(t)
	rep := &reporter{}
	gap := time.Second
	write(t, b.store, change("project:P-1", 0))                    // 1
	idA, relA, doneA := hold(t, b.store, change("project:P-1", 1)) // 2 in flight
	write(t, b.store, change("project:P-1", 2))                    // 3
	bus, _ := newBus(t, b, Options{GapTimeout: gap, OnError: rep.on})
	sub := subscribe(t, bus, 0, "project:P-1")
	if f := bus.frontier.Load(); f != idA-1 {
		t.Fatalf("seed frontier %d, want %d", f, idA-1)
	}
	time.Sleep(600 * time.Millisecond)
	idC, relC, doneC := hold(t, b.store, change("project:P-1", 3)) // 4 in flight
	idD := write(t, b.store, change("project:P-1", 4))[0]          // 5
	close(relA)
	if err := <-doneA; err != nil {
		t.Fatal(err)
	}
	taken := time.Now()
	for _, want := range []int64{idA, idD} {
		if c := next(t, sub, 2*time.Second); c.ID != want {
			t.Fatalf("delivered %d, want %d", c.ID, want)
		}
	}
	time.Sleep(gap - 200*time.Millisecond - time.Since(taken))
	close(relC)
	if err := <-doneC; err != nil {
		t.Fatal(err)
	}
	held := time.Since(taken)
	select {
	case c := <-sub.C():
		if c.ID != idC {
			t.Fatalf("delivered %d, want %d", c.ID, idC)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("LOST: id %d committed %v after A's commit (GapTimeout %v) and was never delivered; frontier %d, GapsPassed %d, reported %v", idC, held, gap, bus.frontier.Load(), bus.GapsPassed(), rep.errs())
	}
	if got := rep.errs(); len(got) != 0 || bus.GapsPassed() != 0 {
		t.Fatalf("GapsPassed %d, reported %v; want none", bus.GapsPassed(), got)
	}
	t.Logf("id %d held %v past A's commit (GapTimeout %v): delivered, GapsPassed 0", idC, held, gap)
}

// A starting tail looks back one page. When the whole page is within GapTimeout and ids lie below
// it, an id in flight there would not be waited for: that is reported, never silent. A log of exactly
// one page (nothing below it) is the control.
func TestASeedWindowAllRecentIsReported(t *testing.T) {
	for _, c := range []struct {
		name   string
		rows   int
		report bool
	}{{"one page", readPage, false}, {"one page and one", readPage + 1, true}} {
		t.Run(c.name, func(t *testing.T) {
			b := memBackend(t)
			in := make([]store.ChangeInput, c.rows)
			for i := range in {
				in[i] = change("project:P-1", i)
			}
			ids := write(t, b.store, in...)
			rep := &reporter{}
			bus := New(b.store, Options{OnError: rep.on})
			t.Cleanup(bus.Close)
			subscribe(t, bus, 0, "project:P-1")
			got := rep.errs()
			if !c.report {
				if len(got) != 0 {
					t.Fatalf("reported %v, want nothing", got)
				}
				return
			}
			below := ids[len(ids)-1] - readPage
			want := fmt.Sprintf("%v: the %d changes after id %d are all within %v; ids in flight at or below it are not waited for", ErrSeedWindowFull, readPage, below, GapTimeout)
			if len(got) != 1 || !errors.Is(got[0], ErrSeedWindowFull) || got[0].Error() != want {
				t.Fatalf("reported %v, want [%s]", got, want)
			}
			t.Logf("%d recent changes: %v", c.rows, got[0])
		})
	}
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
