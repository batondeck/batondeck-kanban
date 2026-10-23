package service

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/memstore"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// searchEngines are the stores search_tasks is judged over: memstore and both engines (the
// Postgres leg skips without BATONDECK_TEST_POSTGRES_DSN).
var searchEngines = []struct {
	name string
	open func(t *testing.T) store.Store
}{
	{"memstore", func(t *testing.T) store.Store {
		s := memstore.New()
		if _, err := s.Migrate(context.Background()); err != nil {
			t.Fatal(err)
		}
		return s
	}},
	{"sqlite", func(t *testing.T) store.Store { return storetest.SQLite(t) }},
	{"postgres", func(t *testing.T) store.Store { return storetest.Postgres(t) }},
}

// searchWorld is two projects carol administers, each with a board and its tasks.
type searchWorld struct {
	ctx   context.Context
	st    store.Store
	c     *Core
	carol domain.Caller
	a, b  string // project ids
	ab    string // project a's board
}

func newSearchWorld(t *testing.T, st store.Store) *searchWorld {
	t.Helper()
	w := &searchWorld{ctx: context.Background(), st: st, carol: domain.Caller{Identity: "carol", TokenScope: "carol"}}
	w.c = New(st, Options{ToolCallFlush: -1})
	t.Cleanup(func() { _ = w.c.Close(context.Background()) })
	project := func(name string) (string, string) {
		p, err := w.c.Projects().Create(w.ctx, w.carol, CreateProjectReq{Name: name})
		if err != nil {
			t.Fatalf("create_project: %v", err)
		}
		b, err := w.c.Boards().Create(w.ctx, w.carol, CreateBoardReq{ProjectID: p.ProjectID, Name: "Main"})
		if err != nil {
			t.Fatalf("create_board: %v", err)
		}
		return p.ProjectID, b.Board.ID
	}
	var bb string
	w.a, w.ab = project("A")
	w.b, bb = project("B")
	for _, c := range []struct{ p, b, title, desc string }{
		{w.a, w.ab, "Rotate the frobnicator", "the hinge squeaks under load"}, // A T-1
		{w.a, w.ab, "Authentication timeout", "retry after login fails"},      // A T-2
		{w.a, w.ab, "Authorize webhook calls", "sign each request"},           // A T-3
		{w.b, bb, "Unrelated chores", "water the plants"},                     // B T-1
	} {
		if _, err := w.c.Tasks().Create(w.ctx, w.carol, CreateTaskReq{ProjectID: c.p, BoardID: c.b, Title: c.title, Description: c.desc}); err != nil {
			t.Fatalf("create_task: %v", err)
		}
	}
	return w
}

func (w *searchWorld) search(t *testing.T, project, query string) []string {
	t.Helper()
	r, err := w.c.Media().SearchTasks(w.ctx, w.carol, SearchTasksReq{ProjectID: project, Query: query})
	if err != nil {
		t.Fatalf("search_tasks %q: %v", query, err)
	}
	if r.Mode != "keyword" {
		t.Errorf("search_tasks %q answered mode %q, want keyword", query, r.Mode)
	}
	ids := []string{}
	for _, x := range r.Results {
		if x.Task.ProjectID != project {
			t.Errorf("search_tasks in %s returned %s of project %s", project, x.Task.ID, x.Task.ProjectID)
		}
		ids = append(ids, x.Task.ID)
	}
	return ids
}

