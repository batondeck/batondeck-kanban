package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/batondeck/batondeck-kanban/internal/oncall"
)

func TestAPIKeyAndBasic(t *testing.T) {
	stored := oncall.HashAPIKey("k-0123456789")
	if !VerifyAPIKey("k-0123456789", stored) {
		t.Fatal("the right key was refused (positive control)")
	}
	for _, bad := range []string{"", "k-012345678", "k-0123456789 "} {
		if VerifyAPIKey(bad, stored) {
			t.Errorf("key %q was accepted", bad)
		}
	}
	basic := func(user, pass string) string {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	}
	if reason, ok := VerifyBasic(basic("anyone", "k-0123456789"), stored); !ok {
		t.Fatalf("basic with the key as password: %s (positive control)", reason)
	}
	for header, want := range map[string]string{
		"":          ReasonBasicMissing,
		"Bearer x":  ReasonBasicMissing,
		"Basic !!!": ReasonBasicMissing,
		"Basic " + base64.StdEncoding.EncodeToString([]byte("nocolon")): ReasonBasicMalformed,
		basic("anyone", "wrong-key-000"):                                ReasonAPIKeyMismatch,
	} {
		if reason, ok := VerifyBasic(header, stored); ok || reason != want {
			t.Errorf("VerifyBasic(%q) = %q, %v; want %q", header, reason, ok, want)
		}
	}
}

var now = time.Date(2026, 10, 24, 13, 0, 0, 0, time.UTC)

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func sign(t *testing.T, k *rsa.PrivateKey, header map[string]string, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(header)
	c, _ := json.Marshal(claims)
	in := b64(h) + "." + b64(c)
	d := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, d[:])
	if err != nil {
		t.Fatal(err)
	}
	return in + "." + b64(sig)
}

func TestJWTVerify(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	set, _ := json.Marshal(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1", "n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes())}}})
	fetches := 0
	v := &Verifier{Fetch: func(context.Context, string) ([]byte, error) { fetches++; return set, nil }, Now: func() time.Time { return now }}
	cfg := JWT{Issuer: "https://issuer.example.com", Audience: "batondeck", JWKSURL: "https://issuer.example.com/jwks"}
	rs := map[string]string{"alg": "RS256", "kid": "k1"}
	good := map[string]any{"iss": cfg.Issuer, "aud": []string{"x", cfg.Audience}, "exp": now.Add(time.Minute).Unix()}
	if reason, ok := v.Verify(context.Background(), "Bearer "+sign(t, key, rs, good), cfg, "I-1"); !ok {
		t.Fatalf("a valid token: %s (positive control)", reason)
	}
	with := func(k string, val any) map[string]any {
		c := map[string]any{}
		for a, b := range good {
			c[a] = b
		}
		if val == nil {
			delete(c, k)
		} else {
			c[k] = val
		}
		return c
	}
	tampered := sign(t, key, rs, good)
	tampered = tampered[:len(tampered)-4] + "AAAA"
	cases := map[string]struct {
		auth string
		want string
	}{
		"no bearer":        {"", ReasonMissingBearer},
		"another key":      {"Bearer " + sign(t, other, rs, good), "verify_failed: signature"},
		"a tampered sig":   {"Bearer " + tampered, "verify_failed"},
		"alg HS256":        {"Bearer " + sign(t, key, map[string]string{"alg": "HS256", "kid": "k1"}, good), "verify_failed: alg"},
		"the wrong issuer": {"Bearer " + sign(t, key, rs, with("iss", "https://evil.example.com")), "verify_failed: issuer"},
		"the wrong aud":    {"Bearer " + sign(t, key, rs, with("aud", "other")), "verify_failed: audience"},
		"expired":          {"Bearer " + sign(t, key, rs, with("exp", now.Add(-time.Second).Unix())), "verify_failed: expired"},
		"no exp":           {"Bearer " + sign(t, key, rs, with("exp", nil)), "verify_failed: expired"},
		"not yet valid":    {"Bearer " + sign(t, key, rs, with("nbf", now.Add(time.Minute).Unix())), "verify_failed: not yet valid"},
	}
	for name, c := range cases {
		if reason, ok := v.Verify(context.Background(), c.auth, cfg, "I-1"); ok || !strings.HasPrefix(reason, c.want) {
			t.Errorf("%s: %q, %v; want a refusal starting %q", name, reason, ok, c.want)
		}
	}
	if fetches != 1 {
		t.Errorf("%d JWKS fetches, want 1 (the set is cached)", fetches)
	}
	plain := cfg
	plain.JWKSURL = "http://issuer.example.com/jwks"
	if reason, _ := v.Verify(context.Background(), "Bearer a.b.c", plain, "I-1"); reason != ReasonJWKSNotHTTPS {
		t.Errorf("an http jwksUrl: %q", reason)
	}
	failing := &Verifier{Fetch: func(context.Context, string) ([]byte, error) { return nil, errors.New("boom") }, Now: v.Now}
	if reason, _ := failing.Verify(context.Background(), "Bearer "+sign(t, key, rs, good), cfg, "I-1"); reason != ReasonJWKSFetch {
		t.Errorf("a failed fetch: %q", reason)
	}
}

