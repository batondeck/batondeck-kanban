package store

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// The engines' methods for the twelve areas of store contract II (contract_{context,memory,
// summaries,dependencies,subtasks,audit,toolcalls,agentsessions,wakesessions,attachments,blobs,
// search}.go), unbuilt: each returns domain INTERNAL naming the task that builds it — R0.10 context,
// memory, summaries, dependencies, subtasks, agent and wake sessions; R0.11 audit and tool calls;
// R1.8 attachments and blobs; R1.9 search. A lane deletes its methods from this file as it writes
// them, and the file with its last method.

// unbuiltDetail is the detail an unbuilt method's error names its task under.
const unbuiltDetail = "unbuilt"

func unbuilt(method, task string) error {
	return domain.NewError(domain.CodeInternal, "store: "+method+" is not built on this engine yet ("+task+")",
		map[string]any{unbuiltDetail: task})
}

// UnbuiltTask reports whether err is an unbuilt method's, and the task that builds it: the
// conformance batteries skip an area on it rather than fail.
func UnbuiltTask(err error) (string, bool) {
	e, ok := domain.AsBatonDeckError(err)
	if !ok || e.Code != domain.CodeInternal {
		return "", false
	}
	task, ok := e.Details[unbuiltDetail].(string)
	return task, ok
}

func (s *SQLite) AddContextItem(context.Context, AddContextItemInput) (ContextItem, error) {
	return ContextItem{}, unbuilt("AddContextItem", "R0.10")
}

func (s *SQLite) ListContextItems(context.Context, string, string, ContextItemsQuery) (ContextItemsPage, error) {
	return ContextItemsPage{}, unbuilt("ListContextItems", "R0.10")
}

func (s *SQLite) DeleteContextItemsByTask(context.Context, string, string) (int64, error) {
	return 0, unbuilt("DeleteContextItemsByTask", "R0.10")
}

func (s *SQLite) WriteMemory(context.Context, WriteMemoryInput) (MemoryEntry, error) {
	return MemoryEntry{}, unbuilt("WriteMemory", "R0.10")
}

func (s *SQLite) ReadMemory(context.Context, string, string, ReadMemoryQuery) ([]MemoryEntry, error) {
	return nil, unbuilt("ReadMemory", "R0.10")
}

func (s *SQLite) RecallMemory(context.Context, RecallMemoryQuery) ([]MemoryEntry, error) {
	return nil, unbuilt("RecallMemory", "R0.10")
}

func (s *SQLite) DeleteExpiredMemory(context.Context, int64, int) (int64, error) {
	return 0, unbuilt("DeleteExpiredMemory", "R0.10")
}

func (s *SQLite) DeleteMemoryByTask(context.Context, string, string) (int64, error) {
	return 0, unbuilt("DeleteMemoryByTask", "R0.10")
}

func (s *SQLite) SetSummary(context.Context, string, string, string, string, int64) (Summary, error) {
	return Summary{}, unbuilt("SetSummary", "R0.10")
}

func (s *SQLite) GetSummary(context.Context, string, string) (Summary, error) {
	return Summary{}, unbuilt("GetSummary", "R0.10")
}

func (s *SQLite) AddEdge(context.Context, AddEdgeInput) (DependencyEdge, error) {
	return DependencyEdge{}, unbuilt("AddEdge", "R0.10")
}

func (s *SQLite) RemoveEdge(context.Context, string, string) (DependencyEdge, error) {
	return DependencyEdge{}, unbuilt("RemoveEdge", "R0.10")
}

func (s *SQLite) ListEdges(context.Context, string) ([]DependencyEdge, error) {
	return nil, unbuilt("ListEdges", "R0.10")
}

func (s *SQLite) ListBlockers(context.Context, string, string) ([]DependencyEdge, error) {
	return nil, unbuilt("ListBlockers", "R0.10")
}

func (s *SQLite) ListDependents(context.Context, string, string) ([]DependencyEdge, error) {
	return nil, unbuilt("ListDependents", "R0.10")
}

func (s *SQLite) SetCaches(context.Context, string, string, []string, []string) error {
	return unbuilt("SetCaches", "R0.10")
}

func (s *SQLite) ListSubtasks(context.Context, string, string) ([]string, error) {
	return nil, unbuilt("ListSubtasks", "R0.10")
}

func (s *SQLite) UpsertAgentSession(context.Context, AgentSessionStart) (AgentSession, error) {
	return AgentSession{}, unbuilt("UpsertAgentSession", "R0.10")
}

func (s *SQLite) TouchAgentSession(context.Context, AgentSessionTouch) error {
	return unbuilt("TouchAgentSession", "R0.10")
}

func (s *SQLite) DeleteAgentSession(context.Context, string, string) error {
	return unbuilt("DeleteAgentSession", "R0.10")
}

func (s *SQLite) ListAgentSessions(context.Context, string, int) ([]AgentSession, error) {
	return nil, unbuilt("ListAgentSessions", "R0.10")
}

