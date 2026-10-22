package auth

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Resolver turns a presented bearer into the principal a call acts as (ADR-0003). An unknown,
// malformed, expired or revoked bearer, or one whose user is disabled, is UNAUTHENTICATED (a
// *domain.BatonDeckError); any other error is the store's. *Service implements it;
// internal/auth/authfake is the double for the layers above.
type Resolver interface {
	ResolveCaller(ctx context.Context, bearer string) (domain.Caller, error)
}

var _ Resolver = (*Service)(nil)
