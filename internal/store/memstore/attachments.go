package memstore

import (
	"context"
	"slices"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The attachments area of contract_attachments.go. This store holds no blobs yet (P-67), so a
// SetAttachmentReady names no blob and is NOT_FOUND, as memory.go's BlobSHA256 is.

var attachmentKinds = map[store.AttachmentKind]bool{
	store.AttachmentFile: true, store.AttachmentImage: true, store.AttachmentAudio: true, store.AttachmentVideo: true,
}

func (s *Store) CreateAttachment(ctx context.Context, in store.CreateAttachmentInput) (store.Attachment, error) {
	switch {
	case !attachmentKinds[in.Kind]:
		return store.Attachment{}, domain.Validation("unknown attachment kind: "+string(in.Kind), map[string]any{"kind": in.Kind})
	case in.MimeType == "":
		return store.Attachment{}, domain.Validation("an attachment needs a mime type", nil)
	case in.CreatedBy == "":
		return store.Attachment{}, domain.Validation("an attachment needs a creator", nil)
	case in.Bytes < 0:
		return store.Attachment{}, domain.Validation("an attachment's size is not negative", map[string]any{"bytes": in.Bytes})
	}
	return get(ctx, s, func(st *state) (store.Attachment, error) {
		if _, err := st.task(in.ProjectID, in.TaskID); err != nil {
			return store.Attachment{}, err
		}
		a := store.Attachment{
			ProjectID: in.ProjectID, TaskID: in.TaskID, ID: newID("A-", 16), MimeType: in.MimeType, Bytes: in.Bytes,
			Kind: in.Kind, Status: store.AttachmentPending, CreatedBy: in.CreatedBy, TS: nowMillis(),
		}
		st.attachments[[3]string{a.ProjectID, a.TaskID, a.ID}] = a
		return a, nil
	})
}

func (s *Store) SetAttachmentReady(ctx context.Context, projectID, taskID, attachmentID, blobSHA256 string) (store.Attachment, error) {
	return get(ctx, s, func(st *state) (store.Attachment, error) {
		a, ok := st.attachments[[3]string{projectID, taskID, attachmentID}]
		if !ok {
			return store.Attachment{}, domain.NotFound("Attachment", attachmentID)
		}
		if a.Status != store.AttachmentPending {
			return store.Attachment{}, domain.Validation("the attachment is not pending", map[string]any{"attachmentId": attachmentID})
		}
		return store.Attachment{}, domain.NotFound("Blob", blobSHA256)
	})
}

func (s *Store) ListAttachments(ctx context.Context, projectID, taskID string) ([]store.Attachment, error) {
	return get(ctx, s, func(st *state) ([]store.Attachment, error) {
		if _, err := st.task(projectID, taskID); err != nil {
			return nil, err
		}
		out := []store.Attachment{}
		for k, a := range st.attachments {
			if k[0] == projectID && k[1] == taskID {
				out = append(out, a)
			}
		}
		slices.SortFunc(out, func(a, b store.Attachment) int { return byTSAndID(a.TS, b.TS, a.ID, b.ID) })
		return out, nil
	})
}

func (s *Store) DeleteAttachmentsByTask(ctx context.Context, projectID, taskID string) (int64, error) {
	return get(ctx, s, func(st *state) (int64, error) {
		var n int64
		for k := range st.attachments {
			if k[0] == projectID && k[1] == taskID {
				delete(st.attachments, k)
				n++
			}
		}
		return n, nil
	})
}
