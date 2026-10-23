package domain

import (
	"strings"
	"time"
)

// BlockedReasonPoison is the blockedReason of a quarantined task: one that failed poisonAfterAttempts
// times, or once with retryable:false. It is BLOCKED like any other blocked task; this reason is what
// keeps it out of the claim path and out of auto-unblock (plan §Fold 1).
const BlockedReasonPoison = "poison"

// IsPoisoned reports whether a task is quarantined. It keys on the reason, never on an attempt count.
func IsPoisoned(status Status, blockedReason string) bool {
	return status == Blocked && blockedReason == BlockedReasonPoison
}

// Lease is the part of a claim the claim decision reads.
type Lease struct {
	ExpiresAt time.Time
}

// ClaimView is everything IsClaimable reads, taken from one snapshot of the task.
type ClaimView struct {
	Status        Status
	BlockedBy     []string
	BlockedReason string
	// Racing: a parent with an open race; its runs carry the work.
	Racing bool
	// Claim is the task's lease, nil when unclaimed.
	Claim *Lease
	// NextEligibleAt is the retry gate set by a failure; the zero time means no backoff.
	NextEligibleAt time.Time
	Assignee       string
}

// HasLiveLease reports whether someone holds the task right now. An expired lease does not count:
// expiry is lazy on read, so a lease past its ExpiresAt is free to be claimed again before any reaper
// has swept it.
func HasLiveLease(claim *Lease, now time.Time) bool {
	return claim != nil && claim.ExpiresAt.After(now)
}

// IsClaimable is the one definition of "an agent may claim this task now": TODO, nothing blocking
// it, not poisoned, not racing, no live lease, and past its retry backoff. Every selector, frontier
// and doorbell calls this rather than re-deriving it.
func IsClaimable(t ClaimView, now time.Time) bool {
	return t.Status == Todo &&
		len(t.BlockedBy) == 0 &&
		t.BlockedReason != BlockedReasonPoison &&
		!t.Racing &&
		!HasLiveLease(t.Claim, now) &&
		IsPastBackoff(t.NextEligibleAt, now)
}

// IsOpenFrontier is claimable and addressed to nobody: the population a broadcast doorbell announces.
// Assigned work already has a directed doorbell.
func IsOpenFrontier(t ClaimView, now time.Time) bool {
	return strings.TrimSpace(t.Assignee) == "" && IsClaimable(t, now)
}
