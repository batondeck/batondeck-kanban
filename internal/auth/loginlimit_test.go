package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// loginFrom POSTs the form to the mounted login handler from the address addr.
func (f *fixture) loginFrom(t *testing.T, addr, login, password string) *http.Response {
	t.Helper()
	form := url.Values{"login": {login}, "password": {password}}
	r := httptest.NewRequest(http.MethodPost, "http://board.test/login", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.RemoteAddr = net.JoinHostPort(addr, "40000")
	w := httptest.NewRecorder()
	f.svc.CSRF(f.svc.LoginHandler()).ServeHTTP(w, r)
	return w.Result()
}

// slowVerify swaps in a verify that holds each check for hold and records the most in flight; each
// check sends on entered, when it is not full, as it starts.
func slowVerify(t *testing.T, hold time.Duration, entered chan<- struct{}) *atomic.Int64 {
	var now, peak atomic.Int64
	orig := verify
	verify = func(p, h string) bool {
		select {
		case entered <- struct{}{}:
		default:
		}
		n := now.Add(1)
		for {
			if m := peak.Load(); n <= m || peak.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(hold)
		now.Add(-1)
		return orig(p, h)
	}
	t.Cleanup(func() { verify = orig })
	return &peak
}

func TestLoginVerifiesAreCapped(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "alice", "right")
	// The default cap, 2, whatever the core count.
	f.svc.Accounts.Limits = &LoginLimits{Wait: 10 * time.Second, PerAddr: 1000}
	peak := slowVerify(t, 20*time.Millisecond, nil)
	var wg sync.WaitGroup
	for range 64 {
		wg.Go(func() {
			_, err := f.svc.Accounts.Authenticate(context.Background(), "192.0.2.1", "alice", "wrong")
			wantCode(t, err, domain.CodeUnauthenticated)
		})
	}
	wg.Wait()
	if p := peak.Load(); p > 2 {
		t.Fatalf("peak verifies in flight %d, want <= 2", p)
	}
	// Control: the cap admits as many checks as it allows at once.
	if p := peak.Load(); p != 2 {
		t.Fatalf("peak verifies in flight %d, want 2", p)
	}
}

func TestLoginVerifyWaitIsBounded(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "alice", "right")
	f.svc.Accounts.Limits = &LoginLimits{Verifies: 1, Wait: 50 * time.Millisecond}
	entered := make(chan struct{}, 1)
	slowVerify(t, 500*time.Millisecond, entered)
	held := make(chan error, 1)
	go func() {
		_, err := f.svc.Accounts.Authenticate(context.Background(), "192.0.2.1", "alice", "right")
		held <- err
	}()
	<-entered
	_, err := f.svc.Accounts.Authenticate(context.Background(), "192.0.2.2", "alice", "right")
	wantCode(t, err, domain.CodeRateLimited)
	// Control: the caller holding the slot succeeds.
	if err := <-held; err != nil {
		t.Fatal(err)
	}
}

func wantRateLimited(t *testing.T, resp *http.Response) {
	t.Helper()
	var body struct{ Code, Message string }
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusTooManyRequests || body.Code != string(domain.CodeRateLimited) {
		t.Fatalf("status %d body %+v, want 429 RATE_LIMITED", resp.StatusCode, body)
	}
	if ra := resp.Header.Get("Retry-After"); ra == "" || ra == "0" {
		t.Fatalf("Retry-After %q", ra)
	}
}

func TestFailedLoginsLimitedPerAddress(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "alice", "right")
	for i := range 10 {
		if resp := f.loginFrom(t, "192.0.2.1", "alice", "wrong"); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status %d, want 401", i+1, resp.StatusCode)
		}
	}
	wantRateLimited(t, f.loginFrom(t, "192.0.2.1", "alice", "wrong"))
	// The limited address is refused even with the right password, until its bucket refills.
	wantRateLimited(t, f.loginFrom(t, "192.0.2.1", "alice", "right"))
	t.Run("Control", func(t *testing.T) {
		// Another address signs in to the same account during the flood.
		if resp := f.loginFrom(t, "192.0.2.2", "alice", "right"); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status %d, want 204", resp.StatusCode)
		}
		f.clk.add(6 * time.Second)
		if resp := f.loginFrom(t, "192.0.2.1", "alice", "right"); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("after a refill: status %d, want 204", resp.StatusCode)
		}
	})
}

