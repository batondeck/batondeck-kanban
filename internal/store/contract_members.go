package store

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Member is one identity's membership of a project. AddedAt is Unix milliseconds.
type Member struct {
	ProjectID  string
	IdentityID string
	Role       domain.Role
	AddedBy    string
	AddedAt    int64
}

// Members is the membership area. Keys: a member is (project_id, identity_id). Errors: a missing
// project is domain.NotFound("Project", id), a missing membership domain.NotFound("Member",
// identityID); an unknown role is domain.Validation. The area is versionless (no version column):
// no call takes an expected version or returns STALE. Policy (who may add, the last-admin guard)
// is the caller's. No list here is paginated.
type Members interface {
	// AddMember adds identityID to the project with role, or, when it is a member already, changes
	// its role and keeps who added it and when (conductor's add_member is "add or update").
	AddMember(ctx context.Context, projectID, identityID string, role domain.Role, addedBy string) (Member, error)
	// RemoveMember removes the membership and the member's capability grant with it. Removing an
	// identity that is not a member is NOT_FOUND.
	RemoveMember(ctx context.Context, projectID, identityID string) error
	// ListMembers is the project's members, oldest first; a missing project is NOT_FOUND, never an
	// empty list.
	ListMembers(ctx context.Context, projectID string) ([]Member, error)
	// GetMember is the membership lookup: identityID's membership of the project, in one
	// statement, so a missing project answers as a missing membership (NotFound("Member", id)).
	GetMember(ctx context.Context, projectID, identityID string) (Member, error)
}

var (
	_ Members = (*SQLite)(nil)
	_ Members = (*Postgres)(nil)
)
