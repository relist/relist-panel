package dockerexec_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"relist-panel/internal/dockerexec"
	"relist-panel/internal/registry"
)

type createBody struct {
	Image      string            `json:"Image"`
	Cmd        []string          `json:"Cmd"`
	Labels     map[string]string `json:"Labels"`
	HostConfig struct {
		Binds         []string `json:"Binds"`
		NetworkMode   string   `json:"NetworkMode"`
		RestartPolicy struct {
			Name string `json:"Name"`
		} `json:"RestartPolicy"`
	} `json:"HostConfig"`
}

// fakeRegistryDaemon records calls and the last container create body.
type fakeRegistryDaemon struct {
	mu      sync.Mutex
	calls   []string
	created map[string]createBody
}

func (f *fakeRegistryDaemon) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/containers/create":
		var body createBody
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		if f.created == nil {
			f.created = map[string]createBody{}
		}
		f.created[r.URL.Query().Get("name")] = body
		_, _ = w.Write([]byte(`{"Id":"new"}`))
	case r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusNotFound)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/json"):
		http.Error(w, `{"message":"No such container"}`, http.StatusNotFound)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func newRegistryHost(t *testing.T) (*dockerexec.RegistryHost, *fakeRegistryDaemon) {
	t.Helper()
	f := &fakeRegistryDaemon{}
	return dockerexec.NewRegistryHost(newFakeDaemon(t, http.HandlerFunc(f.handler))), f
}

func spec(tls string) registry.ContainerSpec {
	return registry.ContainerSpec{Image: registry.DefaultImage, Hostname: "registry.example.com", TLS: tls, Hash: "abc", FilesDir: "/var/lib/relist-panel/.registry"}
}

func TestCreateSpecLabelsLetsEncrypt(t *testing.T) {
	h, f := newRegistryHost(t)
	if err := h.Create(context.Background(), spec(registry.TLSLetsEncrypt)); err != nil {
		t.Fatal(err)
	}
	body := f.created["relist-registry"]
	want := map[string]string{
		"traefik.enable":                                                             "true",
		"traefik.docker.network":                                                     "relist-network",
		"traefik.http.routers.relist-registry.rule":                                  "Host(`registry.example.com`)",
		"traefik.http.routers.relist-registry.entrypoints":                           "websecure",
		"traefik.http.routers.relist-registry.service":                               "relist-registry",
		"traefik.http.routers.relist-registry.tls.certresolver":                      "letsencrypt",
		"traefik.http.routers.relist-registry-http.rule":                             "Host(`registry.example.com`)",
		"traefik.http.routers.relist-registry-http.entrypoints":                      "web",
		"traefik.http.routers.relist-registry-http.middlewares":                      "relist-registry-redirect",
		"traefik.http.routers.relist-registry-http.service":                          "relist-registry",
		"traefik.http.middlewares.relist-registry-redirect.redirectscheme.scheme":    "https",
		"traefik.http.middlewares.relist-registry-redirect.redirectscheme.permanent": "true",
		"traefik.http.services.relist-registry.loadbalancer.server.port":             "5000",
		"relist.registry.hash":                                                       "abc",
	}
	for k, v := range want {
		if body.Labels[k] != v {
			t.Errorf("label %s = %q, want %q", k, body.Labels[k], v)
		}
	}
	if _, ok := body.Labels["traefik.http.routers.relist-registry.tls"]; ok {
		t.Errorf("letsencrypt also set tls=true: %v", body.Labels)
	}
	if body.Image != registry.DefaultImage || body.HostConfig.RestartPolicy.Name != "unless-stopped" || body.HostConfig.NetworkMode != "relist-network" {
		t.Fatalf("body = %+v", body)
	}
	calls := f.calls
	del := slices.Index(calls, "DELETE /containers/relist-registry")
	create := slices.Index(calls, "POST /containers/create")
	start := slices.Index(calls, "POST /containers/relist-registry/start")
	if del < 0 || create < 0 || start < 0 || !(del < create && create < start) {
		t.Fatalf("calls = %v, want remove, create, start", calls)
	}
}

