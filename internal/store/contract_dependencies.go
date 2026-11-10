package store

import "context"

// DependencyType is an edge's type. Only blocks edges gate work: relates is a link.
type DependencyType string

const (
	DependencyBlocks  DependencyType = "blocks"
	DependencyRelates DependencyType = "relates"
)

// DependencyEdge is one canonical edge: FromTaskID blocks ToTaskID (the schema's blocker_id and
// dependent_id). TS is Unix milliseconds.
type DependencyEdge struct {
	ProjectID  string
	ID         string
	FromTaskID string
	ToTaskID   string
	Type       DependencyType
	CreatedBy  string
	TS         int64
}

// AddEdgeInput is an edge to add.
type AddEdgeInput struct {
	ProjectID  string
	FromTaskID string
	ToTaskID   string
	Type       DependencyType
	CreatedBy  string
}

// Dependencies is the dependency area: the canonical edges, and the two caches every task carries,
// blockedBy and blocks (tasks.blocked_by, tasks.blocks), which a blocks edge keeps in step inside the
// same transaction. Keys: an edge is (project_id, id); a task (project_id, task_id); edges never
// cross projects. Errors: a missing task is domain.NotFound("Task", id), FromTaskID checked first; a
// missing edge domain.NotFound("DependencyEdge", id); a missing project domain.NotFound("Project",
// id); a blocks edge that would close a cycle (a self-edge included) is domain.CycleDetected(from,
// to); an unknown type or an empty creator is domain.Validation. A refused call writes nothing: no
// edge and no cache entry. Status is never changed here: the auto-unblock decision
// (domain.DecideUnblock, domain.AutoReady) is the service's, over SetCaches and Tasks.SetStatus.
// No call here changes a task's version, caches included: the bump is the caller's, once per
// mutation (service.mutate, ADR-0003), so an add_dependency moves each task's version at most once.
// Lists are in (ts, id) order and not paginated.
type Dependencies interface {
	// AddEdge adds the edge. It takes the project's lock (Locks.LockProject) before anything else,
	// so concurrent edge writes of one project are serialised wherever they are called from. For
	// blocks it runs domain.CheckCycle over the project's live gates first (the blocks edges whose
	// blocker is still in the dependent's blockedBy: an edge whose blocker reached DONE stays but
	// gates nothing), then appends FromTaskID to the dependent's blockedBy and ToTaskID to the
	// blocker's blocks (each at most once). blockedBy holds open blockers only: a blocker already
	// DONE gets its edge and the blocks entry, and the dependent's blockedBy does not gain it. An edge
	// equal to an existing one (same from, to and type) returns the existing edge and writes nothing,
	// unless it is a blocks edge whose gate cleared and whose blocker is open again: then, after the
	// cycle check, the dependent's blockedBy regains the blocker (the gate re-opens).
	AddEdge(ctx context.Context, in AddEdgeInput) (DependencyEdge, error)
	// RemoveEdge removes the edge and returns it; for blocks it also removes FromTaskID from the
	// dependent's blockedBy and ToTaskID from the blocker's blocks. gated reports whether the
	// dependent's blockedBy named FromTaskID before the removal, i.e. whether the edge was a live
	// gate (false for an edge whose gate had cleared, and for every edge that is not blocks).
	RemoveEdge(ctx context.Context, projectID, edgeID string) (edge DependencyEdge, gated bool, err error)
	// ListEdges is every edge of the project.
	ListEdges(ctx context.Context, projectID string) ([]DependencyEdge, error)
	// ListBlockers is the edges whose ToTaskID is the task: what it waits on.
	ListBlockers(ctx context.Context, projectID, taskID string) ([]DependencyEdge, error)
	// ListDependents is the edges whose FromTaskID is the task: what waits on it.
	ListDependents(ctx context.Context, projectID, taskID string) ([]DependencyEdge, error)
	// SetCaches replaces the task's blockedBy and blocks with the lists given, in their order (nil
	// is empty). It writes an unblock decision: when a blocker reaches DONE its edge stays, and its
	// id is removed from the dependent's blockedBy.
	SetCaches(ctx context.Context, projectID, taskID string, blockedBy, blocks []string) error
}

var (
	_ Dependencies = (*SQLite)(nil)
	_ Dependencies = (*Postgres)(nil)
)
