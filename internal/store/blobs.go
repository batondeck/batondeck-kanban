package store

import (
	"context"
	"fmt"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

// The blobs area of contract_blobs.go, on both engines. A put is an insert that keeps an existing
// row, then its read; a delete judges the references in its own statement, and only a delete that
// took nothing reads the row to tell a missing blob from a referenced one.

type blobQueries interface {
	InsertBlob(ctx context.Context, arg sqlitedb.InsertBlobParams) error
	GetBlob(ctx context.Context, sha256 string) (sqlitedb.Blob, error)
	DeleteUnreferencedBlob(ctx context.Context, sha256 string) (int64, error)
	ListUnreferencedBlobs(ctx context.Context, arg sqlitedb.ListUnreferencedBlobsParams) ([]sqlitedb.Blob, error)
}

func badHash(sha string) error {
	return domain.Validation("a blob hash is 64 lowercase hex digits", map[string]any{"sha256": sha})
}

func (s *SQLite) PutBlob(ctx context.Context, sha256 string, bytes int64) (Blob, error) {
	return putBlob(ctx, s, sha256, bytes)
}

func (s *Postgres) PutBlob(ctx context.Context, sha256 string, bytes int64) (Blob, error) {
	return putBlob(ctx, s, sha256, bytes)
}

func (s *SQLite) GetBlob(ctx context.Context, sha256 string) (Blob, error) {
	return getBlob(ctx, s.queries(false), sha256)
}

func (s *Postgres) GetBlob(ctx context.Context, sha256 string) (Blob, error) {
	return getBlob(ctx, s.queries(false), sha256)
}

func (s *SQLite) DeleteBlob(ctx context.Context, sha256 string) error {
	return deleteBlob(ctx, s, sha256)
}

func (s *Postgres) DeleteBlob(ctx context.Context, sha256 string) error {
	return deleteBlob(ctx, s, sha256)
}

func (s *SQLite) ListUnreferencedBlobs(ctx context.Context, olderThan int64, limit int) ([]Blob, error) {
	return listUnreferencedBlobs(ctx, s, olderThan, limit)
}

func (s *Postgres) ListUnreferencedBlobs(ctx context.Context, olderThan int64, limit int) ([]Blob, error) {
	return listUnreferencedBlobs(ctx, s, olderThan, limit)
}

func putBlob(ctx context.Context, e engine, sha string, bytes int64) (Blob, error) {
	if !domain.ValidSHA256(sha) {
		return Blob{}, badHash(sha)
	}
	if bytes < 0 {
		return Blob{}, domain.Validation("a blob's size is not negative", map[string]any{"bytes": bytes})
	}
	var out Blob
	err := e.Atomically(ctx, func(tx Store) error {
		q := on(tx).queries(true)
		if err := q.InsertBlob(ctx, sqlitedb.InsertBlobParams{Sha256: sha, Bytes: bytes, CreatedAt: nowMillis()}); err != nil {
			return fmt.Errorf("store: put blob: %w", err)
		}
		var err error
		out, err = getBlob(ctx, q, sha)
		return err
	})
	return out, err
}

func getBlob(ctx context.Context, q sqlQueries, sha string) (Blob, error) {
	r, err := q.GetBlob(ctx, sha)
	if err := found(err, "Blob", sha); err != nil {
		return Blob{}, err
	}
	return Blob{SHA256: r.Sha256, Bytes: r.Bytes, CreatedAt: r.CreatedAt}, nil
}

func deleteBlob(ctx context.Context, e engine, sha string) error {
	return e.Atomically(ctx, func(tx Store) error {
		q := on(tx).queries(true)
		n, err := q.DeleteUnreferencedBlob(ctx, sha)
		if err != nil {
			return fmt.Errorf("store: delete blob: %w", err)
		}
		if n > 0 {
			return nil
		}
		if _, err := getBlob(ctx, q, sha); err != nil {
			return err
		}
		return domain.Validation("the blob is referenced", map[string]any{"sha256": sha})
	})
}

func listUnreferencedBlobs(ctx context.Context, e engine, olderThan int64, limit int) ([]Blob, error) {
	if limit <= 0 {
		return nil, domain.Validation("a blob list needs a positive limit", map[string]any{"limit": limit})
	}
	rs, err := e.queries(false).ListUnreferencedBlobs(ctx, sqlitedb.ListUnreferencedBlobsParams{OlderThan: olderThan, PageLimit: int64(limit)})
	if err != nil {
		return nil, fmt.Errorf("store: list unreferenced blobs: %w", err)
	}
	out := make([]Blob, len(rs))
	for i, r := range rs {
		out[i] = Blob{SHA256: r.Sha256, Bytes: r.Bytes, CreatedAt: r.CreatedAt}
	}
	return out, nil
}

// --- the Postgres queries in sqlitedb's types ---

func (p pgQueries) InsertBlob(ctx context.Context, arg sqlitedb.InsertBlobParams) error {
	return p.q.InsertBlob(ctx, pgdb.InsertBlobParams(arg))
}

func (p pgQueries) GetBlob(ctx context.Context, sha256 string) (sqlitedb.Blob, error) {
	r, err := p.q.GetBlob(ctx, sha256)
	return sqlitedb.Blob(r), err
}

func (p pgQueries) DeleteUnreferencedBlob(ctx context.Context, sha256 string) (int64, error) {
	return p.q.DeleteUnreferencedBlob(ctx, sha256)
}

func (p pgQueries) ListUnreferencedBlobs(ctx context.Context, arg sqlitedb.ListUnreferencedBlobsParams) ([]sqlitedb.Blob, error) {
	rs, err := p.q.ListUnreferencedBlobs(ctx, pgdb.ListUnreferencedBlobsParams(arg))
	out := make([]sqlitedb.Blob, len(rs))
	for i, r := range rs {
		out[i] = sqlitedb.Blob(r)
	}
	return out, err
}
