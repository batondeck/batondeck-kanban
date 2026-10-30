package auth

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

// TestClientAddr (P-291): the peer, unless it is a trusted proxy; then the rightmost untrusted
// X-Forwarded-For hop over every header line, the leftmost when all are trusted, and the last
// trusted address when a hop does not parse. No proxies, or an untrusted peer: the header is not read.
func TestClientAddr(t *testing.T) {
	proxies := TrustedProxies{netip.MustParsePrefix("127.0.0.1/32"), netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("fd00::/8")}
	for _, c := range []struct {
		name    string
		proxies TrustedProxies
		peer    string
		xff     []string
		want    string
	}{
		{"no proxies: the header is never read", nil, "127.0.0.1:5000", []string{"203.0.113.7"}, "127.0.0.1"},
		{"untrusted peer forging the header", proxies, "198.51.100.1:5000", []string{"203.0.113.7"}, "198.51.100.1"},
		{"trusted peer, one hop", proxies, "127.0.0.1:5000", []string{"203.0.113.7"}, "203.0.113.7"},
		{"trusted peer, no header", proxies, "127.0.0.1:5000", nil, "127.0.0.1"},
		{"a forged hop left of the real client", proxies, "127.0.0.1:5000", []string{"192.0.2.66, 203.0.113.7"}, "203.0.113.7"},
		{"two proxies, two header lines", proxies, "127.0.0.1:5000", []string{"192.0.2.66, 203.0.113.7", "10.1.2.3"}, "203.0.113.7"},
		{"every hop trusted: the leftmost", proxies, "127.0.0.1:5000", []string{"10.0.0.9, 10.0.0.8"}, "10.0.0.9"},
		{"an unparsable hop ends the walk", proxies, "127.0.0.1:5000", []string{"unknown, 10.0.0.8"}, "10.0.0.8"},
		{"an unparsable hop right of the client", proxies, "127.0.0.1:5000", []string{"203.0.113.7, garbage"}, "127.0.0.1"},
		{"a hop with a port, bracketed IPv6", proxies, "127.0.0.1:5000", []string{"[2001:db8::7]:4711"}, "2001:db8::7"},
		{"a bracketed IPv6 hop", proxies, "127.0.0.1:5000", []string{"[2001:db8::8]"}, "2001:db8::8"},
		{"an IPv4-mapped peer and hop", proxies, "[::ffff:10.0.0.1]:5000", []string{"::ffff:203.0.113.9"}, "203.0.113.9"},
		{"an IPv6 proxy", proxies, "[fd00::1]:5000", []string{"2001:db8::9"}, "2001:db8::9"},
		{"a unix-socket peer is not an address", proxies, "@", []string{"203.0.113.7"}, "@"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/api/login", nil)
			r.RemoteAddr = c.peer
			for _, v := range c.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := c.proxies.ClientAddr(r); got != c.want {
				t.Fatalf("ClientAddr = %q, want %q", got, c.want)
			}
		})
	}
}
