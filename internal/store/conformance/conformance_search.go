package conformance

import (
	"context"
	"slices"
	"strconv"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// searchCorpus is the one corpus the search battery pins its orders over. Every body (title, then
// description) is six tokens, so a ranking that weights a token by how often it occurs (bm25 and
// ts_rank both do) orders it the same on both engines; where two bodies tie on a multi-term query,
// they hold the terms the same distance apart, so ts_rank's proximity weighting ties them too and
// task number decides.
var searchCorpus = []struct {
	board       int // 0 or 1: the project's first or second board
	status      domain.Status
	title, desc string
}{
	{0, domain.Todo, "alpha deploy pipeline", "rerun alpha alpha"}, // T-1: alpha x3
	{0, domain.Todo, "alpha login page", "fix sso alpha"},          // T-2: alpha x2
	{0, domain.Review, "beta login page", "fix sso button"},        // T-3
	{1, domain.Todo, "alpha billing export", "csv gamma report"},   // T-4: alpha x1, gamma x1
	{1, domain.Done, "gamma billing export", "pdf gamma report"},   // T-5: gamma x2
	{0, domain.Blocked, "authentication timeout", "retry after login fails"},
	{1, domain.Todo, "authorize webhook calls", "sign each request"}, // T-7
}

// searchQueries are the ten pinned queries over searchCorpus and the ordered ids each returns.
var searchQueries = []struct {
	name   string
	query  string
	filter func(boards [2]string) store.SearchFilter
	want   []string
}{
	{"MoreOccurrencesRankHigher", "alpha", nil, []string{"T-1", "T-2", "T-4"}},
	{"TiesAreInTaskNumberOrder", "login", nil, []string{"T-2", "T-3", "T-6"}},
	{"EveryTermMustMatch", "login sso", nil, []string{"T-2", "T-3"}},
	{"AnotherTerm", "gamma", nil, []string{"T-5", "T-4"}},
	{"APrefixMatchesTheTokensItBegins", "auth*", nil, []string{"T-6", "T-7"}},
	{"ByBoard", "alpha", func(b [2]string) store.SearchFilter { return store.SearchFilter{BoardID: b[1]} }, []string{"T-4"}},
	{"ByStatus", "login", func([2]string) store.SearchFilter {
		return store.SearchFilter{Statuses: []domain.Status{domain.Todo, domain.Blocked}}
	}, []string{"T-2", "T-6"}},
	{"CaseInsensitive", "ALPHA Deploy", nil, []string{"T-1"}},
	{"PunctuationIsNotSyntax", `fix: "sso"`, nil, []string{"T-2", "T-3"}},
	{"AnotherProjectsTasksAreNotSearched", "deploy", nil, []string{"T-1"}},
}

func init() { register("search", search) }

// search is lexical task search: the ten pinned queries, the count, paging, no hit, the index
// following an update and a delete, scores another project cannot move, no row left by a delete,
// and the refusals.
func search(t *testing.T, newStore Factory) {
	ctx := context.Background()
	probe := func(s store.Store) error {
		_, err := s.CountSearch(ctx, "P-probe", "probe", store.SearchFilter{})
		return err
	}
	area(t, "Search", newStore, probe, func(t *testing.T) {
		// corpus seeds searchCorpus into one project with two boards, and a second project whose
		// T-2 says "deploy" four times: a search that crossed projects would rank it first.
		corpus := func(t *testing.T) (store.Store, string, [2]string) {
			t.Helper()
			s, sd, p, b0 := seeded(t, newStore)
			b1, err := sd.seedBoard(ctx, p)
			noErr(t, err)
			boards := [2]string{b0, b1}
			for i, c := range searchCorpus {
				id, err := sd.seedTask(ctx, seedTaskInput{ProjectID: p, BoardID: boards[c.board], Title: c.title, Description: c.desc, Status: c.status})
				noErr(t, err)
				if want := "T-" + strconv.Itoa(i+1); id != want {
					t.Fatalf("corpus task %d seeded as %s, want %s", i, id, want)
				}
			}
			q, qb, err := sd.seedProject(ctx)
			noErr(t, err)
			for _, c := range [][2]string{{"omega task here", "nothing else here"}, {"deploy rollback deploy", "deploy deploy now"}} {
				_, err := sd.seedTask(ctx, seedTaskInput{ProjectID: q, BoardID: qb, Title: c[0], Description: c[1]})
				noErr(t, err)
			}
			return s, p, boards
		}
		ids := func(hits []store.SearchHit) []string {
			out := make([]string, len(hits))
			for i, h := range hits {
				out[i] = h.TaskID
			}
			return out
		}

		t.Run("TenQueriesPinTheirOrderedIDsAndCounts", func(t *testing.T) {
			s, p, boards := corpus(t)
			for _, c := range searchQueries {
				var f store.SearchFilter
				if c.filter != nil {
					f = c.filter(boards)
				}
				page, err := s.SearchTasks(ctx, p, c.query, f, "", 0)
				noErr(t, err)
				if got := ids(page.Hits); !slices.Equal(got, c.want) || page.NextCursor != "" {
					t.Errorf("%s: SearchTasks(%q) = %v (cursor %q), want %v", c.name, c.query, got, page.NextCursor, c.want)
				}
				n, err := s.CountSearch(ctx, p, c.query, f)
				noErr(t, err)
				if n != int64(len(c.want)) {
					t.Errorf("%s: CountSearch(%q) = %d, want %d", c.name, c.query, n, len(c.want))
				}
			}
		})

		t.Run("PagesByCursorInRankOrder", func(t *testing.T) {
			s, p, _ := corpus(t)
			first, err := s.SearchTasks(ctx, p, "alpha", store.SearchFilter{}, "", 2)
			noErr(t, err)
			if got := ids(first.Hits); !slices.Equal(got, []string{"T-1", "T-2"}) || first.NextCursor == "" {
				t.Fatalf("first page %v (cursor %q), want [T-1 T-2] and a cursor", got, first.NextCursor)
			}
			if first.Hits[0].Rank < first.Hits[1].Rank {
				t.Fatalf("ranks %v descend the wrong way: higher is better", first.Hits)
			}
			second, err := s.SearchTasks(ctx, p, "alpha", store.SearchFilter{}, first.NextCursor, 2)
			noErr(t, err)
			if got := ids(second.Hits); !slices.Equal(got, []string{"T-4"}) || second.NextCursor != "" {
				t.Fatalf("second page %v (cursor %q), want [T-4] and no cursor", got, second.NextCursor)
			}
			ties, err := s.SearchTasks(ctx, p, "login", store.SearchFilter{}, "", 1)
			noErr(t, err)
			rest, err := s.SearchTasks(ctx, p, "login", store.SearchFilter{}, ties.NextCursor, 5)
			noErr(t, err)
			if got := append(ids(ties.Hits), ids(rest.Hits)...); !slices.Equal(got, []string{"T-2", "T-3", "T-6"}) {
				t.Fatalf("paging through a tie read %v, want [T-2 T-3 T-6]", got)
			}
		})

		t.Run("AnEmptyStatusListIsNoFilter", func(t *testing.T) {
			s, p, _ := corpus(t)
			page, err := s.SearchTasks(ctx, p, "login", store.SearchFilter{Statuses: []domain.Status{}}, "", 0)
			noErr(t, err)
			n, err := s.CountSearch(ctx, p, "login", store.SearchFilter{Statuses: []domain.Status{}})
			noErr(t, err)
			if got := ids(page.Hits); !slices.Equal(got, []string{"T-2", "T-3", "T-6"}) || n != 3 {
				t.Fatalf("an empty status list matched %v (count %d), want every status: [T-2 T-3 T-6]", got, n)
			}
		})

		t.Run("NoHitIsAnEmptyPage", func(t *testing.T) {
			s, p, _ := corpus(t)
			page, err := s.SearchTasks(ctx, p, "zeta", store.SearchFilter{}, "", 0)
			noErr(t, err)
			n, err := s.CountSearch(ctx, p, "zeta", store.SearchFilter{})
			noErr(t, err)
			if len(page.Hits) != 0 || page.NextCursor != "" || n != 0 {
				t.Fatalf("zeta: %+v, count %d; want nothing", page, n)
			}
			missing, err := s.SearchTasks(ctx, "P-missing", "alpha", store.SearchFilter{}, "", 0)
			noErr(t, err)
			if len(missing.Hits) != 0 {
				t.Fatalf("a missing project matched %v", ids(missing.Hits))
			}
		})

		// The index follows the task inside its own write: a title change drops the old word and
		// finds the new one, search tokens are searched, and a deleted task is never a hit.
		t.Run("TheIndexFollowsTheTask", func(t *testing.T) {
			s, p, _ := corpus(t)
			hit := func(q string) []string {
				t.Helper()
				page, err := s.SearchTasks(ctx, p, q, store.SearchFilter{}, "", 0)
				noErr(t, err)
				return ids(page.Hits)
			}
			tk, err := s.GetTask(ctx, p, "T-7")
			noErr(t, err)
			tk, err = s.UpdateTask(ctx, p, "T-7", tk.Version, store.TaskPatch{Title: ptr("verify webhook calls"), SearchTokens: ptr("zorblat"), UpdatedBy: seedIdentity, UpdatedAt: 2})
			noErr(t, err)
			if got := hit("authorize"); len(got) != 0 {
				t.Fatalf("the old title word still finds %v", got)
			}
			for _, q := range []string{"verify", "zorblat"} {
				if got := hit(q); !slices.Equal(got, []string{"T-7"}) {
					t.Fatalf("%q found %v, want [T-7]", q, got)
				}
			}
			noErr(t, s.DeleteTask(ctx, p, "T-7", tk.Version))
			for _, q := range []string{"verify", "zorblat", "webhook"} {
				if got := hit(q); len(got) != 0 {
					t.Fatalf("%q still finds %v after the delete", q, got)
				}
			}
		})

		// A project's scores and order read only its own tasks: another project gaining tasks that
		// hold the words moves neither (bm25's IDF, which counts every project's rows, would).
		t.Run("AnotherProjectsRowsMoveNoScore", func(t *testing.T) {
			s, p, _ := corpus(t)
			queries := []string{"alpha", "login sso", "auth*", "gamma"}
			before := map[string]store.SearchPage{}
			for _, q := range queries {
				page, err := s.SearchTasks(ctx, p, q, store.SearchFilter{}, "", 0)
				noErr(t, err)
				before[q] = page
			}
			sd := s.(seeder)
			other, ob, err := sd.seedProject(ctx)
			noErr(t, err)
			for _, title := range []string{"alpha login sso", "authorize alpha gamma", "alpha alpha alpha", "gamma auth login", "sso alpha"} {
				_, err := sd.seedTask(ctx, seedTaskInput{ProjectID: other, BoardID: ob, Title: title, Description: "alpha"})
				noErr(t, err)
			}
			for _, q := range queries {
				page, err := s.SearchTasks(ctx, p, q, store.SearchFilter{}, "", 0)
				noErr(t, err)
				if !slices.Equal(page.Hits, before[q].Hits) {
					t.Errorf("%q in %s: %v before another project's tasks, %v after", q, p, before[q].Hits, page.Hits)
				}
			}
		})

		// A deleted task leaves no index row: a task of another project that reuses its row id
		// (SQLite's rid has no AUTOINCREMENT) is never found by the deleted task's words.
		t.Run("ADeletedTasksWordsNeverMatchARowThatReusesItsID", func(t *testing.T) {
			s, sd, p, b := seeded(t, newStore)
			id, err := sd.seedTask(ctx, seedTaskInput{ProjectID: p, BoardID: b, Title: "zorblat launch", Description: "quuxly"})
			noErr(t, err)
			noErr(t, s.DeleteTask(ctx, p, id, stateOf(t, sd, p, id).Version))
			q, qb, err := sd.seedProject(ctx)
			noErr(t, err)
			_, err = sd.seedTask(ctx, seedTaskInput{ProjectID: q, BoardID: qb, Title: "plain words", Description: "nothing more"})
			noErr(t, err)
			for _, w := range []string{"zorblat", "quuxly"} {
				page, err := s.SearchTasks(ctx, q, w, store.SearchFilter{}, "", 0)
				noErr(t, err)
				if len(page.Hits) != 0 {
					t.Errorf("%q in %s found %v: a deleted task's words match another project's task", w, q, ids(page.Hits))
				}
			}
			page, err := s.SearchTasks(ctx, q, "plain", store.SearchFilter{}, "", 0)
			noErr(t, err)
			if got := ids(page.Hits); !slices.Equal(got, []string{"T-1"}) {
				t.Errorf("plain in %s found %v, want [T-1]", q, got)
			}
		})

		t.Run("TheRefusals", func(t *testing.T) {
			s, p, _ := corpus(t)
			for _, q := range []string{"", "   ", "*", `"" ()`} {
				_, err := s.SearchTasks(ctx, p, q, store.SearchFilter{}, "", 0)
				wantErr(t, err, domain.CodeValidation, "")
				_, err = s.CountSearch(ctx, p, q, store.SearchFilter{})
				wantErr(t, err, domain.CodeValidation, "")
			}
			_, err := s.SearchTasks(ctx, p, "alpha", store.SearchFilter{}, "not a cursor!", 0)
			wantErr(t, err, domain.CodeValidation, "")
		})
	})
}
