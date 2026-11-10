package store

import "context"

// Blob is the row of a file on local disk named by its content hash (internal/blob writes the
// file). SHA256 is 64 lowercase hex digits. CreatedAt is Unix milliseconds.
type Blob struct {
	SHA256    string
	Bytes     int64
	CreatedAt int64
}

// Blobs is the blob table: content-addressed, so a blob is written once and never changed.
// Attachments, memory entries and Chronicle bodies (a signed download URL a body embeds,
// ChronicleBlobRefs) reference blobs; a referenced blob cannot be deleted. Key: SHA256.
// Errors: a hash that is not 64 lowercase hex digits, a negative size, a delete of a referenced
// blob and a limit <= 0 are domain.Validation; a missing blob is domain.NotFound("Blob", sha256).
type Blobs interface {
	// PutBlob records the blob and returns its row; a hash already recorded returns the existing
	// row unchanged.
	PutBlob(ctx context.Context, sha256 string, bytes int64) (Blob, error)
	// GetBlob is the blob's row.
	GetBlob(ctx context.Context, sha256 string) (Blob, error)
	// DeleteBlob deletes the row of a blob no attachment, memory entry or Chronicle body references.
	DeleteBlob(ctx context.Context, sha256 string) error
	// ListUnreferencedBlobs is at most limit blobs created before olderThan that no attachment,
	// memory entry or Chronicle body references, oldest first (created_at, then hash): retention's GC list. The
	// age bound keeps a blob whose attachment is still pending its SetAttachmentReady off the list.
	ListUnreferencedBlobs(ctx context.Context, olderThan int64, limit int) ([]Blob, error)
}

var (
	_ Blobs = (*SQLite)(nil)
	_ Blobs = (*Postgres)(nil)
)
