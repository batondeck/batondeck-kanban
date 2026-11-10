package service

import (
	"cmp"
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/blob"
	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// BlobURLTTL is how long a signed URL is valid, conductor's SIGNED_URL_TTL_MS.
const BlobURLTTL = 15 * time.Minute

// MediaOptions are the blob store attach_file and list_attachments sign URLs into. With no Files or
// no Signer (serve found no keyring) attach_file is INTERNAL and every downloadUrl is null.
type MediaOptions struct {
	Files  *blob.Store
	Signer *blob.Signer
	// BaseURL is public_url: when set, every signed URL starts with it, whatever the request.
	BaseURL string
	// FallbackURL is the base when BaseURL is unset and the ctx carries no request base
	// (WithRequestBase): a request on the Unix socket, or none. serve sets the first TCP
	// listener's address, an unspecified host made loopback; "" signs a path-only URL.
	FallbackURL string
	// MaxBytes is the largest attachment; zero is core.DefaultBlobMaxBytes.
	MaxBytes int64
}

// media is service.Media: attach_file and list_attachments here, search_tasks in search.go.
type media struct{ c *Core }

var _ Media = media{}

// Media is the media service.
func (c *Core) Media() Media { return media{c} }

// allowedApplicationMime is conductor's ALLOWED_APPLICATION_MIME (src/tools/media.ts).
var allowedApplicationMime = []string{
	"application/pdf", "application/json", "application/zip", "application/msword",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"application/octet-stream",
}

var mediaMimePrefix = regexp.MustCompile(`^(image|audio|video|text)/`)

// allowedMime is conductor's isAllowedMime.
func allowedMime(m string) bool {
	return mediaMimePrefix.MatchString(m) || slices.Contains(allowedApplicationMime, m)
}

// requestBaseKey carries the scheme://host the request reached the server at (WithRequestBase).
type requestBaseKey struct{}

// WithRequestBase is ctx carrying base, the scheme://host of the request ctx serves, which a URL
// signed under ctx starts with when public_url is unset (BlobBase). serve sets it for every TCP
// request; MCP carries it from the POST to the tool call.
func WithRequestBase(ctx context.Context, base string) context.Context {
	return context.WithValue(ctx, requestBaseKey{}, base)
}

// RequestBase is the base WithRequestBase put on ctx, "" for none.
func RequestBase(ctx context.Context) string {
	b, _ := ctx.Value(requestBaseKey{}).(string)
	return b
}

// BlobBase is the base a URL signed under ctx starts with: public_url when set, else the
// request's (RequestBase), else FallbackURL. It is chosen per call and never stored, so a
// request's Host shapes only the URLs in its own response.
func (c *Core) BlobBase(ctx context.Context) string {
	return cmp.Or(c.media.BaseURL, RequestBase(ctx), c.media.FallbackURL)
}

// SignBlobURL is id's URL signed for p until exp, at BlobBase(ctx). Every signed blob URL a
// response carries is made here; "" when serve found no keyring (no Signer).
func (c *Core) SignBlobURL(ctx context.Context, id string, p blob.Purpose, exp time.Time) string {
	if c.media.Signer == nil {
		return ""
	}
	return c.media.Signer.URL(c.BlobBase(ctx), id, p, exp)
}

// signedExpiry is now plus BlobURLTTL, to the second the URL carries.
func (c *Core) signedExpiry() time.Time {
	return c.now().Add(BlobURLTTL).Truncate(time.Second)
}

// AttachFile is attach_file: a pending attachment row and an upload URL for it, signed for upload
// until expiresAt. The task's version moves once; the event is attachment.created.
func (m media) AttachFile(ctx context.Context, caller domain.Caller, req AttachFileReq) (AttachFileResp, error) {
	c := m.c
	a, err := c.authorize(ctx, caller, req.ProjectID, "attach_file", domain.GrantSubject{}, true)
	if err != nil {
		return AttachFileResp{}, err
	}
	for _, f := range []struct{ name, v string }{{"taskId", req.TaskID}, {"fileName", req.FileName}, {"mimeType", req.MimeType}} {
		if err := required(f.name, f.v); err != nil {
			return AttachFileResp{}, err
		}
	}
	switch {
	case !slices.Contains(MediaKindValues, req.Kind):
		return AttachFileResp{}, domain.Validation("unknown attachment kind: "+string(req.Kind), map[string]any{"field": "kind"})
	case req.Bytes < 0:
		return AttachFileResp{}, domain.Validation("bytes must be a nonnegative integer", map[string]any{"field": "bytes"})
	case req.Bytes > c.media.MaxBytes:
		return AttachFileResp{}, domain.Validation("bytes exceeds blob.max_bytes", map[string]any{"field": "bytes", "max": c.media.MaxBytes})
	case !allowedMime(req.MimeType):
		return AttachFileResp{}, domain.Validation("mimeType not allowed: "+req.MimeType, map[string]any{"field": "mimeType"})
	}
	if err := checkIdempotencyKey(req.IdempotencyKey); err != nil {
		return AttachFileResp{}, err
	}
	if c.media.Files == nil || c.media.Signer == nil {
		return AttachFileResp{}, internalf("attach_file: no blob signing key (serve found no keyring)")
	}
	return mutate(ctx, c, caller, mutation{
		Tool: "attach_file", Args: map[string]any{"projectId": req.ProjectID, "taskId": req.TaskID},
		ProjectID: req.ProjectID, TaskID: req.TaskID, IdempotencyKey: req.IdempotencyKey, Request: req, Access: a,
	}, func(t *txn) (AttachFileResp, error) {
		att, err := t.Tx.CreateAttachment(ctx, store.CreateAttachmentInput{
			ProjectID: req.ProjectID, TaskID: req.TaskID, MimeType: req.MimeType, Bytes: req.Bytes,
			Kind: store.AttachmentKind(req.Kind), CreatedBy: caller.Identity,
		})
		if err != nil {
			return AttachFileResp{}, err
		}
		data, err := json.Marshal(map[string]any{"attachmentId": att.ID, "kind": req.Kind, "mimeType": req.MimeType, "bytes": req.Bytes})
		if err != nil {
			return AttachFileResp{}, err
		}
		t.Event(store.EventInput{Type: domain.EventAttachmentCreated, Data: data})
		exp := c.signedExpiry()
		ref := blob.Ref{ProjectID: att.ProjectID, TaskID: att.TaskID, AttachmentID: att.ID}
		return AttachFileResp{
			AttachmentID: att.ID, ExpiresAt: isoMs(exp.UnixMilli()), UploadURL: c.SignBlobURL(ctx, ref.String(), blob.Upload, exp),
		}, nil
	})
}

// ListAttachments is list_attachments: the task's attachments, oldest first, each ready one with a
// download URL valid for BlobURLTTL. gcsUri is "blob:" and the attachment's upload ref, the
// locator conductor's gs:// URI was; thumbUri and extractedText are null (no media function).
func (m media) ListAttachments(ctx context.Context, caller domain.Caller, req ListAttachmentsReq) (ListAttachmentsResp, error) {
	c := m.c
	if _, err := c.authorize(ctx, caller, req.ProjectID, "list_attachments", domain.GrantSubject{}, true); err != nil {
		return ListAttachmentsResp{}, err
	}
	if err := required("taskId", req.TaskID); err != nil {
		return ListAttachmentsResp{}, err
	}
	atts, err := c.store.ListAttachments(ctx, req.ProjectID, req.TaskID)
	if err != nil {
		return ListAttachmentsResp{}, c.mapErr("list_attachments", err)
	}
	exp := c.signedExpiry()
	out := make([]Attachment, 0, len(atts))
	for _, a := range atts {
		row := Attachment{
			Bytes: float64(a.Bytes), CreatedBy: a.CreatedBy, GCSURI: "blob:" + blob.Ref{ProjectID: a.ProjectID, TaskID: a.TaskID, AttachmentID: a.ID}.String(),
			ID: a.ID, Kind: MediaKind(a.Kind), MimeType: a.MimeType, Status: AttachmentStatus(a.Status), TaskID: a.TaskID, Ts: isoMs(a.TS),
		}
		if a.Status == store.AttachmentReady {
			sha := a.BlobSHA256
			row.SHA256 = &sha
			if c.media.Signer != nil {
				u := c.SignBlobURL(ctx, sha, blob.Download, exp)
				row.DownloadURL = &u
			}
		}
		out = append(out, row)
	}
	return ListAttachmentsResp{Attachments: out}, nil
}

// CompleteUpload ends a signed PUT /blobs/{id} (internal/api): st, the body read to disk, becomes
// ref's blob. The signature is the authority, so no membership is checked; the mutation runs as the
// attachment's creator. Under the hash's lock, the file is moved into place first and then, in one
// mutation, the blob row (PutBlob), the attachment ready on it, attachment.updated {attachmentId,
// status, sha256} and the task's one version bump: a ready row always has its file. A refusal
// leaves the attachment pending and unlinks the file unless a blob row still names it. A missing
// attachment is NOT_FOUND, one not pending INVALID_TRANSITION, a body whose size is not the
// declared bytes VALIDATION.
func (c *Core) CompleteUpload(ctx context.Context, ref blob.Ref, st *blob.Staged) error {
	files := c.media.Files
	if files == nil {
		st.Discard()
		return internalf("blob upload: no blob store")
	}
	att, err := c.pendingAttachment(ctx, c.store, ref, st)
	if err != nil {
		st.Discard()
		return c.mapErr("blob upload", err)
	}
	unlock := files.Lock(st.SHA256)
	defer unlock()
	if err := files.Commit(st); err != nil {
		st.Discard()
		return c.mapErr("blob upload", err)
	}
	_, err = mutate(ctx, c, domain.Caller{Identity: att.CreatedBy}, mutation{
		Tool: "attach_file", Args: map[string]any{"projectId": ref.ProjectID, "taskId": ref.TaskID},
		ProjectID: ref.ProjectID, TaskID: ref.TaskID,
	}, func(t *txn) (struct{}, error) {
		if _, err := c.pendingAttachment(ctx, t.Tx, ref, st); err != nil {
			return struct{}{}, err
		}
		if _, err := t.Tx.PutBlob(ctx, st.SHA256, st.Bytes); err != nil {
			return struct{}{}, err
		}
		if _, err := t.Tx.SetAttachmentReady(ctx, ref.ProjectID, ref.TaskID, ref.AttachmentID, st.SHA256); err != nil {
			if e, ok := domain.AsBatonDeckError(err); ok && e.Code == domain.CodeValidation {
				return struct{}{}, uploadedAlready(ref.AttachmentID, store.AttachmentReady)
			}
			return struct{}{}, err
		}
		data, err := json.Marshal(map[string]any{"attachmentId": ref.AttachmentID, "status": store.AttachmentReady, "sha256": st.SHA256})
		if err != nil {
			return struct{}{}, err
		}
		t.Event(store.EventInput{Type: domain.EventAttachmentUpdated, Data: data})
		return struct{}{}, nil
	})
	if err != nil {
		c.unlinkUnreferenced(ctx, st.SHA256)
	}
	return err
}

// pendingAttachment is ref's attachment, refused unless it is pending and st is its declared size.
func (c *Core) pendingAttachment(ctx context.Context, s store.Store, ref blob.Ref, st *blob.Staged) (store.Attachment, error) {
	atts, err := s.ListAttachments(ctx, ref.ProjectID, ref.TaskID)
	if err != nil {
		return store.Attachment{}, err
	}
	i := slices.IndexFunc(atts, func(a store.Attachment) bool { return a.ID == ref.AttachmentID })
	if i < 0 {
		return store.Attachment{}, domain.NotFound("Attachment", ref.AttachmentID)
	}
	a := atts[i]
	if a.Status != store.AttachmentPending {
		return store.Attachment{}, uploadedAlready(a.ID, a.Status)
	}
	if st.Bytes != a.Bytes {
		return store.Attachment{}, domain.Validation("the body is not the size attach_file declared", map[string]any{"bytes": st.Bytes, "declared": a.Bytes})
	}
	return a, nil
}

func uploadedAlready(id string, status store.AttachmentStatus) *domain.BatonDeckError {
	return domain.NewError(domain.CodeInvalidTransition, "the attachment is "+string(status)+", not pending: it was uploaded already",
		map[string]any{"attachmentId": id, "status": status})
}

// unlinkUnreferenced removes sha's file when no blob row names it (an upload refused after its file
// was moved into place). Call it under the hash's lock.
func (c *Core) unlinkUnreferenced(ctx context.Context, sha string) {
	_, err := c.store.GetBlob(ctx, sha)
	if e, ok := domain.AsBatonDeckError(err); !ok || e.Code != domain.CodeNotFound {
		return
	}
	if err := c.media.Files.Remove(sha); err != nil {
		c.log.Printf("service: blob %s: %+v", sha, err)
	}
}

// CollectBlobs is the blob GC (Options.BlobGC's default, and retention's for the unreferenced
// blobs it lists): for each hash, under its lock, the row is deleted
// unless an attachment or memory entry still names it, and then the file. A hash still referenced
// keeps both; one with no row left loses its file.
func (c *Core) CollectBlobs(ctx context.Context, sha256s []string) {
	for _, sha := range sha256s {
		func() {
			defer c.media.Files.Lock(sha)()
			err := c.store.DeleteBlob(ctx, sha)
			e, _ := domain.AsBatonDeckError(err)
			switch {
			case err == nil, e != nil && e.Code == domain.CodeNotFound:
				err = c.media.Files.Remove(sha)
			case e != nil && e.Code == domain.CodeValidation: // still referenced
				err = nil
			}
			if err != nil {
				c.log.Printf("service: blob gc %s: %+v", sha, err)
			}
		}()
	}
}
