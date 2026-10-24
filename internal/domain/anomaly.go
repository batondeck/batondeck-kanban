package domain

import (
	"cmp"
	"encoding/json"
	"slices"
	"time"
)

// Stuck-work detection (conductor anomaly.ts, T-10): pure, over one bounded board page and the
// bounded newest-first event tail the caller already read. stale_in_progress needs a LIVE lease
// (the shape the reaper cannot see); an expired lease shows only as the board-level wip_jam, whose
// remedy is reap_stale_leases, so the two never fire on one task. A dead blocker is a poisoned one
// (BLOCKED, blockedReason "poison", plan §Fold 1): a warn, since moving it back to TODO recovers it.
// There is no CANCELLED row: a cancelled task is a label, and delete is a hard delete (ADR-0001).

// AnomalyKind is one failure shape.
type AnomalyKind string

const (
	AnomalyStaleInProgress AnomalyKind = "stale_in_progress"
	AnomalyClaimThrash     AnomalyKind = "claim_thrash"
	AnomalyReworkLoop      AnomalyKind = "rework_loop"
	AnomalyWIPJam          AnomalyKind = "wip_jam"
	AnomalyStarvedUrgent   AnomalyKind = "starved_urgent"
	AnomalyDepDeadlock     AnomalyKind = "dep_deadlock"
)

// AllAnomalyKinds is every kind, in conductor's order.
func AllAnomalyKinds() []AnomalyKind {
	return []AnomalyKind{AnomalyStaleInProgress, AnomalyClaimThrash, AnomalyReworkLoop, AnomalyWIPJam, AnomalyStarvedUrgent, AnomalyDepDeadlock}
}

// AnomalySeverity is warn or critical.
type AnomalySeverity string

const (
	SeverityWarn     AnomalySeverity = "warn"
	SeverityCritical AnomalySeverity = "critical"
)

// AnomalyPolicy is the detector's thresholds (ProjectSettings.anomalyPolicy).
type AnomalyPolicy struct {
	StaleInProgress  time.Duration
	ClaimThrashCount int64
	ReworkLoopCount  int64
	WIPJamMinTasks   int64
	StarvedUrgent    time.Duration
	Window           time.Duration
}

// DefaultAnomalyPolicy is conductor's DEFAULT_ANOMALY_POLICY.
var DefaultAnomalyPolicy = AnomalyPolicy{
	StaleInProgress: 30 * time.Minute, ClaimThrashCount: 3, ReworkLoopCount: 2, WIPJamMinTasks: 2,
	StarvedUrgent: 24 * time.Hour, Window: 6 * time.Hour,
}

// PolicyOrDefault is conductor's anomalyPolicy({...DEFAULT, ...p}) for a stored policy whose unset
// thresholds read zero: update_settings {anomalyPolicy:{}} stores every field zero (omitzero), and
// that must mean "the defaults", never "fire on everything". A zero or negative threshold is the
// default's.
func PolicyOrDefault(p AnomalyPolicy) AnomalyPolicy {
	d := DefaultAnomalyPolicy
	if p.StaleInProgress > 0 {
		d.StaleInProgress = p.StaleInProgress
	}
	if p.ClaimThrashCount > 0 {
		d.ClaimThrashCount = p.ClaimThrashCount
	}
	if p.ReworkLoopCount > 0 {
		d.ReworkLoopCount = p.ReworkLoopCount
	}
	if p.WIPJamMinTasks > 0 {
		d.WIPJamMinTasks = p.WIPJamMinTasks
	}
	if p.StarvedUrgent > 0 {
		d.StarvedUrgent = p.StarvedUrgent
	}
	if p.Window > 0 {
		d.Window = p.Window
	}
	return d
}

// AnomalyTask is what the detector reads of one task. Claim nil is no lease.
type AnomalyTask struct {
	ID, Title            string
	Status               Status
	BlockedReason        string
	Priority             Priority
	Assignee             *string
	RequiredCapabilities []string
	BlockedBy            []string
	ClaimOwner           string
	ClaimAgent           *string
	ClaimExpiresAt       *time.Time
	ClaimAttempts        int64
	Racing               bool
	NextEligibleAt       time.Time
	CreatedAt, UpdatedAt time.Time
}

// AnomalyEvent is one event of the tail: Data is its JSON object.
type AnomalyEvent struct {
	Type   EventType
	TaskID string
	TS     time.Time
	Data   json.RawMessage
}

