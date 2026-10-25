package mcp

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/batondeck/batondeck-kanban/internal/auth"
	"github.com/batondeck/batondeck-kanban/internal/domain"
)

// hostCheck is go-sdk's DNS-rebinding protection with the public_url host admitted: a request
// arriving on a loopback address whose Host is neither loopback nor publicURL's host is 403
// FORBIDDEN in the {code, message} shape. A request on any other address passes, as in go-sdk.
func hostCheck(publicURL string) func(http.Handler) http.Handler {
	public := ""
	if u, err := url.Parse(publicURL); err == nil {
		public = u.Hostname()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			local, _ := req.Context().Value(http.LocalAddrContextKey).(net.Addr)
			if local != nil && isLoopback(local.String()) && !isLoopback(req.Host) &&
				(public == "" || !strings.EqualFold(hostname(req.Host), public)) {
				auth.WriteError(w, http.StatusForbidden, domain.CodeForbidden, fmt.Sprintf("invalid Host header %q", req.Host))
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}

// hostname is addr without its port and brackets.
func hostname(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil {
		return h
	}
	return strings.Trim(addr, "[]")
}

// isLoopback is go-sdk's internal/util.IsLoopback: localhost or a loopback IP, with or without a port.
func isLoopback(addr string) bool {
	h := hostname(addr)
	if h == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(h)
	return err == nil && ip.IsLoopback()
}
