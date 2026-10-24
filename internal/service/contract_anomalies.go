package service

import (
	"context"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// Anomalies is the stuck-work sensor (conductor anomalies.ts, T-10): lazy, on read, over one
// board's bounded task pages and its bounded feed tail, with no background sweeper.
type Anomalies interface {
	// Detect is detect_anomalies.
	// Tier: write (a first sighting appends an event). One board is scanned: boardId, or taskId's
	// board, whose findings it then keeps alone; neither is VALIDATION. Severity keys on
	// blockedReason: a dependent stuck behind a poisoned blocker is warn (plan §Fold 1); there is no
	// CANCELLED row. Events: task.anomaly_detected on the first sighting of each (kind, task), on the
	// task, or on the board for wip_jam, best-effort (a failed append never fails the read). Audit:
	// none. Doorbell: none (a changed row, which rings nobody).
	Detect(ctx context.Context, caller domain.Caller, req DetectAnomaliesReq) (DetectAnomaliesResp, error)
}

// AnomalyKind is one failure shape detect_anomalies reports.
type AnomalyKind string

// AnomalyKindValues are AnomalyKind's values, in the schema's order.
var AnomalyKindValues = []AnomalyKind{"claim_thrash", "dep_deadlock", "rework_loop", "stale_in_progress", "starved_urgent", "wip_jam"}

// AnomalySeverity is a finding's severity.
type AnomalySeverity string

// AnomalySeverityValues are AnomalySeverity's values, in the schema's order.
var AnomalySeverityValues = []AnomalySeverity{"critical", "warn"}

// DetectAnomaliesReq is detect_anomalies's input.
type DetectAnomaliesReq struct {
	ProjectID string        `json:"projectId"`
	BoardID   string        `json:"boardId,omitzero"`
	Kinds     []AnomalyKind `json:"kinds,omitzero"`
	SinceSec  int64         `json:"sinceSec,omitzero"`
	TaskID    string        `json:"taskId,omitzero"`
}

// DetectAnomaliesResp is detect_anomalies's output.
type DetectAnomaliesResp struct {
	Anomalies []Anomaly    `json:"anomalies"`
	BoardID   string       `json:"boardId"`
	Scanned   AnomalyScope `json:"scanned"`
}

// Anomaly is detect_anomalies's output anomalies[]; taskId is null for a board-level finding.
type Anomaly struct {
	DetectedAt string          `json:"detectedAt"`
	Evidence   JSONObject      `json:"evidence"`
	Kind       AnomalyKind     `json:"kind"`
	Severity   AnomalySeverity `json:"severity"`
	TaskID     *string         `json:"taskId"`
}

// AnomalyScope is detect_anomalies's output scanned: what the scan could see, so a truncated tail
// is never read as healthy.
type AnomalyScope struct {
	EventsScanned int64   `json:"eventsScanned"`
	OldestEventTS *string `json:"oldestEventTs"`
	TasksScanned  int64   `json:"tasksScanned"`
	Truncated     bool    `json:"truncated"`
	WindowFrom    string  `json:"windowFrom"`
}
