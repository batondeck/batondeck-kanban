package memstore

import (
	"context"
	"slices"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store"
)

// The blobs area of contract_blobs.go: a row per content hash, referenced while an attachment or a
// memory entry names it.

func (st *state) blobReferenced(sha string) bool {
	for _, a := range st.attachments {
		if a.BlobSHA256 == sha {
			return true
		}
	}
	for _, e := range st.memory {
		if e.BlobSHA256 == sha {
			return true
		}
	}
	return false
}

func (s *Store) PutBlob(ctx context.Context, sha256 string, bytes int64) (store.Blob, error) {
	switch {
	case !domain.ValidSHA256(sha256):
		return store.Blob{}, domain.Validation("a blob hash is 64 lowercase hex digits", map[string]any{"sha256": sha256})
	case bytes < 0:
		return store.Blob{}, domain.Validation("a blob's size is not negative", map[string]any{"bytes": bytes})
	}
	return get(ctx, s, func(st *state) (store.Blob, error) {
		if b, ok := st.blobs[sha256]; ok {
			return b, nil
		}
		b := store.Blob{SHA256: sha256, Bytes: bytes, CreatedAt: nowMillis()}
		st.blobs[sha256] = b
		return b, nil
	})
}

func (s *Store) GetBlob(ctx context.Context, sha256 string) (store.Blob, error) {
	return get(ctx, s, func(st *state) (store.Blob, error) {
		b, ok := st.blobs[sha256]
		if !ok {
			return store.Blob{}, domain.NotFound("Blob", sha256)
		}
		return b, nil
	})
}

func (s *Store) DeleteBlob(ctx context.Context, sha256 string) error {
	return s.do(ctx, func(st *state) error {
		if _, ok := st.blobs[sha256]; !ok {
			return domain.NotFound("Blob", sha256)
		}
		if st.blobReferenced(sha256) {
			return domain.Validation("the blob is referenced", map[string]any{"sha256": sha256})
		}
		delete(st.blobs, sha256)
		return nil
	})
}

func (s *Store) ListUnreferencedBlobs(ctx context.Context, olderThan int64, limit int) ([]store.Blob, error) {
	if limit <= 0 {
		return nil, domain.Validation("a blob list needs a positive limit", map[string]any{"limit": limit})
	}
	return get(ctx, s, func(st *state) ([]store.Blob, error) {
		out := []store.Blob{}
		for sha, b := range st.blobs {
			if b.CreatedAt < olderThan && !st.blobReferenced(sha) {
				out = append(out, b)
			}
		}
		slices.SortFunc(out, func(a, b store.Blob) int { return byTSAndID(a.CreatedAt, b.CreatedAt, a.SHA256, b.SHA256) })
		return out[:min(limit, len(out))], nil
	})
}
