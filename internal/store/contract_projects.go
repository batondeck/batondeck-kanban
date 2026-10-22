package store

import (
	"context"
	"encoding/json"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Project is one project. Times are Unix milliseconds. Its members are the Members area's.
type Project struct {
	ID        string
	Name      string
	CreatedBy string
	CreatedAt int64
	Settings  ProjectSettings
}

// ProjectSettings is conductor's ProjectSettings as the schema splits it: reviewEnabled and
// retryPolicy are columns, every other setting is kept as its JSON value by name. There is no
// transitionTable: the table is fixed.
type ProjectSettings struct {
	ReviewEnabled bool
	// RetryPolicy is nil when the project uses domain.DefaultRetryPolicy.
	RetryPolicy *domain.RetryPolicy
	// Other holds the remaining settings by name. A new project has quotaEnabled false and txnCap
	// null, as conductor's createProject writes them.
	Other map[string]json.RawMessage
}

// ProjectMembership is a project with the role one identity holds in it.
type ProjectMembership struct {
	Project Project
	Role    domain.Role
}

// CreateProjectInput is a new project's name and its creator, who becomes its admin member.
type CreateProjectInput struct {
	Name      string
	CreatedBy string
}

// SettingsPatch changes settings by name: each key replaces that setting, and a JSON null removes
// it (retryPolicy null returns the project to the default policy). reviewEnabled must be a
// boolean and retryPolicy a domain.RetryPolicy object; transitionTable is refused. Value shapes
// beyond those two are the caller's to have validated.
type SettingsPatch map[string]json.RawMessage

// Projects is the project area. Keys: a project is its id. Errors: a missing project is
// domain.NotFound("Project", id); a malformed argument is domain.Validation. The area is
// versionless (the schema has no version column on projects): no call takes an expected version
// or returns STALE. No list here is paginated, so none takes a cursor.
type Projects interface {
	// CreateProject creates the project, with its creator as its one admin member and the default
	// settings (reviewEnabled true), in one transaction. Under opts.IdempotencyKey it is replayed
	// for (GlobalScope, opts.Identity, key) within opts.IdempotencyTTL: the same project comes back
	// and no second row is written. An empty name or creator is VALIDATION.
	CreateProject(ctx context.Context, in CreateProjectInput, opts WriteOptions) (Project, error)
	// GetProject is one project by id.
	GetProject(ctx context.Context, projectID string) (Project, error)
	// ListProjects is every project identityID is a member of, with its role, oldest first. An
	// identity in no project gets an empty list.
	ListProjects(ctx context.Context, identityID string) ([]ProjectMembership, error)
	// UpdateSettings applies patch to the project's settings in one transaction (the row locked on
	// Postgres) and returns the project after it. A patch naming transitionTable is VALIDATION and
	// changes nothing.
	UpdateSettings(ctx context.Context, projectID string, patch SettingsPatch) (Project, error)
	// DeleteProjectRecursive deletes the project and everything it owns (members, grants, the task
	// counter, boards, columns, tasks and every task-owned row, events, memory, chronicle,
	// integrations, idempotency keys) in one transaction. It keeps audit_log, tool_calls and
	// change_log rows, which are the trace of what happened. Blob files on disk are not touched.
	DeleteProjectRecursive(ctx context.Context, projectID string) error
}

var (
	_ Projects = (*SQLite)(nil)
	_ Projects = (*Postgres)(nil)
)
