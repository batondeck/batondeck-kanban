package store

import (
	"context"
	"time"
)

// GlobalScope is the project id an idempotency entry is kept under when the mutation creates the
// project itself (create_project), as conductor's '_global'. No project id has this form.
const GlobalScope = "_global"

// WriteOptions are what a creating mutation takes besides its input: who is acting, and the
// caller's optional idempotency key with the window it is replayed in (IDEMPOTENCY_TTL_MS, read by
// the caller's config, never here).
type WriteOptions struct {
	Identity       string
	IdempotencyKey string
	IdempotencyTTL time.Duration
}

// Idempotency is the replay of a creating mutation's result. Keys are (project_id, identity, key):
// one identity's key never replays another identity's result, and the key of one project never
// another's.
type Idempotency interface {
	// WithIdempotency runs fn in ONE transaction and keeps its result under (projectID, identity,
	// key) until ttl has passed; a call with the same three inside that window returns the kept
	// result and does not run fn. Outside the window the key counts as never seen: fn runs again and
	// its result replaces the old one. A key of "" runs fn with nothing kept. fn's error rolls back
	// everything, the key included, so a failed call can be retried under the same key. Two
	// concurrent calls with one key run fn once: the second waits for the first transaction and
	// returns its result. ttl <= 0 with a key is VALIDATION. The window is fixed at the first write,
	// never extended by a replay.
	WithIdempotency(ctx context.Context, projectID, identity, key string, ttl time.Duration, fn func(tx Store) ([]byte, error)) ([]byte, error)
	// DeleteExpiredIdempotencyKeys deletes at most limit kept results whose window ended at or before
	// now, the oldest expiry first, global ones included, and returns how many: such a key counts as
	// never seen, so its row only holds a mutation's result. limit <= 0 is VALIDATION. The retention
	// sweep calls it.
	DeleteExpiredIdempotencyKeys(ctx context.Context, now int64, limit int) (int64, error)
}

var (
	_ Idempotency = (*SQLite)(nil)
	_ Idempotency = (*Postgres)(nil)
)
