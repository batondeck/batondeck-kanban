// Package bus is the change-log tail: the one event source behind the /wake doorbell, MCP
// resource push and the UI stream (plan §Notifications). It reads store.ChangeLog and nothing
// else, and fans each change out in process to the subscribers holding its token scope.
//
// Cost: the tail runs only while a subscriber exists, and reads the log at most once per
// PollInterval. When the engine gives a wake (store.ChangeWatcher: Postgres LISTEN/NOTIFY,
// SQLite's in-process commit signal) it reads only after a wake, so an idle tail issues no
// statement however many subscribers it has. Without a watcher (memstore) every poll reads.
//
// Delivery never blocks on a slow subscriber: each has a bounded buffer, and a change that does not
// fit is dropped and counted (Subscription.Dropped). A change is a hint; a subscriber that dropped
// one re-reads what it serves, or replays the log from its cursor (Replay).
//
// Order: on Postgres a change-log id is taken at the INSERT and becomes visible at the commit, so a
// lower id can appear after a higher one (contract_changelog.go). The tail therefore never treats
// the highest id it has read as a watermark: it keeps a frontier below which every id has been
// delivered, re-reads from it, and passes over the ids above it it has already delivered. An id
// missing below delivered ones for GapTimeout is taken for a rolled-back transaction and passed.
package bus

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/store"
)

const (
	// PollInterval is the tail's poll period: the longest a woken tail waits to read, and with no
	// watcher the longest a change waits to be read.
	PollInterval = 250 * time.Millisecond
	// GapTimeout is how long an id missing below delivered ones is waited for before the tail
	// takes it for a rolled-back transaction.
	GapTimeout = 10 * time.Second
	// DefaultBuffer is a subscription's buffer when Subscribe is given none.
	DefaultBuffer = 64
	// watchRetry is how long the tail waits before restarting a watch that failed; it polls every
	// PollInterval meanwhile.
	watchRetry = time.Second
	// readPage is how many changes one read takes; a full page is read again at once.
	readPage = 500
)

// Options tunes a Bus. The zero value is a plain poll every PollInterval.
type Options struct {
	// Watcher wakes the tail when a change commits. Pass the engine (*store.SQLite, *store.Postgres)
	// that log is; nil polls.
	Watcher store.ChangeWatcher
	// PollInterval and GapTimeout default to the package constants.
	PollInterval time.Duration
	GapTimeout   time.Duration
	// OnError hears a read or a watch that failed; nil drops it. The tail goes on.
	OnError func(error)
}

// Bus is the tail and its subscribers. Make one with New; the zero value is not usable.
type Bus struct {
	log  store.ChangeLog
	opts Options

	mu     sync.RWMutex
	scopes map[string]map[*Subscription]struct{}
	count  int
	run    *runner
	closed bool
	// hint is the frontier the last tail reached: the next one finds the log's end from it.
	hint int64

	active atomic.Int32 // tails running, for Running
	// frontier is the running tail's frontier, published after each read (tests read it).
	frontier atomic.Int64
}

// runner is one tail, from the first subscription to the last one's Close.
type runner struct {
	cancel context.CancelFunc
	ready  chan struct{} // closed once the tail position is known, or err is set
	err    error
	done   chan struct{}
}

// New is a bus over log. It reads nothing until the first Subscribe.
func New(log store.ChangeLog, opts Options) *Bus {
	if opts.PollInterval <= 0 {
		opts.PollInterval = PollInterval
	}
	if opts.GapTimeout <= 0 {
		opts.GapTimeout = GapTimeout
	}
	return &Bus{log: log, opts: opts, scopes: map[string]map[*Subscription]struct{}{}}
}

// ErrClosed is Subscribe on a closed Bus.
var ErrClosed = errors.New("bus: closed")

// Subscription is one subscriber: the changes of its scopes, in the order the tail reads them.
type Subscription struct {
	b       *Bus
	scopes  []string
	ch      chan store.Change
	dropped atomic.Int64
	once    sync.Once
}

// C is the subscription's changes. It is closed by Close, and by the Bus's Close.
func (s *Subscription) C() <-chan store.Change { return s.ch }

// Dropped is how many changes did not fit the buffer and were dropped.
func (s *Subscription) Dropped() int64 { return s.dropped.Load() }

// Scopes is the token scopes the subscription receives.
func (s *Subscription) Scopes() []string { return slices.Clone(s.scopes) }

// Close ends the subscription and closes C; the last one stops the tail. It is idempotent.
func (s *Subscription) Close() { s.once.Do(func() { s.b.remove(s) }) }

