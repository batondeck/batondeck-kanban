package store

import "context"

// The engines' methods for the areas of store contract II not built yet (contract_{attachments,
// blobs,search}.go; R0.10's seven areas and R0.11's audit and tool calls are in their own files):
// each returns unbuilt's INTERNAL (unbuilt.go) naming the task that builds it — R1.8 attachments
// and blobs; R1.9 search. A lane deletes its methods from this file as it writes them, and the file
// with its last method.

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
