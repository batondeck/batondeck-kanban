package memstore

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The in-memory store's methods for the twelve areas of store contract II, unbuilt: each returns
// store.Unbuilt's INTERNAL naming the task that writes it here, so the batteries skip on memstore as
// they do on the engines - R0.21 audit, tool calls, agent and wake sessions; P-67 attachments,
// blobs and search (R0.10's context, memory, summaries, dependencies and subtasks are in their own
// files). A lane deletes its methods
// from this file as it writes them, and the file with its last method.

func (s *Store) UpsertAgentSession(context.Context, store.AgentSessionStart) (store.AgentSession, error) {
	return store.AgentSession{}, store.Unbuilt("UpsertAgentSession", "R0.21")
}

func (s *Store) TouchAgentSession(context.Context, store.AgentSessionTouch) error {
	return store.Unbuilt("TouchAgentSession", "R0.21")
}

func (s *Store) DeleteAgentSession(context.Context, string, string) error {
	return store.Unbuilt("DeleteAgentSession", "R0.21")
}

func (s *Store) ListAgentSessions(context.Context, string, int) ([]store.AgentSession, error) {
	return nil, store.Unbuilt("ListAgentSessions", "R0.21")
}

func (s *Store) DeleteIdleAgentSessions(context.Context, int64) (int64, error) {
	return 0, store.Unbuilt("DeleteIdleAgentSessions", "R0.21")
}

func (s *Store) PutWakeSession(context.Context, store.WakeSession) error {
	return store.Unbuilt("PutWakeSession", "R0.21")
}

func (s *Store) GetWakeSession(context.Context, string) (store.WakeSession, error) {
	return store.WakeSession{}, store.Unbuilt("GetWakeSession", "R0.21")
}

func (s *Store) DeleteWakeSession(context.Context, string) error {
	return store.Unbuilt("DeleteWakeSession", "R0.21")
}

func (s *Store) AppendAudit(context.Context, store.AuditRow) (int64, error) {
	return 0, store.Unbuilt("AppendAudit", "R0.21")
}

func (s *Store) ListAuditLog(context.Context, store.AuditFilter, string, int) (store.AuditPage, error) {
	return store.AuditPage{}, store.Unbuilt("ListAuditLog", "R0.21")
}

func (s *Store) InsertToolCalls(context.Context, []store.ToolCall) error {
	return store.Unbuilt("InsertToolCalls", "R0.21")
}

func (s *Store) DeleteToolCallsOlderThan(context.Context, int64, int) (int64, error) {
	return 0, store.Unbuilt("DeleteToolCallsOlderThan", "R0.21")
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
