package domain

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

// Ported from conductor test/unit/retry.test.ts. Dropped: "treats null/absent/garbage nextEligibleAt as
// immediately eligible" keeps only its null case (the zero time), since a time.Time holds no garbage
// string; the RetryPolicySchema block, which tests a Zod refine on update_settings' input — a handler
// check in F4, not this package. Added: ApplyFailure (conductor lifecycle.ts:222-248, which had no
// unit table) and un-poison (plan §Fold 1).

var noJitter = RetryPolicy{BaseDelayMs: 1000, MaxDelayMs: 100_000, Jitter: 0, PoisonAfterAttempts: 5}

func ms(n int64) time.Duration { return time.Duration(n) * time.Millisecond }

func TestBackoffDelayDoubles(t *testing.T) {
	for attempts, want := range map[int]int64{1: 1000, 2: 2000, 3: 4000, 4: 8000, 0: 1000} {
		if got := BackoffDelay(attempts, noJitter, 0.5); got != ms(want) {
			t.Errorf("BackoffDelay(%d) = %v, want %dms", attempts, got, want)
		}
	}
}

func TestBackoffDelayCaps(t *testing.T) {
	if got := BackoffDelay(50, noJitter, 0.5); got != ms(noJitter.MaxDelayMs) {
		t.Fatalf("BackoffDelay(50) = %v, want the cap", got)
	}
}

func TestBackoffDelayJitter(t *testing.T) {
	p := noJitter
	p.Jitter = 0.5
	for rand, want := range map[float64]int64{0: 500, 1: 1500, 0.5: 1000} {
		if got := BackoffDelay(1, p, rand); got != ms(want) {
			t.Errorf("jitter 0.5, rand %v: %v, want %dms", rand, got, want)
		}
	}
	tight := RetryPolicy{BaseDelayMs: 100_000, MaxDelayMs: 100_000, Jitter: 0.5, PoisonAfterAttempts: 5}
	if got := BackoffDelay(1, tight, 1); got != ms(100_000) {
		t.Fatalf("jitter must not push past the cap: %v", got)
	}
}

// The floor on the result is what owes the guarantee; reaching it takes a malformed policy (a
// negative base or cap), not a malformed attempt count, since jitter and rand are clamped first.
func TestBackoffDelayNeverNegative(t *testing.T) {
	policies := []RetryPolicy{
		noJitter,
		{BaseDelayMs: 1000, MaxDelayMs: 100_000, Jitter: 1, PoisonAfterAttempts: 5},
		{BaseDelayMs: 1000, MaxDelayMs: 100_000, Jitter: 1.5, PoisonAfterAttempts: 5},
		{BaseDelayMs: 0, MaxDelayMs: 0, Jitter: 0, PoisonAfterAttempts: 5},
		{BaseDelayMs: -1000, MaxDelayMs: 100_000, Jitter: 0, PoisonAfterAttempts: 5},
		{BaseDelayMs: -1000, MaxDelayMs: -1, Jitter: 0, PoisonAfterAttempts: 5},
	}
	for _, p := range policies {
		for _, attempts := range []int{-3, 0, 1, 50} {
			for _, rand := range []float64{-1, 0, 0.5, 1, 2} {
				if d := BackoffDelay(attempts, p, rand); d < 0 {
					t.Errorf("%+v attempts=%d rand=%v: %v < 0", p, attempts, rand, d)
				}
			}
		}
	}
}

func TestIsPoison(t *testing.T) {
	if IsPoison(4, noJitter) || !IsPoison(5, noJitter) || !IsPoison(6, noJitter) {
		t.Fatal("poison at or past poisonAfterAttempts, not before")
	}
}

func TestNextEligibleAtAndBackoffGate(t *testing.T) {
	now := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	at := NextEligibleAt(2, noJitter, now, 0.5)
	if !at.Equal(now.Add(2 * time.Second)) {
		t.Fatalf("NextEligibleAt(2) = %v, want now+2s", at)
	}
	if IsPastBackoff(at, now) {
		t.Fatal("still backing off")
	}
	if !IsPastBackoff(at, now.Add(2001*time.Millisecond)) {
		t.Fatal("the gate opens once the backoff elapses")
	}
	if !IsPastBackoff(time.Time{}, now) {
		t.Fatal("no nextEligibleAt is immediately eligible")
	}
}

func TestRetryPolicyOrDefault(t *testing.T) {
	if RetryPolicyOrDefault(nil) != DefaultRetryPolicy {
		t.Fatal("no policy falls back to the default")
	}
	if RetryPolicyOrDefault(&noJitter) != noJitter {
		t.Fatal("a project policy is used as is")
	}
}

