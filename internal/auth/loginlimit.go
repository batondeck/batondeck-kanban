package auth

import (
	"container/list"
	"context"
	"math"
	"net/netip"
	"sync"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// LoginLimits bounds password checks, each of which allocates argon2id's 64 MiB: at most Verifies of
// them run at once, a caller waits up to Wait for a slot, and every attempt debits a token bucket of
// its client address (an IPv6 address by its /64, and its /48 site as well unless the pair is known)
// before it runs; a success refunds it, and so does a refusal that checked no password (no
// verification slot within Wait, a store error). There is no bucket per login: one would let a few
// addresses lock anyone out of an account. A login that signed in from an address before (KnownFor)
// is served first when a slot frees, so a flood from addresses that never signed in, however many,
// cannot keep an account's owner out from where they sign in: a waiter behind the flood would give
// up after Wait.
type LoginLimits struct {
	// Verifies is the most password checks at once; 0 means 2 (128 MiB), whatever the core count.
	Verifies int
	// Wait is how long a check waits for a slot before RATE_LIMITED; 0 means 3 s.
	Wait time.Duration
	// PerAddr is the failed attempts allowed an address a minute (and the burst); 0 means 10.
	PerAddr int
	// PerSite is the failed attempts allowed an IPv6 /48 a minute (and the burst), on top of each of
	// its /64s' PerAddr; 0 means 5 × PerAddr. One end site, or one free tunnel broker allocation,
	// holds 65,536 /64s.
	PerSite int

	once  sync.Once
	slots *slots
	addrs *buckets
	sites *buckets
	known *knownPairs
}

// KnownFor is how long a successful sign-in from an address (its bucket key) makes that login's
// later checks from it go first. The record is in memory: a restart forgets it.
const KnownFor = 30 * 24 * time.Hour

// loginLimits is the process-wide default, used by every Accounts whose Limits is nil.
var loginLimits = &LoginLimits{}

func (l *LoginLimits) init() {
	l.once.Do(func() {
		l.slots = &slots{free: orDefault(l.Verifies, 2)}
		l.addrs = newBuckets(orDefault(l.PerAddr, 10))
		l.sites = newBuckets(orDefault(l.PerSite, 5*orDefault(l.PerAddr, 10)))
		l.known = &knownPairs{m: map[string]time.Time{}}
	})
}

func orDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

// admit debits addr's bucket, and its site's first when it has one and the caller is not known:
// RATE_LIMITED, with the wait, when either is empty (a site's token is put back when the address
// refuses). A known pair (isKnown) skips the site's budget, so other /64s of its /48 cannot lock it
// out; its own /64's budget still holds. The returned refund puts the tokens back.
func (l *LoginLimits) admit(addr string, known bool, now time.Time) (refund func(), err error) {
	l.init()
	key, site := AddrKey(addr), siteKey(addr)
	if known {
		site = ""
	}
	if site != "" {
		if wait, ok := l.sites.take(site, now); !ok {
			return nil, rateLimited(wait)
		}
	}
	if wait, ok := l.addrs.take(key, now); !ok {
		if site != "" {
			l.sites.refund(site)
		}
		return nil, rateLimited(wait)
	}
	return func() {
		l.addrs.refund(key)
		if site != "" {
			l.sites.refund(site)
		}
	}, nil
}

// signedIn records that login signed in from addr, so its checks from there go first (KnownFor).
func (l *LoginLimits) signedIn(addr, login string, now time.Time) {
	l.init()
	l.known.add(AddrKey(addr)+"\x00"+login, now)
}

// isKnown reports whether login signed in from addr within KnownFor.
func (l *LoginLimits) isKnown(addr, login string, now time.Time) bool {
	l.init()
	return l.known.has(AddrKey(addr)+"\x00"+login, now)
}

// AddrKey is what a client address counts against, in the sign-in limits and in OAuth
// registration's: an IPv4 address itself, an IPv6 address its /64 (one host commonly holds a whole
// /64). Anything unparsable is its own key.
func AddrKey(addr string) string {
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return addr
	}
	if a = a.Unmap(); a.Is4() {
		return a.String()
	}
	p, _ := a.WithZone("").Prefix(64)
	return p.String()
}

// siteKey is the /48 an IPv6 address belongs to, "" for any other address.
func siteKey(addr string) string {
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return ""
	}
	if a = a.Unmap(); a.Is4() {
		return ""
	}
	p, _ := a.WithZone("").Prefix(48)
	return p.String()
}

// acquire takes a verification slot, waiting up to Wait: RATE_LIMITED past it. A known caller (a
// login that signed in from this address before) is handed a freed slot before every other waiter.
func (l *LoginLimits) acquire(ctx context.Context, known bool) (release func(), err error) {
	l.init()
	wait := l.Wait
	if wait == 0 {
		wait = 3 * time.Second
	}
	return l.slots.acquire(ctx, known, wait)
}

// waiting is how many callers wait for a slot.
func (l *LoginLimits) waiting() int {
	l.init()
	l.slots.mu.Lock()
	defer l.slots.mu.Unlock()
	return l.slots.q[0].Len() + l.slots.q[1].Len()
}

// slots is a counting semaphore with two queues: a release hands the slot to the oldest known
// waiter, else to the oldest other one, else frees it.
type slots struct {
	mu   sync.Mutex
	free int
	q    [2]list.List // of chan struct{}: [0] known callers, [1] the rest
}

func (s *slots) acquire(ctx context.Context, known bool, wait time.Duration) (func(), error) {
	s.mu.Lock()
	if s.free > 0 {
		s.free--
		s.mu.Unlock()
		return s.release, nil
	}
	lane := 1
	if known {
		lane = 0
	}
	ch := make(chan struct{}, 1)
	el := s.q[lane].PushBack(ch)
	s.mu.Unlock()
	t := time.NewTimer(wait)
	defer t.Stop()
	var err error
	select {
	case <-ch:
		return s.release, nil
	case <-t.C:
		err = rateLimited(time.Second)
	case <-ctx.Done():
		err = ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-ch:
		// Handed the slot as the wait ended: pass it on.
		s.releaseLocked()
	default:
		s.q[lane].Remove(el)
	}
	return nil, err
}

func (s *slots) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.releaseLocked()
}

func (s *slots) releaseLocked() {
	for i := range s.q {
		if e := s.q[i].Front(); e != nil {
			s.q[i].Remove(e)
			e.Value.(chan struct{}) <- struct{}{}
			return
		}
	}
	s.free++
}

// knownPairs are the (address key, login) pairs that signed in, each until its expiry, at most
// maxTracked of them: past that the expired are dropped and then any others, down to three quarters.
type knownPairs struct {
	mu sync.Mutex
	m  map[string]time.Time
}

func (k *knownPairs) has(key string, now time.Time) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	exp, ok := k.m[key]
	return ok && now.Before(exp)
}

func (k *knownPairs) add(key string, now time.Time) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.m[key]; !ok && len(k.m) >= maxTracked {
		for kk, exp := range k.m {
			if !now.Before(exp) {
				delete(k.m, kk)
			}
		}
		for kk := range k.m {
			if len(k.m) <= maxTracked*3/4 {
				break
			}
			delete(k.m, kk)
		}
	}
	k.m[key] = now.Add(KnownFor)
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
