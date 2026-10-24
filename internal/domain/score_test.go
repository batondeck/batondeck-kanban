package domain

import (
	"math"
	"testing"
	"time"
)

// Conductor's scoreTask and capability matching, by value.
func TestScoreTask(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	due := now.Add(6 * time.Hour)
	s := ScoreTask(ScoreInput{Priority: PriorityUrgent, DueAt: &due, Blocks: 5, RequiredCapabilities: []string{"go", "sql"}, CreatedAt: now.Add(-8 * 24 * time.Hour)}, now, SelectionWeights{}, 0)
	near := func(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
	if !near(s.Priority.Value, 1) || !near(s.Deadline.Value, 0.75) || !near(s.Readiness.Value, 0.5) || !near(s.Capability.Value, 2.0/3) || !near(s.Aging.Value, 1) || !s.AgedOut {
		t.Fatalf("factors %+v", s)
	}
	want := 0.4*1 + 0.2*0.75 + 0.25*0.5 + 0.05*2.0/3 + 0.1*1
	if !near(s.Score, want) || s.Reason != "urgent · unblocks 5 · due soon · aged out · needs go+sql" {
		t.Fatalf("score %v (want %v), reason %q", s.Score, want, s.Reason)
	}
	est := 1.0
	far := now.Add(24 * time.Hour)
	if d := ScoreTask(ScoreInput{Priority: PriorityLow, DueAt: &far, EstimateHours: &est, CreatedAt: now}, now, SelectionWeights{Priority: 1}, time.Hour); d.Deadline.Value != 0 || d.Score != 0 || d.Priority.Weight != 1 {
		t.Fatalf("a far deadline under a 1h estimate, custom weights: %+v", d)
	}
	if ByScoreDesc(1, "b", 1, "a") <= 0 || ByScoreDesc(2, "z", 1, "a") >= 0 {
		t.Fatal("ByScoreDesc: score descending, then order key ascending")
	}
}

func TestMatchCapabilities(t *testing.T) {
	aliases := map[string]string{"Golang": "go"}
	for _, c := range []struct {
		req      []string
		eligible bool
		tier     MatchTier
		penalty  float64
	}{
		{nil, true, MatchExact, 0},
		{[]string{"GO "}, true, MatchExact, 0},
		{[]string{"golang"}, true, MatchSynonym, 0},
		{[]string{"browser-automation"}, true, MatchTag, TagMatchPenalty},
		{[]string{"go", "browser.use", "golang"}, true, MatchTag, TagMatchPenalty},
		{[]string{"mongo"}, false, MatchNone, 0},
		{[]string{"go", "rust"}, false, MatchNone, 0},
	} {
		m := MatchCapabilities(c.req, []string{"go"}, []string{"browser", " "}, aliases)
		if m.Eligible != c.eligible || m.Tier != c.tier || m.Penalty != c.penalty {
			t.Fatalf("%v: %+v, want %v %s %v", c.req, m, c.eligible, c.tier, c.penalty)
		}
	}
	if r := ReliabilityFor(0, 0); r != 0.8 {
		t.Fatalf("the prior is %v, want 0.8", r)
	}
	if r := TaskReliability([]string{"a", "b"}, func(c string) (int64, int64) {
		if c == "a" {
			return 5, 0
		}
		return 0, 5
	}); math.Abs(r-((9.0/10+4.0/10)/2)) > 1e-12 {
		t.Fatalf("task reliability %v", r)
	}
}
