package memstore

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The in-memory store's methods for the areas of store contract II still unbuilt here: each returns
// store.Unbuilt's INTERNAL naming the task that writes it here, so the batteries skip on memstore as
// they do on the engines - P-67 blobs and search. R0.10 wrote context, memory,
// summaries, dependencies and subtasks, and R0.21 audit, tool calls, agent and wake sessions, and R1.3
// attachments, each in its own files. A lane deletes its methods
// from this file as it writes them, and the file with its last method.

func (s *Store) PutBlob(context.Context, string, int64) (store.Blob, error) {
	return store.Blob{}, store.Unbuilt("PutBlob", "P-67")
}

func (s *Store) GetBlob(context.Context, string) (store.Blob, error) {
	return store.Blob{}, store.Unbuilt("GetBlob", "P-67")
}

func (s *Store) DeleteBlob(context.Context, string) error {
	return store.Unbuilt("DeleteBlob", "P-67")
}

func (s *Store) ListUnreferencedBlobs(context.Context, int64, int) ([]store.Blob, error) {
	return nil, store.Unbuilt("ListUnreferencedBlobs", "P-67")
}

func (s *Store) SearchTasks(context.Context, string, string, store.SearchFilter, string, int) (store.SearchPage, error) {
	return store.SearchPage{}, store.Unbuilt("SearchTasks", "P-67")
}

func (s *Store) CountSearch(context.Context, string, string, store.SearchFilter) (int64, error) {
	return 0, store.Unbuilt("CountSearch", "P-67")
}
