package dockerexec_test

import (
	"context"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/dockerexec"
	"relist-panel/internal/hostmetrics"
)

// newFakeDaemon serves the Docker API over a real unix socket at a temp path
// — the same transport the panel uses in production, without a real daemon.
func newFakeDaemon(t *testing.T, handler http.Handler) string {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "docker.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("listen on %s: %v", socket, err)
	}
	server := &http.Server{Handler: handler}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()
	t.Cleanup(func() {
		_ = server.Close()
		_ = <-serveErr
	})
	return socket
}

// recording captures what the adapter actually put on the wire.
type recording struct {
	mu     sync.Mutex
	method string
	path   string
	query  string
}

func (r *recording) handler(body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.method = req.Method
		r.path = req.URL.Path
		r.query = req.URL.RawQuery
		_, _ = w.Write([]byte(body))
	})
}

func (r *recording) snapshot() (method, path, query string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.method, r.path, r.query
}

func TestContainerSourceListsContainers(t *testing.T) {
	record := &recording{}
	socket := newFakeDaemon(t, record.handler(`[
		{
			"Id": "abc123",
			"Names": ["/p-e-c-web-1"],
			"Image": "nginx:1.27",
			"State": "running",
			"Status": "Up 2 minutes (healthy)",
			"Ports": [
				{"IP": "0.0.0.0", "PrivatePort": 80, "PublicPort": 8080, "Type": "tcp"},
				{"PrivatePort": 9000, "Type": "udp"}
			],
			"Labels": {
				"com.docker.compose.project": "p-e-c",
				"com.docker.compose.service": "web"
			}
		}
	]`))
	source := dockerexec.NewContainerSource(socket)

	containers, err := source.ListContainers(context.Background())
	if err != nil {
		t.Fatalf("ListContainers() error = %v", err)
	}

	if method, path, query := record.snapshot(); method != http.MethodGet || path != "/containers/json" || query != "" {
		t.Errorf("request was %s %s?%s, want GET /containers/json", method, path, query)
	}
	if len(containers) != 1 {
		t.Fatalf("ListContainers() returned %d containers, want 1", len(containers))
	}
	container := containers[0]
	if container.ID != "abc123" || container.Name != "p-e-c-web-1" || container.Image != "nginx:1.27" ||
		container.State != "running" || container.Status != "Up 2 minutes (healthy)" {
		t.Errorf("container = %+v", container)
	}
	if container.Labels[hostmetrics.LabelComposeProject] != "p-e-c" ||
		container.Labels[hostmetrics.LabelComposeService] != "web" {
		t.Errorf("labels = %v", container.Labels)
	}
	wantPorts := []hostmetrics.PortMapping{
		{HostIP: "0.0.0.0", HostPort: 8080, ContainerPort: 80, Protocol: "tcp"},
		{HostPort: 0, ContainerPort: 9000, Protocol: "udp"},
	}
	if len(container.Ports) != 2 || container.Ports[0] != wantPorts[0] || container.Ports[1] != wantPorts[1] {
		t.Errorf("ports = %+v, want %+v", container.Ports, wantPorts)
	}
}

const rawStatsV1 = `{
	"cpu_stats": {
		"cpu_usage": {"total_usage": 1000},
		"system_cpu_usage": 100000,
		"online_cpus": 4
	},
	"memory_stats": {"usage": 5000, "limit": 10000, "stats": {"cache": 1200}},
	"networks": {
		"eth0": {"rx_bytes": 100, "tx_bytes": 50},
		"eth1": {"rx_bytes": 10, "tx_bytes": 5}
	},
	"blkio_stats": {
		"io_service_bytes_recursive": [
			{"major": 8, "minor": 0, "op": "Read", "value": 111},
			{"major": 8, "minor": 0, "op": "write", "value": 222},
			{"major": 8, "minor": 0, "op": "total", "value": 999}
		]
	}
}`