func TestFailedLoginsLimitedPerIPv6Prefix(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "alice", "right")
	for i := range 10 {
		if resp := f.loginFrom(t, fmt.Sprintf("2001:db8:0:1::%x", i+1), "alice", "wrong"); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status %d, want 401", i+1, resp.StatusCode)
		}
	}
	// Every other address of the /64 shares the spent bucket.
	for i := 10; i < 25; i++ {
		wantRateLimited(t, f.loginFrom(t, fmt.Sprintf("2001:db8:0:1:ffff::%x", i+1), "alice", "wrong"))
	}
	t.Run("Control", func(t *testing.T) {
		// The next /64 has its own bucket.
		if resp := f.loginFrom(t, "2001:db8:0:2::1", "alice", "wrong"); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status %d, want 401", resp.StatusCode)
		}
	})
}

func TestFailedLoginsDoNotLockOutTheAccount(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "alice", "right")
	for a := range 3 {
		for range 10 {
			f.loginFrom(t, fmt.Sprintf("198.51.100.%d", a+1), "alice", "wrong")
		}
		wantRateLimited(t, f.loginFrom(t, fmt.Sprintf("198.51.100.%d", a+1), "alice", "wrong"))
	}
	// The owner, from a fresh address, signs in through three spent attackers.
	if resp := f.loginFrom(t, "203.0.113.9", "alice", "right"); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status %d, want 204", resp.StatusCode)
	}
}

func TestSuccessfulLoginsAreNotCounted(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "alice", "right")
	for i := range 40 {
		if resp := f.loginFrom(t, "192.0.2.1", "alice", "right"); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("login %d: status %d, want 204", i+1, resp.StatusCode)
		}
	}
}

// One IPv6 site (a /48, what one end site or a free tunnel broker hands out) has a budget of its
// own on top of each /64's, so failed attempts from its 65,536 /64s cannot each spend a fresh
// bucket.
func TestFailedLoginsLimitedPerIPv6Site(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "alice", "right")
	for i := range 50 {
		if resp := f.loginFrom(t, fmt.Sprintf("2001:db8:0:%x::1", i+1), "alice", "wrong"); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status %d, want 401", i+1, resp.StatusCode)
		}
	}
	// Every further /64 of the /48 is refused before its own bucket is touched.
	for i := 50; i < 80; i++ {
		wantRateLimited(t, f.loginFrom(t, fmt.Sprintf("2001:db8:0:%x::1", i+1), "alice", "wrong"))
	}
	t.Run("Control", func(t *testing.T) {
		// Another /48 has its own budget, and the owner signs in from it.
		if resp := f.loginFrom(t, "2001:db8:1:1::1", "alice", "wrong"); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status %d, want 401", resp.StatusCode)
		}
		if resp := f.loginFrom(t, "2001:db8:1:2::1", "alice", "right"); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status %d, want 204", resp.StatusCode)
		}
	})
}