// Anomaly is one finding. TaskID "" is a board-level finding (wip_jam alone).
type Anomaly struct {
	Kind       AnomalyKind
	TaskID     string
	Severity   AnomalySeverity
	Evidence   map[string]any
	DetectedAt time.Time
}

// ScanWindow is what the event-derived detectors could see: a tail shorter than the window is
// Truncated, so a quiet scan is never mistaken for a healthy one.
type ScanWindow struct {
	WindowFrom    time.Time
	EventsScanned int
	OldestEventTS *time.Time
	Truncated     bool
	TasksScanned  int
}

// DetectInput is one scan's input. Events are newest first; EventLimit is the tail's page cap.
type DetectInput struct {
	Tasks      []AnomalyTask
	Events     []AnomalyEvent
	Now        time.Time
	Policy     AnomalyPolicy
	Kinds      []AnomalyKind // nil is every kind
	Since      time.Duration // 0 is Policy.Window
	TaskID     string        // "" is every task; a task drops the board-level findings
	EventLimit int
}

func severityFor(observed, threshold float64) AnomalySeverity {
	if observed >= threshold*2 {
		return SeverityCritical
	}
	return SeverityWarn
}

func leaseLive(t AnomalyTask, now time.Time) bool {
	return t.ClaimExpiresAt != nil && t.ClaimExpiresAt.After(now)
}

// deadBlockerSeverity is the severity of a dependent stuck behind b, "" when b can still reach DONE.
func deadBlockerSeverity(b AnomalyTask) AnomalySeverity {
	if IsPoisoned(b.Status, b.BlockedReason) {
		return SeverityWarn
	}
	return ""
}

