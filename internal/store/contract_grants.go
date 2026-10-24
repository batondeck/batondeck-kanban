package store

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Grant is one member's capability grant in a project. UpdatedAt is Unix milliseconds.
type Grant struct {
	ProjectID  string
	IdentityID string
	Grant      domain.CapabilityGrant
	UpdatedBy  string
	UpdatedAt  int64
}

// SetGrantInput is a grant to write; Now stamps it.
type SetGrantInput struct {
	ProjectID  string
	IdentityID string
	Grant      domain.CapabilityGrant
	UpdatedBy  string
	Now        int64
}

// MemberGrant is one membership of an identity and its grant, nil when it holds none.
type MemberGrant struct {
	ProjectID string
	Role      domain.Role
	Grant     *domain.CapabilityGrant
}

// Grants is the capability grants area. Keys: a grant is (project_id, identity_id), and exists only
// beside that membership: removing the member or the project removes it. Errors: a grant for a
// non-member is domain.NotFound("Member", identityID), a missing grant domain.NotFound("Grant",
// identityID); an empty updater or Now <= 0 is domain.Validation. The area is versionless.
type Grants interface {
	// SetGrant writes the member's grant, replacing any it held.
	SetGrant(ctx context.Context, in SetGrantInput) (Grant, error)
	// GetGrant is one member's grant.
	GetGrant(ctx context.Context, projectID, identityID string) (Grant, error)
	// DeleteGrant removes the member's grant and reports whether it held one.
	DeleteGrant(ctx context.Context, projectID, identityID string) (bool, error)
	// ListMemberGrants is every membership of identityID with its grant, by project id ascending.
	ListMemberGrants(ctx context.Context, identityID string) ([]MemberGrant, error)
}

var (
	_ Grants = (*SQLite)(nil)
	_ Grants = (*Postgres)(nil)
)

// CheckGrantInput is SetGrant's validation, shared with memstore.
func CheckGrantInput(in SetGrantInput) error {
	switch {
	case in.UpdatedBy == "":
		return domain.Validation("a grant needs who set it", nil)
	case in.Now <= 0:
		return domain.Validation("a grant needs a time", nil)
	}
	return nil
}
