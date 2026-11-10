package auth

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// JWT bearer verification against an integration's JWKS (conductor auth/jwt.ts): RS256 only, the
// issuer and audience the integration names (and the subject and verified email it pins, if any),
// the key set fetched from its jwksUrl. That fetch is the
// one outbound network call ingest makes, and it is logged as such (plan §Clarifications 8). The
// URL is operator input fetched server-side, so the fetch holds conductor's SSRF posture: https
// only; the host resolved once and refused when any address is private, loopback, link-local,
// unspecified, reserved (carrier-grade NAT among them) or the metadata host; the connection pinned to the vetted address (no second
// resolution to rebind); no redirects; 64 KiB and 5 s.

// JWT is an integration's jwt block. Subject and Email, when set, pin the token's sub and its
// (verified) email: on a shared issuer anyone with an account can mint a token for an audience they
// choose, so issuer and audience alone do not say who sent it.
type JWT struct {
	Issuer, Audience, JWKSURL string
	Subject, Email            string
}

// The refusal reasons a JWT verification returns.
const (
	ReasonMissingBearer  = "missing_bearer"
	ReasonJWKSURLInvalid = "jwks_url_invalid"
	ReasonJWKSNotHTTPS   = "jwks_url_not_https"
	ReasonJWKSFetch      = "jwks_fetch_failed"
	ReasonVerifyFailed   = "verify_failed"
)

const (
	jwksMaxBytes = 64 << 10
	jwksTimeout  = 5 * time.Second
	// jwksTTL is how long a fetched key set serves; jwksRefetchAfter how soon an unknown kid may
	// fetch it again.
	jwksTTL          = 10 * time.Minute
	jwksRefetchAfter = 30 * time.Second
	minRSABits       = 2048
)

// Fetch gets a JWKS document. Verifier's default is SafeFetch; a test stubs it.
type Fetch func(ctx context.Context, rawURL string) ([]byte, error)

// Verifier verifies JWT bearers, caching each URL's key set. Logf receives the outbound-call line
// (nil drops it); Now is the clock (nil is time.Now).
type Verifier struct {
	Fetch Fetch
	Logf  func(format string, args ...any)
	Now   func() time.Time

	mu     sync.Mutex
	cache  map[string]keySet
	failed map[string]time.Time // a URL's last failed fetch or unusable set (P-231)
}

type keySet struct {
	keys    map[string]*rsa.PublicKey // by kid; "" for a key with none
	fetched time.Time
}

var bearer = regexp.MustCompile(`(?i)^Bearer\s+(\S+)$`)

func (v *Verifier) now() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func (v *Verifier) logf(format string, args ...any) {
	if v.Logf != nil {
		v.Logf(format, args...)
	}
}

// Verify checks authorization's bearer against cfg for integrationID (named in the outbound log
// line): ok, or the reason it refused.
func (v *Verifier) Verify(ctx context.Context, authorization string, cfg JWT, integrationID string) (reason string, ok bool) {
	m := bearer.FindStringSubmatch(authorization)
	if m == nil {
		return ReasonMissingBearer, false
	}
	u, err := url.Parse(cfg.JWKSURL)
	if err != nil || u.Host == "" {
		return ReasonJWKSURLInvalid, false
	}
	if u.Scheme != "https" {
		return ReasonJWKSNotHTTPS, false
	}
	parts := strings.Split(m[1], ".")
	if len(parts) != 3 {
		return ReasonVerifyFailed + ": malformed token", false
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := decodeSegment(parts[0], &header); err != nil {
		return ReasonVerifyFailed + ": malformed header", false
	}
	if header.Alg != "RS256" {
		return ReasonVerifyFailed + ": alg " + quoted(header.Alg) + " is not RS256", false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return ReasonVerifyFailed + ": malformed signature", false
	}
	key, reason := v.key(ctx, cfg.JWKSURL, header.Kid, integrationID)
	if key == nil {
		return reason, false
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig) != nil {
		return ReasonVerifyFailed + ": signature", false
	}
	var claims struct {
		Iss           string          `json:"iss"`
		Aud           json.RawMessage `json:"aud"`
		Exp           *float64        `json:"exp"`
		Nbf           *float64        `json:"nbf"`
		Sub           string          `json:"sub"`
		Email         string          `json:"email"`
		EmailVerified any             `json:"email_verified"`
	}
	if err := decodeSegment(parts[1], &claims); err != nil {
		return ReasonVerifyFailed + ": malformed claims", false
	}
	now := float64(v.now().Unix())
	switch {
	case claims.Iss != cfg.Issuer:
		return ReasonVerifyFailed + ": issuer", false
	case !audienceHas(claims.Aud, cfg.Audience):
		return ReasonVerifyFailed + ": audience", false
	case claims.Exp == nil || now >= *claims.Exp:
		return ReasonVerifyFailed + ": expired", false
	case claims.Nbf != nil && now < *claims.Nbf:
		return ReasonVerifyFailed + ": not yet valid", false
	case cfg.Subject != "" && claims.Sub != cfg.Subject:
		return ReasonVerifyFailed + ": subject", false
	case cfg.Email != "" && (claims.Email != cfg.Email || !verifiedEmail(claims.EmailVerified)):
		return ReasonVerifyFailed + ": email", false
	}
	return "", true
}

// verifiedEmail reads an email_verified claim: true, or the string "true" some issuers send.
func verifiedEmail(v any) bool {
	return v == true || v == "true"
}

// headerTextMax is the most runes of a token header's alg or kid a refusal reason names.
const headerTextMax = 32

// quoted is s, a value from an unverified token's header, as a refusal reason may carry it: cut to
// headerTextMax runes and Go-quoted, so the delivery log does not store the sender's text at length
// and a line break in it cannot start a line of its own in the server log.
func quoted(s string) string {
	if r := []rune(s); len(r) > headerTextMax {
		return strconv.Quote(string(r[:headerTextMax])) + "…"
	}
	return strconv.Quote(s)
}

func decodeSegment(seg string, into any) error {
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, into)
}

