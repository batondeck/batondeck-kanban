package store

import (
	"context"
	"fmt"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The attachments area of contract_attachments.go, on both engines. A create is one insert, its
// missing task the foreign key's refusal; a list is one statement; SetAttachmentReady checks the
// blob, then updates the pending row, inside one transaction.

type attachmentQueries interface {
	InsertAttachment(ctx context.Context, arg sqlitedb.InsertAttachmentParams) (sqlitedb.Attachment, error)
	GetAttachment(ctx context.Context, arg sqlitedb.GetAttachmentParams) (sqlitedb.Attachment, error)
	SetAttachmentReady(ctx context.Context, arg sqlitedb.SetAttachmentReadyParams) (sqlitedb.Attachment, error)
	ListAttachments(ctx context.Context, arg sqlitedb.ListAttachmentsParams) ([]sqlitedb.ListAttachmentsRow, error)
	DeleteAttachmentsByTask(ctx context.Context, arg sqlitedb.DeleteAttachmentsByTaskParams) (int64, error)
}

var attachmentKinds = map[AttachmentKind]bool{AttachmentFile: true, AttachmentImage: true, AttachmentAudio: true, AttachmentVideo: true}

func (s *SQLite) CreateAttachment(ctx context.Context, in CreateAttachmentInput) (Attachment, error) {
	return createAttachment(ctx, s, in)
}

func (s *Postgres) CreateAttachment(ctx context.Context, in CreateAttachmentInput) (Attachment, error) {
	return createAttachment(ctx, s, in)
}

func (s *SQLite) SetAttachmentReady(ctx context.Context, projectID, taskID, attachmentID, blobSHA256 string) (Attachment, error) {
	return setAttachmentReady(ctx, s, projectID, taskID, attachmentID, blobSHA256)
}

func (s *Postgres) SetAttachmentReady(ctx context.Context, projectID, taskID, attachmentID, blobSHA256 string) (Attachment, error) {
	return setAttachmentReady(ctx, s, projectID, taskID, attachmentID, blobSHA256)
}

func (s *SQLite) ListAttachments(ctx context.Context, projectID, taskID string) ([]Attachment, error) {
	return listAttachments(ctx, s, projectID, taskID)
}

func (s *Postgres) ListAttachments(ctx context.Context, projectID, taskID string) ([]Attachment, error) {
	return listAttachments(ctx, s, projectID, taskID)
}

func (s *SQLite) DeleteAttachmentsByTask(ctx context.Context, projectID, taskID string) (int64, error) {
	return deleteAttachmentsByTask(ctx, s, projectID, taskID)
}

func (s *Postgres) DeleteAttachmentsByTask(ctx context.Context, projectID, taskID string) (int64, error) {
	return deleteAttachmentsByTask(ctx, s, projectID, taskID)
}

func attachmentFrom(r sqlitedb.Attachment) Attachment {
	return Attachment{
		ProjectID: r.ProjectID, TaskID: r.TaskID, ID: r.ID, BlobSHA256: r.BlobSha256.String, MimeType: r.MimeType,
		Bytes: r.Bytes, Kind: AttachmentKind(r.Kind), Status: AttachmentStatus(r.Status), CreatedBy: r.CreatedBy, TS: r.Ts,
	}
}

func createAttachment(ctx context.Context, e engine, in CreateAttachmentInput) (Attachment, error) {
	switch {
	case !attachmentKinds[in.Kind]:
		return Attachment{}, domain.Validation("unknown attachment kind: "+string(in.Kind), map[string]any{"kind": in.Kind})
	case in.MimeType == "":
		return Attachment{}, domain.Validation("an attachment needs a mime type", nil)
	case in.CreatedBy == "":
		return Attachment{}, domain.Validation("an attachment needs a creator", nil)
	case in.Bytes < 0:
		return Attachment{}, domain.Validation("an attachment's size is not negative", map[string]any{"bytes": in.Bytes})
	}
	r, err := e.queries(true).InsertAttachment(ctx, sqlitedb.InsertAttachmentParams{
		ProjectID: in.ProjectID, TaskID: in.TaskID, ID: newID("A-", 16), MimeType: in.MimeType, Bytes: in.Bytes,
		Kind: string(in.Kind), CreatedBy: in.CreatedBy, Ts: nowMillis(),
	})
	if isForeignKeyViolation(err) {
		return Attachment{}, domain.NotFound("Task", in.TaskID)
	}
	if err != nil {
		return Attachment{}, fmt.Errorf("store: create attachment: %w", err)
	}
	return attachmentFrom(r), nil
}

func setAttachmentReady(ctx context.Context, e engine, projectID, taskID, attachmentID, blobSHA256 string) (Attachment, error) {
	var out Attachment
	err := e.Atomically(ctx, func(tx Store) error {
		q := on(tx).queries(true)
		cur, err := q.GetAttachment(ctx, sqlitedb.GetAttachmentParams{ProjectID: projectID, TaskID: taskID, ID: attachmentID})
		if err := found(err, "Attachment", attachmentID); err != nil {
			return err
		}
		if cur.Status != string(AttachmentPending) {
			return domain.Validation("the attachment is not pending", map[string]any{"attachmentId": attachmentID, "status": cur.Status})
		}
		n, err := q.BlobExists(ctx, blobSHA256)
		if err != nil {
			return fmt.Errorf("store: blob: %w", err)
		}
		if n == 0 {
			return domain.NotFound("Blob", blobSHA256)
		}
		r, err := q.SetAttachmentReady(ctx, sqlitedb.SetAttachmentReadyParams{BlobSha256: nullText(blobSHA256), ProjectID: projectID, TaskID: taskID, ID: attachmentID})
		if isNoRows(err) {
			// A concurrent SetAttachmentReady committed between the read and this update.
			return domain.Validation("the attachment is not pending", map[string]any{"attachmentId": attachmentID})
		}
		if err != nil {
			return fmt.Errorf("store: set attachment ready: %w", err)
		}
		out = attachmentFrom(r)
		return nil
	})
	return out, err
}

func listAttachments(ctx context.Context, e engine, projectID, taskID string) ([]Attachment, error) {
	rows, err := e.queries(false).ListAttachments(ctx, sqlitedb.ListAttachmentsParams{ProjectID: projectID, ID: taskID})
	if err != nil {
		return nil, fmt.Errorf("store: list attachments: %w", err)
	}
	if len(rows) == 0 {
		return nil, domain.NotFound("Task", taskID)
	}
	out := []Attachment{}
	for _, r := range rows {
		if !r.ID.Valid {
			continue
		}
		out = append(out, Attachment{
			ProjectID: projectID, TaskID: taskID, ID: r.ID.String, BlobSHA256: r.BlobSha256.String, MimeType: r.MimeType.String,
			Bytes: r.Bytes.Int64, Kind: AttachmentKind(r.Kind.String), Status: AttachmentStatus(r.Status.String),
			CreatedBy: r.CreatedBy.String, TS: r.Ts.Int64,
		})
	}
	return out, nil
}

func deleteAttachmentsByTask(ctx context.Context, e engine, projectID, taskID string) (int64, error) {
	n, err := e.queries(true).DeleteAttachmentsByTask(ctx, sqlitedb.DeleteAttachmentsByTaskParams{ProjectID: projectID, TaskID: taskID})
	if err != nil {
		return 0, fmt.Errorf("store: delete attachments: %w", err)
	}
	return n, nil
}

// --- the Postgres queries in sqlitedb's types ---

func (p pgQueries) InsertAttachment(ctx context.Context, arg sqlitedb.InsertAttachmentParams) (sqlitedb.Attachment, error) {
	r, err := p.q.InsertAttachment(ctx, pgdb.InsertAttachmentParams(arg))
	return sqlitedb.Attachment(r), err
}

func (p pgQueries) GetAttachment(ctx context.Context, arg sqlitedb.GetAttachmentParams) (sqlitedb.Attachment, error) {
	r, err := p.q.GetAttachment(ctx, pgdb.GetAttachmentParams(arg))
	return sqlitedb.Attachment(r), err
}

func (p pgQueries) SetAttachmentReady(ctx context.Context, arg sqlitedb.SetAttachmentReadyParams) (sqlitedb.Attachment, error) {
	r, err := p.q.SetAttachmentReady(ctx, pgdb.SetAttachmentReadyParams(arg))
	return sqlitedb.Attachment(r), err
}

func (p pgQueries) ListAttachments(ctx context.Context, arg sqlitedb.ListAttachmentsParams) ([]sqlitedb.ListAttachmentsRow, error) {
	rs, err := p.q.ListAttachments(ctx, pgdb.ListAttachmentsParams(arg))
	out := make([]sqlitedb.ListAttachmentsRow, len(rs))
	for i, r := range rs {
		out[i] = sqlitedb.ListAttachmentsRow(r)
	}
	return out, err
}

func (p pgQueries) DeleteAttachmentsByTask(ctx context.Context, arg sqlitedb.DeleteAttachmentsByTaskParams) (int64, error) {
	return p.q.DeleteAttachmentsByTask(ctx, pgdb.DeleteAttachmentsByTaskParams(arg))
}
