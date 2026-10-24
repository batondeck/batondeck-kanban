package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Analytics is the operations summary over the tool_calls rows (conductor's analytics service,
// gateway/analytics/src/bq.ts): no tool, the REST route GET /api/analytics/operations alone.
type Analytics interface {
	// Operations is projectId's tool calls of the last Days days (default 7, clamped to 1..90),
	// rolled up as conductor's aggregateOps does, with the retention row naming how far back the rows
	// are kept. Read tier under get_project's posture: a non-member is FORBIDDEN, never an empty
	// summary. One statement once the membership is cached. Writes nothing.
	Operations(ctx context.Context, caller domain.Caller, req OperationsReq) (OperationsResp, error)
}

// OperationsReq names the project and the window in days.
type OperationsReq struct {
	ProjectID string `json:"projectId"`
	Days      int64  `json:"days,omitzero"`
}

// OperationsResp is conductor's OpsSummary and the retention row: Days is the window queried,
// after the default and the clamp.
type OperationsResp struct {
	ProjectID    string          `json:"projectId"`
	Days         int64           `json:"days"`
	Total        int64           `json:"total"`
	Errors       int64           `json:"errors"`
	ErrorRate    float64         `json:"errorRate"`
	AvgLatencyMs float64         `json:"avgLatencyMs"`
	ByTool       []OpsToolRow    `json:"byTool"`
	ByDay        []OpsDayRow     `json:"byDay"`
	ByAgent      []OpsAgentRow   `json:"byAgent"`
	Retention    OpsRetentionRow `json:"retention"`
}

// OpsToolRow is one tool's calls and their mean latency, rounded to 0.1 ms.
type OpsToolRow struct {
	Tool  string  `json:"tool"`
	Calls int64   `json:"calls"`
	AvgMs float64 `json:"avgMs"`
}

// OpsDayRow is one UTC day's calls; Day is "YYYY-MM-DD".
type OpsDayRow struct {
	Day   string `json:"day"`
	Calls int64  `json:"calls"`
}

// OpsAgentRow is one agent's calls; a call with no agent is "(unknown)".
type OpsAgentRow struct {
	Agent string `json:"agent"`
	Calls int64  `json:"calls"`
}

// OpsRetentionRow is how long a tool_calls row is kept and the oldest UTC day that window reaches:
// a window wider than Days below it covers days the sweep has deleted.
type OpsRetentionRow struct {
	Days      int64  `json:"days"`
	OldestDay string `json:"oldestDay"`
}
