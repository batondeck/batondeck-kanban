package store

import (
	"context"
	"fmt"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// projectLockNamespace keeps the project locks' keys apart from any other advisory lock (goose's
// migration lock among them).
const projectLockNamespace = "batondeck:project:"

func outsideTx(op string) error {
	return domain.NewError(domain.CodeInternal, "store: "+op+" outside Atomically", nil)
}

// LockProject is a no-op inside a transaction: BEGIN IMMEDIATE already holds the database's one
// write lock from BEGIN to the commit.
func (s *SQLite) LockProject(_ context.Context, _ string) error {
	if s.tx == nil {
		return outsideTx("LockProject")
	}
	return nil
}

// LockProject takes pg_advisory_xact_lock on the project's key, released at commit or rollback.
func (s *Postgres) LockProject(ctx context.Context, projectID string) error {
	if s.tx == nil {
		return outsideTx("LockProject")
	}
	countStatement(ctx, s.issued)
	if _, err := s.tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1, 0))", projectLockNamespace+projectID); err != nil {
		return fmt.Errorf("store: lock project: %w", err)
	}
	return nil
}
