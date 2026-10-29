package oncall

import (
	"testing"
	"time"
)

func TestRateLimiter(t *testing.T) {
	l := NewRateLimiter()
	t0 := time.Unix(1_700_000_000, 0)
	// ratePerMin 40 is a burst of 10, refilled at 2 every 3 s.
	if c := Capacity(40); c != 10 {
		t.Fatalf("Capacity(40) = %d", c)
	}
	for i := range 10 {
		if !l.Check("I-a", 40, t0, string(rune('a'+i))) {
			t.Fatalf("delivery %d of 10 refused", i+1)
		}
	}
	if l.Check("I-a", 40, t0, "k") {
		t.Fatal("the 11th delivery in the burst was allowed")
	}
	if !l.Check("I-a", 40, t0, "a") {
		t.Fatal("a delivery id already charged paid again")
	}
	if !l.Check("I-b", 40, t0, "k") {
		t.Fatal("another integration shares the bucket")
	}
	if !l.Check("I-a", 40, t0.Add(1500*time.Millisecond), "k") {
		t.Fatal("refused after the refill window")
	}
	if l.Check("I-a", 40, t0.Add(1500*time.Millisecond), "") {
		t.Fatal("a rejection (no delivery id) was free")
	}
	for i := range preCapacity {
		if !l.PreCheck("I-x", t0) {
			t.Fatalf("pre-check %d refused", i)
		}
	}
	if l.PreCheck("I-x", t0) {
		t.Fatal("the pre-check burst is unbounded")
	}
	for range RejectionsPerHour {
		if !l.RejectionBudget("I-a", t0) {
			t.Fatal("the rejection budget ran out early")
		}
	}
	if l.RejectionBudget("I-a", t0) || !l.RejectionBudget("I-a", t0.Add(time.Hour)) {
		t.Fatal("the rejection budget is not per hour")
	}
}

func TestUntouched(t *testing.T) {
	if !Untouched("TODO", false, 0, 3, 3) {
		t.Fatal("an untouched ticket (positive control)")
	}
	for name, got := range map[string]bool{
		"in progress":     Untouched("IN_PROGRESS", false, 0, 3, 3),
		"claimed":         Untouched("TODO", true, 0, 3, 3),
		"attempted":       Untouched("TODO", false, 1, 3, 3),
		"written by some": Untouched("TODO", false, 0, 4, 3),
	} {
		if got {
			t.Errorf("%s: untouched", name)
		}
	}
}

// P-231: Refund gives one token back, never past the burst; a bucket never charged is untouched.
func TestRateLimiterRefund(t *testing.T) {
	l := NewRateLimiter()
	t0 := time.Unix(1_700_000_000, 0)
	for i := range 10 {
		if !l.Check("I-r", 40, t0, "") {
			t.Fatalf("charge %d of 10 refused", i+1)
		}
	}
	if l.Check("I-r", 40, t0, "") {
		t.Fatal("the 11th charge was allowed (control)")
	}
	l.Refund("I-r", 40)
	if !l.Check("I-r", 40, t0, "") {
		t.Fatal("the refunded token is not there")
	}
	if l.Check("I-r", 40, t0, "") {
		t.Fatal("a refund gave back more than one token")
	}
	full := NewRateLimiter()
	if !full.Check("I-f", 40, t0, "") {
		t.Fatal("the first charge was refused")
	}
	for range 5 {
		full.Refund("I-f", 40)
	}
	for i := range Capacity(40) {
		if !full.Check("I-f", 40, t0, "") {
			t.Fatalf("charge %d after refunds refused", i+1)
		}
	}
	if full.Check("I-f", 40, t0, "") {
		t.Fatal("refunds raised the bucket past Capacity")
	}
	l.Refund("I-never", 40)
	l.mu.Lock()
	_, made := l.buckets["I-never"]
	l.mu.Unlock()
	if made {
		t.Fatal("a refund made a bucket it never charged")
	}
}
