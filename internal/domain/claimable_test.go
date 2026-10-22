package domain

import (
	"testing"
	"time"
)

// Ported from conductor test/unit/wake-frontier.test.ts ("isClaimable is exactly what the selector
// accepts", "isOpenFrontier adds addressed to nobody and nothing else"). Dropped: nextEligibleAt
// null / garbage ISO strings, which a time.Time cannot hold (the zero time is "no backoff"); the
// FrontierBuffer cases, which test the doorbell, not this predicate. Added: the poison cases (plan
// §Fold 1) and the before/after auto-unblock pair (R0.6 AC3).

var claimNow = time.Unix(1_700_000_000, 0)

func readyView() ClaimView { return ClaimView{Status: Ready} }

func TestIsClaimable(t *testing.T) {
	cases := []struct {
		name string
		view ClaimView
		want bool
	}{
		{"plain unblocked READY is claimable", readyView(), true},
		{"blockers make it unclaimable even in READY", ClaimView{Status: Ready, BlockedBy: []string{"T-9"}}, false},
		{"a racing parent is not claimable", ClaimView{Status: Ready, Racing: true}, false},
		{"inside its retry backoff", ClaimView{Status: Ready, NextEligibleAt: claimNow.Add(time.Minute)}, false},
		{"past its backoff the gate opens", ClaimView{Status: Ready, NextEligibleAt: claimNow.Add(-time.Millisecond)}, true},
		{"backoff ending exactly now has opened", ClaimView{Status: Ready, NextEligibleAt: claimNow}, true},
		{"a live lease disqualifies", ClaimView{Status: Ready, Claim: &Lease{ExpiresAt: claimNow.Add(time.Minute)}}, false},
		{"an expired lease does not: expiry is lazy on read", ClaimView{Status: Ready, Claim: &Lease{ExpiresAt: claimNow.Add(-time.Minute)}}, true},
		{"a lease expiring exactly now has expired", ClaimView{Status: Ready, Claim: &Lease{ExpiresAt: claimNow}}, true},
		// AC3: a poisoned task, whatever its attempt count or dependencies.
		{"poisoned is not claimable", ClaimView{Status: Blocked, BlockedReason: BlockedReasonPoison}, false},
		// The reason alone keeps it out, even on a row whose status a bad write moved to READY.
		{"READY still carrying the poison reason is not claimable", ClaimView{Status: Ready, BlockedReason: BlockedReasonPoison}, false},
		// AC3: blockedBy just emptied, auto-unblock not yet run: still BLOCKED, not claimable.
		{"BLOCKED with empty blockedBy before auto-unblock", ClaimView{Status: Blocked, BlockedReason: "dependency"}, false},
		// AC3: the same task after auto-unblock moved it to READY.
		{"after auto-unblock it is claimable", ClaimView{Status: Ready}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsClaimable(c.view, claimNow); got != c.want {
				t.Fatalf("IsClaimable(%+v) = %v, want %v", c.view, got, c.want)
			}
		})
	}
	for _, s := range AllStatuses() {
		if s == Ready {
			continue
		}
		if IsClaimable(ClaimView{Status: s}, claimNow) {
			t.Errorf("status %s is claimable; only READY is", s)
		}
	}
}

func TestIsOpenFrontier(t *testing.T) {
	if !IsOpenFrontier(readyView(), claimNow) {
		t.Fatal("claimable and unassigned must be open frontier")
	}
	if IsOpenFrontier(ClaimView{Status: Ready, Assignee: "claude-backend"}, claimNow) {
		t.Fatal("an assigned claimable task is not open frontier: it has a directed doorbell")
	}
	if !IsOpenFrontier(ClaimView{Status: Ready, Assignee: "   "}, claimNow) {
		t.Fatal("whitespace is not an assignee")
	}
	// Parity: every disqualifier of IsClaimable disqualifies the frontier too.
	for _, v := range []ClaimView{
		{Status: Ready, BlockedBy: []string{"T-9"}},
		{Status: Ready, Racing: true},
		{Status: Ready, NextEligibleAt: claimNow.Add(time.Minute)},
		{Status: Backlog},
		{Status: Ready, Claim: &Lease{ExpiresAt: claimNow.Add(time.Minute)}},
		{Status: Blocked, BlockedReason: BlockedReasonPoison},
		{Status: Ready, BlockedReason: BlockedReasonPoison},
	} {
		if IsClaimable(v, claimNow) || IsOpenFrontier(v, claimNow) {
			t.Errorf("%+v: claimable %v, frontier %v; both must be false", v, IsClaimable(v, claimNow), IsOpenFrontier(v, claimNow))
		}
	}
}

func TestIsPoisoned(t *testing.T) {
	if !IsPoisoned(Blocked, BlockedReasonPoison) {
		t.Fatal("BLOCKED + poison is poisoned")
	}
	if IsPoisoned(Blocked, "dependency") || IsPoisoned(Blocked, "") || IsPoisoned(Ready, BlockedReasonPoison) {
		t.Fatal("only BLOCKED with reason poison is poisoned")
	}
}