func (s *SQLite) DeleteIdleAgentSessions(context.Context, int64) (int64, error) {
	return 0, unbuilt("DeleteIdleAgentSessions", "R0.10")
}

func (s *SQLite) PutWakeSession(context.Context, WakeSession) error {
	return unbuilt("PutWakeSession", "R0.10")
}

func (s *SQLite) GetWakeSession(context.Context, string) (WakeSession, error) {
	return WakeSession{}, unbuilt("GetWakeSession", "R0.10")
}

func (s *SQLite) DeleteWakeSession(context.Context, string) error {
	return unbuilt("DeleteWakeSession", "R0.10")
}

func (s *SQLite) AppendAudit(context.Context, AuditRow) (int64, error) {
	return 0, unbuilt("AppendAudit", "R0.11")
}

func (s *SQLite) ListAuditLog(context.Context, AuditFilter, string, int) (AuditPage, error) {
	return AuditPage{}, unbuilt("ListAuditLog", "R0.11")
}

func (s *SQLite) InsertToolCalls(context.Context, []ToolCall) error {
	return unbuilt("InsertToolCalls", "R0.11")
}

func (s *SQLite) DeleteToolCallsOlderThan(context.Context, int64, int) (int64, error) {
	return 0, unbuilt("DeleteToolCallsOlderThan", "R0.11")
}

func (s *SQLite) CreateAttachment(context.Context, CreateAttachmentInput) (Attachment, error) {
	return Attachment{}, unbuilt("CreateAttachment", "R1.8")
}

func (s *SQLite) SetAttachmentReady(context.Context, string, string, string, string) (Attachment, error) {
	return Attachment{}, unbuilt("SetAttachmentReady", "R1.8")
}

func (s *SQLite) ListAttachments(context.Context, string, string) ([]Attachment, error) {
	return nil, unbuilt("ListAttachments", "R1.8")
}

func (s *SQLite) DeleteAttachmentsByTask(context.Context, string, string) (int64, error) {
	return 0, unbuilt("DeleteAttachmentsByTask", "R1.8")
}

func (s *SQLite) PutBlob(context.Context, string, int64) (Blob, error) {
	return Blob{}, unbuilt("PutBlob", "R1.8")
}

func (s *SQLite) GetBlob(context.Context, string) (Blob, error) {
	return Blob{}, unbuilt("GetBlob", "R1.8")
}

func (s *SQLite) DeleteBlob(context.Context, string) error {
	return unbuilt("DeleteBlob", "R1.8")
}

func (s *SQLite) ListUnreferencedBlobs(context.Context, int64, int) ([]Blob, error) {
	return nil, unbuilt("ListUnreferencedBlobs", "R1.8")
}

func (s *SQLite) SearchTasks(context.Context, string, string, SearchFilter, string, int) (SearchPage, error) {
	return SearchPage{}, unbuilt("SearchTasks", "R1.9")
}

func (s *SQLite) CountSearch(context.Context, string, string, SearchFilter) (int64, error) {
	return 0, unbuilt("CountSearch", "R1.9")
}

func (s *Postgres) AddContextItem(context.Context, AddContextItemInput) (ContextItem, error) {
	return ContextItem{}, unbuilt("AddContextItem", "R0.10")
}

func (s *Postgres) ListContextItems(context.Context, string, string, ContextItemsQuery) (ContextItemsPage, error) {
	return ContextItemsPage{}, unbuilt("ListContextItems", "R0.10")
}

func (s *Postgres) DeleteContextItemsByTask(context.Context, string, string) (int64, error) {
	return 0, unbuilt("DeleteContextItemsByTask", "R0.10")
}

func (s *Postgres) WriteMemory(context.Context, WriteMemoryInput) (MemoryEntry, error) {
	return MemoryEntry{}, unbuilt("WriteMemory", "R0.10")
}

func (s *Postgres) ReadMemory(context.Context, string, string, ReadMemoryQuery) ([]MemoryEntry, error) {
	return nil, unbuilt("ReadMemory", "R0.10")
}

func (s *Postgres) RecallMemory(context.Context, RecallMemoryQuery) ([]MemoryEntry, error) {
	return nil, unbuilt("RecallMemory", "R0.10")
}

func (s *Postgres) DeleteExpiredMemory(context.Context, int64, int) (int64, error) {
	return 0, unbuilt("DeleteExpiredMemory", "R0.10")
}

func (s *Postgres) DeleteMemoryByTask(context.Context, string, string) (int64, error) {
	return 0, unbuilt("DeleteMemoryByTask", "R0.10")
}

func (s *Postgres) SetSummary(context.Context, string, string, string, string, int64) (Summary, error) {
	return Summary{}, unbuilt("SetSummary", "R0.10")
}

func (s *Postgres) GetSummary(context.Context, string, string) (Summary, error) {
	return Summary{}, unbuilt("GetSummary", "R0.10")
}