func TestCreateSpecLabelsCertificate(t *testing.T) {
	h, f := newRegistryHost(t)
	if err := h.Create(context.Background(), spec(registry.TLSCertificate)); err != nil {
		t.Fatal(err)
	}
	labels := f.created["relist-registry"].Labels
	if labels["traefik.http.routers.relist-registry.tls"] != "true" {
		t.Errorf("tls = %q, want true", labels["traefik.http.routers.relist-registry.tls"])
	}
	if _, ok := labels["traefik.http.routers.relist-registry.tls.certresolver"]; ok {
		t.Errorf("certificate mode set a resolver: %v", labels)
	}
}

func TestCreateMountsFilesDirReadOnly(t *testing.T) {
	h, f := newRegistryHost(t)
	if err := h.Create(context.Background(), spec(registry.TLSLetsEncrypt)); err != nil {
		t.Fatal(err)
	}
	binds := f.created["relist-registry"].HostConfig.Binds
	want := []string{"relist-registry:/var/lib/registry", "/var/lib/relist-panel/.registry:/etc/distribution:ro"}
	if !slices.Equal(binds, want) {
		t.Fatalf("binds = %v, want %v", binds, want)
	}
}

func TestGCCommand(t *testing.T) {
	h, f := newRegistryHost(t)
	if err := h.RunGC(context.Background(), registry.DefaultImage, "/var/lib/relist-panel/.registry"); err != nil {
		t.Fatal(err)
	}
	body := f.created["relist-registry-gc"]
	if !slices.Equal(body.Cmd, []string{"garbage-collect", "--delete-untagged", "/etc/distribution/config.yml"}) {
		t.Fatalf("cmd = %v", body.Cmd)
	}
	if body.HostConfig.RestartPolicy.Name != "" && body.HostConfig.RestartPolicy.Name != "no" {
		t.Fatalf("gc restart policy = %q", body.HostConfig.RestartPolicy.Name)
	}
	if body.HostConfig.NetworkMode != "none" || len(body.HostConfig.Binds) != 2 {
		t.Fatalf("gc host config = %+v", body.HostConfig)
	}
	if !slices.Contains(f.calls, "POST /containers/relist-registry-gc/start") {
		t.Fatalf("calls = %v", f.calls)
	}
}

func TestInspectMissingContainer(t *testing.T) {
	h, _ := newRegistryHost(t)
	st, err := h.Inspect(context.Background())
	if err != nil || st.Exists {
		t.Fatalf("state = %+v, %v", st, err)
	}
	gc, err := h.GCState(context.Background())
	if err != nil || gc.Exists {
		t.Fatalf("gc = %+v, %v", gc, err)
	}
}

func TestInspectReadsImageAndHash(t *testing.T) {
	socket := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"State":{"Running":true},"Config":{"Image":"registry:3@sha256:x","Labels":{"relist.registry.hash":"h1"}}}`))
	}))
	st, err := dockerexec.NewRegistryHost(socket).Inspect(context.Background())
	if err != nil || st != (registry.ContainerState{Exists: true, Running: true, Image: "registry:3@sha256:x", Hash: "h1"}) {
		t.Fatalf("state = %+v, %v", st, err)
	}
}

func TestVolumeBytes(t *testing.T) {
	socket := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/system/df" || r.URL.Query().Get("type") != "volume" {
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"Volumes":[{"Name":"other","UsageData":{"Size":5}},{"Name":"relist-registry","UsageData":{"Size":2468000}}]}`))
	}))
	n, err := dockerexec.NewRegistryHost(socket).VolumeBytes(context.Background())
	if err != nil || n != 2468000 {
		t.Fatalf("bytes = %d, %v", n, err)
	}
}

var _ registry.Host = (*dockerexec.RegistryHost)(nil)
