package domain

import "strings"

// Capability matching and the outcome-weighted reliability prior (conductor capability.ts, T-3):
// pure, over a registered profile, the project's alias map and the agent's skill stats.

// MatchTier is how strongly a profile covers a task's required capabilities.
type MatchTier string

const (
	MatchExact   MatchTier = "exact"
	MatchSynonym MatchTier = "synonym"
	MatchTag     MatchTier = "tag"
	MatchNone    MatchTier = "none"
)

// TagMatchPenalty is subtracted from a task's score per capability covered only by a tag.
const TagMatchPenalty = 0.15

var tierOrder = map[MatchTier]int{MatchExact: 0, MatchSynonym: 1, MatchTag: 2, MatchNone: 3}

// TierRank orders tiers, strongest first.
func TierRank(t MatchTier) int { return tierOrder[t] }

// CapabilityMatch is a profile's fit for one task: eligible, the weakest tier used and the summed
// tag penalty.
type CapabilityMatch struct {
	Eligible bool
	Tier     MatchTier
	Penalty  float64
}

func normCap(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

func capTokens(c string) []string {
	return strings.FieldsFunc(c, func(r rune) bool {
		return r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || r == ' ' || r == '\t' || r == '\n'
	})
}

// MatchCapabilities is conductor's matchCapabilities: exact, then alias (synonym), then a tag equal
// to the label or one of its boundary tokens (penalised), else not eligible.
func MatchCapabilities(required, capabilities, tags []string, aliases map[string]string) CapabilityMatch {
	if len(required) == 0 {
		return CapabilityMatch{Eligible: true, Tier: MatchExact}
	}
	aliasMap := map[string]string{}
	for k, v := range aliases {
		aliasMap[normCap(k)] = v
	}
	canonical := func(c string) string {
		n := normCap(c)
		if hit, ok := aliasMap[n]; ok && hit != "" {
			return normCap(hit)
		}
		return n
	}
	exact, canon := map[string]bool{}, map[string]bool{}
	for _, c := range capabilities {
		exact[normCap(c)] = true
		canon[canonical(c)] = true
	}
	tagSet := map[string]bool{}
	for _, t := range tags {
		if n := normCap(t); n != "" {
			tagSet[n] = true
		}
	}
	weakest, penalty := MatchExact, 0.0
	for _, req := range required {
		rn, rc := normCap(req), canonical(req)
		var tier MatchTier
		switch {
		case exact[rn]:
			tier = MatchExact
		case canon[rc]:
			tier = MatchSynonym
		case coveredByTag(rn, rc, tagSet):
			tier = MatchTag
			penalty += TagMatchPenalty
		default:
			return CapabilityMatch{Tier: MatchNone}
		}
		if tierOrder[tier] > tierOrder[weakest] {
			weakest = tier
		}
	}
	return CapabilityMatch{Eligible: true, Tier: weakest, Penalty: penalty}
}

func coveredByTag(rn, rc string, tags map[string]bool) bool {
	if len(tags) == 0 {
		return false
	}
	for _, c := range append([]string{rn, rc}, append(capTokens(rn), capTokens(rc)...)...) {
		if tags[c] {
			return true
		}
	}
	return false
}

// The reliability prior: Beta(α, β) with mean ReliabilityPriorMean over ReliabilityPriorStrength
// pseudo-samples, so a newcomer reads as 0.8 and competence must be proven over several tasks.
const (
	ReliabilityPriorMean     = 0.8
	ReliabilityPriorStrength = 5
)

// ReliabilityFor is conductor's reliabilityFor: (completed + α) / (samples + α + β).
func ReliabilityFor(completed, blocked int64) float64 {
	alpha := ReliabilityPriorMean * ReliabilityPriorStrength
	beta := (1 - ReliabilityPriorMean) * ReliabilityPriorStrength
	return (float64(completed) + alpha) / (float64(completed+blocked) + alpha + beta)
}

// TaskReliability is the mean ReliabilityFor across the task's required capabilities, the prior
// mean when it requires none. stat answers (completed, blocked) for one capability, zeros if none.
func TaskReliability(required []string, stat func(capability string) (completed, blocked int64)) float64 {
	if len(required) == 0 {
		return ReliabilityFor(0, 0)
	}
	sum := 0.0
	for _, c := range required {
		sum += ReliabilityFor(stat(c))
	}
	return sum / float64(len(required))
}
