package tools

import (
	"github.com/batondeck/batondeck-kanban/internal/mcp"
	"github.com/batondeck/batondeck-kanban/internal/service"
)

func init() { register("anomalies", RegisterAnomalies) }

// RegisterAnomalies adds detect_anomalies, one service.Anomalies call.
func RegisterAnomalies(s *mcp.Server, svc service.Services) {
	mcp.Tool(s, "anomalies", "detect_anomalies",
		"Report a board's stuck work: stale leases, claim thrash, review ping-pong, a wedged IN_PROGRESS column, starved urgent cards, and tasks blocked behind a poisoned one.",
		svc.Anomalies.Detect)
}
