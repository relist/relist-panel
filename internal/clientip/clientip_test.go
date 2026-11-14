package clientip

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const traefikIP = "172.20.0.2"

type fakeProxy struct {
	ip    atomic.Value // string
	calls atomic.Int32
	err   error
}

func newFakeProxy(ip string) *fakeProxy {
	p := &fakeProxy{}
	p.ip.Store(ip)
	return p
}

func (p *fakeProxy) lookup(context.Context) (string, error) {
	p.calls.Add(1)
	if p.err != nil {
		return "", p.err
	}
	return p.ip.Load().(string), nil
}

func newTestResolver(p *fakeProxy, trusted []string, now func() time.Time) *Resolver {
	r := New(p.lookup, func() []string { return trusted })
	if now != nil {
		r.now = now
	}
	return r
}

// resolve runs req through the middleware and returns what From saw.
func resolve(t *testing.T, r *Resolver, req *http.Request) string {
	t.Helper()
	var got string
	r.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) {
		got = From(req)
	})).ServeHTTP(httptest.NewRecorder(), req)
	return got
}

func request(peer string, headers map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = peer
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func TestFromTraefikTakesTheAddressTraefikAppended(t *testing.T) {
	r := newTestResolver(newFakeProxy(traefikIP), nil, nil)
	got := resolve(t, r, request(traefikIP+":41234", map[string]string{"X-Forwarded-For": "203.0.113.7"}))
	if got != "203.0.113.7" {
		t.Fatalf("client = %q, want 203.0.113.7", got)
	}
}

func TestFromTraefikSkipsItsTrustedProxies(t *testing.T) {
	// Traefik trusts Cloudflare, so it keeps Cloudflare's header and appends
	// Cloudflare's address; an address the client wrote is left of that.
	r := newTestResolver(newFakeProxy(traefikIP), []string{"173.245.48.0/20", "198.51.100.9"}, nil)
	got := resolve(t, r, request(traefikIP+":41234", map[string]string{
		"X-Forwarded-For": "6.6.6.6, 203.0.113.7, 198.51.100.9, 173.245.48.10",
	}))
	if got != "203.0.113.7" {
		t.Fatalf("client = %q, want 203.0.113.7", got)
	}
}

func TestFromTraefikAllTrustedTakesTheLeftmost(t *testing.T) {
	r := newTestResolver(newFakeProxy(traefikIP), []string{"10.0.0.0/8"}, nil)
	got := resolve(t, r, request(traefikIP+":1", map[string]string{"X-Forwarded-For": "10.0.0.5, 10.0.0.6"}))
	if got != "10.0.0.5" {
		t.Fatalf("client = %q, want 10.0.0.5", got)
	}
}

func TestFromTraefikStopsAtAnUnparsableEntry(t *testing.T) {
	r := newTestResolver(newFakeProxy(traefikIP), []string{"173.245.48.0/20"}, nil)
	got := resolve(t, r, request(traefikIP+":1", map[string]string{"X-Forwarded-For": "<script>, 173.245.48.10"}))
	if got != "173.245.48.10" {
		t.Fatalf("client = %q, want the trusted proxy 173.245.48.10", got)
	}
}

func TestFromTraefikNormalisesIPv6(t *testing.T) {
	r := newTestResolver(newFakeProxy(traefikIP), nil, nil)
	got := resolve(t, r, request(traefikIP+":1", map[string]string{"X-Forwarded-For": "::ffff:203.0.113.7"}))
	if got != "203.0.113.7" {
		t.Fatalf("client = %q, want 203.0.113.7", got)
	}
	got = resolve(t, r, request(traefikIP+":1", map[string]string{"X-Forwarded-For": " 2001:DB8::1 "}))
	if got != "2001:db8::1" {
		t.Fatalf("client = %q, want 2001:db8::1", got)
	}
}

func TestFromTraefikWithoutXFFFallsBackToXRealIP(t *testing.T) {
	// notAppendXForwardedFor: Traefik still sets X-Real-Ip to its peer.
	r := newTestResolver(newFakeProxy(traefikIP), nil, nil)
	got := resolve(t, r, request(traefikIP+":1", map[string]string{"X-Real-Ip": "203.0.113.7"}))
	if got != "203.0.113.7" {
		t.Fatalf("client = %q, want 203.0.113.7", got)
	}
	got = resolve(t, r, request(traefikIP+":1", nil))
	if got != traefikIP {
		t.Fatalf("client = %q, want the peer %s", got, traefikIP)
	}
}

func TestForgedHeaderFromAnotherContainerIsIgnored(t *testing.T) {
	r := newTestResolver(newFakeProxy(traefikIP), nil, nil)
	got := resolve(t, r, request("172.20.0.9:5555", map[string]string{
		"X-Forwarded-For": "203.0.113.7", "X-Real-Ip": "203.0.113.7",
	}))
	if got != "172.20.0.9" {
		t.Fatalf("client = %q, want the peer 172.20.0.9", got)
	}
}

func TestNoTraefikRecordsThePeer(t *testing.T) {
	p := newFakeProxy("")
	p.err = errors.New("no traefik")
	r := newTestResolver(p, nil, nil)
	got := resolve(t, r, request("172.17.0.1:5555", map[string]string{"X-Forwarded-For": "203.0.113.7"}))
	if got != "172.17.0.1" {
		t.Fatalf("client = %q, want the peer", got)
	}
	// An empty IP (Traefik not on relist-network) never matches a peer.
	r = newTestResolver(newFakeProxy(""), nil, nil)
	got = resolve(t, r, request("1.2.3.4:5", map[string]string{"X-Forwarded-For": "203.0.113.7"}))
	if got != "1.2.3.4" {
		t.Fatalf("client = %q, want the peer", got)
	}
}

func TestRecreatedTraefikIsFoundAgainButLookupsAreRateLimited(t *testing.T) {
	now := time.Unix(1_000_000, 0)
	p := newFakeProxy(traefikIP)
	r := newTestResolver(p, nil, func() time.Time { return now })
	xff := map[string]string{"X-Forwarded-For": "203.0.113.7"}

	if got := resolve(t, r, request(traefikIP+":1", xff)); got != "203.0.113.7" {
		t.Fatalf("first: client = %q", got)
	}
	if p.calls.Load() != 1 {
		t.Fatalf("lookups = %d, want 1", p.calls.Load())
	}
	// The cached address matches: no lookup.
	resolve(t, r, request(traefikIP+":1", xff))
	if p.calls.Load() != 1 {
		t.Fatalf("lookups after a hit = %d, want 1", p.calls.Load())
	}

	// Traefik is recreated with a new address. Within the interval the new
	// peer is not trusted and a flood of misses does not reach Docker.
	p.ip.Store("172.20.0.3")
	for range 50 {
		if got := resolve(t, r, request("172.20.0.3:1", xff)); got != "172.20.0.3" {
			t.Fatalf("within interval: client = %q, want the peer", got)
		}
	}
	if p.calls.Load() != 1 {
		t.Fatalf("lookups within interval = %d, want 1", p.calls.Load())
	}

	now = now.Add(lookupInterval)
	if got := resolve(t, r, request("172.20.0.3:1", xff)); got != "203.0.113.7" {
		t.Fatalf("after interval: client = %q, want 203.0.113.7", got)
	}
	// The old address is no longer trusted.
	if got := resolve(t, r, request(traefikIP+":1", xff)); got != traefikIP {
		t.Fatalf("old Traefik address: client = %q, want the peer", got)
	}
}

func TestACachedAddressIsCheckedAgainAfterAWhile(t *testing.T) {
	// Traefik was recreated elsewhere and Docker gave its old address to
	// another Container before any request came through the new Traefik.
	now := time.Unix(1_000_000, 0)
	p := newFakeProxy(traefikIP)
	r := newTestResolver(p, nil, func() time.Time { return now })
	xff := map[string]string{"X-Forwarded-For": "203.0.113.7"}
	resolve(t, r, request(traefikIP+":1", xff))

	p.ip.Store("172.20.0.3")
	now = now.Add(maxProxyAge - time.Second)
	if got := resolve(t, r, request(traefikIP+":1", xff)); got != "203.0.113.7" {
		t.Fatalf("fresh cache: client = %q", got)
	}
	now = now.Add(time.Second)
	if got := resolve(t, r, request(traefikIP+":1", xff)); got != traefikIP {
		t.Fatalf("stale cache: client = %q, want the peer", got)
	}
	if p.calls.Load() != 2 {
		t.Fatalf("lookups = %d, want 2", p.calls.Load())
	}
}

func TestFromWithoutMiddlewareIsThePeer(t *testing.T) {
	req := request("[2001:db8::5]:443", map[string]string{"X-Forwarded-For": "203.0.113.7"})
	if got := From(req); got != "2001:db8::5" {
		t.Fatalf("From = %q, want 2001:db8::5", got)
	}
	req.RemoteAddr = "pipe"
	if got := From(req); got != "pipe" {
		t.Fatalf("From = %q, want pipe", got)
	}
}
