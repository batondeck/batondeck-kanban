// Package memstore is the in-memory store.Store: every area's rows in maps under one mutex, for the
// service lanes to build and test against before the engines' statements exist (ADR-0003 B2). It is
// test-only — imported from _test files alone, which the layering guard allows — and it is judged by
// the same internal/store/conformance battery as SQLite and Postgres (TestMemstoreConformance), so
// it cannot answer differently from them where the battery looks. It imports internal/domain and
// internal/store and nothing else of this module.
package memstore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/store"
)

// Store is the in-memory store. The zero value is not usable: call New.
//
// Concurrency is the SQLite engine's: one writer at a time. Every call, read or write, holds the
// database's one mutex for its whole length, and Atomically holds it for the whole callback, so a
// transaction sees no other caller's writes and no caller sees its writes before it returns.
type Store struct {
	db *database
	// tx is true for the Store an Atomically callback is handed: its calls run under the mutex the
	// transaction already holds.
	tx bool
}

type database struct {
	mu     sync.Mutex
	closed bool
	st     *state
}

// state is every row. Rows are held by value and copied on the way in and out (clone), so a caller
// can never change a row the store holds, and a snapshot is a deep copy.
type state struct {
	migrated bool

	projects map[string]store.Project   // by id
	members  map[[2]string]store.Member // by (project, identity)
	counters map[string]int64           // last task number, by project
	boards   map[[2]string]store.Board  // by (project, board); Columns unsorted
	idem     map[[3]string]idemEntry    // by (project, identity, key)
	users    map[string]store.User      // by id
	sessions map[string]store.Session   // by id hash
	tokens   map[string]store.Token     // by id
	tasks    map[[2]string]store.Task   // by (project, task)
	events   map[[2]string]store.Event  // by (project, event)
	changes  []store.Change             // id ascending
	lastChg  int64                      // the last change id handed out
}

type idemEntry struct {
	result    []byte
	expiresAt int64
}

// New returns an empty, unmigrated store.
func New() *Store {
	return &Store{db: &database{st: newState()}}
}

func newState() *state {
	return &state{
		projects: map[string]store.Project{},
		members:  map[[2]string]store.Member{},
		counters: map[string]int64{},
		boards:   map[[2]string]store.Board{},
		idem:     map[[3]string]idemEntry{},
		users:    map[string]store.User{},
		sessions: map[string]store.Session{},
		tokens:   map[string]store.Token{},
		tasks:    map[[2]string]store.Task{},
		events:   map[[2]string]store.Event{},
	}
}

var _ store.Store = (*Store)(nil)

// do runs fn on the state: under the mutex, or, inside Atomically, under the mutex the transaction
// holds. A cancelled context, a closed store and an unmigrated one fail before fn runs.
func (s *Store) do(ctx context.Context, fn func(st *state) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !s.tx {
		s.db.mu.Lock()
		defer s.db.mu.Unlock()
	}
	if s.db.closed {
		return fmt.Errorf("memstore: closed")
	}
	if !s.db.st.migrated {
		return fmt.Errorf("memstore: not migrated")
	}
	return fn(s.db.st)
}

// get is do for a call that returns a value.
func get[T any](ctx context.Context, s *Store, fn func(st *state) (T, error)) (T, error) {
	var out T
	err := s.do(ctx, func(st *state) error {
		v, err := fn(st)
		out = v
		return err
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return out, nil
}

// Migrate marks the store migrated: there is no schema to apply.
func (s *Store) Migrate(ctx context.Context) (store.Migrated, error) {
	if err := ctx.Err(); err != nil {
		return store.Migrated{}, err
	}
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if s.db.st.migrated {
		return store.Migrated{Version: 1}, nil
	}
	s.db.st.migrated = true
	return store.Migrated{Applied: 1, Version: 1}, nil
}

// MigrateDown empties the store and marks it unmigrated, as the engines' down migrations drop every
// table.
func (s *Store) MigrateDown(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	s.db.st = newState()
	return nil
}

// SchemaCurrent is nil once Migrate has run.
func (s *Store) SchemaCurrent(ctx context.Context) error {
	return s.do(ctx, func(*state) error { return nil })
}

// ServerVersion names the in-memory store.
func (s *Store) ServerVersion(context.Context) (string, error) { return "memstore", nil }

// Close closes the store; every later call fails.
func (s *Store) Close() error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	s.db.closed = true
	return nil
}

// Atomically runs fn with the mutex held throughout, on a snapshot it restores if fn returns an
// error or panics. Called inside fn, it runs fn on the same transaction.
func (s *Store) Atomically(ctx context.Context, fn func(tx store.Store) error) (err error) {
	if s.tx {
		return fn(s)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if s.db.closed {
		return fmt.Errorf("memstore: closed")
	}
	snapshot := s.db.st.clone()
	committed := false
	defer func() {
		if !committed {
			s.db.st = snapshot
		}
	}()
	if err := fn(&Store{db: s.db, tx: true}); err != nil {
		return err
	}
	committed = true
	return nil
}

func (st *state) clone() *state {
	c := newState()
	c.migrated = st.migrated
	for k, v := range st.projects {
		c.projects[k] = cloneProject(v)
	}
	for k, v := range st.members {
		c.members[k] = v
	}
	for k, v := range st.counters {
		c.counters[k] = v
	}
	for k, v := range st.boards {
		c.boards[k] = cloneBoard(v)
	}
	for k, v := range st.idem {
		c.idem[k] = idemEntry{append([]byte(nil), v.result...), v.expiresAt}
	}
	for k, v := range st.users {
		c.users[k] = v
	}
	for k, v := range st.sessions {
		c.sessions[k] = v
	}
	for k, v := range st.tokens {
		c.tokens[k] = v
	}
	for k, v := range st.tasks {
		c.tasks[k] = cloneTask(v)
	}
	for k, v := range st.events {
		c.events[k] = cloneEvent(v)
	}
	c.changes = make([]store.Change, len(st.changes))
	for i, ch := range st.changes {
		c.changes[i] = cloneChange(ch)
	}
	c.lastChg = st.lastChg
	return c
}

// newID is prefix and n random hex digits, the engines' id shape.
func newID(prefix string, n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("memstore: crypto/rand: %v", err))
	}
	return prefix + hex.EncodeToString(b)[:n]
}

func nowMillis() int64 { return time.Now().UnixMilli() }

// clamp is a list limit as the contracts read one: <= 0 is def, above max is max.
func clamp(limit, def, max int) int {
	switch {
	case limit <= 0:
		return def
	case limit > max:
		return max
	}
	return limit
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append([]byte{}, b...)
}

func cloneStrings(ss []string) []string {
	if ss == nil {
		return nil
	}
	return append([]string{}, ss...)
}
