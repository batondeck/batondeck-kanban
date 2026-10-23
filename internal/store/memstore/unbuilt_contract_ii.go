package memstore

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The in-memory store's methods for the areas of store contract II still unbuilt here: each returns
// store.Unbuilt's INTERNAL naming the task that writes it here, so the batteries skip on memstore as
// they do on the engines - P-67 search. R0.10 wrote context, memory, summaries, dependencies and
// subtasks, R0.21 audit, tool calls, agent and wake sessions, R1.3 attachments and R1.8 blobs, each
// in its own files. A lane deletes its methods
// from this file as it writes them, and the file with its last method.

func (s *Store) SearchTasks(context.Context, string, string, store.SearchFilter, string, int) (store.SearchPage, error) {
	return store.SearchPage{}, store.Unbuilt("SearchTasks", "P-67")
}

func (s *Store) CountSearch(context.Context, string, string, store.SearchFilter) (int64, error) {
	return 0, store.Unbuilt("CountSearch", "P-67")
}