// Subscribe receives every change of the given token scopes committed after it returns, each once,
// with room for buffer unread ones (<= 0 is DefaultBuffer). The first subscription starts the tail
// and waits for it to find the end of the log; an error there, or ctx ending first, is returned and
// subscribes nothing. A scope listed twice counts once; no scope, or an empty one, is an error.
func (b *Bus) Subscribe(ctx context.Context, scopes []string, buffer int) (*Subscription, error) {
	scopes, err := validScopes(scopes)
	if err != nil {
		return nil, err
	}
	if buffer <= 0 {
		buffer = DefaultBuffer
	}
	s := &Subscription{b: b, scopes: scopes, ch: make(chan store.Change, buffer)}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, ErrClosed
	}
	for _, sc := range scopes {
		if b.scopes[sc] == nil {
			b.scopes[sc] = map[*Subscription]struct{}{}
		}
		b.scopes[sc][s] = struct{}{}
	}
	b.count++
	if b.run == nil {
		b.start()
	}
	r := b.run
	b.mu.Unlock()
	select {
	case <-r.ready:
		if r.err != nil {
			s.Close()
			return nil, r.err
		}
		return s, nil
	case <-ctx.Done():
		s.Close()
		return nil, ctx.Err()
	}
}

func validScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		return nil, errors.New("bus: a subscription needs a scope")
	}
	out := make([]string, 0, len(scopes))
	for _, sc := range scopes {
		if sc == "" {
			return nil, errors.New("bus: an empty scope")
		}
		if !slices.Contains(out, sc) {
			out = append(out, sc)
		}
	}
	return out, nil
}

func (b *Bus) remove(s *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, sc := range s.scopes {
		delete(b.scopes[sc], s)
		if len(b.scopes[sc]) == 0 {
			delete(b.scopes, sc)
		}
	}
	close(s.ch)
	b.count--
	if b.count == 0 && b.run != nil {
		b.run.cancel()
		b.run = nil
	}
}

// Running reports whether a tail goroutine is running: true from the first Subscribe until shortly
// after the last subscription closes.
func (b *Bus) Running() bool { return b.active.Load() > 0 }

// Close closes every subscription, stops the tail and waits for it. Subscribe then fails.
func (b *Bus) Close() {
	b.mu.Lock()
	b.closed = true
	var subs []*Subscription
	for _, set := range b.scopes {
		for s := range set {
			if !slices.Contains(subs, s) {
				subs = append(subs, s)
			}
		}
	}
	r := b.run
	b.mu.Unlock()
	for _, s := range subs {
		s.Close()
	}
	if r != nil {
		<-r.done
	}
}

// Replay is the changes of the given scopes after since, oldest first, at most limit (<= 0 is 100,
// above 1000 is 1000): one ReadChangesForScope per scope, merged by id. It is a client's catch-up
// from its cursor; subscribe first and pass over the ids already seen, so nothing falls between.
// On Postgres a change with an id at or below since can still commit later (the package comment):
// a cursor is safe only once it is older than GapTimeout.
func (b *Bus) Replay(ctx context.Context, scopes []string, since int64, limit int) ([]store.Change, error) {
	scopes, err := validScopes(scopes)
	if err != nil {
		return nil, err
	}
	switch {
	case limit <= 0:
		limit = 100
	case limit > 1000:
		limit = 1000
	}
	var all []store.Change
	for _, sc := range scopes {
		rows, err := b.log.ReadChangesForScope(ctx, sc, since, limit)
		if err != nil {
			return nil, err
		}
		all = append(all, rows...)
	}
	slices.SortFunc(all, func(x, y store.Change) int { return compareID(x.ID, y.ID) })
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

func compareID(x, y int64) int {
	switch {
	case x < y:
		return -1
	case x > y:
		return 1
	}
	return 0
}

// start makes the tail. Caller holds b.mu.
func (b *Bus) start() {
	ctx, cancel := context.WithCancel(context.Background())
	r := &runner{cancel: cancel, ready: make(chan struct{}), done: make(chan struct{})}
	b.run = r
	b.active.Add(1)
	go b.tail(ctx, r, b.hint)
}

func (b *Bus) fail(err error) {
	if b.opts.OnError != nil && err != nil {
		b.opts.OnError(err)
	}
}

// tail is one tail's goroutine: start the watch, find the end of the log, then read on every
// woken poll until ctx ends.
func (b *Bus) tail(ctx context.Context, r *runner, hint int64) {
	t := &tailState{}
	var watching sync.WaitGroup
	defer func() {
		r.cancel()
		watching.Wait()
		b.mu.Lock()
		if t.frontier > b.hint {
			b.hint = t.frontier
		}
		b.mu.Unlock()
		b.active.Add(-1)
		close(r.done)
	}()
	wake := make(chan struct{}, 1)
	var degraded atomic.Bool // the watch is down: every poll reads
	if w := b.opts.Watcher; w != nil {
		watching.Go(func() { b.watch(ctx, w, wake, &degraded) })
		// The watch wakes once as soon as it is watching: a change committed after the end of the log
		// is found below is then heard.
		select {
		case <-wake:
		case <-ctx.Done():
			r.err = ctx.Err()
			close(r.ready)
			return
		}
	}
	end, err := b.end(ctx, hint)
	if err != nil {
		r.err = err
		close(r.ready)
		return
	}
	t.frontier = end
	b.frontier.Store(end)
	close(r.ready)
	tick := time.NewTicker(b.opts.PollInterval)
	defer tick.Stop()
	// At most one read per poll: a wake reads at once when the last read is a poll old, and is
	// otherwise held for the next tick, which coalesces every wake since.
	dirty := false
	var last time.Time
	read := func() {
		dirty, last = false, time.Now()
		b.read(ctx, t)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
			if time.Since(last) >= b.opts.PollInterval {
				read()
			} else {
				dirty = true
			}
		case <-tick.C:
			if dirty || b.opts.Watcher == nil || degraded.Load() {
				read()
			}
		}
	}
}

