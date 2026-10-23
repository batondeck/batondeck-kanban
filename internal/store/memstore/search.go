package memstore

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The search area of contract_search.go: a scan of the project's tasks. The query and each body
// (title, description, search tokens) are split by store.SearchTerms, as the engines' match
// strings are built; rank is how many of the body's tokens the terms match, which orders this
// store's results as SQLite's matched-token count and Postgres's ts_rank order the engines'.

func (s *Store) SearchTasks(ctx context.Context, projectID, query string, f store.SearchFilter, cursor string, limit int) (store.SearchPage, error) {
	hits, err := get(ctx, s, func(st *state) ([]store.SearchHit, error) { return st.searchHits(projectID, query, f) })
	if err != nil {
		return store.SearchPage{}, err
	}
	if cursor != "" {
		order, id, err := store.DecodeCursor(cursor)
		if err != nil {
			return store.SearchPage{}, err
		}
		rank, err := strconv.ParseFloat(order, 64)
		if err != nil || !strings.HasPrefix(id, "T-") {
			return store.SearchPage{}, domain.Validation("Invalid cursor", nil)
		}
		hits = slices.DeleteFunc(hits, func(h store.SearchHit) bool {
			return h.Rank > rank || (h.Rank == rank && taskNumber(h.TaskID) <= taskNumber(id))
		})
	}
	limit = clamp(limit, 10, 100)
	page := store.SearchPage{Hits: hits}
	if len(hits) > limit {
		page.Hits = hits[:limit]
		last := page.Hits[limit-1]
		page.NextCursor = store.EncodeCursor(strconv.FormatFloat(last.Rank, 'g', -1, 64), last.TaskID)
	}
	return page, nil
}

func (s *Store) CountSearch(ctx context.Context, projectID, query string, f store.SearchFilter) (int64, error) {
	return get(ctx, s, func(st *state) (int64, error) {
		hits, err := st.searchHits(projectID, query, f)
		return int64(len(hits)), err
	})
}

// searchHits is every hit of query in the project under f, best first, ties in task-number order.
func (st *state) searchHits(projectID, query string, f store.SearchFilter) ([]store.SearchHit, error) {
	terms := store.SearchTerms(query)
	if len(terms) == 0 {
		return nil, domain.Validation("the query has no word to search for", map[string]any{"field": "query"})
	}
	out := []store.SearchHit{}
	for k, t := range st.tasks {
		if k[0] != projectID || (f.BoardID != "" && t.BoardID != f.BoardID) || (len(f.Statuses) > 0 && !slices.Contains(f.Statuses, t.Status)) {
			continue
		}
		body := store.SearchTerms(t.Title + " " + t.Description + " " + t.SearchTokens)
		rank := 0
		for _, q := range terms {
			n := 0
			for _, tok := range body {
				if tok.Text == q.Text || (q.Prefix && strings.HasPrefix(tok.Text, q.Text)) {
					n++
				}
			}
			if n == 0 {
				rank = 0
				break
			}
			rank += n
		}
		if rank > 0 {
			out = append(out, store.SearchHit{TaskID: t.ID, Rank: float64(rank)})
		}
	}
	slices.SortFunc(out, func(a, b store.SearchHit) int {
		if c := cmp.Compare(b.Rank, a.Rank); c != 0 {
			return c
		}
		return cmp.Compare(taskNumber(a.TaskID), taskNumber(b.TaskID))
	})
	return out, nil
}