func iso(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// DetectAnomalies is conductor's detectAnomalies.
func DetectAnomalies(in DetectInput) ([]Anomaly, ScanWindow) {
	now := in.Now
	window := in.Since
	if window <= 0 {
		window = in.Policy.Window
	}
	window = max(window, time.Second)
	start := now.Add(-window)
	scan := ScanWindow{WindowFrom: start, EventsScanned: len(in.Events), TasksScanned: len(in.Tasks)}
	if n := len(in.Events); n > 0 {
		oldest := in.Events[n-1].TS
		scan.OldestEventTS = &oldest
		scan.Truncated = n >= in.EventLimit && oldest.After(start)
	}
	windowEvidence := map[string]any{"windowSec": int64(window.Round(time.Second) / time.Second), "eventsScanned": len(in.Events), "windowTruncated": scan.Truncated}
	want := func(k AnomalyKind) bool { return in.Kinds == nil || slices.Contains(in.Kinds, k) }

	claims, releases, returns := map[string]int64{}, map[string]int64{}, map[string]int64{}
	for _, e := range in.Events {
		if !e.TS.IsZero() && e.TS.Before(start) {
			continue
		}
		var d struct {
			From Status `json:"from"`
			To   Status `json:"to"`
		}
		_ = json.Unmarshal(e.Data, &d)
		switch {
		case e.TaskID == "":
		case e.Type == EventTaskClaimed:
			claims[e.TaskID]++
		case e.Type == EventTaskReleased:
			releases[e.TaskID]++
		case e.Type == EventTaskMoved && d.From == Review && d.To == InProgress:
			returns[e.TaskID]++
		case e.Type == EventTaskReopened && d.From == Review:
			returns[e.TaskID]++
		}
	}
	byID := map[string]AnomalyTask{}
	var inProgress []AnomalyTask
	for _, t := range in.Tasks {
		byID[t.ID] = t
		if t.Status == InProgress {
			inProgress = append(inProgress, t)
		}
	}
	merge := func(a, b map[string]any) map[string]any {
		for k, v := range b {
			a[k] = v
		}
		return a
	}
	var out []Anomaly
	push := func(k AnomalyKind, taskID string, sev AnomalySeverity, ev map[string]any) {
		out = append(out, Anomaly{Kind: k, TaskID: taskID, Severity: sev, Evidence: ev, DetectedAt: now})
	}
	for _, t := range in.Tasks {
		if want(AnomalyStaleInProgress) && t.Status == InProgress && leaseLive(t, now) {
			age := now.Sub(t.UpdatedAt)
			if age >= in.Policy.StaleInProgress {
				push(AnomalyStaleInProgress, t.ID, severityFor(float64(age), float64(in.Policy.StaleInProgress)), map[string]any{
					"ageMs": age.Milliseconds(), "thresholdMs": in.Policy.StaleInProgress.Milliseconds(), "updatedAt": iso(t.UpdatedAt),
					"leaseExpiresAt": iso(*t.ClaimExpiresAt), "owner": t.ClaimOwner, "agent": t.ClaimAgent,
				})
			}
		}
		if n := claims[t.ID]; want(AnomalyClaimThrash) && n >= in.Policy.ClaimThrashCount {
			push(AnomalyClaimThrash, t.ID, severityFor(float64(n), float64(in.Policy.ClaimThrashCount)), merge(map[string]any{
				"claims": n, "releases": releases[t.ID], "threshold": in.Policy.ClaimThrashCount, "claimAttempts": t.ClaimAttempts,
			}, windowEvidence))
		}
		if n := returns[t.ID]; want(AnomalyReworkLoop) && n >= in.Policy.ReworkLoopCount {
			push(AnomalyReworkLoop, t.ID, severityFor(float64(n), float64(in.Policy.ReworkLoopCount)), merge(map[string]any{
				"returnsFromReview": n, "threshold": in.Policy.ReworkLoopCount, "status": t.Status,
			}, windowEvidence))
		}
		if want(AnomalyStarvedUrgent) && t.Status == Todo && t.Priority == PriorityUrgent && !t.Racing && IsPastBackoff(t.NextEligibleAt, now) {
			if age := now.Sub(t.CreatedAt); age >= in.Policy.StarvedUrgent {
				caps := t.RequiredCapabilities
				if caps == nil {
					caps = []string{}
				}
				push(AnomalyStarvedUrgent, t.ID, severityFor(float64(age), float64(in.Policy.StarvedUrgent)), map[string]any{
					"ageMs": age.Milliseconds(), "thresholdMs": in.Policy.StarvedUrgent.Milliseconds(), "createdAt": iso(t.CreatedAt),
					"priority": t.Priority, "assignee": t.Assignee, "requiredCapabilities": caps,
				})
			}
		}
		if want(AnomalyDepDeadlock) && len(t.BlockedBy) > 0 {
			dead, notOnPage := []any{}, []string{}
			var worst AnomalySeverity
			for _, id := range t.BlockedBy {
				b, ok := byID[id]
				if !ok {
					notOnPage = append(notOnPage, id)
					continue
				}
				sev := deadBlockerSeverity(b)
				if sev == "" {
					continue
				}
				dead = append(dead, map[string]any{"taskId": b.ID, "status": b.Status, "blockedReason": b.BlockedReason, "title": b.Title})
				if sev == SeverityCritical || worst == "" {
					worst = sev
				}
			}
			if len(dead) > 0 {
				push(AnomalyDepDeadlock, t.ID, worst, map[string]any{"status": t.Status, "deadBlockers": dead, "blockersNotOnPage": notOnPage})
			}
		}
	}
	if want(AnomalyWIPJam) && int64(len(inProgress)) >= in.Policy.WIPJamMinTasks {
		expired := []string{}
		for _, t := range inProgress {
			if !leaseLive(t, now) {
				expired = append(expired, t.ID)
			}
		}
		if len(expired) == len(inProgress) {
			push(AnomalyWIPJam, "", SeverityCritical, map[string]any{
				"inProgress": len(inProgress), "expiredLeases": len(expired), "threshold": in.Policy.WIPJamMinTasks,
				"taskIds": expired, "remedy": "reap_stale_leases",
			})
		}
	}
	if in.TaskID != "" {
		out = slices.DeleteFunc(out, func(a Anomaly) bool { return a.TaskID != in.TaskID })
	}
	slices.SortStableFunc(out, func(a, b Anomaly) int {
		ca, cb := a.Severity == SeverityCritical, b.Severity == SeverityCritical
		if ca != cb {
			if ca {
				return -1
			}
			return 1
		}
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.TaskID, b.TaskID))
	})
	if out == nil {
		out = []Anomaly{}
	}
	return out, scan
}

// FirstSightings drops the findings already recorded as task.anomaly_detected in the tail, by
// (kind, task). The dedupe horizon is the tail itself (conductor's ponytail): a sighting that ages
// out of it re-emits.
func FirstSightings(found []Anomaly, events []AnomalyEvent) []Anomaly {
	seen := map[[2]string]bool{}
	for _, e := range events {
		if e.Type != EventTaskAnomalyDetected {
			continue
		}
		var d struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(e.Data, &d) == nil && d.Kind != "" {
			seen[[2]string{d.Kind, e.TaskID}] = true
		}
	}
	var out []Anomaly
	for _, a := range found {
		if !seen[[2]string{string(a.Kind), a.TaskID}] {
			out = append(out, a)
		}
	}
	return out
}
