package api

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/blob"
	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Uploads is the service's end of an upload (service.Core.CompleteUpload).
type Uploads interface {
	CompleteUpload(ctx context.Context, ref blob.Ref, st *blob.Staged) error
}

// BlobRoutes are PUT and GET /blobs/{id}?exp=…&sig=…, the URLs attach_file and list_attachments
// sign. The signature is the authority: they are mounted outside every bearer, session and CSRF
// check. Each refuses a signature for the other purpose, another id or expiry, or a tampered one
// with 403, and an expired one with 403; every refusal is logged with its reason and never the
// signature. Use it by pointer: it holds the set of uploads in flight.
type BlobRoutes struct {
	Signer *blob.Signer
	Files  *blob.Store
	// MaxBytes caps an upload's body, counted in bytes read.
	MaxBytes int64
	Uploads  Uploads
	Logger   *log.Logger
	// Now is the clock; nil is time.Now.
	Now func() time.Time

	mu       sync.Mutex
	inFlight map[string]bool // upload ids whose body is being read
}

// verify checks the request's signature for p and answers its id and expiry, or writes the 403.
func (b *BlobRoutes) verify(w http.ResponseWriter, r *http.Request, p blob.Purpose) (string, time.Time, bool) {
	now := time.Now
	if b.Now != nil {
		now = b.Now
	}
	id, q := r.PathValue("id"), r.URL.Query()
	err := b.Signer.Verify(id, p, q.Get("exp"), q.Get("sig"), now())
	if err == nil {
		exp, _ := strconv.ParseInt(q.Get("exp"), 10, 64) // Verify parsed it
		return id, time.Unix(exp, 0), true
	}
	reason, message := "bad signature", "the URL's signature does not match it"
	if errors.Is(err, blob.ErrExpired) {
		reason, message = "expired", "the URL has expired"
	}
	b.Logger.Printf("api: blob %s %s refused: %s", r.Method, id, reason)
	auth.WriteError(w, http.StatusForbidden, domain.CodeForbidden, message)
	return "", time.Time{}, false
}

// claim marks id's upload in flight; false if one already is.
func (b *BlobRoutes) claim(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.inFlight[id] {
		return false
	}
	if b.inFlight == nil {
		b.inFlight = map[string]bool{}
	}
	b.inFlight[id] = true
	return true
}

func (b *BlobRoutes) release(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.inFlight, id)
}

// Upload is PUT /blobs/{projectId.taskId.attachmentId}: the body becomes the pending attachment's
// blob (service.Core.CompleteUpload) and the answer is 200. Refusals: the signature 403; a body
// over MaxBytes 413, judged by its Content-Length before reading and by the bytes read, with nothing
// left on disk; a body still arriving when the URL expires 403 (the connection's read deadline is
// the expiry), with nothing left on disk; a second PUT while one is reading the same id's body 409;
// an id that is not an attachment ref 404; a body that would leave the blob volume under its reserve
// (blob.Store.Reserve) 507 QUOTA_EXCEEDED, with nothing left on disk; the service's NOT_FOUND 404,
// INVALID_TRANSITION (uploaded already) 409, VALIDATION (not the declared size) 400.
func (b *BlobRoutes) Upload() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, exp, ok := b.verify(w, r, blob.Upload)
		if !ok {
			return
		}
		ref, ok := blob.ParseRef(id)
		if !ok {
			auth.WriteError(w, http.StatusNotFound, domain.CodeNotFound, "no such upload")
			return
		}
		if r.ContentLength > b.MaxBytes {
			b.tooLarge(w, id)
			return
		}
		if err := http.NewResponseController(w).SetReadDeadline(exp); err != nil {
			b.Logger.Printf("api: blob PUT %s: setting the read deadline: %+v", id, err)
			auth.WriteError(w, http.StatusInternalServerError, domain.CodeInternal, "Internal error")
			return
		}
		if !b.claim(id) {
			b.Logger.Printf("api: blob PUT %s refused: an upload of it is in flight", id)
			auth.WriteError(w, http.StatusConflict, domain.CodeInvalidTransition, "an upload of this attachment is in progress")
			return
		}
		defer b.release(id)
		st, err := b.Files.Stage(http.MaxBytesReader(w, r.Body, b.MaxBytes))
		var mb *http.MaxBytesError
		var ne net.Error
		switch {
		case errors.As(err, &mb):
			b.tooLarge(w, id)
			return
		case errors.Is(err, blob.ErrFull):
			b.Logger.Printf("api: blob PUT %s refused: the blob volume is down to its reserve of %d bytes", id, b.Files.Reserve)
			auth.WriteError(w, http.StatusInsufficientStorage, domain.CodeQuotaExceeded, "the server's attachment storage is full")
			return
		case errors.As(err, &ne) && ne.Timeout():
			b.Logger.Printf("api: blob PUT %s refused: expired mid-body", id)
			auth.WriteError(w, http.StatusForbidden, domain.CodeForbidden, "the URL expired before the body arrived")
			return
		case err != nil:
			b.Logger.Printf("api: blob PUT %s: reading the body: %+v", id, err)
			auth.WriteError(w, http.StatusBadRequest, domain.CodeValidation, "the body could not be read")
			return
		}
		if err := b.Uploads.CompleteUpload(r.Context(), ref, st); err != nil {
			writeServiceError(w, err, b.Logger)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
}

func (b *BlobRoutes) tooLarge(w http.ResponseWriter, id string) {
	b.Logger.Printf("api: blob PUT %s refused: over %d bytes", id, b.MaxBytes)
	auth.WriteError(w, http.StatusRequestEntityTooLarge, domain.CodeValidation, "the body is over blob.max_bytes")
}

// Download is GET /blobs/{sha256}: the blob's bytes as application/octet-stream, served as an
// attachment and never sniffed, so no upload renders as a page of this origin. A signature passes
// before the file is looked up; a blob with no file is 404.
func (b *BlobRoutes) Download() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _, ok := b.verify(w, r, blob.Download)
		if !ok {
			return
		}
		f, err := b.Files.OpenFile(id)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				b.Logger.Printf("api: blob GET %s: %+v", id, err)
			}
			auth.WriteError(w, http.StatusNotFound, domain.CodeNotFound, "no such blob")
			return
		}
		defer f.Close()
		h := w.Header()
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", "attachment")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "private, no-store")
		http.ServeContent(w, r, "", time.Time{}, f)
	})
}
