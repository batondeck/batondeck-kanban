package store

import "context"

// The engines' methods for the areas of store contract II not built yet (contract_{audit,
// toolcalls,attachments,blobs,search}.go; R0.10's seven are in their own files): each returns
// unbuilt's INTERNAL (unbuilt.go) naming the task that builds it — R0.11 audit and tool calls;
// R1.8 attachments and blobs; R1.9 search. A lane deletes its methods from this file as it writes
// them, and the file with its last method.

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