func audienceHas(raw json.RawMessage, want string) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == want
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		for _, a := range many {
			if a == want {
				return true
			}
		}
	}
	return false
}

// key is kid's key from rawURL's set: cached for jwksTTL, fetched again for an unknown kid once the
// cached set is jwksRefetchAfter old. A fetch that fails, or a set that is unusable, is remembered
// for jwksRefetchAfter too: until then the URL answers jwks_fetch_failed with no fetch (P-231), and
// a good fetch after it clears it.
func (v *Verifier) key(ctx context.Context, rawURL, kid, integrationID string) (*rsa.PublicKey, string) {
	now := v.now()
	v.mu.Lock()
	ks, cached := v.cache[rawURL]
	v.mu.Unlock()
	fresh := cached && now.Sub(ks.fetched) < jwksTTL
	if k := pickKey(ks.keys, kid); fresh && k != nil {
		return k, ""
	}
	if fresh && now.Sub(ks.fetched) < jwksRefetchAfter {
		return nil, ReasonVerifyFailed + ": no key " + quoted(kid)
	}
	v.mu.Lock()
	at, failed := v.failed[rawURL]
	v.mu.Unlock()
	if failed && now.Sub(at) < jwksRefetchAfter {
		return nil, ReasonJWKSFetch
	}
	fetch := v.Fetch
	if fetch == nil {
		fetch = SafeFetch
	}
	v.logf("ingest: outbound JWKS fetch GET %s for integration %s", rawURL, integrationID)
	body, err := fetch(ctx, rawURL)
	if err != nil {
		v.logf("ingest: JWKS fetch %s for integration %s failed: %v", rawURL, integrationID, err)
		v.fetchFailed(rawURL, now)
		return nil, ReasonJWKSFetch
	}
	keys, err := parseJWKS(body)
	if err != nil {
		v.logf("ingest: JWKS %s for integration %s is unusable: %v", rawURL, integrationID, err)
		v.fetchFailed(rawURL, now)
		return nil, ReasonJWKSFetch
	}
	v.mu.Lock()
	if v.cache == nil {
		v.cache = map[string]keySet{}
	}
	v.cache[rawURL] = keySet{keys: keys, fetched: now}
	delete(v.failed, rawURL)
	v.mu.Unlock()
	if k := pickKey(keys, kid); k != nil {
		return k, ""
	}
	return nil, ReasonVerifyFailed + ": no key " + quoted(kid)
}

// fetchFailed remembers rawURL's failed fetch at now.
func (v *Verifier) fetchFailed(rawURL string, now time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.failed == nil {
		v.failed = map[string]time.Time{}
	}
	v.failed[rawURL] = now
}

// pickKey is kid's key; a token with no kid takes the set's only key.
func pickKey(keys map[string]*rsa.PublicKey, kid string) *rsa.PublicKey {
	if k, ok := keys[kid]; ok {
		return k
	}
	if kid == "" && len(keys) == 1 {
		for _, k := range keys {
			return k
		}
	}
	return nil
}

