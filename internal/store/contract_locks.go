package store

import "context"

// Locks serialises writers of one project that would otherwise race past each other under READ
// COMMITTED: two add_dependency calls on disjoint pairs each pass domain.CheckCycle and together
// close a cycle (the R0.10 review's P-110 repro). The caller takes the lock first in its
// transaction, before any task row is locked, so lock order is project then tasks.
type Locks interface {
	// LockProject holds the project's lock until the transaction ends; a second caller waits for
	// it. Postgres takes a transaction-scoped advisory lock (no row lock, so the foreign-key KEY
	// SHARE locks every child insert takes never wait on it); SQLite and memstore already admit one
	// writer at a time, so it is a no-op there. Outside Atomically it is INTERNAL: a lock that ends
	// with its own statement serialises nothing. It checks nothing about the project.
	LockProject(ctx context.Context, projectID string) error
}

var (
	_ Locks = (*SQLite)(nil)
	_ Locks = (*Postgres)(nil)
)
