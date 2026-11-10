package service

import (
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// retract_artifact matches its selector as add_artifact stored the entry, trimmed: the very string
// an agent sent (a url pasted with its newline) retracts what it added, and a blank url beside a ref
// does not narrow the match to nothing.
func TestRetractArtifactTrimsItsSelector(t *testing.T) {
	f := newFixture(t, Options{})
	task := f.task(t, domain.Todo, nil)
	art := f.c.Artifacts()
	if _, err := art.AddArtifact(f.ctx, f.alice, AddArtifactReq{ProjectID: f.project.ID, TaskID: task.ID, Artifacts: []ArtifactInput{
		{Kind: "pr", URL: "  https://example.com/pr/1\n"},
		{Kind: "commit", Ref: " abc123 ", Repo: " org/repo "},
	}}); err != nil {
		t.Fatal(err)
	}
	r, err := art.RetractArtifact(f.ctx, f.alice, RetractArtifactReq{ProjectID: f.project.ID, TaskID: task.ID, Reason: "wrong PR",
		Artifact: ArtifactSelector{Kind: "pr", URL: "  https://example.com/pr/1\n"}})
	if err != nil || r.Retracted != 1 {
		t.Fatalf("retract with the url as sent: %+v, %v; want 1 retracted", r, err)
	}
	r, err = art.RetractArtifact(f.ctx, f.alice, RetractArtifactReq{ProjectID: f.project.ID, TaskID: task.ID, Reason: "wrong commit",
		Artifact: ArtifactSelector{Kind: "commit", URL: " ", Ref: "abc123 ", Repo: "org/repo\t"}})
	if err != nil || r.Retracted != 1 {
		t.Fatalf("retract with a blank url and padded ref and repo: %+v, %v; want 1 retracted", r, err)
	}
	_, err = art.RetractArtifact(f.ctx, f.alice, RetractArtifactReq{ProjectID: f.project.ID, TaskID: task.ID, Reason: "x",
		Artifact: ArtifactSelector{Kind: "pr", URL: " ", Ref: "\t"}})
	wantCode(t, err, domain.CodeValidation)
}