// parseJWKS reads the RSA signing keys of a JWK set, each at least minRSABits.
func parseJWKS(body []byte) (map[string]*rsa.PublicKey, error) {
	var doc struct {
		Keys []struct {
			Kty, Kid, Use, Alg, N, E string
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("not a JWK set: %w", err)
	}
	out := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") || (k.Alg != "" && k.Alg != "RS256") {
			continue
		}
		n, errN := base64.RawURLEncoding.DecodeString(k.N)
		e, errE := base64.RawURLEncoding.DecodeString(k.E)
		if errN != nil || errE != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
		if pub.N.BitLen() < minRSABits || pub.E < 3 {
			continue
		}
		out[k.Kid] = pub
	}
	if len(out) == 0 {
		return nil, errors.New("no RS256 key of at least 2048 bits")
	}
	return out, nil
}

// reservedPrefixes are the ranges ForbiddenAddress refuses beyond the net/netip classes: 0.0.0.0/8,
// carrier-grade NAT (a tailnet's and some clouds' internal services), IETF protocol assignments,
// benchmarking, class E and broadcast, the v6 prefixes that embed a v4 address and so can reach
// any of the above (NAT64, local-use NAT64, 6to4, Teredo, the deprecated v4-compatible ::/96), and
// the deprecated site-local fec0::/10.
var reservedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("2001::/32"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("fec0::/10"),
}

// ForbiddenAddress reports whether the fetch must never reach a: private, loopback, link-local,
// unspecified, unique-local, multicast, a reservedPrefixes range, or one of those inside a
// v4-mapped v6 address.
func ForbiddenAddress(a netip.Addr) bool {
	a = a.Unmap()
	if !a.IsValid() || a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsUnspecified() || a.IsMulticast() || a.IsInterfaceLocalMulticast() {
		return true
	}
	for _, p := range reservedPrefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

var forbiddenHosts = map[string]bool{"metadata.google.internal": true, "metadata.google.internal.": true, "localhost": true}

// CheckJWKSURL is why raw can never be fetched as an integration's jwksUrl, "" when it can be
// tried: not an absolute https URL with a host, credentials in it, a forbidden host name, or an IP
// literal in a forbidden range. It resolves nothing (SafeFetch vets every address a name resolves
// to on each fetch), so create_integration and update_integration refuse on save what every
// delivery would otherwise be refused for.
func CheckJWKSURL(raw string) string {
	u, err := url.Parse(raw)
	switch {
	case err != nil || u.Scheme == "" || u.Hostname() == "":
		return "must be an absolute https URL with a host"
	case u.Scheme != "https":
		return "must be https"
	case u.User != nil:
		return "must not carry credentials"
	case forbiddenHosts[strings.ToLower(u.Hostname())]:
		return "names a host the fetch refuses"
	}
	if a, err := netip.ParseAddr(u.Hostname()); err == nil && ForbiddenAddress(a) {
		return "is a private, loopback, link-local or reserved address, which the fetch refuses"
	}
	return ""
}

// resolver is the DNS lookup SafeFetch vets; a variable only so a test can answer it.
var resolver = func(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// SafeFetch GETs an https JWKS URL under the SSRF posture: the host resolved once and every
// address vetted, the dial pinned to the vetted set (TLS still verifies the host name), redirects
// refused, the body capped at 64 KiB, the whole request bounded by 5 s.
func SafeFetch(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return nil, errors.New("jwks url must be https")
	}
	host := strings.ToLower(u.Hostname())
	if forbiddenHosts[host] {
		return nil, errors.New("jwks host is forbidden")
	}
	ctx, cancel := context.WithTimeout(ctx, jwksTimeout)
	defer cancel()
	var addrs []netip.Addr
	if a, err := netip.ParseAddr(host); err == nil {
		addrs = []netip.Addr{a}
	} else if addrs, err = resolver(ctx, host); err != nil || len(addrs) == 0 {
		return nil, fmt.Errorf("jwks host does not resolve: %v", err)
	}
	for _, a := range addrs {
		if ForbiddenAddress(a) {
			return nil, fmt.Errorf("jwks host resolves to a forbidden address %s", a)
		}
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	dialer := &net.Dialer{Timeout: jwksTimeout}
	tr := &http.Transport{
		// Pinned: the dial goes to the vetted addresses, never to a second resolution of the name.
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var last error
			for _, a := range addrs {
				c, err := dialer.DialContext(ctx, network, net.JoinHostPort(a.String(), port))
				if err == nil {
					return c, nil
				}
				last = err
			}
			return nil, last
		},
		TLSClientConfig:       &tls.Config{ServerName: u.Hostname(), MinVersion: tls.VersionTLS12},
		ResponseHeaderTimeout: jwksTimeout,
		Proxy:                 nil,
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: jwksTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("jwks redirects are refused") }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks response status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, jwksMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > jwksMaxBytes {
		return nil, fmt.Errorf("jwks response exceeds %d bytes", jwksMaxBytes)
	}
	return b, nil
}
