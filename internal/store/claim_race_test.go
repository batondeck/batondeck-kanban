package store

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// raceClock is the time the claims here are judged at.
const raceClock int64 = 1_700_000_000_000

// raceBoard is a project with one board of the default columns and n TODO tasks on it.
func raceBoard(t *testing.T, s Store, n int) (FrontierQuery, string) {
	t.Helper()
	ctx := context.Background()
	p, err := s.CreateProject(ctx, CreateProjectInput{Name: "race", CreatedBy: "alice"}, WriteOptions{Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateBoard(ctx, CreateBoardInput{ProjectID: p.ID, Name: "b", CreatedBy: "alice"}, WriteOptions{Identity: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	col := map[domain.Status]string{}
	for _, c := range b.Columns {
		col[c.Status] = c.ID
	}
	for i := range n {
		if _, err := s.CreateTask(ctx, CreateTaskInput{
			ProjectID: p.ID, BoardID: b.ID, ColumnID: col[domain.Todo], Title: fmt.Sprint("task ", i),
			Status: domain.Todo, OrderKey: "a0", CreatedBy: "alice", CreatedAt: raceClock,
		}); err != nil {
			t.Fatal(err)
		}
	}
	return FrontierQuery{ProjectID: p.ID, BoardID: b.ID, Now: raceClock}, col[domain.InProgress]
}

func migratedStore(t *testing.T, s Store) Store {
	t.Helper()
	t.Cleanup(func() { s.Close() })
	if _, err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func openSQLiteStore(t *testing.T, path string) Store {
	t.Helper()
	s, err := OpenSQLite(path, "FULL")
	if err != nil {
		t.Fatal(err)
	}
	return migratedStore(t, s)
}

func openPostgresStore(t *testing.T) Store {
	t.Helper()
	s, err := OpenPostgres(context.Background(), postgresDB(t))
	if err != nil {
		t.Fatal(err)
	}
	return migratedStore(t, s)
}

// claimRace runs one ClaimNext per caller, at once, each through stores[i % len(stores)], over a
// board of as many TODO tasks: every caller must win, and no two win the same task.
func claimRace(t *testing.T, stores []Store, q FrontierQuery, inProgress string) {
	const callers = 50
	ctx := context.Background()
	ids := make([]string, callers)
	oks := make([]bool, callers)
	errs := make([]error, callers)
	took := make([]time.Duration, callers)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			began := time.Now()
			task, ok, err := stores[i%len(stores)].ClaimNext(ctx, q, ClaimInput{
				Owner: fmt.Sprint("agent-", i), LeaseID: fmt.Sprint("L-", i), ExpiresAt: raceClock + 60_000,
				Now: raceClock, ColumnID: inProgress,
			})
			took[i] = time.Since(began)
			ids[i], oks[i], errs[i] = task.ID, ok, err
		}()
	}
	close(start)
	wg.Wait()
	seen := map[string]int{}
	failed := 0
	for i := range callers {
		if errs[i] != nil || !oks[i] {
			failed++
			t.Errorf("caller %d: ok %v, %v", i, oks[i], errs[i])
			continue
		}
		seen[ids[i]]++
	}
	dup := 0
	for _, c := range seen {
		dup += c - 1
	}
	slices.Sort(took)
	t.Logf("%d claimers over %d store(s): %d distinct winners, %d duplicates, %d errors; claim latency p50 %v p95 %v",
		callers, len(stores), len(seen), dup, failed, took[callers/2], took[callers*95/100])
	if len(seen) != callers || dup != 0 || failed != 0 {
		t.Fatalf("%d distinct winners, %d duplicates, %d errors: want %d, 0, 0", len(seen), dup, failed, callers)
	}
	if _, ok, err := stores[0].ClaimNext(ctx, q, ClaimInput{Owner: "late", LeaseID: "L-late", ExpiresAt: raceClock + 1, Now: raceClock}); err != nil || ok {
		t.Fatalf("the 51st claimer: ok %v, %v; want an empty frontier", ok, err)
	}
}

// TestClaimRace is the plan's two-callers-one-winner at the Store: 50 goroutines × ClaimNext on a
// board of 50 TODO tasks, 50 distinct winners, no error (no SQLITE_BUSY, no duplicate). On SQLite
// the callers are spread over five stores on one file, five processes' worth of write connections,
// so the statement's own write lock is what keeps them apart, not one connection's queue.
func TestClaimRace(t *testing.T) {
	t.Run("SQLite", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "race.db")
		stores := make([]Store, 5)
		for i := range stores {
			stores[i] = openSQLiteStore(t, path)
		}
		q, col := raceBoard(t, stores[0], 50)
		claimRace(t, stores, q, col)
	})
	t.Run("Postgres", func(t *testing.T) {
		s := openPostgresStore(t)
		q, col := raceBoard(t, s, 50)
		claimRace(t, []Store{s}, q, col)
	})
}

// statementsPerCall pins the statements a store call issues for the plan's per-tool pins
// (get_task <= 2, list_tasks <= 2, claim_next <= 4): GetTask is one, a ListTasks page holding
// tasks is one (an empty one asks for the board: two), and a winning ClaimNext is one.
func statementsPerCall(t *testing.T, s Store) {
	ctx := context.Background()
	q, col := raceBoard(t, s, 3)
	count := func(name string, want int64, call func(ctx context.Context) error) {
		t.Helper()
		var n atomic.Int64
		if err := call(countingStatements(ctx, &n)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		t.Logf("%s: %d statement(s)", name, n.Load())
		if n.Load() != want {
			t.Fatalf("%s issued %d statements, want %d", name, n.Load(), want)
		}
	}
	count("GetTask", 1, func(ctx context.Context) error {
		_, err := s.GetTask(ctx, q.ProjectID, "T-1")
		return err
	})
	count("ListTasks (a page of tasks)", 1, func(ctx context.Context) error {
		_, err := s.ListTasks(ctx, q.ProjectID, TaskFilter{BoardID: q.BoardID}, "", 2)
		return err
	})
	count("ListTasks (an empty page)", 2, func(ctx context.Context) error {
		_, err := s.ListTasks(ctx, q.ProjectID, TaskFilter{BoardID: q.BoardID, Label: "none"}, "", 2)
		return err
	})
	count("ClaimNext (a win)", 1, func(ctx context.Context) error {
		_, ok, err := s.ClaimNext(ctx, q, ClaimInput{Owner: "bob", LeaseID: "L-1", ExpiresAt: raceClock + 1, Now: raceClock, ColumnID: col})
		if err == nil && !ok {
			err = fmt.Errorf("no win")
		}
		return err
	})
}

func TestStatementsPerCall(t *testing.T) {
	t.Run("SQLite", func(t *testing.T) {
		statementsPerCall(t, openSQLiteStore(t, filepath.Join(t.TempDir(), "statements.db")))
	})
	t.Run("Postgres", func(t *testing.T) {
		statementsPerCall(t, openPostgresStore(t))
	})
}

// TestListTasksPagesThreeBySeven is the plan's paging check (R0.9 AC5): 21 tasks, read 7 at a time
// by cursor, are three pages holding every task once, in (order key, id) order, and no fourth.
func TestListTasksPagesThreeBySeven(t *testing.T) {
	pages := func(t *testing.T, s Store) {
		ctx := context.Background()
		q, _ := raceBoard(t, s, 21)
		var got [][]string
		cursor := ""
		for {
			page, err := s.ListTasks(ctx, q.ProjectID, TaskFilter{BoardID: q.BoardID}, cursor, 7)
			if err != nil {
				t.Fatal(err)
			}
			ids := make([]string, len(page.Tasks))
			for i, task := range page.Tasks {
				ids[i] = task.ID
			}
			got = append(got, ids)
			if page.NextCursor == "" || len(got) > 4 {
				break
			}
			cursor = page.NextCursor
		}
		all := make([]string, 21)
		for i := range all {
			all[i] = fmt.Sprint("T-", i+1)
		}
		slices.Sort(all) // every task is at "a0": the order is the ids', as bytes
		want := [][]string{all[:7], all[7:14], all[14:]}
		t.Logf("pages %v", got)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("pages %v, want %v", got, want)
		}
	}
	t.Run("SQLite", func(t *testing.T) { pages(t, openSQLiteStore(t, filepath.Join(t.TempDir(), "pages.db"))) })
	t.Run("Postgres", func(t *testing.T) { pages(t, openPostgresStore(t)) })
}
