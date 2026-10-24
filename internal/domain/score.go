package domain

import (
	"cmp"
	"math"
	"strconv"
	"strings"
	"time"
)

// The cost-aware work-selection scorer (conductor score.ts, T-1): pure, over fields a fetched TODO
// page already holds. Each factor is normalised to 0..1 and weighted; the result carries the
// breakdown and whether the card has aged past the starvation ceiling.

// SelectionWeights are the scorer's per-factor weights (ProjectSettings.selectionWeights).
type SelectionWeights struct {
	Priority, Readiness, Deadline, Aging, Capability float64
}

// DefaultSelectionWeights is conductor's DEFAULT_SELECTION_WEIGHTS.
var DefaultSelectionWeights = SelectionWeights{Priority: 0.4, Readiness: 0.25, Deadline: 0.2, Aging: 0.1, Capability: 0.05}

// DefaultAgingCeiling is conductor's DEFAULT_AGING_CEILING_MS: where the aging term saturates.
const DefaultAgingCeiling = 7 * 24 * time.Hour

const (
	fanoutHalf      = 5              // readiness = fanout/(fanout+5)
	capNorm         = 3              // capability tightness saturates at 3 required capabilities
	deadlineHorizon = 24 * time.Hour // the urgency window of a task with no estimate
)

// ScoreInput is what the scorer reads of one task.
type ScoreInput struct {
	Priority             Priority
	DueAt                *time.Time
	EstimateHours        *float64
	Blocks               int
	RequiredCapabilities []string
	CreatedAt            time.Time
}

// ScoreFactor is one factor: its normalised value, its weight and their product.
type ScoreFactor struct {
	Value, Weight, Contribution float64
}

// TaskScore is a task's score, its five factors, whether it aged out and a readable reason.
type TaskScore struct {
	Score                                            float64
	Priority, Deadline, Readiness, Capability, Aging ScoreFactor
	AgedOut                                          bool
	Reason                                           string
}

// ScoreTask is conductor's scoreTask. A zero weight set is the default; a ceiling <= 0 is
// DefaultAgingCeiling.
func ScoreTask(t ScoreInput, now time.Time, w SelectionWeights, ceiling time.Duration) TaskScore {
	if w == (SelectionWeights{}) {
		w = DefaultSelectionWeights
	}
	if ceiling <= 0 {
		ceiling = DefaultAgingCeiling
	}
	priority := float64(priorityRank[t.Priority]) / float64(priorityRank[PriorityUrgent])
	deadline := 0.0
	if t.DueAt != nil {
		horizon := deadlineHorizon
		if t.EstimateHours != nil && *t.EstimateHours > 0 {
			horizon = time.Duration(*t.EstimateHours * float64(time.Hour))
		}
		deadline = clamp01(1 - float64(t.DueAt.Sub(now))/float64(horizon))
	}
	readiness := float64(t.Blocks) / float64(t.Blocks+fanoutHalf)
	capability := 0.0
	if n := len(t.RequiredCapabilities); n > 0 {
		capability = math.Min(1, float64(n)/capNorm)
	}
	age := max(0, now.Sub(t.CreatedAt))
	aging := clamp01(float64(age) / float64(ceiling))
	agedOut := age >= ceiling
	f := func(v, wt float64) ScoreFactor { return ScoreFactor{Value: v, Weight: wt, Contribution: v * wt} }
	s := TaskScore{
		Priority: f(priority, w.Priority), Deadline: f(deadline, w.Deadline), Readiness: f(readiness, w.Readiness),
		Capability: f(capability, w.Capability), Aging: f(aging, w.Aging), AgedOut: agedOut,
	}
	s.Score = s.Priority.Contribution + s.Deadline.Contribution + s.Readiness.Contribution + s.Capability.Contribution + s.Aging.Contribution
	bits := []string{string(t.Priority)}
	if t.Blocks > 0 {
		bits = append(bits, "unblocks "+strconv.Itoa(t.Blocks))
	}
	if t.DueAt != nil && deadline > 0.5 {
		bits = append(bits, "due soon")
	}
	if agedOut {
		bits = append(bits, "aged out")
	}
	if len(t.RequiredCapabilities) > 0 {
		bits = append(bits, "needs "+strings.Join(t.RequiredCapabilities, "+"))
	}
	s.Reason = strings.Join(bits, " · ")
	return s
}

// ByScoreDesc orders by score descending, ties by order key ascending (conductor's byScoreDesc).
func ByScoreDesc(aScore float64, aOrder string, bScore float64, bOrder string) int {
	if aScore != bScore {
		return cmp.Compare(bScore, aScore)
	}
	return strings.Compare(aOrder, bOrder)
}