// SafeFetch refuses every address class the JWKS fetch must never reach, before it dials; the
// resolver is answered so the test opens no connection and makes no DNS query.
func TestSafeFetchRefusesForbiddenAddresses(t *testing.T) {
	defer func(r func(context.Context, string) ([]netip.Addr, error)) { resolver = r }(resolver)
	for _, a := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "0.0.0.0", "::1", "fc00::1", "fe80::1", "::ffff:127.0.0.1", "::ffff:169.254.169.254"} {
		addr := netip.MustParseAddr(a)
		if !ForbiddenAddress(addr) {
			t.Errorf("%s is not forbidden", a)
		}
		resolver = func(context.Context, string) ([]netip.Addr, error) { return []netip.Addr{addr}, nil }
		if _, err := SafeFetch(context.Background(), "https://keys.example.com/jwks"); err == nil || !strings.Contains(err.Error(), "forbidden address") {
			t.Errorf("a host resolving to %s: %v", a, err)
		}
	}
	if ForbiddenAddress(netip.MustParseAddr("142.250.70.78")) {
		t.Error("a public address is forbidden (positive control)")
	}
	for _, u := range []string{"http://keys.example.com/jwks", "https://localhost/jwks", "https://metadata.google.internal/x"} {
		if _, err := SafeFetch(context.Background(), u); err == nil {
			t.Errorf("%s was fetched", u)
		}
	}
	if _, err := SafeFetch(context.Background(), "https://[::ffff:7f00:1]/jwks"); err == nil {
		t.Error("a hex v4-mapped loopback literal was fetched")
	}
}

// P-231: a failed JWKS fetch, or a set that is unusable, is remembered for jwksRefetchAfter: an
// unknown-kid bearer within it answers jwks_fetch_failed with no fetch and no outbound line; one
// after it fetches again; a good set then replaces the failure and serves a known kid unfetched.
func TestJWTNegativeCache(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	set, _ := json.Marshal(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1", "n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes())}}})
	cfg := JWT{Issuer: "https://issuer.example.com", Audience: "batondeck", JWKSURL: "https://issuer.example.com/jwks"}
	rs := map[string]string{"alg": "RS256", "kid": "k1"}
	good := map[string]any{"iss": cfg.Issuer, "aud": cfg.Audience, "exp": now.Add(time.Hour).Unix()}
	junk := "Bearer " + sign(t, key, map[string]string{"alg": "RS256", "kid": "junk"}, good)
	for _, c := range []struct {
		name  string
		serve func() ([]byte, error)
	}{
		{"a failed fetch", func() ([]byte, error) { return nil, errors.New("HTTP 500") }},
		{"an unusable set", func() ([]byte, error) { return []byte(`{"keys":"nope"}`), nil }},
	} {
		t.Run(c.name, func(t *testing.T) {
			clock := now
			fetches, lines := 0, 0
			healthy := false
			v := &Verifier{
				Fetch: func(context.Context, string) ([]byte, error) {
					fetches++
					if healthy {
						return set, nil
					}
					return c.serve()
				},
				Logf: func(format string, _ ...any) {
					if strings.HasPrefix(format, "ingest: outbound JWKS fetch") {
						lines++
					}
				},
				Now: func() time.Time { return clock },
			}
			for i := range 2 {
				if reason, ok := v.Verify(context.Background(), junk, cfg, "I-1"); ok || reason != ReasonJWKSFetch {
					t.Fatalf("junk bearer %d: %q, %v", i+1, reason, ok)
				}
			}
			if fetches != 1 || lines != 1 {
				t.Fatalf("two bearers within 30 s: %d fetches, %d outbound lines; want 1 and 1", fetches, lines)
			}
			clock = clock.Add(31 * time.Second)
			if reason, _ := v.Verify(context.Background(), junk, cfg, "I-1"); reason != ReasonJWKSFetch || fetches != 2 {
				t.Fatalf("a bearer at +31 s: %q, %d fetches; want the 2nd fetch", reason, fetches)
			}
			// Still failed at +31 s; the next good fetch, past the window, replaces the failure.
			clock = clock.Add(31 * time.Second)
			healthy = true
			if reason, ok := v.Verify(context.Background(), "Bearer "+sign(t, key, rs, good), cfg, "I-1"); !ok || fetches != 3 {
				t.Fatalf("a valid bearer once the set is good: %q, %v, %d fetches", reason, ok, fetches)
			}
			if reason, ok := v.Verify(context.Background(), "Bearer "+sign(t, key, rs, good), cfg, "I-1"); !ok || fetches != 3 {
				t.Fatalf("a known kid from the good set: %q, %v, %d fetches; want no fetch", reason, ok, fetches)
			}
			// The good set's unknown-kid rule is unchanged: within 30 s of the fetch, no refetch.
			if reason, _ := v.Verify(context.Background(), junk, cfg, "I-1"); !strings.HasPrefix(reason, ReasonVerifyFailed) || fetches != 3 {
				t.Fatalf("an unknown kid against the fresh good set: %q, %d fetches", reason, fetches)
			}
		})
	}
}
