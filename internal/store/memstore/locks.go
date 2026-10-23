package memstore

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// LockProject is a no-op inside a transaction, which already holds the store's one mutex; outside
// one it is INTERNAL, as on the engines.
func (s *Store) LockProject(ctx context.Context, _ string) error {
	if !s.tx {
		return domain.NewError(domain.CodeInternal, "store: LockProject outside Atomically", nil)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.over()
}
