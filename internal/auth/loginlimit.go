package auth

import (
	"context"
	"math"
	"net"
	"net/http"
	"runtime"
	"sync"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// LoginLimits bounds password checks, each of which allocates argon2id's 64 MiB: at most Verifies of
// them run at once, a caller waits up to Wait for a slot, and every attempt debits a token bucket per
// client address and one per login before it runs (a success refunds both). A login's bucket is
// larger than an address's, so one address cannot lock another out of an account.
type LoginLimits struct {
	// Verifies is the most password checks at once; 0 means GOMAXPROCS, never fewer than 2.
	Verifies int
	// Wait is how long a check waits for a slot before RATE_LIMITED; 0 means 3 s.
	Wait time.Duration
	// PerAddr and PerLogin are the failed attempts allowed a minute (and the burst); 0 means 10 and 30.
	PerAddr, PerLogin int

	once  sync.Once
	sem   chan struct{}
	addrs *buckets
	names *buckets
}

// loginLimits is the process-wide default, used by every Accounts whose Limits is nil.
var loginLimits = &LoginLimits{}

func (l *LoginLimits) init() {
	l.once.Do(func() {
		n := l.Verifies
		if n == 0 {
			n = max(runtime.GOMAXPROCS(0), 2)
		}
		l.sem = make(chan struct{}, n)
		l.addrs = newBuckets(orDefault(l.PerAddr, 10))
		l.names = newBuckets(orDefault(l.PerLogin, 30))
	})
}

func orDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

// admit debits addr's and login's buckets: RATE_LIMITED, with the wait, when either is empty. The
// returned refund puts both tokens back.
func (l *LoginLimits) admit(addr, login string, now time.Time) (refund func(), err error) {
	l.init()
	if wait, ok := l.addrs.take(addr, now); !ok {
		return nil, rateLimited(wait)
	}
	if wait, ok := l.names.take(login, now); !ok {
		l.addrs.refund(addr)
		return nil, rateLimited(wait)
	}
	return func() { l.addrs.refund(addr); l.names.refund(login) }, nil
}

// acquire takes a verification slot, waiting up to Wait: RATE_LIMITED past it.
func (l *LoginLimits) acquire(ctx context.Context) (release func(), err error) {
	l.init()
	wait := l.Wait
	if wait == 0 {
		wait = 3 * time.Second
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case l.sem <- struct{}{}:
		return func() { <-l.sem }, nil
	case <-t.C:
		return nil, rateLimited(time.Second)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func rateLimited(retry time.Duration) error {
	s := max(int(math.Ceil(retry.Seconds())), 1)
	return domain.NewError(domain.CodeRateLimited, "Too many sign-in attempts; try again later.",
		map[string]any{"retryAfterSeconds": s})
}

// buckets are token buckets by key, each holding up to burst tokens and refilling burst a minute.
// At most maxTracked keys are held; past that the full buckets are dropped, and when none is full
// a new key is refused rather than the map growing (the per-address limit makes that need many
// addresses).
type buckets struct {
	mu    sync.Mutex
	burst float64
	m     map[string]*bucket
}

type bucket struct {
	tokens float64
	at     time.Time
}

const maxTracked = 10000

func newBuckets(burst int) *buckets {
	return &buckets{burst: float64(burst), m: map[string]*bucket{}}
}

func (b *buckets) perSecond() float64 { return b.burst / 60 }

func (b *buckets) fill(k *bucket, now time.Time) {
	if d := now.Sub(k.at).Seconds(); d > 0 {
		k.tokens = math.Min(b.burst, k.tokens+d*b.perSecond())
	}
	k.at = now
}

func (b *buckets) take(key string, now time.Time) (time.Duration, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	k, ok := b.m[key]
	if !ok {
		if len(b.m) >= maxTracked {
			for kk, v := range b.m {
				if b.fill(v, now); v.tokens >= b.burst {
					delete(b.m, kk)
				}
			}
			if len(b.m) >= maxTracked {
				return time.Minute, false
			}
		}
		k = &bucket{tokens: b.burst, at: now}
		b.m[key] = k
	}
	b.fill(k, now)
	if k.tokens < 1 {
		return time.Duration((1 - k.tokens) / b.perSecond() * float64(time.Second)), false
	}
	k.tokens--
	return 0, true
}

func (b *buckets) refund(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if k, ok := b.m[key]; ok {
		k.tokens = math.Min(b.burst, k.tokens+1)
	}
}

// clientAddr is the address a sign-in is counted against: the peer's IP. X-Forwarded-For is not
// read, so it cannot be forged; behind a proxy every client shares the proxy's budget.
func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
