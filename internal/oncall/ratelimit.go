package oncall

import (
	"math"
	"sync"
	"time"
)

// RateLimiter is the per-integration ingest budget (conductor src/metering/ingest-limiter.ts), in
// memory: one process serves every delivery, so no row is needed. Check is a token bucket of
// ceil(ratePerMin/4) refilled at ratePerMin a minute, keyed by integration, charging a delivery id
// once (a source's retry of a recorded delivery is free, so a partly landed group can drain);
// PreCheck is a coarse bucket keyed by the id as presented, before any lookup, so a flood on one
// URL costs bounded reads. RejectionBudget bounds the raw bodies a refused sender can make us keep.
type RateLimiter struct {
	mu        sync.Mutex
	buckets   map[string]*bucket
	pre       map[string]*bucket
	charged   map[string]struct{}
	rejection map[string]int // by integration and hour
}

type bucket struct {
	tokens float64
	last   time.Time
}

const (
	limiterMaxKeys = 10_000
	preCapacity    = 30
	prePerSec      = 120
	// RejectionsPerHour is how many refused deliveries an integration keeps the raw body of each
	// hour (conductor INGEST_REJECTION_BUDGET_PER_HOUR); the rest are recorded without one.
	RejectionsPerHour = 20
)

// NewRateLimiter is an empty limiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{buckets: map[string]*bucket{}, pre: map[string]*bucket{}, charged: map[string]struct{}{}, rejection: map[string]int{}}
}

// halve drops half of m's keys once it reaches limiterMaxKeys, so a key flood is bounded.
func halve[V any](m map[string]V) {
	if len(m) < limiterMaxKeys {
		return
	}
	drop := len(m) / 2
	for k := range m {
		if drop == 0 {
			return
		}
		delete(m, k)
		drop--
	}
}

func take(m map[string]*bucket, key string, capacity, perSec float64, now time.Time) bool {
	b, ok := m[key]
	if !ok {
		halve(m)
		b = &bucket{tokens: capacity, last: now}
		m[key] = b
	}
	if el := now.Sub(b.last).Seconds(); el > 0 {
		b.tokens = math.Min(capacity, b.tokens+el*perSec)
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// PreCheck charges the presented id's coarse bucket: false is over it.
func (l *RateLimiter) PreCheck(presentedID string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return take(l.pre, presentedID, preCapacity, prePerSec, now)
}

// Check charges integrationID's bucket for one delivery: false is over it. deliveryID "" always
// pays; a delivery id already charged is free.
func (l *RateLimiter) Check(integrationID string, ratePerMin int, now time.Time, deliveryID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := integrationID + "/" + deliveryID
	if deliveryID != "" {
		if _, ok := l.charged[key]; ok {
			return true
		}
	}
	rate := math.Max(1, float64(ratePerMin))
	if !take(l.buckets, integrationID, math.Ceil(rate/4), rate/60, now) {
		return false
	}
	if deliveryID != "" {
		halve(l.charged)
		l.charged[key] = struct{}{}
	}
	return true
}

// Refund gives one token back to integrationID's bucket, never past Capacity(ratePerMin): a charge
// taken before a delivery is verified (P-231) is returned once the delivery's events charge
// themselves. A bucket it has never charged is left alone.
func (l *RateLimiter) Refund(integrationID string, ratePerMin int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if b, ok := l.buckets[integrationID]; ok {
		b.tokens = math.Min(float64(Capacity(ratePerMin)), b.tokens+1)
	}
}

// Capacity is the burst Check allows ratePerMin: ceil(ratePerMin/4), at least 1.
func Capacity(ratePerMin int) int { return int(math.Ceil(math.Max(1, float64(ratePerMin)) / 4)) }

// RejectionBudget takes one of integrationID's raw-body slots for now's hour: false is spent.
func (l *RateLimiter) RejectionBudget(integrationID string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := integrationID + "@" + now.UTC().Format("2006-01-02T15")
	if l.rejection[key] >= RejectionsPerHour {
		return false
	}
	halve(l.rejection)
	l.rejection[key]++
	return true
}
