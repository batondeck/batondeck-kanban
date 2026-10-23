package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The search area of contract_search.go, on both engines: a page is one statement, a count one.
// The caller's text never reaches the engine's query syntax: SearchTerms reduces it to
// letters-and-digits terms, and each engine's match string is built from those alone (SQLite
// `"a" "auth"*` under FTS5, Postgres `'a' & 'auth':*` under to_tsquery), bound as a parameter.

type searchQueries interface {
	SearchTasks(ctx context.Context, arg sqlitedb.SearchTasksParams) ([]sqlitedb.SearchTasksRow, error)
	CountSearch(ctx context.Context, arg sqlitedb.CountSearchParams) (int64, error)
}

// SearchTerm is one term of a search query: lowercased letters and digits, a prefix when a '*'
// directly followed it.
type SearchTerm struct {
	Text   string
	Prefix bool
}

// SearchTerms is the contract's query normalisation (contract_search.go), shared by the engines
// and memstore: split on every character that is not a letter or a digit, lowercase, and a '*'
// directly after a term makes it a prefix.
func SearchTerms(query string) []SearchTerm {
	var out []SearchTerm
	rs := []rune(query)
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }
	for i := 0; i < len(rs); {
		if !word(rs[i]) {
			i++
			continue
		}
		j := i
		for j < len(rs) && word(rs[j]) {
			j++
		}
		out = append(out, SearchTerm{Text: strings.ToLower(string(rs[i:j])), Prefix: j < len(rs) && rs[j] == '*'})
		i = j
	}
	return out
}

func (s *SQLite) SearchTasks(ctx context.Context, projectID, query string, f SearchFilter, cursor string, limit int) (SearchPage, error) {
	return searchTasks(ctx, s, ftsMatch, projectID, query, f, cursor, limit)
}

func (s *Postgres) SearchTasks(ctx context.Context, projectID, query string, f SearchFilter, cursor string, limit int) (SearchPage, error) {
	return searchTasks(ctx, s, tsqueryMatch, projectID, query, f, cursor, limit)
}

func (s *SQLite) CountSearch(ctx context.Context, projectID, query string, f SearchFilter) (int64, error) {
	return countSearch(ctx, s, ftsMatch, projectID, query, f)
}

func (s *Postgres) CountSearch(ctx context.Context, projectID, query string, f SearchFilter) (int64, error) {
	return countSearch(ctx, s, tsqueryMatch, projectID, query, f)
}

// ftsMatch is the FTS5 query of terms: each a quoted string (a phrase of one token, never an
// operator), a prefix one followed by *, juxtaposed (AND).
func ftsMatch(terms []SearchTerm) string {
	parts := make([]string, len(terms))
	for i, t := range terms {
		parts[i] = `"` + t.Text + `"`
		if t.Prefix {
			parts[i] += "*"
		}
	}
	return strings.Join(parts, " ")
}

// tsqueryMatch is the to_tsquery input of terms: each a quoted lexeme, a prefix one :*, joined by &.
func tsqueryMatch(terms []SearchTerm) string {
	parts := make([]string, len(terms))
	for i, t := range terms {
		parts[i] = "'" + t.Text + "'"
		if t.Prefix {
			parts[i] += ":*"
		}
	}
	return strings.Join(parts, " & ")
}

// searchArgs is the match string and the JSON status list of a query and filter; a query with no
// term is VALIDATION.
func searchArgs(match func([]SearchTerm) string, query string, f SearchFilter) (string, string, error) {
	terms := SearchTerms(query)
	if len(terms) == 0 {
		return "", "", domain.Validation("the query has no word to search for", map[string]any{"field": "query"})
	}
	statuses := "[]"
	if len(f.Statuses) > 0 {
		b, err := json.Marshal(f.Statuses)
		if err != nil {
			return "", "", err
		}
		statuses = string(b)
	}
	return match(terms), statuses, nil
}

func searchTasks(ctx context.Context, e engine, match func([]SearchTerm) string, projectID, query string, f SearchFilter, cursor string, limit int) (SearchPage, error) {
	m, statuses, err := searchArgs(match, query, f)
	if err != nil {
		return SearchPage{}, err
	}
	arg := sqlitedb.SearchTasksParams{Match: m, ProjectID: projectID, BoardID: f.BoardID, Statuses: statuses}
	if cursor != "" {
		order, id, err := DecodeCursor(cursor)
		if err != nil {
			return SearchPage{}, err
		}
		rank, rerr := strconv.ParseFloat(order, 64)
		num, nerr := taskNumber(id)
		if rerr != nil || nerr != nil {
			return SearchPage{}, domain.Validation("Invalid cursor", nil)
		}
		arg.AfterID, arg.AfterRank, arg.AfterNum = id, rank, num
	}
	limit = clamp(limit, 10, 100)
	// One row past the page says whether a next page exists.
	arg.PageLimit = int64(limit + 1)
	rows, err := e.queries(false).SearchTasks(ctx, arg)
	if err != nil {
		return SearchPage{}, fmt.Errorf("store: search tasks: %w", err)
	}
	page := SearchPage{Hits: make([]SearchHit, 0, min(len(rows), limit))}
	for _, r := range rows[:min(len(rows), limit)] {
		page.Hits = append(page.Hits, SearchHit{TaskID: r.ID, Rank: r.Score})
	}
	if len(rows) > limit {
		last := page.Hits[limit-1]
		page.NextCursor = EncodeCursor(strconv.FormatFloat(last.Rank, 'g', -1, 64), last.TaskID)
	}
	return page, nil
}

func countSearch(ctx context.Context, e engine, match func([]SearchTerm) string, projectID, query string, f SearchFilter) (int64, error) {
	m, statuses, err := searchArgs(match, query, f)
	if err != nil {
		return 0, err
	}
	n, err := e.queries(false).CountSearch(ctx, sqlitedb.CountSearchParams{Match: m, ProjectID: projectID, BoardID: f.BoardID, Statuses: statuses})
	if err != nil {
		return 0, fmt.Errorf("store: count search: %w", err)
	}
	return n, nil
}

// taskNumber is n of a task id T-n.
func taskNumber(id string) (int64, error) {
	n, ok := strings.CutPrefix(id, "T-")
	if !ok {
		return 0, fmt.Errorf("not a task id: %q", id)
	}
	return strconv.ParseInt(n, 10, 64)
}

// --- the Postgres queries in sqlitedb's types ---

func (p pgQueries) SearchTasks(ctx context.Context, arg sqlitedb.SearchTasksParams) ([]sqlitedb.SearchTasksRow, error) {
	rows, err := p.q.SearchTasks(ctx, pgdb.SearchTasksParams(arg))
	return convert(rows, func(r pgdb.SearchTasksRow) sqlitedb.SearchTasksRow { return sqlitedb.SearchTasksRow(r) }), err
}

func (p pgQueries) CountSearch(ctx context.Context, arg sqlitedb.CountSearchParams) (int64, error) {
	return p.q.CountSearch(ctx, pgdb.CountSearchParams(arg))
}