// R1.9 AC2-AC4 and requirement 6 over the real service on each store: a title word, a description
// phrase, a search token, a prefix; another project's task never found (refusal) while the same
// query in its own project finds it (control); update_task and delete_task move the index; the
// four hostile inputs are VALIDATION or a result; semantic is VALIDATION, hybrid runs keyword.
func TestSearchTasksOverEachStore(t *testing.T) {
	for _, e := range searchEngines {
		t.Run(e.name, func(t *testing.T) {
			w := newSearchWorld(t, e.open(t))
			want := func(project, query string, ids ...string) {
				t.Helper()
				if ids == nil {
					ids = []string{}
				}
				if got := w.search(t, project, query); !slices.Equal(got, ids) {
					t.Errorf("search %s %q = %v, want %v", project, query, got, ids)
				}
			}
			want(w.a, "frobnicator", "T-1")   // a title word
			want(w.a, "hinge squeaks", "T-1") // a description phrase
			want(w.a, "auth*", "T-2", "T-3")  // a prefix
			want(w.a, "Login FAILS", "T-2")   // case-insensitive, every term
			want(w.b, "frobnicator")          // AC4 refusal: B never sees A's task
			want(w.b, "plants", "T-1")        // AC4 control: B's own task
			want(w.a, "plants")               // and A never sees B's
			tk, err := w.st.GetTask(w.ctx, w.a, "T-3")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.st.UpdateTask(w.ctx, w.a, "T-3", tk.Version, store.TaskPatch{SearchTokens: ptr("zorblat"), UpdatedBy: "carol", UpdatedAt: time.Now().UnixMilli()}); err != nil {
				t.Fatal(err)
			}
			want(w.a, "zorblat", "T-3") // a search token

			// AC2: update_task's new title is found and the old word is not; delete_task's id never
			// appears again.
			cur, err := w.c.Tasks().Get(w.ctx, w.carol, GetTaskReq{ProjectID: w.a, TaskID: "T-1"})
			if err != nil {
				t.Fatal(err)
			}
			up, err := w.c.Tasks().Update(w.ctx, w.carol, UpdateTaskReq{ProjectID: w.a, TaskID: "T-1", Version: int64(cur.Task.Version), Patch: TaskPatch{Title: "Polish the widget"}})
			if err != nil {
				t.Fatalf("update_task: %v", err)
			}
			want(w.a, "frobnicator")
			want(w.a, "widget", "T-1")
			if _, err := w.c.Tasks().Delete(w.ctx, w.carol, DeleteTaskReq{ProjectID: w.a, TaskID: "T-1", Version: int64(up.Task.Version)}); err != nil {
				t.Fatalf("delete_task: %v", err)
			}
			want(w.a, "widget")
			want(w.a, "hinge")

			// AC3: never an engine error.
			for _, q := range []string{`"`, `*`, `OR`, strings.Repeat("ab ", 166) + "ab"} {
				r, err := w.c.Media().SearchTasks(w.ctx, w.carol, SearchTasksReq{ProjectID: w.a, Query: q})
				if e, ok := domain.AsBatonDeckError(err); err != nil && (!ok || e.Code != domain.CodeValidation) {
					t.Errorf("search %q (%d chars) = %v, want VALIDATION or a result", q, len(q), err)
				}
				t.Logf("%s: search %.12q (%d chars) -> %d results, err %v", e.name, q, len(q), len(r.Results), err)
			}

			for _, c := range []struct {
				name string
				req  SearchTasksReq
			}{
				{"semantic", SearchTasksReq{ProjectID: w.a, Query: "retry", Mode: "semantic"}},
				{"k_above_50", SearchTasksReq{ProjectID: w.a, Query: "retry", K: 51}},
				{"query_above_1000", SearchTasksReq{ProjectID: w.a, Query: strings.Repeat("a", 1001)}},
				{"no_query", SearchTasksReq{ProjectID: w.a}},
			} {
				_, err := w.c.Media().SearchTasks(w.ctx, w.carol, c.req)
				if e, ok := domain.AsBatonDeckError(err); !ok || e.Code != domain.CodeValidation {
					t.Errorf("%s: %v, want VALIDATION", c.name, err)
				}
				if c.name == "semantic" && !strings.Contains(err.Error(), "keyword only") {
					t.Errorf("semantic's VALIDATION does not name the decision: %v", err)
				}
			}
			r, err := w.c.Media().SearchTasks(w.ctx, w.carol, SearchTasksReq{ProjectID: w.a, Query: "auth*", Mode: "hybrid", K: 1})
			if err != nil || r.Mode != "keyword" || len(r.Results) != 1 || r.Results[0].Task.ID != "T-2" {
				t.Errorf("hybrid k=1: %+v, %v; want mode keyword and [T-2]", r, err)
			}
			_, err = w.c.Media().SearchTasks(w.ctx, domain.Caller{Identity: "mallory", TokenScope: "mallory"}, SearchTasksReq{ProjectID: w.a, Query: "retry"})
			if e, ok := domain.AsBatonDeckError(err); !ok || e.Code != domain.CodeForbidden {
				t.Errorf("a non-member's search = %v, want FORBIDDEN", err)
			}
		})
	}
}

// readCounting counts the store reads search_tasks could make.
type readCounting struct {
	store.Store
	mu sync.Mutex
	n  map[string]int
}

func (s *readCounting) hit(m string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n[m]++
}

func (s *readCounting) take() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.n
	s.n = map[string]int{}
	return out
}

func (s *readCounting) SearchTasks(ctx context.Context, p, q string, f store.SearchFilter, cur string, limit int) (store.SearchPage, error) {
	s.hit("SearchTasks")
	return s.Store.SearchTasks(ctx, p, q, f, cur, limit)
}

func (s *readCounting) CountSearch(ctx context.Context, p, q string, f store.SearchFilter) (int64, error) {
	s.hit("CountSearch")
	return s.Store.CountSearch(ctx, p, q, f)
}

func (s *readCounting) ListTasksByIDs(ctx context.Context, p string, ids []string) ([]store.Task, error) {
	s.hit("ListTasksByIDs")
	return s.Store.ListTasksByIDs(ctx, p, ids)
}

func (s *readCounting) GetTask(ctx context.Context, p, id string) (store.Task, error) {
	s.hit("GetTask")
	return s.Store.GetTask(ctx, p, id)
}

func (s *readCounting) ListTasks(ctx context.Context, p string, f store.TaskFilter, cur string, limit int) (store.TaskPage, error) {
	s.hit("ListTasks")
	return s.Store.ListTasks(ctx, p, f, cur, limit)
}

func (s *readCounting) GetMember(ctx context.Context, p, id string) (store.Member, error) {
	s.hit("GetMember")
	return s.Store.GetMember(ctx, p, id)
}

// AC5: with the caller's membership cached, search_tasks is one SearchTasks and one
// ListTasksByIDs, each one statement (TestSearchStatements*, internal/store), so at most two.
func TestSearchTasksIsTwoStoreReads(t *testing.T) {
	st := &readCounting{Store: searchEngines[0].open(t), n: map[string]int{}}
	w := newSearchWorld(t, st)
	w.search(t, w.a, "auth*") // warms the membership cache
	st.take()
	if got := w.search(t, w.a, "auth*"); len(got) != 2 {
		t.Fatalf("search auth* = %v", got)
	}
	got := st.take()
	if want := map[string]int{"SearchTasks": 1, "ListTasksByIDs": 1}; !maps.Equal(got, want) {
		t.Fatalf("search_tasks read the store %v, want %v", got, want)
	}
}
