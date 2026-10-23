package domain

import (
	"math"
	"time"
)

// RetryPolicy is a project's backoff and quarantine policy for failed and reaped tasks. The JSON names
// are the project settings' (`settings.retryPolicy`).
type RetryPolicy struct {
	// BaseDelayMs is the first retry's delay; it doubles each attempt.
	BaseDelayMs int64 `json:"baseDelayMs"`
	// MaxDelayMs caps the delay.
	MaxDelayMs int64 `json:"maxDelayMs"`
	// Jitter is the ± fraction of the delay randomised, 0..1.
	Jitter float64 `json:"jitter"`
	// PoisonAfterAttempts: claimAttempts at or past this poisons the task instead of retrying it.
	PoisonAfterAttempts int `json:"poisonAfterAttempts"`
}

// DefaultRetryPolicy: 30 s first retry, doubling, capped at 1 h, ±20 % jitter, poison after 5 attempts.
var DefaultRetryPolicy = RetryPolicy{
	BaseDelayMs:         30_000,
	MaxDelayMs:          60 * 60_000,
	Jitter:              0.2,
	PoisonAfterAttempts: 5,
}

// RetryPolicyOrDefault returns the project's policy, or DefaultRetryPolicy when it has none.
func RetryPolicyOrDefault(p *RetryPolicy) RetryPolicy {
	if p == nil {
		return DefaultRetryPolicy
	}
	return *p
}

// BackoffDelay is the wait before the attempts-th failure may be retried: min(max, base·2^(attempts-1)),
// times a jitter factor in [1-jitter, 1+jitter], capped at max again and never negative. rand is a
// uniform sample in [0,1) supplied by the caller, so the function stays pure; attempts ≤ 1 uses base.
func BackoffDelay(attempts int, p RetryPolicy, rand float64) time.Duration {
	exp := math.Max(0, float64(attempts-1))
	raw := float64(p.BaseDelayMs) * math.Pow(2, exp)
	capped := math.Min(float64(p.MaxDelayMs), raw)
	j := clamp01(p.Jitter)
	factor := 1 - j + 2*j*clamp01(rand)
	ms := math.Max(0, math.Round(math.Min(float64(p.MaxDelayMs), capped*factor)))
	return time.Duration(ms) * time.Millisecond
}

// IsPoison reports whether attempts failures are enough to quarantine the task instead of retrying it.
func IsPoison(attempts int, p RetryPolicy) bool {
	return attempts >= p.PoisonAfterAttempts
}

// NextEligibleAt is when a retried task may be claimed again: now + BackoffDelay(attempts).
func NextEligibleAt(attempts int, p RetryPolicy, now time.Time, rand float64) time.Time {
	return now.Add(BackoffDelay(attempts, p, rand))
}

// IsPastBackoff reports whether a task's retry gate has opened. The zero time is no gate.
func IsPastBackoff(nextEligibleAt, now time.Time) bool {
	return nextEligibleAt.IsZero() || !nextEligibleAt.After(now)
}

// Failure is a failed or reaped outcome of a claimed task, read from one snapshot inside its
// transaction.
type Failure struct {
	// ClaimAttempts is the task's count before this failure.
	ClaimAttempts int
	Labels        []string
	Retryable     bool
	Reason        string
	// RetryEvent is the event the retry outcome appends: EventTaskFailed (fail_task) or
	// EventTaskLeaseReaped (the reaper).
	RetryEvent EventType
}

// FailureOutcome is what the transaction writes. The caller also drops the lease and lands the task
// in the column for Status.
type FailureOutcome struct {
	Status         Status
	BlockedReason  string
	ClaimAttempts  int
	NextEligibleAt time.Time
	Labels         []string
	Event          EventType
	EventData      map[string]any
}

// ApplyFailure counts the failure, then either schedules a backed-off retry (TODO, NextEligibleAt in
// the future) or poisons the task (BLOCKED, reason "poison", the poisoned label, no backoff) once it
// is not retryable or has reached poisonAfterAttempts. There is no third outcome. The count is kept
// on poison, not reset: resetting it is un-poison's job.
func ApplyFailure(f Failure, p RetryPolicy, now time.Time, rand float64) FailureOutcome {
	attempts := f.ClaimAttempts + 1
	if !f.Retryable || IsPoison(attempts, p) {
		return FailureOutcome{
			Status:        Blocked,
			BlockedReason: BlockedReasonPoison,
			ClaimAttempts: attempts,
			Labels:        WithLabel(f.Labels, LabelPoisoned),
			Event:         EventTaskPoisoned,
			EventData:     map[string]any{"reason": f.Reason, "attempts": attempts, "retryable": f.Retryable},
		}
	}
	at := NextEligibleAt(attempts, p, now, rand)
	return FailureOutcome{
		Status:         Todo,
		ClaimAttempts:  attempts,
		NextEligibleAt: at,
		Labels:         append([]string(nil), f.Labels...),
		Event:          f.RetryEvent,
		EventData:      map[string]any{"reason": f.Reason, "attempts": attempts, "nextEligibleAt": at},
	}
}

// IsUnpoison reports whether a move takes a poisoned task out of BLOCKED. Such a move requires the
// master role, so a worker cannot un-poison its own ticket (plan §Fold 1).
func IsUnpoison(from Status, blockedReason string, to Status) bool {
	return IsPoisoned(from, blockedReason) && to != Blocked
}

// UnpoisonOutcome is what an un-poisoning move writes besides the move itself.
type UnpoisonOutcome struct {
	BlockedReason  string
	ClaimAttempts  int
	NextEligibleAt time.Time
	Labels         []string
}

// Unpoison clears the poison: no reason, claimAttempts and the backoff reset, the poisoned label
// removed.
func Unpoison(labels []string) UnpoisonOutcome {
	return UnpoisonOutcome{Labels: WithoutLabel(labels, LabelPoisoned)}
}

func clamp01(x float64) float64 {
	return math.Max(0, math.Min(1, x))
}
