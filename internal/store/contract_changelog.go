package store

import (
	"context"
	"encoding/json"
)

// Change is one change-log row: what the bus fans out. ID is monotonic, Ts Unix milliseconds,
// Scope the token scope that may see it, Payload a JSON object.
type Change struct {
	ID        int64
	Scope     string
	ProjectID *string
	Kind      string
	Payload   json.RawMessage
	Ts        int64
}

// ChangeInput is a change to append. ProjectID "" is none; Payload nil is {}.
type ChangeInput struct {
	Scope     string
	ProjectID string
	Kind      string
	Payload   json.RawMessage
	Ts        int64
}

// ChangeLog is the change log the bus tails. Keys: a change is its id, assigned here, each one
// higher than every id appended before it on the store; an id a rolled-back transaction took may
// never appear (a gap). Written inside the service's Atomically, a change is visible to readers
// only once that transaction commits; ids follow the order of appends, not of commits, so where
// writers run concurrently (Postgres) a lower id can become visible after a higher one. Errors: an empty scope or kind, Ts <= 0 and a payload that is
// not a JSON object are domain.Validation. Reads are oldest first (id ascending), strictly after
// since; a limit <= 0 is 100 and above 1000 is 1000.
type ChangeLog interface {
	// AppendChange appends the change and returns its id.
	AppendChange(ctx context.Context, in ChangeInput) (int64, error)
	// ReadChanges is the changes after since, of every scope.
	ReadChanges(ctx context.Context, since int64, limit int) ([]Change, error)
	// ReadChangesForScope is the changes after since of one scope, over the (token_scope, id) index.
	ReadChangesForScope(ctx context.Context, scope string, since int64, limit int) ([]Change, error)
	// DeleteChangesOlderThan deletes at most limit changes with Ts before before, lowest ids first, and
	// returns how many it deleted. A limit <= 0 is domain.Validation.
	DeleteChangesOlderThan(ctx context.Context, before int64, limit int) (int64, error)
}

var (
	_ ChangeLog = (*SQLite)(nil)
	_ ChangeLog = (*Postgres)(nil)
)