// A login that signed in from a /64 before counts only against that /64's budget, not its /48's, so
// a flood from other /64s of the same site (a carrier pool, a campus, a cloud) cannot lock out the
// people who share it from where they sign in. Only a sign-in that succeeded makes a pair known, so
// the flood cannot use it.
func TestKnownSignInSkipsTheSiteBudget(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "alice", "right")
	if resp := f.loginFrom(t, "2001:db8:0:ffff::1", "alice", "right"); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("first sign-in: status %d, want 204", resp.StatusCode)
	}
	for i := range 50 {
		if resp := f.loginFrom(t, fmt.Sprintf("2001:db8:0:%x::1", i+1), "alice", "wrong"); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status %d, want 401", i+1, resp.StatusCode)
		}
	}
	wantRateLimited(t, f.loginFrom(t, "2001:db8:0:51::1", "alice", "wrong"))
	if resp := f.loginFrom(t, "2001:db8:0:ffff::2", "alice", "right"); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("the owner, from the /64 she signed in from, once her /48's budget is spent: status %d, want 204", resp.StatusCode)
	}
	t.Run("Control", func(t *testing.T) {
		// A /64 of the site that never signed in is refused, even with the right password.
		wantRateLimited(t, f.loginFrom(t, "2001:db8:0:fffe::1", "alice", "right"))
		// The known /64 still has its own budget.
		for i := range 10 {
			if resp := f.loginFrom(t, "2001:db8:0:ffff::3", "alice", "wrong"); resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("known /64, attempt %d: status %d, want 401", i+1, resp.StatusCode)
			}
		}
		wantRateLimited(t, f.loginFrom(t, "2001:db8:0:ffff::3", "alice", "right"))
	})
}

// flood keeps the verification slots busy with wrong passwords for alice, each check taking hold:
// a new attempt every few milliseconds, each from its own address that never signed in, for as long
// as the test runs (stop ends it and waits for every attempt). It returns once the queue has been
// full for longer than the limiter's Wait, so whatever queues behind it waits behind fresh attempts.
func flood(t *testing.T, f *fixture, hold, wait time.Duration) (stop func()) {
	t.Helper()
	slowVerify(t, hold, nil)
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			case <-tick.C:
			}
			addr := fmt.Sprintf("198.51.%d.%d", i/250, i%250+1)
			wg.Go(func() { _, _ = f.svc.Accounts.Authenticate(context.Background(), addr, "alice", "wrong") })
		}
	})
	time.Sleep(wait + hold)
	return func() { close(done); wg.Wait() }
}

// A login that signed in before from an address is served before the flood when a verification
// slot frees. Without that, a steady stream of wrong passwords from addresses that never signed in
// keeps its owner out: each request waits behind attempts that arrived before it and gives up after
// Wait, however many addresses the flood comes from.
func TestKnownSignInIsServedBeforeTheFlood(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "alice", "right")
	// The owner waits about one hold for her slot; Wait is fifteen holds, so a loaded runner does not
	// time her out (TestSlotsServeKnownFirst pins the order without timing).
	const hold, wait = 100 * time.Millisecond, 1500 * time.Millisecond
	f.svc.Accounts.Limits = &LoginLimits{Verifies: 1, Wait: wait, PerAddr: 1000}
	if _, err := f.svc.Accounts.Authenticate(context.Background(), "192.0.2.50", "alice", "right"); err != nil {
		t.Fatal(err)
	}
	stop := flood(t, f, hold, wait)
	defer stop()
	for i := range 3 {
		if _, err := f.svc.Accounts.Authenticate(context.Background(), "192.0.2.50", "alice", "right"); err != nil {
			t.Fatalf("sign-in %d by the owner, from where she signed in before, during the flood: %v", i+1, err)
		}
	}
}

// The slots serve a known pair's waiter first, then the others oldest first: the mechanism
// TestKnownSignInIsServedBeforeTheFlood relies on, without its timing.
func TestSlotsServeKnownFirst(t *testing.T) {
	l := &LoginLimits{Verifies: 1, Wait: time.Minute}
	release, err := l.acquire(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 3)
	var wg sync.WaitGroup
	for i, w := range []struct {
		name  string
		known bool
	}{{"unknown-1", false}, {"unknown-2", false}, {"known", true}} {
		wg.Go(func() {
			r, err := l.acquire(context.Background(), w.known)
			if err != nil {
				t.Error(err)
				return
			}
			got <- w.name
			r()
		})
		for l.waiting() != i+1 {
			time.Sleep(time.Millisecond)
		}
	}
	release()
	wg.Wait()
	close(got)
	var order []string
	for n := range got {
		order = append(order, n)
	}
	if want := "known unknown-1 unknown-2"; strings.Join(order, " ") != want {
		t.Fatalf("served %v, want %s", order, want)
	}
}
