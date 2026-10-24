package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Artifacts is the evidence a task carries: the typed entries (pr, branch, commit, file, doc,
// attachment) recorded by add_artifact and tombstoned by retract_artifact.
type Artifacts interface {
	// AddArtifact is add_artifact.
	// Tier: write; version optional (supplied → checked, STALE). Appends the entries that are not a
	// live entry's (kind, url case-folded, ref) already, at most 50 per task. Event:
	// task.artifact_added. Audit: none. Doorbell: none.
	AddArtifact(ctx context.Context, caller domain.Caller, req AddArtifactReq) (AddArtifactResp, error)

	// RetractArtifact is retract_artifact.
	// Tier: write; version optional (supplied → checked, STALE). Tombstones every live entry the
	// selector matches; a selector matching no entry, live or tombstoned, is NOT_FOUND. Event:
	// task.artifact_retracted. Audit: none. Doorbell: none.
	RetractArtifact(ctx context.Context, caller domain.Caller, req RetractArtifactReq) (RetractArtifactResp, error)
}

// AddArtifactReq is add_artifact's input.
type AddArtifactReq struct {
	Artifacts []ArtifactInput `json:"artifacts"`
	ProjectID string          `json:"projectId"`
	TaskID    string          `json:"taskId"`
	Version   Optional[int64] `json:"version,omitzero"`
}

// AddArtifactResp is add_artifact's output: the task with its artifacts, and how many were new.
type AddArtifactResp struct {
	Added int64 `json:"added"`
	Task  Task  `json:"task"`
}

// RetractArtifactReq is retract_artifact's input.
type RetractArtifactReq struct {
	Artifact  ArtifactSelector `json:"artifact"`
	ProjectID string           `json:"projectId"`
	Reason    string           `json:"reason"`
	TaskID    string           `json:"taskId"`
	Version   Optional[int64]  `json:"version,omitzero"`
}

// RetractArtifactResp is retract_artifact's output: the task with its artifacts, and how many live
// entries were tombstoned.
type RetractArtifactResp struct {
	Retracted int64 `json:"retracted"`
	Task      Task  `json:"task"`
}

// ArtifactSelector is retract_artifact's input artifact. It is an open object: conductor's schema
// allows properties it does not name.
type ArtifactSelector struct {
	Kind ArtifactKind `json:"kind"`
	Ref  string       `json:"ref,omitzero"`
	Repo string       `json:"repo,omitzero"`
	URL  string       `json:"url,omitzero"`
}