func TestContainerSourceReadsRawAPINumbers(t *testing.T) {
	record := &recording{}
	socket := newFakeDaemon(t, record.handler(rawStatsV1))
	source := dockerexec.NewContainerSource(socket)

	stats, err := source.ContainerStats(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("ContainerStats() error = %v", err)
	}

	if method, path, query := record.snapshot(); method != http.MethodGet ||
		path != "/containers/abc123/stats" || query != "stream=false" {
		t.Errorf("request was %s %s?%s, want GET /containers/abc123/stats?stream=false", method, path, query)
	}
	want := hostmetrics.ContainerStatsSample{
		CPUTotalNanos:   1000,
		CPUSystemNanos:  100000,
		CPUOnline:       4,
		MemUsageBytes:   5000,
		MemCacheBytes:   1200,
		MemLimitBytes:   10000,
		NetRXBytes:      110, // interfaces summed
		NetTXBytes:      55,
		BlockReadBytes:  111, // op matching is case-insensitive; "total" skipped
		BlockWriteBytes: 222,
	}
	if stats != want {
		t.Errorf("ContainerStats() = %+v, want %+v", stats, want)
	}
}

// cgroup v2 reports inactive_file where v1 reported cache; the daemon
// decides which one is there and the adapter follows, like the docker CLI.
func TestContainerSourcePrefersInactiveFileOnCgroupV2(t *testing.T) {
	body := `{
		"cpu_stats": {"cpu_usage": {"total_usage": 1}, "system_cpu_usage": 2, "online_cpus": 1},
		"memory_stats": {"usage": 5000, "limit": 10000, "stats": {"inactive_file": 700}},
		"blkio_stats": {}
	}`
	socket := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	source := dockerexec.NewContainerSource(socket)

	stats, err := source.ContainerStats(context.Background(), "abc123")
	if err != nil {
		t.Fatalf("ContainerStats() error = %v", err)
	}
	if stats.MemCacheBytes != 700 {
		t.Errorf("MemCacheBytes = %d, want inactive_file's 700", stats.MemCacheBytes)
	}
}

func TestContainerSourceSurfacesDaemonErrors(t *testing.T) {
	socket := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no such container", http.StatusNotFound)
	}))
	source := dockerexec.NewContainerSource(socket)

	_, err := source.ContainerStats(context.Background(), "missing")
	if err == nil {
		t.Fatal("ContainerStats() = nil error for a 404, want an error")
	}
	if want := "404"; !strings.Contains(err.Error(), want) {
		t.Errorf("ContainerStats() error = %v, want it to mention %q", err, want)
	}
}

// The full path over the real adapter: daemon JSON in, keyed Service samples
// out — the port the collector consumes and main.go will wire (ticket 04).
func TestServiceCollectorOverTheRealAdapter(t *testing.T) {
	var mu sync.Mutex
	requests := 0
	mux := http.NewServeMux()
	mux.HandleFunc("GET /containers/json", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		_, _ = w.Write([]byte(`[{
			"Id": "abc123",
			"Names": ["/p-e-c-web-1"],
			"Image": "nginx:1.27",
			"State": "running",
			"Status": "Up 2 minutes (healthy)",
			"Labels": {"com.docker.compose.project": "p-e-c", "com.docker.compose.service": "web"}
		}]`))
	})
	mux.HandleFunc("GET /containers/abc123/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("stream") != "false" {
			t.Errorf("stats request missing stream=false: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(rawStatsV1))
	})
	socket := newFakeDaemon(t, mux)

	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	collector := hostmetrics.NewServiceCollector(
		dockerexec.NewContainerSource(socket),
		func(context.Context) (map[string]bool, error) { return map[string]bool{"p-e-c": true}, nil },
		func() time.Time { return now },
	)
	collector.Sweep(context.Background())

	services := collector.Services()
	if len(services) != 1 {
		t.Fatalf("Services() returned %d entries, want 1", len(services))
	}
	sample := services[0]
	if sample.Service != "web" || sample.ContainerID != "abc123" || sample.Health != "healthy" {
		t.Errorf("sample = %+v", sample)
	}
	if sample.MemUsedBytes != 3800 {
		t.Errorf("MemUsedBytes = %v, want 3800", sample.MemUsedBytes)
	}
	mu.Lock()
	defer mu.Unlock()
	if requests != 1 {
		t.Errorf("%d /containers/json requests in one sweep, want 1", requests)
	}
}
