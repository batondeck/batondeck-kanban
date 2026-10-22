// Package authfake is the auth.Resolver double for the layers above auth (ADR-0003): a fixed map
// of bearer to Caller, and UNAUTHENTICATED for anything else.
package authfake

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Resolver resolves exactly the bearers in Callers.
type Resolver struct {
	Callers map[string]domain.Caller
}

var _ auth.Resolver = Resolver{}

// ResolveCaller returns the configured Caller, or UNAUTHENTICATED.
func (f Resolver) ResolveCaller(_ context.Context, bearer string) (domain.Caller, error) {
	if c, ok := f.Callers[bearer]; ok {
		return c, nil
	}
	return domain.Caller{}, domain.NewError(domain.CodeUnauthenticated, "invalid, expired or revoked credentials", nil)
}
