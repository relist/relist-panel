package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"relist-panel/internal/clientip"
	"relist-panel/internal/traefikconfig"
)

type stateOnlyTraefik struct {
	traefikconfig.Traefik
	ip string
}

func (t stateOnlyTraefik) State(context.Context) (traefikconfig.State, error) {
	return traefikconfig.State{Running: true, IP: t.ip}, nil
}

func TestClientIPResolverUsesTraefikStateAndTrustedProxies(t *testing.T) {
	dataDir := t.TempDir()
	effective := traefikconfig.PathsFor(dataDir).Effective()
	if err := os.MkdirAll(filepath.Dir(effective), 0o755); err != nil {
		t.Fatal(err)
	}
	yml := "entryPoints:\n  websecure:\n    forwardedHeaders:\n      trustedIPs:\n        - 173.245.48.0/20\n"
	if err := os.WriteFile(effective, []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	r := clientIPResolver(stateOnlyTraefik{ip: "172.20.0.2"}, dataDir)

	var got string
	h := r.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) { got = clientip.From(req) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.20.0.2:40000"
	req.Header.Set("X-Forwarded-For", "6.6.6.6, 203.0.113.7, 173.245.48.10")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got != "203.0.113.7" {
		t.Fatalf("client = %q, want 203.0.113.7", got)
	}
}

func TestClientIPResolverWithoutTraefikRecordsThePeer(t *testing.T) {
	r := clientIPResolver(nil, t.TempDir())
	var got string
	h := r.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, req *http.Request) { got = clientip.From(req) }))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.20.0.2:40000"
	req.Header.Set("X-Forwarded-For", "203.0.113.7")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got != "172.20.0.2" {
		t.Fatalf("client = %q, want the peer", got)
	}
}
