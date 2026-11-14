// Package clientip finds the address a request came from for the Audit log:
// the client Traefik saw when the TCP peer is the install's Traefik, and the
// TCP peer itself otherwise (ADR-0037).
package clientip

import (
	"context"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// lookupInterval is the least time between two lookups of Traefik's address,
// so requests from other Containers on relist-network cannot keep the panel
// asking Docker.
const lookupInterval = 10 * time.Second

// maxProxyAge is how long a found address is trusted before it is checked
// again.
const maxProxyAge = 5 * time.Minute

// lookupTimeout bounds one lookup, which runs in the request that needed it.
const lookupTimeout = 3 * time.Second

// Resolver trusts X-Forwarded-For only from Traefik's own address.
type Resolver struct {
	// proxyIP is Traefik's address on relist-network; "" when it has none.
	proxyIP func(ctx context.Context) (string, error)
	// trusted is Traefik's forwardedHeaders.trustedIPs (IPs and CIDRs).
	trusted func() []string
	now     func() time.Time

	mu         sync.Mutex
	proxy      netip.Addr // invalid until a lookup finds one
	lastLookup time.Time
	lastErr    string // logged once until it changes
}

func New(proxyIP func(ctx context.Context) (string, error), trusted func() []string) *Resolver {
	return &Resolver{proxyIP: proxyIP, trusted: trusted, now: time.Now}
}

type ctxKey struct{}

// Middleware stores the client address for From.
func (r *Resolver) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ip := r.resolve(req)
		next.ServeHTTP(w, req.WithContext(context.WithValue(req.Context(), ctxKey{}, ip)))
	})
}

// From is the client address Middleware found, or the TCP peer when the
// request did not pass through it.
func From(req *http.Request) string {
	if ip, ok := req.Context().Value(ctxKey{}).(string); ok {
		return ip
	}
	return peer(req)
}

func peer(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		return req.RemoteAddr
	}
	return host
}

func (r *Resolver) resolve(req *http.Request) string {
	p := peer(req)
	addr, err := netip.ParseAddr(p)
	if err != nil || !r.isProxy(req.Context(), addr.Unmap().WithZone("")) {
		return p
	}
	if client, ok := r.fromHeaders(req.Header); ok {
		return client
	}
	return p
}

// isProxy reports whether addr is Traefik's address. Traefik gets a new one
// whenever it is recreated, and Docker may hand the old one to another
// Container, so a miss looks the address up again, at most once per
// lookupInterval, and a hit on an address older than maxProxyAge does too.
func (r *Resolver) isProxy(ctx context.Context, addr netip.Addr) bool {
	r.mu.Lock()
	now := r.now()
	age := now.Sub(r.lastLookup)
	if r.proxy.IsValid() && r.proxy == addr && age < maxProxyAge {
		r.mu.Unlock()
		return true
	}
	if !r.lastLookup.IsZero() && age < lookupInterval {
		r.mu.Unlock()
		return false
	}
	r.lastLookup = now
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lookupTimeout)
	defer cancel()
	found := netip.Addr{}
	ip, err := r.proxyIP(ctx)
	errText := ""
	if err != nil {
		errText = err.Error()
	} else if a, perr := netip.ParseAddr(ip); perr == nil {
		found = a.Unmap()
	}

	r.mu.Lock()
	r.proxy = found
	logIt := errText != "" && errText != r.lastErr
	r.lastErr = errText
	r.mu.Unlock()
	if logIt {
		log.Printf("clientip: find Traefik's address, so the Audit log records the TCP peer: %v", err)
	}
	return found.IsValid() && found == addr
}

// fromHeaders reads the client from headers Traefik wrote. Traefik drops
// X-Forwarded-For from an untrusted peer and appends its peer to it, so the
// rightmost entry that is not one of Traefik's trusted proxies is the client.
// With notAppendXForwardedFor the header may be empty; Traefik still sets
// X-Real-Ip to its peer.
func (r *Resolver) fromHeaders(h http.Header) (string, bool) {
	var entries []string
	for _, v := range h.Values("X-Forwarded-For") {
		for _, e := range strings.Split(v, ",") {
			if e = strings.TrimSpace(e); e != "" {
				entries = append(entries, e)
			}
		}
	}
	if len(entries) == 0 {
		a, err := netip.ParseAddr(strings.TrimSpace(h.Get("X-Real-Ip")))
		if err != nil {
			return "", false
		}
		return a.Unmap().WithZone("").String(), true
	}

	var trusted []netip.Prefix
	if len(entries) > 1 {
		trusted = parsePrefixes(r.trusted())
	}
	last := "" // the rightmost trusted proxy walked past
	for i := len(entries) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(entries[i])
		if err != nil {
			// Written by the client or a proxy we cannot read; the last
			// address we can vouch for is the proxy right of it.
			return last, last != ""
		}
		a = a.Unmap().WithZone("")
		if i == 0 || !contains(trusted, a) {
			return a.String(), true
		}
		last = a.String()
	}
	return last, last != ""
}

func parsePrefixes(list []string) []netip.Prefix {
	var out []netip.Prefix
	for _, s := range list {
		s = strings.TrimSpace(s)
		if p, err := netip.ParsePrefix(s); err == nil {
			out = append(out, p.Masked())
		} else if a, err := netip.ParseAddr(s); err == nil {
			a = a.Unmap()
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	return out
}

func contains(prefixes []netip.Prefix, a netip.Addr) bool {
	for _, p := range prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
