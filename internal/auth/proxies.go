package auth

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// TrustedProxies are the reverse proxies in front of the server (the trusted_proxies setting,
// core.ParseTrustedProxies): a request whose peer is one of them is counted against the client
// its X-Forwarded-For names, not against the proxy (P-291).
type TrustedProxies []netip.Prefix

// ClientAddr is the address a request is counted against, by the sign-in limits and by OAuth
// registration: the peer's IP, unless the peer is a trusted proxy. Then it is the rightmost
// X-Forwarded-For hop (every header line, in order) that is not a trusted proxy, or the leftmost
// hop when all are: each proxy appends the address it was reached from, so the hops left of the
// last untrusted one are the client's to forge. A hop that does not parse ends the walk at the last
// trusted address. With no trusted proxies the header is never read.
func (t TrustedProxies) ClientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if len(t) == 0 {
		return host
	}
	peer, err := netip.ParseAddr(host)
	if err != nil || !t.trusted(peer) {
		return host
	}
	client := peer.Unmap().WithZone("").String()
	var hops []string
	for _, line := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(line, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		a, ok := parseHop(hops[i])
		if !ok {
			break
		}
		client = a.String()
		if !t.trusted(a) {
			break
		}
	}
	return client
}

func (t TrustedProxies) trusted(a netip.Addr) bool {
	a = a.Unmap().WithZone("")
	for _, p := range t {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// parseHop reads one X-Forwarded-For entry: an address, bracketed or not, with or without a port.
func parseHop(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	a, err := netip.ParseAddr(s)
	if err != nil {
		var ap netip.AddrPort
		if ap, err = netip.ParseAddrPort(s); err == nil {
			a = ap.Addr()
		} else if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
			a, err = netip.ParseAddr(s[1 : len(s)-1])
		}
	}
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap().WithZone(""), true
}
