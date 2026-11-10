package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("runs", RegisterRuns) }

// RegisterRuns adds the five race tools, each one service.Runs call.
func RegisterRuns(s *mcp.Server, svc service.Services) {
	r := svc.Runs
	mcp.Tool(s, "runs", "start_runs",
		"Race a task with no open blockers: create N claimable run tasks (`count`, or one per `agents` name) and take it out of the claim pool. Master role; version-checked; capped by maxRunsPerTask.",
		r.Start)
	mcp.Tool(s, "runs", "open_run",
		"Add one run to a racing task, routed to `agent` if given. Master role; capped by maxRunsPerTask.",
		r.Open)
	mcp.Tool(s, "runs", "list_runs",
		"List a task's runs: status, assignee, lease holder and deliverable. A run with a deliverable in REVIEW or DONE can be picked.",
		r.List)
	mcp.Tool(s, "runs", "pick_run",
		"Pick a race's winner: its deliverable goes to the parent (REVIEW if the project reviews, else DONE) and every other run is deleted. Master role; version-checked on the parent.",
		r.Pick)
	mcp.Tool(s, "runs", "cancel_runs",
		"End a race without a winner: every run is deleted and the parent returns to TODO, back in the pool. Master role; version-checked on the parent.",
		r.Cancel)
}
