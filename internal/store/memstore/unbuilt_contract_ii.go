package memstore

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The in-memory store's methods for the areas of store contract II still unbuilt here: each returns
// store.Unbuilt's INTERNAL naming the task that writes it here, so the batteries skip on memstore as
// they do on the engines - R0.10 context, memory, summaries, dependencies and subtasks; P-67
// attachments, blobs and search. R0.21 wrote audit, tool calls, agent and wake sessions (audit.go,
// agentsessions.go). A lane deletes its methods
// from this file as it writes them, and the file with its last method.

func (s *Store) AddContextItem(context.Context, store.AddContextItemInput) (store.ContextItem, error) {
	return store.ContextItem{}, store.Unbuilt("AddContextItem", "R0.10")
}

func (s *Store) ListContextItems(context.Context, string, string, store.ContextItemsQuery) (store.ContextItemsPage, error) {
	return store.ContextItemsPage{}, store.Unbuilt("ListContextItems", "R0.10")
}

func (s *Store) DeleteContextItemsByTask(context.Context, string, string) (int64, error) {
	return 0, store.Unbuilt("DeleteContextItemsByTask", "R0.10")
}

func (s *Store) WriteMemory(context.Context, store.WriteMemoryInput) (store.MemoryEntry, error) {
	return store.MemoryEntry{}, store.Unbuilt("WriteMemory", "R0.10")
}

func (s *Store) ReadMemory(context.Context, string, string, store.ReadMemoryQuery) ([]store.MemoryEntry, error) {
	return nil, store.Unbuilt("ReadMemory", "R0.10")
}

func (s *Store) RecallMemory(context.Context, store.RecallMemoryQuery) ([]store.MemoryEntry, error) {
	return nil, store.Unbuilt("RecallMemory", "R0.10")
}

func (s *Store) DeleteExpiredMemory(context.Context, int64, int) (int64, error) {
	return 0, store.Unbuilt("DeleteExpiredMemory", "R0.10")
}

func (s *Store) DeleteMemoryByTask(context.Context, string, string) (int64, error) {
	return 0, store.Unbuilt("DeleteMemoryByTask", "R0.10")
}

func (s *Store) SetSummary(context.Context, string, string, string, string, int64) (store.Summary, error) {
	return store.Summary{}, store.Unbuilt("SetSummary", "R0.10")
}

func (s *Store) GetSummary(context.Context, string, string) (store.Summary, error) {
	return store.Summary{}, store.Unbuilt("GetSummary", "R0.10")
}

func (s *Store) AddEdge(context.Context, store.AddEdgeInput) (store.DependencyEdge, error) {
	return store.DependencyEdge{}, store.Unbuilt("AddEdge", "R0.10")
}

func (s *Store) RemoveEdge(context.Context, string, string) (store.DependencyEdge, error) {
	return store.DependencyEdge{}, store.Unbuilt("RemoveEdge", "R0.10")
}

func (s *Store) ListEdges(context.Context, string) ([]store.DependencyEdge, error) {
	return nil, store.Unbuilt("ListEdges", "R0.10")
}

func (s *Store) ListBlockers(context.Context, string, string) ([]store.DependencyEdge, error) {
	return nil, store.Unbuilt("ListBlockers", "R0.10")
}

func (s *Store) ListDependents(context.Context, string, string) ([]store.DependencyEdge, error) {
	return nil, store.Unbuilt("ListDependents", "R0.10")
}

func (s *Store) SetCaches(context.Context, string, string, []string, []string) error {
	return store.Unbuilt("SetCaches", "R0.10")
}

func (s *Store) ListSubtasks(context.Context, string, string) ([]string, error) {
	return nil, store.Unbuilt("ListSubtasks", "R0.10")
}

func (s *Store) CreateAttachment(context.Context, store.CreateAttachmentInput) (store.Attachment, error) {
	return store.Attachment{}, store.Unbuilt("CreateAttachment", "P-67")
}

func (s *Store) SetAttachmentReady(context.Context, string, string, string, string) (store.Attachment, error) {
	return store.Attachment{}, store.Unbuilt("SetAttachmentReady", "P-67")
}

func (s *Store) ListAttachments(context.Context, string, string) ([]store.Attachment, error) {
	return nil, store.Unbuilt("ListAttachments", "P-67")
}

func (s *Store) DeleteAttachmentsByTask(context.Context, string, string) (int64, error) {
	return 0, store.Unbuilt("DeleteAttachmentsByTask", "P-67")
}

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