func (s *Postgres) AddEdge(context.Context, AddEdgeInput) (DependencyEdge, error) {
	return DependencyEdge{}, unbuilt("AddEdge", "R0.10")
}

func (s *Postgres) RemoveEdge(context.Context, string, string) (DependencyEdge, error) {
	return DependencyEdge{}, unbuilt("RemoveEdge", "R0.10")
}

func (s *Postgres) ListEdges(context.Context, string) ([]DependencyEdge, error) {
	return nil, unbuilt("ListEdges", "R0.10")
}

func (s *Postgres) ListBlockers(context.Context, string, string) ([]DependencyEdge, error) {
	return nil, unbuilt("ListBlockers", "R0.10")
}

func (s *Postgres) ListDependents(context.Context, string, string) ([]DependencyEdge, error) {
	return nil, unbuilt("ListDependents", "R0.10")
}

func (s *Postgres) SetCaches(context.Context, string, string, []string, []string) error {
	return unbuilt("SetCaches", "R0.10")
}

func (s *Postgres) ListSubtasks(context.Context, string, string) ([]string, error) {
	return nil, unbuilt("ListSubtasks", "R0.10")
}

func (s *Postgres) UpsertAgentSession(context.Context, AgentSessionStart) (AgentSession, error) {
	return AgentSession{}, unbuilt("UpsertAgentSession", "R0.10")
}

func (s *Postgres) TouchAgentSession(context.Context, AgentSessionTouch) error {
	return unbuilt("TouchAgentSession", "R0.10")
}

func (s *Postgres) DeleteAgentSession(context.Context, string, string) error {
	return unbuilt("DeleteAgentSession", "R0.10")
}

func (s *Postgres) ListAgentSessions(context.Context, string, int) ([]AgentSession, error) {
	return nil, unbuilt("ListAgentSessions", "R0.10")
}

func (s *Postgres) DeleteIdleAgentSessions(context.Context, int64) (int64, error) {
	return 0, unbuilt("DeleteIdleAgentSessions", "R0.10")
}

func (s *Postgres) PutWakeSession(context.Context, WakeSession) error {
	return unbuilt("PutWakeSession", "R0.10")
}

func (s *Postgres) GetWakeSession(context.Context, string) (WakeSession, error) {
	return WakeSession{}, unbuilt("GetWakeSession", "R0.10")
}

func (s *Postgres) DeleteWakeSession(context.Context, string) error {
	return unbuilt("DeleteWakeSession", "R0.10")
}

func (s *Postgres) AppendAudit(context.Context, AuditRow) (int64, error) {
	return 0, unbuilt("AppendAudit", "R0.11")
}

func (s *Postgres) ListAuditLog(context.Context, AuditFilter, string, int) (AuditPage, error) {
	return AuditPage{}, unbuilt("ListAuditLog", "R0.11")
}

func (s *Postgres) InsertToolCalls(context.Context, []ToolCall) error {
	return unbuilt("InsertToolCalls", "R0.11")
}

func (s *Postgres) DeleteToolCallsOlderThan(context.Context, int64, int) (int64, error) {
	return 0, unbuilt("DeleteToolCallsOlderThan", "R0.11")
}

func (s *Postgres) CreateAttachment(context.Context, CreateAttachmentInput) (Attachment, error) {
	return Attachment{}, unbuilt("CreateAttachment", "R1.8")
}

func (s *Postgres) SetAttachmentReady(context.Context, string, string, string, string) (Attachment, error) {
	return Attachment{}, unbuilt("SetAttachmentReady", "R1.8")
}

func (s *Postgres) ListAttachments(context.Context, string, string) ([]Attachment, error) {
	return nil, unbuilt("ListAttachments", "R1.8")
}

func (s *Postgres) DeleteAttachmentsByTask(context.Context, string, string) (int64, error) {
	return 0, unbuilt("DeleteAttachmentsByTask", "R1.8")
}

func (s *Postgres) PutBlob(context.Context, string, int64) (Blob, error) {
	return Blob{}, unbuilt("PutBlob", "R1.8")
}

func (s *Postgres) GetBlob(context.Context, string) (Blob, error) {
	return Blob{}, unbuilt("GetBlob", "R1.8")
}

func (s *Postgres) DeleteBlob(context.Context, string) error {
	return unbuilt("DeleteBlob", "R1.8")
}

func (s *Postgres) ListUnreferencedBlobs(context.Context, int64, int) ([]Blob, error) {
	return nil, unbuilt("ListUnreferencedBlobs", "R1.8")
}

func (s *Postgres) SearchTasks(context.Context, string, string, SearchFilter, string, int) (SearchPage, error) {
	return SearchPage{}, unbuilt("SearchTasks", "R1.9")
}

func (s *Postgres) CountSearch(context.Context, string, string, SearchFilter) (int64, error) {
	return 0, unbuilt("CountSearch", "R1.9")
}
