package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("artifacts", RegisterArtifacts) }

// RegisterArtifacts adds the artifact tools, each one service.Artifacts call.
func RegisterArtifacts(s *mcp.Server, svc service.Services) {
	a := svc.Artifacts
	mcp.Tool(s, "artifacts", "add_artifact",
		"Bind produced work to a task: a pr url, the branch, commits, files, a doc or an attachment. Each needs a url or a ref; duplicates are skipped, so a retry is safe. version is optional.",
		a.AddArtifact)
	mcp.Tool(s, "artifacts", "retract_artifact",
		"Tombstone a wrongly recorded artifact (kind plus url or ref, repo to tell twins apart) with a reason; it stops counting as evidence. Matching nothing is NOT_FOUND.",
		a.RetractArtifact)
}
