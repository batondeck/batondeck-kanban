package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
)

// ChangeWatcher is the wake an engine can give the bus (internal/bus) so that a tail with nothing
// new to read issues no statement: WatchChanges calls wake once as soon as it is watching, then
// after each commit that appended a change, until ctx ends (it then returns nil) or the watch fails
// (an error). wake must not block. A wake is a hint to read the change log, never the change.
//
// Postgres LISTENs on one pooled connection for the NOTIFY every AppendChange sends after its
// INSERT: inside Atomically on the same transaction, so Postgres delivers it at that commit;
// outside, as a statement of its own once the INSERT has committed. Every process sharing the
// database is heard.
// SQLite has no notification. Its wake is in-process, fired after the commit of a write connection
// opened on the same file in this process, and, for a second process writing the same file (several
// `serve` on one host, core/lock.go), the watch asks `PRAGMA data_version` every sqliteWatchPoll on
// a connection of its own, which moves when another connection commits: that process's commits are
// heard within the poll.
type ChangeWatcher interface {
	WatchChanges(ctx context.Context, wake func()) error
}

var (
	_ ChangeWatcher = (*SQLite)(nil)
	_ ChangeWatcher = (*Postgres)(nil)
)

// changeSignal is SQLite's in-process wake: the watchers of one database file.
type changeSignal struct {
	mu    sync.Mutex
	wakes map[*func()]struct{}
}

// sqliteSignals holds one changeSignal per absolute file path, so that every SQLite store this
// process opens on a file hears every other's commits.
var sqliteSignals sync.Map

func signalFor(path string) *changeSignal {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	sig, _ := sqliteSignals.LoadOrStore(path, &changeSignal{wakes: map[*func()]struct{}{}})
	return sig.(*changeSignal)
}

func (c *changeSignal) fire() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for w := range c.wakes {
		(*w)()
	}
}

// noteAppended records an appended change: on the copy Atomically handed its callback it marks the
// transaction, whose commit fires the signal; outside one the write has committed, and fires it.
func (s *SQLite) noteAppended() {
	if s.tx != nil {
		s.appended.Store(true)
		return
	}
	s.sig.fire()
}

// sqliteWatchPoll is how often a SQLite watch asks whether another process committed: the longest
// another serve's write on the same file waits to be heard (the bus's own PollInterval).
const sqliteWatchPoll = 250 * time.Millisecond

// WatchChanges wakes on the file's in-process signal, and on another process's commit, until ctx
// ends. The second it hears by `PRAGMA data_version` every sqliteWatchPoll, on a connection of its
// own (not one of the read pool's, and outside the statement count): the value moves when any other
// connection commits, this process's write connection included, so after each in-process wake the
// value it left is read before wake is called, and only another process's commit moves it from
// there. An idle watch costs that one pragma per poll and no read of the change log.
func (s *SQLite) WatchChanges(ctx context.Context, wake func()) error {
	db, err := sql.Open("sqlite", s.watchDSN)
	if err != nil {
		return fmt.Errorf("store: watch changes: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("store: watch changes: %w", err)
	}
	defer conn.Close()
	dataVersion := func() (int64, error) {
		var v int64
		err := conn.QueryRowContext(ctx, "PRAGMA data_version").Scan(&v)
		return v, err
	}
	seen, err := dataVersion()
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("store: watch changes: %w", err)
	}
	local := make(chan struct{}, 1)
	signal := func() {
		select {
		case local <- struct{}{}:
		default:
		}
	}
	w := &signal
	s.sig.mu.Lock()
	s.sig.wakes[w] = struct{}{}
	s.sig.mu.Unlock()
	defer func() {
		s.sig.mu.Lock()
		delete(s.sig.wakes, w)
		s.sig.mu.Unlock()
	}()
	wake()
	tick := time.NewTicker(sqliteWatchPoll)
	defer tick.Stop()
	for {
		var polled bool
		select {
		case <-ctx.Done():
			return nil
		case <-local:
		case <-tick.C:
			polled = true
		}
		v, err := dataVersion()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("store: watch changes: %w", err)
		}
		moved := v != seen
		seen = v
		if !polled || moved {
			wake()
		}
	}
}

// notifyChanges sends the NOTIFY WatchChanges listens for: on Atomically's transaction when there
// is one (delivered at its commit), else on the pool.
func (s *Postgres) notifyChanges(ctx context.Context) error {
	var db pgdb.DBTX = s.pool
	if s.tx != nil {
		db = s.tx
	}
	if err := pgdb.New(pgCounted{db, s.issued}).NotifyChanges(ctx); err != nil {
		return fmt.Errorf("store: notify changes: %w", err)
	}
	return nil
}

// WatchChanges holds one pooled connection LISTENing for AppendChange's notification, until ctx
// ends. The connection is closed rather than returned to the pool: it is still listening, and
// nothing else should hear what it hears.
func (s *Postgres) WatchChanges(ctx context.Context, wake func()) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("store: watch changes: %w", err)
	}
	defer func() {
		_ = conn.Conn().Close(context.Background())
		conn.Release()
	}()
	if err := pgdb.New(conn).ListenChanges(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("store: watch changes: %w", err)
	}
	wake()
	for {
		if _, err := conn.Conn().WaitForNotification(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("store: watch changes: %w", err)
		}
		wake()
	}
}
