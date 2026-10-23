package service

import (
	"bytes"
	"context"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/memstore"
)

// The service tests run over memstore (ADR-0003 B2): the store the engines' battery judges.

// logBuf is a log sink the tests read while the flusher may write.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// clock is a settable clock for the cache TTL tests.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type fixture struct {
	ctx     context.Context
	st      *memstore.Store
	c       *Core
	logs    *logBuf
	clock   *clock
	project store.Project
	board   store.Board
	alice   domain.Caller // the project's admin
}

// newFixture is a migrated memstore holding one project (alice its admin) and one default board,
// and a Core over it with a settable clock and a captured log. ToolCallFlush defaults to manual.
func newFixture(t *testing.T, o Options) *fixture {
	t.Helper()
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	f := &fixture{ctx: ctx, st: st, logs: &logBuf{}, clock: &clock{t: time.Now()}}
	o.Logger = log.New(f.logs, "", 0)
	if o.Now == nil {
		o.Now = f.clock.now
	}
	if o.ToolCallFlush == 0 {
		o.ToolCallFlush = -1
	}
	f.c = New(st, o)
	t.Cleanup(func() {
		_ = f.c.Close(context.Background())
		_ = st.Close()
	})
	var err error
	f.project, err = st.CreateProject(ctx, store.CreateProjectInput{Name: "P", CreatedBy: "alice"}, store.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.board, err = st.CreateBoard(ctx, store.CreateBoardInput{ProjectID: f.project.ID, Name: "B", CreatedBy: "alice"}, store.WriteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.alice = domain.Caller{Identity: "alice", Agent: "claude", TokenScope: "alice"}
	return f
}

func (f *fixture) column(t *testing.T, s domain.Status) string {
	t.Helper()
	for _, c := range f.board.Columns {
		if c.Status == s {
			return c.ID
		}
	}
	t.Fatalf("no %s column", s)
	return ""
}

// task seeds a task straight through the store, as nothing in this package creates one yet.
func (f *fixture) task(t *testing.T, s domain.Status, assignee *string) store.Task {
	t.Helper()
	task, err := f.st.CreateTask(f.ctx, store.CreateTaskInput{
		ProjectID: f.project.ID, BoardID: f.board.ID, ColumnID: f.column(t, s), Title: "t", Status: s,
		Assignee: assignee, OrderKey: "a0", CreatedBy: "alice", CreatedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

// rows is the count of each table mutate writes.
type rows struct{ events, audit, changes int }

func (f *fixture) count(t *testing.T) rows {
	t.Helper()
	feed, err := f.st.ListBoardFeed(f.ctx, f.project.ID, f.board.ID, "", 200)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := f.st.ListAuditLog(f.ctx, store.AuditFilter{}, "", 500)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := f.st.ReadChanges(f.ctx, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return rows{len(feed.Events), len(audit.Rows), len(changes)}
}

func (f *fixture) changes(t *testing.T) []store.Change {
	t.Helper()
	ch, err := f.st.ReadChanges(f.ctx, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func wantCode(t *testing.T, err error, code domain.Code) *domain.BatonDeckError {
	t.Helper()
	e, ok := domain.AsBatonDeckError(err)
	if !ok || e.Code != code {
		t.Fatalf("error %v, want %s", err, code)
	}
	return e
}

func ptr[T any](v T) *T { return &v }