// watch runs the engine's watch until ctx ends, restarting it after watchRetry when it fails and
// marking the tail degraded meanwhile.
func (b *Bus) watch(ctx context.Context, w store.ChangeWatcher, wake chan struct{}, degraded *atomic.Bool) {
	signal := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	for {
		err := w.WatchChanges(ctx, func() { degraded.Store(false); signal() })
		if ctx.Err() != nil {
			return
		}
		b.fail(err)
		degraded.Store(true)
		signal()
		select {
		case <-ctx.Done():
			return
		case <-time.After(watchRetry):
		}
	}
}

// end is the highest id in the log, found from hint, a position no later than it: ReadChanges(x, 1)
// answers the first id after x, so a gallop then a bisection over that costs about 2·log2 of the
// distance in statements.
func (b *Bus) end(ctx context.Context, hint int64) (int64, error) {
	after := func(x int64) (int64, bool, error) {
		rows, err := b.log.ReadChanges(ctx, x, 1)
		if err != nil || len(rows) == 0 {
			return 0, false, err
		}
		return rows[0].ID, true, nil
	}
	lo, ok, err := after(hint)
	if err != nil || !ok {
		return hint, err
	}
	var hi int64
	for step := int64(1); ; step *= 2 {
		p := lo + step
		if p < lo { // overflow: nothing lies past MaxInt64
			hi = 1<<63 - 1
			break
		}
		f, ok, err := after(p)
		if err != nil {
			return 0, err
		}
		if !ok {
			hi = p
			break
		}
		lo = f
	}
	// lo is an id; no id is above hi.
	for hi-lo > 1 {
		mid := lo + (hi-lo)/2
		f, ok, err := after(mid)
		if err != nil {
			return 0, err
		}
		if ok {
			lo = f
		} else {
			hi = mid
		}
	}
	f, ok, err := after(lo)
	if err != nil {
		return 0, err
	}
	if ok {
		return f, nil
	}
	return lo, nil
}

// tailState is what a tail knows of the log: every id at or below frontier has been delivered (or
// given up as a gap), and seen holds the delivered ids above it.
type tailState struct {
	frontier int64
	seen     map[int64]struct{}
	// stalled is when the frontier last stopped at a missing id; zero while it is not stopped.
	stalled time.Time
}

// read delivers every change after the frontier not delivered yet, then moves the frontier up.
func (b *Bus) read(ctx context.Context, t *tailState) {
	if t.seen == nil {
		t.seen = map[int64]struct{}{}
	}
	since := t.frontier
	for {
		rows, err := b.log.ReadChanges(ctx, since, readPage)
		if err != nil {
			if ctx.Err() == nil {
				b.fail(err)
			}
			return
		}
		b.mu.RLock()
		for _, c := range rows {
			since = c.ID
			if _, ok := t.seen[c.ID]; ok {
				continue
			}
			t.seen[c.ID] = struct{}{}
			b.deliverLocked(c)
		}
		b.mu.RUnlock()
		if len(rows) < readPage {
			break
		}
	}
	t.advance(time.Now(), b.opts.GapTimeout)
	b.frontier.Store(t.frontier)
}

// advance moves the frontier over the delivered ids above it, and past a missing one waited for
// longer than gap.
func (t *tailState) advance(now time.Time, gap time.Duration) {
	for {
		for {
			if _, ok := t.seen[t.frontier+1]; !ok {
				break
			}
			delete(t.seen, t.frontier+1)
			t.frontier++
		}
		if len(t.seen) == 0 {
			t.stalled = time.Time{}
			return
		}
		switch {
		case t.stalled.IsZero():
			t.stalled = now
			return
		case now.Sub(t.stalled) < gap:
			return
		}
		// Given up: jump to just below the lowest delivered id, and wait afresh for the next gap.
		low := int64(-1)
		for id := range t.seen {
			if low < 0 || id < low {
				low = id
			}
		}
		t.frontier = low - 1
		t.stalled = time.Time{}
	}
}

// deliverLocked hands c to every subscriber of its scope without blocking. Caller holds b.mu for
// reading.
func (b *Bus) deliverLocked(c store.Change) {
	for s := range b.scopes[c.Scope] {
		select {
		case s.ch <- c:
		default:
			s.dropped.Add(1)
		}
	}
}
