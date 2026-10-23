package store

import "context"

// The engines' methods for the areas of store contract II not built yet (contract_search.go; R0.10's
// seven areas, R0.11's audit and tool calls and R1.8's attachments and blobs are in their own files):
// each returns unbuilt's INTERNAL (unbuilt.go) naming the task that builds it — R1.9 search. A lane
// deletes its methods from this file as it writes them, and the file with its last method.

func (s *SQLite) SearchTasks(context.Context, string, string, SearchFilter, string, int) (SearchPage, error) {
	return SearchPage{}, unbuilt("SearchTasks", "R1.9")
}

func (s *SQLite) CountSearch(context.Context, string, string, SearchFilter) (int64, error) {
	return 0, unbuilt("CountSearch", "R1.9")
}

func (s *Postgres) SearchTasks(context.Context, string, string, SearchFilter, string, int) (SearchPage, error) {
	return SearchPage{}, unbuilt("SearchTasks", "R1.9")
}

func (s *Postgres) CountSearch(context.Context, string, string, SearchFilter) (int64, error) {
	return 0, unbuilt("CountSearch", "R1.9")
}
