package cli

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/core"
	"github.com/batondeck/batondeck-kanban/internal/service"
	"github.com/batondeck/batondeck-kanban/internal/store"
	"github.com/batondeck/batondeck-kanban/internal/store/storetest"
)

// Behind a trusted reverse proxy every request's peer is the proxy, so the sign-in and
// registration limits count each client by the X-Forwarded-For hop the proxy appended (P-291):
// one client's failures lock out that client, not everyone behind the proxy. With no proxy
// trusted the header is never read and every request counts against the peer.
func TestLoginLimitBehindAProxy(t *testing.T) {
	for _, eng := range []struct {
		name string
		open func(t *testing.T) store.Store
	}{
		{"sqlite", func(t *testing.T) store.Store { return storetest.SQLite(t) }},
		{"postgres", func(t *testing.T) store.Store { return storetest.Postgres(t) }},
	} {
		for _, trusted := range []bool{true, false} {
			name := eng.name + "/untrusted"
			if trusted {
				name = eng.name + "/trusted"
			}
			t.Run(name, func(t *testing.T) { loginLimitBehindAProxy(t, eng.open(t), trusted) })
		}
	}
}

func loginLimitBehindAProxy(t *testing.T, st store.Store, trusted bool) {
	ctx := context.Background()
	if _, err := (&auth.Accounts{Store: st, Params: auth.Argon2Params{Time: 1, Memory: 64, Threads: 1}}).Add(ctx, "proxied", "Proxied", "right password", false); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(nil)
	a, err := compose(st, "x", "http://"+srv.Listener.Addr().String(), log.New(io.Discard, "", 0), nil, service.MediaOptions{}, core.Retention{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.core.Close(context.Background())
	defer a.bus.Close()
	// Its own buckets: the process-wide default is shared with every other test in the package.
	a.auth.Accounts.Limits = &auth.LoginLimits{}
	if trusted {
		// As runServer does with trusted_proxies; httptest's peer is 127.0.0.1.
		var logged strings.Builder
		a.trust(auth.TrustedProxies{netip.MustParsePrefix("127.0.0.1/32")}, log.New(&logged, "", 0))
		if want := "serve: trusted proxies 127.0.0.1/32: "; !strings.HasPrefix(logged.String(), want) {
			t.Fatalf("trust logged %q, want it to start %q", logged.String(), want)
		}
	}
	srv.Config.Handler = a.handler("x")
	srv.Start()
	defer srv.Close()

	login := func(xff, password string) int {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/login", strings.NewReader(url.Values{"login": {"proxied"}, "password": {password}}.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("X-Forwarded-For", xff)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for i := range 10 {
		if code := login("203.0.113.7", "wrong"); code != http.StatusUnauthorized {
			t.Fatalf("wrong password %d from 203.0.113.7: %d, want 401", i+1, code)
		}
	}
	if code := login("203.0.113.7", "wrong"); code != http.StatusTooManyRequests {
		t.Fatalf("11th wrong password from 203.0.113.7: %d, want 429", code)
	}
	// A client may write any leftmost hop; the proxy appends the address it saw, and that counts.
	if code := login("198.51.100.9, 203.0.113.7", "right password"); code != http.StatusTooManyRequests {
		t.Fatalf("203.0.113.7 forging 198.51.100.9 to the left: %d, want 429", code)
	}
	other, otherRight := http.StatusUnauthorized, http.StatusNoContent
	if !trusted {
		// Every request is 127.0.0.1's, and its bucket is empty.
		other, otherRight = http.StatusTooManyRequests, http.StatusTooManyRequests
	}
	if code := login("198.51.100.9", "wrong"); code != other {
		t.Fatalf("a wrong password from 198.51.100.9: %d, want %d", code, other)
	}
	if code := login("198.51.100.9", "right password"); code != otherRight {
		t.Fatalf("the right password from 198.51.100.9: %d, want %d", code, otherRight)
	}

	register := func(xff string) int {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/register", strings.NewReader(`{"client_name":"probe","redirect_uris":["http://127.0.0.1:43999/cb"]}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", xff)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	for i := range 20 {
		if code := register("203.0.113.7"); code != http.StatusCreated {
			t.Fatalf("registration %d from 203.0.113.7: %d, want 201", i+1, code)
		}
	}
	if code := register("203.0.113.7"); code != http.StatusTooManyRequests {
		t.Fatalf("21st registration from 203.0.113.7: %d, want 429", code)
	}
	want := http.StatusCreated
	if !trusted {
		want = http.StatusTooManyRequests
	}
	if code := register("198.51.100.9"); code != want {
		t.Fatalf("a registration from 198.51.100.9: %d, want %d", code, want)
	}
}
