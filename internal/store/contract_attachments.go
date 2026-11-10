package store

import "context"

// AttachmentKind is what an attachment holds.
type AttachmentKind string

const (
	AttachmentFile  AttachmentKind = "file"
	AttachmentImage AttachmentKind = "image"
	AttachmentAudio AttachmentKind = "audio"
	AttachmentVideo AttachmentKind = "video"
)

// AttachmentStatus is an attachment's upload state: pending until its blob is uploaded, then ready.
type AttachmentStatus string

const (
	AttachmentPending AttachmentStatus = "pending"
	AttachmentReady   AttachmentStatus = "ready"
)

// Attachment is one file on a task. BlobSHA256 is "" while pending. TS is Unix milliseconds.
// FileName is the name attach_file was given, "" on a row from before it was kept.
type Attachment struct {
	ProjectID  string
	TaskID     string
	ID         string
	BlobSHA256 string
	MimeType   string
	Bytes      int64
	Kind       AttachmentKind
	Status     AttachmentStatus
	CreatedBy  string
	TS         int64
	FileName   string
}

// CreateAttachmentInput is an attachment to create, pending.
type CreateAttachmentInput struct {
	ProjectID string
	TaskID    string
	MimeType  string
	Bytes     int64
	Kind      AttachmentKind
	CreatedBy string
	FileName  string
}

// Attachments is the attachment area. Keys: (project_id, task_id, id). Errors: a missing task is
// domain.NotFound("Task", id); a missing attachment domain.NotFound("Attachment", id); a missing blob
// domain.NotFound("Blob", sha256); an unknown kind, an empty mime type or creator, a negative size
// and SetAttachmentReady on an attachment that is not pending are domain.Validation. Lists are
// oldest first (ts, then id), not paginated.
type Attachments interface {
	// CreateAttachment creates the attachment, pending, with a new id and the current time.
	CreateAttachment(ctx context.Context, in CreateAttachmentInput) (Attachment, error)
	// SetAttachmentReady points the pending attachment at its uploaded blob, which must exist, and
	// marks it ready; it returns the attachment after.
	SetAttachmentReady(ctx context.Context, projectID, taskID, attachmentID, blobSHA256 string) (Attachment, error)
	// ListAttachments is the task's attachments.
	ListAttachments(ctx context.Context, projectID, taskID string) ([]Attachment, error)
	// DeleteAttachmentsByTask deletes the task's attachment rows and returns how many; their blobs
	// are left for ListUnreferencedBlobs (delete_task's cascade, plan §Fold 2).
	DeleteAttachmentsByTask(ctx context.Context, projectID, taskID string) (int64, error)
	// DeleteStalePendingAttachments deletes at most limit attachments still pending that were
	// created before before (Unix ms), oldest first, and returns how many: the retention sweep's
	// uploads that never came. A ready attachment is never deleted. A limit <= 0 is
	// domain.Validation.
	DeleteStalePendingAttachments(ctx context.Context, before int64, limit int) (int64, error)
}

var (
	_ Attachments = (*SQLite)(nil)
	_ Attachments = (*Postgres)(nil)
)
