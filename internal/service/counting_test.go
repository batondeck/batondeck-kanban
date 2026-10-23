package service

import (
	"context"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/store"
)

// countingStore counts the store reads the R1.16 services make, inside a transaction too. memstore
// has no statement counter; each counted call is the statements its store contract states
// (GetBoard: 2, the board and one grouped count; GetMember: 1).
type countingStore struct {
	store.Store
	n *calls
}

type calls struct {
	mu sync.Mutex
	m  map[string]int
}

func (c *calls) hit(method string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[method]++
}

// take returns the counts since the last take and resets them.
func (c *calls) take() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.m
	c.m = map[string]int{}
	return out
}

func (s *countingStore) wrap(tx store.Store) store.Store { return &countingStore{Store: tx, n: s.n} }

func (s *countingStore) Atomically(ctx context.Context, fn func(tx store.Store) error) error {
	return s.Store.Atomically(ctx, func(tx store.Store) error { return fn(s.wrap(tx)) })
}

func (s *countingStore) WithIdempotency(ctx context.Context, projectID, identity, key string, ttl time.Duration, fn func(tx store.Store) ([]byte, error)) ([]byte, error) {
	return s.Store.WithIdempotency(ctx, projectID, identity, key, ttl, func(tx store.Store) ([]byte, error) { return fn(s.wrap(tx)) })
}

func (s *countingStore) GetBoard(ctx context.Context, projectID, boardID string) (store.Board, store.TaskCounts, error) {
	s.n.hit("GetBoard")
	return s.Store.GetBoard(ctx, projectID, boardID)
}

func (s *countingStore) ListBoards(ctx context.Context, projectID string) ([]store.Board, error) {
	s.n.hit("ListBoards")
	return s.Store.ListBoards(ctx, projectID)
}

func (s *countingStore) GetMember(ctx context.Context, projectID, identityID string) (store.Member, error) {
	s.n.hit("GetMember")
	return s.Store.GetMember(ctx, projectID, identityID)
}

func (s *countingStore) ListMembers(ctx context.Context, projectID string) ([]store.Member, error) {
	s.n.hit("ListMembers")
	return s.Store.ListMembers(ctx, projectID)
}

func (s *countingStore) GetProject(ctx context.Context, projectID string) (store.Project, error) {
	s.n.hit("GetProject")
	return s.Store.GetProject(ctx, projectID)
}

func (s *countingStore) ListTasks(ctx context.Context, projectID string, f store.TaskFilter, cursor string, limit int) (store.TaskPage, error) {
	s.n.hit("ListTasks")
	return s.Store.ListTasks(ctx, projectID, f, cursor, limit)
}

func (s *countingStore) ReadChanges(ctx context.Context, since int64, limit int) ([]store.Change, error) {
	s.n.hit("ReadChanges")
	return s.Store.ReadChanges(ctx, since, limit)
}

func (s *countingStore) ListBoardFeed(ctx context.Context, projectID, boardID, cursor string, limit int) (store.EventPage, error) {
	s.n.hit("ListBoardFeed")
	return s.Store.ListBoardFeed(ctx, projectID, boardID, cursor, limit)
}

// counted is newFixture with its Core rebuilt over a countingStore of the same memstore.
func counted(t *testing.T) (*fixture, *calls) {
	t.Helper()
	f := newFixture(t, Options{})
	n := &calls{m: map[string]int{}}
	old := f.c
	f.c = New(&countingStore{Store: f.st, n: n}, Options{Logger: log.New(f.logs, "", 0), Now: f.clock.now, ToolCallFlush: -1})
	if err := old.Close(f.ctx); err != nil {
		t.Fatal(err)
	}
	return f, n
}
