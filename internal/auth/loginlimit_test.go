package auth

import (
	"context"
	"encoding/json"
	"fmt"
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
	r.RemoteAddr = addr + ":40000"
	w := httptest.NewRecorder()
	f.svc.CSRF(f.svc.LoginHandler()).ServeHTTP(w, r)
	return w.Result()
}

// slowVerify swaps in a verify that holds each check for hold and records the most in flight.
func slowVerify(t *testing.T, hold time.Duration) *atomic.Int64 {
	var now, peak atomic.Int64
	orig := verify
	verify = func(p, h string) bool {
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
	f.svc.Accounts.Limits = &LoginLimits{Verifies: 2, Wait: 10 * time.Second, PerAddr: 1000, PerLogin: 1000}
	peak := slowVerify(t, 20*time.Millisecond)
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
	slowVerify(t, 500*time.Millisecond)
	held := make(chan error, 1)
	go func() {
		_, err := f.svc.Accounts.Authenticate(context.Background(), "192.0.2.1", "alice", "right")
		held <- err
	}()
	time.Sleep(100 * time.Millisecond)
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

func TestFailedLoginsLimitedPerLogin(t *testing.T) {
	f := newFixture(t)
	f.addUser(t, "alice", "right")
	f.addUser(t, "bob", "right")
	for i := range 30 {
		if resp := f.loginFrom(t, fmt.Sprintf("198.51.100.%d", i+1), "Alice", "wrong"); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status %d, want 401", i+1, resp.StatusCode)
		}
	}
	wantRateLimited(t, f.loginFrom(t, "203.0.113.1", "alice", "wrong"))
	t.Run("Control", func(t *testing.T) {
		// The same fresh address still signs in to another account.
		if resp := f.loginFrom(t, "203.0.113.1", "bob", "right"); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status %d, want 204", resp.StatusCode)
		}
	})
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
