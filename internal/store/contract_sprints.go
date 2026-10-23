package store

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Sprint is one sprints row. Times are Unix milliseconds; a nil pointer is a NULL column.
type Sprint struct {
	ProjectID      string
	ID             string
	BoardID        string
	Name           string
	Goal           string
	Acceptance     string
	Status         domain.SprintStatus
	ProposalTaskID *string
	ApprovedBy     *string
	MemberCount    int64
	Checkpoint     *string
	EventCursor    *string
	Version        int64
	CreatedBy      string
	CreatedAt      int64
	StartedAt      *int64
	ClosedAt       *int64
	UpdatedBy      string
	UpdatedAt      int64
}

// CreateSprintInput is a new sprint: PROPOSED, no members, version 1, its id minted here ("S-" and
// 12 random hex digits, conductor's shape). CreatedBy is also its first UpdatedBy.
type CreateSprintInput struct {
	ProjectID  string
	BoardID    string
	Name       string
	Goal       string
	Acceptance string
	CreatedBy  string
	CreatedAt  int64
}

// SprintPatch changes the named fields of a sprint; a nil field is unchanged, a Nullable whose
// Value is nil sets the column NULL. The transition table and the activation gates are the
// service's (domain.CheckSprintTransition, domain.CheckSprintActivation): the store writes what it
// is given. UpdatedBy and UpdatedAt are required.
type SprintPatch struct {
	Name           *string
	Goal           *string
	Acceptance     *string
	Status         *domain.SprintStatus
	ProposalTaskID *Nullable[string]
	ApprovedBy     *Nullable[string]
	Checkpoint     *Nullable[string]
	EventCursor    *Nullable[string]
	StartedAt      *Nullable[int64]
	ClosedAt       *Nullable[int64]
	UpdatedBy      string
	UpdatedAt      int64
}

// SprintFilter narrows ListSprints: BoardID "" is every board, Status nil every status.
type SprintFilter struct {
	BoardID string
	Status  *domain.SprintStatus
}

// SprintPage is one page of sprints; NextCursor is "" on the last page.
type SprintPage struct {
	Sprints    []Sprint
	NextCursor string
}

// Sprints is the sprint area (conductor's store.ts:1075-1123). Keys: (project_id, sprint_id).
// Errors: a missing sprint is domain.NotFound("Sprint", id); a board that is not the project's is
// domain.NotFound("Board", id); an empty name (and, on a create, an empty goal, acceptance or
// creator), a status outside the four, or no UpdatedBy or UpdatedAt is domain.Validation. A versioned write takes expectedVersion
// and returns the sprint one version higher; another version is domain.Stale(expected, actual) and
// changes nothing. A write appends no event: that is the service's, inside Atomically.
type Sprints interface {
	// CreateSprint mints the id and inserts the sprint.
	CreateSprint(ctx context.Context, in CreateSprintInput) (Sprint, error)
	// GetSprint is one sprint by (project, id).
	GetSprint(ctx context.Context, projectID, sprintID string) (Sprint, error)
	// ListSprints is one page of the project's sprints matching f, oldest first ((createdAt, id)
	// ascending, the id compared as bytes), paged by cursor (EncodeCursor of the last row's createdAt
	// in decimal and its id), read strictly after; a malformed cursor is VALIDATION. A limit <= 0 is
	// 50 and above 200 is 200.
	ListSprints(ctx context.Context, projectID string, f SprintFilter, cursor string, limit int) (SprintPage, error)
	// UpdateSprint applies patch at expectedVersion.
	UpdateSprint(ctx context.Context, projectID, sprintID string, expectedVersion int64, patch SprintPatch) (Sprint, error)
	// AddSprintMembers moves the sprint's member count by delta (never below 0) and returns the
	// sprint; the version does not move, as conductor's setTaskSprint leaves it.
	AddSprintMembers(ctx context.Context, projectID, sprintID string, delta int64) (Sprint, error)
	// ListSprintMembers is the tasks of the sprint's board whose SprintIDs hold sprintID, (order key,
	// id) ascending as bytes, at most limit (<= 0 or above 500 is 500); an unknown sprint has none.
	ListSprintMembers(ctx context.Context, projectID, sprintID string, limit int) ([]Task, error)
}

var (
	_ Sprints = (*SQLite)(nil)
	_ Sprints = (*Postgres)(nil)
)
