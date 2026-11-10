package memstore

import (
	"context"
	"slices"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The attachments area of contract_attachments.go. SetAttachmentReady names a blob of this store
// (blobs.go) or is NOT_FOUND.

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
			Kind: in.Kind, Status: store.AttachmentPending, CreatedBy: in.CreatedBy, TS: nowMillis(), FileName: in.FileName,
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
		if _, ok := st.blobs[blobSHA256]; !ok {
			return store.Attachment{}, domain.NotFound("Blob", blobSHA256)
		}
		a.BlobSHA256, a.Status = blobSHA256, store.AttachmentReady
		st.attachments[[3]string{projectID, taskID, attachmentID}] = a
		return a, nil
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

func (s *Store) DeleteStalePendingAttachments(ctx context.Context, before int64, limit int) (int64, error) {
	if limit <= 0 {
		return 0, domain.Validation("a delete needs a positive limit", map[string]any{"limit": limit})
	}
	return get(ctx, s, func(st *state) (int64, error) {
		var stale []store.Attachment
		for _, a := range st.attachments {
			if a.Status == store.AttachmentPending && a.TS < before {
				stale = append(stale, a)
			}
		}
		slices.SortFunc(stale, func(a, b store.Attachment) int { return byTSAndID(a.TS, b.TS, a.ID, b.ID) })
		stale = stale[:min(len(stale), limit)]
		for _, a := range stale {
			delete(st.attachments, [3]string{a.ProjectID, a.TaskID, a.ID})
		}
		return int64(len(stale)), nil
	})
}