// AC2. "claimAttempts kept" means not reset: ApplyFailure counts this failure (conductor
// lifecycle.ts:230, attempts = claimAttempts + 1, stored on both outcomes) and the poison outcome keeps
// that count. Resetting it to 0 is un-poison's job.
func TestApplyFailure(t *testing.T) {
	now := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)
	f := Failure{ClaimAttempts: 4, Labels: []string{"ingest"}, Retryable: true, Reason: "boom", RetryEvent: EventTaskFailed}

	t.Run("at poisonAfterAttempts: BLOCKED poison, count kept", func(t *testing.T) {
		o := ApplyFailure(f, noJitter, now, 0.5)
		want := FailureOutcome{
			Status: Blocked, BlockedReason: BlockedReasonPoison, ClaimAttempts: 5,
			Labels: []string{"ingest", LabelPoisoned}, Event: EventTaskPoisoned,
			EventData: map[string]any{"reason": "boom", "attempts": 5, "retryable": true},
		}
		if !reflect.DeepEqual(o, want) {
			t.Fatalf("got %+v\nwant %+v", o, want)
		}
	})
	t.Run("retryable false on the first attempt: poison regardless", func(t *testing.T) {
		first := f
		first.ClaimAttempts, first.Retryable = 0, false
		o := ApplyFailure(first, noJitter, now, 0.5)
		if o.Status != Blocked || o.BlockedReason != BlockedReasonPoison || o.ClaimAttempts != 1 ||
			!o.NextEligibleAt.IsZero() || o.Event != EventTaskPoisoned {
			t.Fatalf("got %+v", o)
		}
	})
	t.Run("below the threshold: TODO with a future nextEligibleAt", func(t *testing.T) {
		below := f
		below.ClaimAttempts = 1
		o := ApplyFailure(below, noJitter, now, 0.5)
		if o.Status != Todo || o.BlockedReason != "" || o.ClaimAttempts != 2 || o.Event != EventTaskFailed {
			t.Fatalf("got %+v", o)
		}
		if !o.NextEligibleAt.After(now) || !o.NextEligibleAt.Equal(now.Add(2*time.Second)) {
			t.Fatalf("nextEligibleAt %v, want now+2s", o.NextEligibleAt)
		}
		if !reflect.DeepEqual(o.Labels, []string{"ingest"}) {
			t.Fatalf("a retry adds no label: %v", o.Labels)
		}
	})
	t.Run("the reaper's retry event", func(t *testing.T) {
		reaped := f
		reaped.ClaimAttempts, reaped.RetryEvent = 0, EventTaskLeaseReaped
		if o := ApplyFailure(reaped, noJitter, now, 0.5); o.Event != EventTaskLeaseReaped || o.Status != Todo {
			t.Fatalf("got %+v", o)
		}
	})
	t.Run("never a third state", func(t *testing.T) {
		for attempts := 0; attempts < 8; attempts++ {
			for _, retryable := range []bool{true, false} {
				o := ApplyFailure(Failure{ClaimAttempts: attempts, Retryable: retryable, RetryEvent: EventTaskFailed}, noJitter, now, 0.5)
				poisoned := o.Status == Blocked && o.BlockedReason == BlockedReasonPoison
				retried := o.Status == Todo && o.BlockedReason == ""
				if poisoned == retried {
					t.Errorf("attempts=%d retryable=%v: %+v is neither or both outcomes", attempts, retryable, o)
				}
			}
		}
	})
}

func TestUnpoison(t *testing.T) {
	for _, c := range []struct {
		from   Status
		reason string
		to     Status
		want   bool
	}{
		{Blocked, BlockedReasonPoison, Todo, true},
		{Blocked, BlockedReasonPoison, InProgress, true},
		{Blocked, BlockedReasonPoison, Blocked, false},
		{Blocked, "dependency", Todo, false},
		{Todo, BlockedReasonPoison, InProgress, false},
	} {
		t.Run(fmt.Sprintf("%s/%s->%s", c.from, c.reason, c.to), func(t *testing.T) {
			if got := IsUnpoison(c.from, c.reason, c.to); got != c.want {
				t.Fatalf("IsUnpoison = %v, want %v", got, c.want)
			}
		})
	}
	o := Unpoison([]string{"ingest", LabelPoisoned})
	want := UnpoisonOutcome{Labels: []string{"ingest"}}
	if !reflect.DeepEqual(o, want) {
		t.Fatalf("Unpoison = %+v, want %+v (no reason, attempts 0, no backoff, label removed)", o, want)
	}
}
