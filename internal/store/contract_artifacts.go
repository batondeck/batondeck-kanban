package store

import "context"

// ArtifactKind is what an artifact points at.
type ArtifactKind string

const (
	ArtifactPR         ArtifactKind = "pr"
	ArtifactBranch     ArtifactKind = "branch"
	ArtifactCommit     ArtifactKind = "commit"
	ArtifactFile       ArtifactKind = "file"
	ArtifactDoc        ArtifactKind = "doc"
	ArtifactAttachment ArtifactKind = "attachment"
)

// ArtifactRetraction is an artifact's tombstone: who withdrew it, when (Unix milliseconds) and why.
type ArtifactRetraction struct {
	By      string
	ByAgent *string
	At      int64
	Reason  string
}

// Artifact is one entry of a task's evidence. TS is Unix milliseconds; Retraction is nil while the
// entry is live.
type Artifact struct {
	ProjectID    string
	TaskID       string
	ID           string
	Kind         ArtifactKind
	URL          *string
	Ref          *string
	Repo         *string
	Title        *string
	AddedBy      string
	AddedByAgent *string
	TS           int64
	Retraction   *ArtifactRetraction
}

// AddArtifactInput is an artifact to append to a task.
type AddArtifactInput struct {
	ProjectID    string
	TaskID       string
	Kind         ArtifactKind
	URL          *string
	Ref          *string
	Repo         *string
	Title        *string
	AddedBy      string
	AddedByAgent *string
}

// Artifacts is the artifact area. Keys: an artifact is (project_id, task_id, id); a task is
// (project_id, task_id), never a task id alone (conductor rule 6, T-111). Errors: a missing task is
// domain.NotFound("Task", taskID); an unknown kind, an entry with neither url nor ref and an empty
// addedBy are domain.Validation; retracting a missing or already retracted artifact is
// domain.NotFound("Artifact", id). Entries are appended and tombstoned, never edited or deleted
// (they leave with their task, by its cascade). De-duplication and the per-task cap are the
// service's (it reads ListArtifacts in the same transaction).
type Artifacts interface {
	// AddArtifact appends the entry, live, stamped with a new id and the current time.
	AddArtifact(ctx context.Context, in AddArtifactInput) (Artifact, error)
	// ListArtifacts is every entry of the task, tombstones included, oldest first ((ts, id)).
	ListArtifacts(ctx context.Context, projectID, taskID string) ([]Artifact, error)
	// RetractArtifact tombstones the live entry id with r and returns it.
	RetractArtifact(ctx context.Context, projectID, taskID, id string, r ArtifactRetraction) (Artifact, error)
	// LiveArtifactTasks is the subset of taskIDs that carry at least one live entry, in one
	// statement (list_tasks missingArtifacts); no ids is none.
	LiveArtifactTasks(ctx context.Context, projectID string, taskIDs []string) (map[string]bool, error)
}

var (
	_ Artifacts = (*SQLite)(nil)
	_ Artifacts = (*Postgres)(nil)
)
