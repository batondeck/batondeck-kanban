package store

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// SearchFilter narrows a search: BoardID "" is every board; Statuses nil or empty is every status
// (an empty list is no filter, never "match nothing"; conductor has no status filter to follow).
type SearchFilter struct {
	BoardID  string
	Statuses []domain.Status
}

// SearchHit is one match. Rank is the engine's relevance, higher is better (SQLite's bm25 negated,
// Postgres's ts_rank); it orders one engine's results and is not comparable across engines.
type SearchHit struct {
	TaskID string
	Rank   float64
}

// SearchPage is one page of hits; NextCursor is "" on the last page.
type SearchPage struct {
	Hits       []SearchHit
	NextCursor string
}

// Search is lexical task search within one project over each task's title, description and search
// tokens as one body (tasks.search_body under FTS5, tasks.search under tsvector 'simple').
//
// The query is user text, never engine syntax: it is split into terms on every character that is
// not a letter or a digit, terms are lowercased, and a '*' directly after a term makes it a prefix.
// A term matches a whole token of the body, case-insensitively and without stemming; a prefix term
// matches every token it begins; every term must match (AND). A query with no term is
// domain.Validation, as is a malformed cursor.
//
// Order is best match first: a body holding a term more often ranks above one holding it less
// often, among bodies of the same token count; equal ranks are in task-number order (T-2 before
// T-10). A cursor is base64url JSON {order, id} with order the rank. A missing project matches
// nothing: search reads one project and checks nothing else (the caller has authorised it), so a
// page and its count are two statements.
type Search interface {
	// SearchTasks is one page of the hits for query in the project, filtered by f. Limit <= 0 is 10
	// and a limit above 100 is 100.
	SearchTasks(ctx context.Context, projectID, query string, f SearchFilter, cursor string, limit int) (SearchPage, error)
	// CountSearch is the number of hits SearchTasks pages through for the same query and filter.
	CountSearch(ctx context.Context, projectID, query string, f SearchFilter) (int64, error)
}

var (
	_ Search = (*SQLite)(nil)
	_ Search = (*Postgres)(nil)
)
