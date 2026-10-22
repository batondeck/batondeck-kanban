// Package store is the persistence boundary: one Store contract, one implementation per
// engine — SQLite (default) and Postgres. internal/store/conformance is the single suite both
// are to be judged by, so the engines cannot drift apart; no test runs it yet.
package store

import "context"

// Store is the whole persistence surface every engine implements: the role interfaces below,
// one per area of the schema. A consumer that needs one area takes that role, not the whole Store.
// It holds no area yet; the domain lanes add them.
type Store interface {
	Lifecycle
}

// Lifecycle opens, migrates and closes: what every engine does before it holds anything.
type Lifecycle interface {
	// Migrate applies every pending migration of the engine's schema (migrations/sqlite or
	// migrations/postgres). Postgres processes sharing a database take turns under a session-level
	// advisory lock.
	Migrate(ctx context.Context) error
	// SchemaCurrent reports whether the store's schema is exactly the one this binary migrates
	// to: an error names the two versions when it is behind (a migration has not run) or ahead (a
	// newer binary migrated it).
	SchemaCurrent(ctx context.Context) error
	// Close releases the engine's connections. SQLite's closes its pool and returns the driver's error;
	// Postgres's closes the pool and returns nil.
	Close() error
}
