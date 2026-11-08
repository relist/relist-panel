// Package clientip names the address a request came from, for the audit log.
//
// The panel is only reached through Traefik, which has already decided the
// client address: it drops a forwarded header from an untrusted peer and keeps
// it from a trusted one (ADR-0036). So the leftmost X-Forwarded-For entry is
// the client, and the TCP peer is only the fallback.
package clientip

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// From returns the client address of r: the leftmost X-Forwarded-For entry
// when it is an IP address, otherwise the host of r.RemoteAddr.
func From(r *http.Request) string {
	if xff := strings.Join(r.Header.Values("X-Forwarded-For"), ","); xff != "" {
		first, _, _ := strings.Cut(xff, ",")
		if ip, ok := parse(strings.TrimSpace(first)); ok {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// parse accepts an IP address with or without a port.
func parse(s string) (string, bool) {
	if a, err := netip.ParseAddr(s); err == nil {
		return a.String(), true
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().String(), true
	}
	return "", false
}
