package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
	"github.com/batondeck/batondeck-kanban/internal/store/pgdb"
	"github.com/batondeck/batondeck-kanban/internal/store/sqlitedb"
)

type idempotencyQueries interface {
	ClaimIdempotencyKey(ctx context.Context, arg sqlitedb.ClaimIdempotencyKeyParams) (string, error)
	GetIdempotencyKey(ctx context.Context, arg sqlitedb.GetIdempotencyKeyParams) (sqlitedb.IdempotencyKey, error)
	SetIdempotencyResult(ctx context.Context, arg sqlitedb.SetIdempotencyResultParams) error
	DeleteProjectIdempotencyKeys(ctx context.Context, projectID string) error
	DeleteExpiredIdempotencyKeys(ctx context.Context, arg sqlitedb.DeleteExpiredIdempotencyKeysParams) (int64, error)
}

func (s *SQLite) DeleteExpiredIdempotencyKeys(ctx context.Context, now int64, limit int) (int64, error) {
	return deleteExpiredIdempotencyKeys(ctx, s, now, limit)
}

func (s *Postgres) DeleteExpiredIdempotencyKeys(ctx context.Context, now int64, limit int) (int64, error) {
	return deleteExpiredIdempotencyKeys(ctx, s, now, limit)
}

func deleteExpiredIdempotencyKeys(ctx context.Context, e engine, now int64, limit int) (int64, error) {
	if limit <= 0 {
		return 0, domain.Validation("a sweep needs a positive limit", map[string]any{"limit": limit})
	}
	n, err := e.queries(true).DeleteExpiredIdempotencyKeys(ctx, sqlitedb.DeleteExpiredIdempotencyKeysParams{Now: now, PageLimit: int64(limit)})
	if err != nil {
		return 0, fmt.Errorf("store: sweep idempotency keys: %w", err)
	}
	return n, nil
}

func (s *SQLite) WithIdempotency(ctx context.Context, projectID, identity, key string, ttl time.Duration, fn func(tx Store) ([]byte, error)) ([]byte, error) {
	return withIdempotency(ctx, s, projectID, identity, key, ttl, fn)
}

func (s *Postgres) WithIdempotency(ctx context.Context, projectID, identity, key string, ttl time.Duration, fn func(tx Store) ([]byte, error)) ([]byte, error) {
	return withIdempotency(ctx, s, projectID, identity, key, ttl, fn)
}

// withIdempotency claims the key first, in the transaction fn then runs in: a claim that finds a
// live entry returns its result without running fn, and a claim that wins holds the key's row until
// the commit, so a concurrent call with the same key waits at its own claim (Postgres) or at BEGIN
// (SQLite) and then finds the result.
func withIdempotency(ctx context.Context, e engine, projectID, identity, key string, ttl time.Duration, fn func(tx Store) ([]byte, error)) ([]byte, error) {
	var out []byte
	err := e.Atomically(ctx, func(tx Store) error {
		if key == "" {
			r, err := fn(tx)
			out = r
			return err
		}
		if ttl <= 0 {
			return domain.Validation("an idempotency key needs a positive window", map[string]any{"ttlMs": ttl.Milliseconds()})
		}
		q := on(tx).queries(true)
		now := nowMillis()
		_, err := q.ClaimIdempotencyKey(ctx, sqlitedb.ClaimIdempotencyKeyParams{
			ProjectID: projectID, Identity: identity, Key: key, CreatedAt: now, ExpiresAt: now + ttl.Milliseconds(),
		})
		switch {
		case isNoRows(err):
			kept, err := q.GetIdempotencyKey(ctx, sqlitedb.GetIdempotencyKeyParams{ProjectID: projectID, Identity: identity, Key: key})
			if err != nil {
				return fmt.Errorf("store: idempotency replay: %w", err)
			}
			out = []byte(kept.Result)
			return nil
		case err != nil:
			return fmt.Errorf("store: idempotency claim: %w", err)
		}
		r, err := fn(tx)
		if err != nil {
			return err
		}
		if err := q.SetIdempotencyResult(ctx, sqlitedb.SetIdempotencyResultParams{
			Result: string(r), ProjectID: projectID, Identity: identity, Key: key,
		}); err != nil {
			return fmt.Errorf("store: idempotency result: %w", err)
		}
		out = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// idempotent is WithIdempotency for a typed result: fn's value is kept as JSON and a replay decodes
// it. The key is opts.IdempotencyKey under (scope, opts.Identity).
func idempotent[T any](ctx context.Context, s Store, scope string, opts WriteOptions, fn func(tx Store) (T, error)) (T, error) {
	var zero T
	raw, err := s.WithIdempotency(ctx, scope, opts.Identity, opts.IdempotencyKey, opts.IdempotencyTTL, func(tx Store) ([]byte, error) {
		v, err := fn(tx)
		if err != nil {
			return nil, err
		}
		return json.Marshal(v)
	})
	if err != nil {
		return zero, err
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return zero, fmt.Errorf("store: idempotency result: %w", err)
	}
	return v, nil
}

func (p pgQueries) ClaimIdempotencyKey(ctx context.Context, arg sqlitedb.ClaimIdempotencyKeyParams) (string, error) {
	return p.q.ClaimIdempotencyKey(ctx, pgdb.ClaimIdempotencyKeyParams(arg))
}

func (p pgQueries) GetIdempotencyKey(ctx context.Context, arg sqlitedb.GetIdempotencyKeyParams) (sqlitedb.IdempotencyKey, error) {
	r, err := p.q.GetIdempotencyKey(ctx, pgdb.GetIdempotencyKeyParams(arg))
	return sqlitedb.IdempotencyKey(r), err
}

func (p pgQueries) SetIdempotencyResult(ctx context.Context, arg sqlitedb.SetIdempotencyResultParams) error {
	return p.q.SetIdempotencyResult(ctx, pgdb.SetIdempotencyResultParams(arg))
}

func (p pgQueries) DeleteExpiredIdempotencyKeys(ctx context.Context, arg sqlitedb.DeleteExpiredIdempotencyKeysParams) (int64, error) {
	return p.q.DeleteExpiredIdempotencyKeys(ctx, pgdb.DeleteExpiredIdempotencyKeysParams(arg))
}

func (p pgQueries) DeleteProjectIdempotencyKeys(ctx context.Context, projectID string) error {
	return p.q.DeleteProjectIdempotencyKeys(ctx, projectID)
}
