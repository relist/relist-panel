package metrics_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/database"
	"relist-panel/internal/hostmetrics"
	"relist-panel/internal/metrics"
	"relist-panel/internal/slug"
)

// The HTTP-seam doubles for the endpoints: the sampler's read sides as fakes,
// so no test reads the real /proc or talks to a real daemon, over the real
// migrated SQLite the rollup windows are served from.

type fakeHostHistory struct {
	mu      sync.Mutex
	window  []hostmetrics.HostSample
	sources map[string]hostmetrics.SourceStatus
	lastN   int
}

func (f *fakeHostHistory) Window(n int) []hostmetrics.HostSample {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastN = n
	if n > len(f.window) {
		n = len(f.window)
	}
	return f.window[len(f.window)-n:]
}

func (f *fakeHostHistory) Sources() map[string]hostmetrics.SourceStatus {
	out := make(map[string]hostmetrics.SourceStatus, len(f.sources))
	for name, status := range f.sources {
		out[name] = status
	}
	return out
}

type fakeServiceFacts struct {
	mu             sync.Mutex
	services       []hostmetrics.ServiceSample
	windows        map[string][]hostmetrics.ServiceSample
	status         hostmetrics.SourceStatus
	windowRequests []windowRequest
}

type windowRequest struct {
	project, service string
	n                int
}

func (f *fakeServiceFacts) Services() []hostmetrics.ServiceSample {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.services
}

func (f *fakeServiceFacts) ServiceWindow(project, service string, n int) []hostmetrics.ServiceSample {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.windowRequests = append(f.windowRequests, windowRequest{project, service, n})
	window := f.windows[project+"/"+service]
	if n > len(window) {
		n = len(window)
	}
	return window[len(window)-n:]
}

func (f *fakeServiceFacts) Status() hostmetrics.SourceStatus {
	return hostmetrics.SourceStatus{Available: f.status.Available, Reason: f.status.Reason}
}

type metricsServer struct {
	handler http.Handler
	store   *metrics.Store
	db      *sql.DB
	history *fakeHostHistory
	facts   *fakeServiceFacts
}

func newMetricsServer(t *testing.T) *metricsServer {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	store := metrics.NewStore(db)
	history := &fakeHostHistory{
		sources: map[string]hostmetrics.SourceStatus{
			hostmetrics.SourceCPU:        {Available: true},
			hostmetrics.SourceDockerDisk: {Reason: "docker api /system/df: 500"},
		},
	}
	facts := &fakeServiceFacts{
		status:  hostmetrics.SourceStatus{Available: true},
		windows: make(map[string][]hostmetrics.ServiceSample),
	}
	service := metrics.New(store, history, facts)
	mux := http.NewServeMux()
	service.RegisterRoutes(mux)
	return &metricsServer{handler: mux, store: store, db: db, history: history, facts: facts}
}

func (s *metricsServer) get(t *testing.T, path string) (*http.Response, map[string]any) {
	t.Helper()
	response, raw := s.getRaw(t, path)
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode response body for %s: %v", path, err)
	}
	return response, body
}

func (s *metricsServer) getRaw(t *testing.T, path string) (*http.Response, []byte) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://relist-panel.test"+path, nil)
	recorder := httptest.NewRecorder()
	s.handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body for %s: %v", path, err)
	}
	return response, raw
}

// A sub-hour window is served from the ring at 10-second resolution and says
// so; `partial` reports the restart-gap honestly.
func TestHostMetricsSubHourWindowServesFromRing(t *testing.T) {
	server := newMetricsServer(t)
	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	server.history.window = []hostmetrics.HostSample{
		{At: base, CPUPercent: 10},
		{At: base.Add(10 * time.Second), CPUPercent: 20},
	}

	response, body := server.get(t, "/api/host/metrics?window=15m")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if body["window"] != "15m" || body["from"] != "ring" || body["intervalSeconds"] != float64(10) {
		t.Errorf("envelope = %v/%v/%v, want 15m/ring/10", body["window"], body["from"], body["intervalSeconds"])
	}
	if body["partial"] != true {
		t.Errorf("partial = %v, want true (2 samples of a 15m window's 90)", body["partial"])
	}
	samples := body["samples"].([]any)
	if len(samples) != 2 {
		t.Fatalf("samples = %d, want the ring's 2", len(samples))
	}
	first := samples[0].(map[string]any)
	if first["cpu"] != float64(10) {
		t.Errorf("first sample cpu = %v, want 10", first["cpu"])
	}
	if server.history.lastN != 90 {
		t.Errorf("Window(n) called with n=%d, want 90 (15m at 10s)", server.history.lastN)
	}
}

// The day's shape comes from the rollups at minute resolution — with the
// minutes a restart skipped absent, not zeroed.
func TestHostMetricsDayWindowServesFromRollups(t *testing.T) {
	server := newMetricsServer(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Minute)

	older := now.Add(-23 * time.Hour)
	if err := server.store.WriteHostRollup(ctx, hostmetrics.HostSample{
		At: older, CPUPercent: 42, MemTotalBytes: 1000, DockerDiskBytes: math.NaN(),
	}); err != nil {
		t.Fatalf("write older rollup: %v", err)
	}
	if err := server.store.WriteHostRollup(ctx, hostmetrics.HostSample{
		At: now.Add(-time.Hour), CPUPercent: 12,
	}); err != nil {
		t.Fatalf("write windowed rollup: %v", err)
	}

	response, body := server.get(t, "/api/host/metrics?window=24h")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if body["from"] != "rollups" || body["intervalSeconds"] != float64(60) {
		t.Errorf("envelope = %v/%v, want rollups/60", body["from"], body["intervalSeconds"])
	}
	if body["partial"] != true {
		t.Errorf("partial = %v, want true (2 rollups of a day's 1440)", body["partial"])
	}
	samples := body["samples"].([]any)
	if len(samples) != 2 {
		t.Fatalf("samples = %d, want 2 (the 26h-old retention-adjacent row was never written)", len(samples))
	}
	first := samples[0].(map[string]any)
	if first["cpu"] != float64(42) {
		t.Errorf("first rollup cpu = %v, want 42", first["cpu"])
	}
	// The rollup's NULL docker disk column arrives as JSON null — never a
	// zero wearing a measurement's clothes.
	if first["dockerUsed"] != nil {
		t.Errorf("dockerUsed = %v, want null", first["dockerUsed"])
	}
}

// The sources block names what is readable and why not, so the screen can
// render `unavailable` with a reason instead of a zeroed line.
func TestHostMetricsSourcesReportUnavailableWithReason(t *testing.T) {
	server := newMetricsServer(t)
	server.history.sources = map[string]hostmetrics.SourceStatus{
		hostmetrics.SourceCPU:        {Available: true},
		hostmetrics.SourceNetwork:    {Reason: "read /host/proc/1/net/dev: no such file or directory"},
		hostmetrics.SourceDockerDisk: {Reason: "docker api /system/df: 500"},
	}

	response, body := server.get(t, "/api/host/metrics?window=1h")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	sources := body["sources"].(map[string]any)
	cpu := sources["cpu"].(map[string]any)
	if cpu["available"] != true {
		t.Errorf("sources.cpu = %v, want available", cpu)
	}
	network := sources["network"].(map[string]any)
	if network["available"] != false {
		t.Errorf("sources.network = %v, want unavailable", network)
	}
	if reason, _ := network["reason"].(string); reason == "" {
		t.Errorf("sources.network reason = %q, want the reason it cannot be read", reason)
	}
	dockerDisk := sources["dockerDisk"].(map[string]any)
	if dockerDisk["available"] != false {
		t.Errorf("sources.dockerDisk = %v, want unavailable", dockerDisk)
	}
}

// A Service's sub-hour window comes from the collector keyed by the frozen
// (compose project, service) identity; a Service the collector never swept
// is an empty partial window, not a 404 — its rollups are its past.
func TestServiceMetricsSubHourWindow(t *testing.T) {
	server := newMetricsServer(t)
	project := seedCompose(t, server.db, "Storefront", "Web App")

	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	server.facts.windows[project+"/web"] = []hostmetrics.ServiceSample{
		{At: base, CPUPercent: 55},
		{At: base.Add(30 * time.Second), CPUPercent: 65},
	}

	path := "/api/projects/1/environments/1/composes/1/services/web/metrics?window=15m"
	response, body := server.get(t, path)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if body["from"] != "ring" || body["intervalSeconds"] != float64(30) {
		t.Errorf("envelope = %v/%v, want ring/30 (the sweep cadence)", body["from"], body["intervalSeconds"])
	}
	samples := body["samples"].([]any)
	if len(samples) != 2 || samples[1].(map[string]any)["cpu"] != float64(65) {
		t.Errorf("samples = %v, want the two sweeps", samples)
	}
	requests := server.facts.windowRequests
	if len(requests) != 1 || requests[0].project != project || requests[0].service != "web" || requests[0].n != 30 {
		t.Errorf("ServiceWindow called with %+v, want (%q, web, 30)", requests, project)
	}
	dockerStats := body["sources"].(map[string]any)["dockerStats"].(map[string]any)
	if dockerStats["available"] != true {
		t.Errorf("sources.dockerStats = %v, want available", dockerStats)
	}

	// A Service with no sweep history in a known Compose: empty and partial.
	response, body = server.get(t, "/api/projects/1/environments/1/composes/1/services/ghost/metrics?window=15m")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("unknown service status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if body["partial"] != true || len(body["samples"].([]any)) != 0 {
		t.Errorf("unknown service body = %v, want empty partial window", body)
	}
}

// Beyond the hour, a Service's windows are its rollups.
func TestServiceMetricsDayWindowFromRollups(t *testing.T) {
	server := newMetricsServer(t)
	project := seedCompose(t, server.db, "Storefront", "Web App")
	ctx := context.Background()

	if err := server.store.WriteServiceRollups(ctx, []hostmetrics.ServiceSample{{
		Project: project, Service: "web", At: time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Minute),
		CPUPercent: 33, Pids: math.NaN(),
	}}); err != nil {
		t.Fatalf("write service rollup: %v", err)
	}

	response, body := server.get(t, "/api/projects/1/environments/1/composes/1/services/web/metrics?window=24h")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if body["from"] != "rollups" {
		t.Errorf("from = %v, want rollups", body["from"])
	}
	samples := body["samples"].([]any)
	if len(samples) != 1 {
		t.Fatalf("samples = %d, want 1", len(samples))
	}
	first := samples[0].(map[string]any)
	if first["cpu"] != float64(33) {
		t.Errorf("rollup cpu = %v, want 33", first["cpu"])
	}
	if first["pids"] != nil {
		t.Errorf("rollup pids = %v, want null", first["pids"])
	}
}

// The Services endpoint serves the collector's cache, filtered by the
// Compose's frozen identity: the rail's rows, 404 for an unknown Compose.
func TestServicesEndpointServesCollectorCache(t *testing.T) {
	server := newMetricsServer(t)
	project := seedCompose(t, server.db, "Storefront", "Web App")

	server.facts.services = []hostmetrics.ServiceSample{
		{
			Project: project, Service: "web",
			ContainerID: "abc123", Name: "storefront-web-1", Image: "nginx:1.27",
			State: "running", Health: "healthy",
			Ports: []hostmetrics.PortMapping{{HostIP: "0.0.0.0", HostPort: 8080, ContainerPort: 80, Protocol: "tcp"}},
		},
		{Project: "some-other-compose", Service: "api", ContainerID: "zzz", Name: "api", Image: "api", State: "running"},
	}

	response, raw := server.getRaw(t, "/api/projects/1/environments/1/composes/1/services")
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var services []struct {
		Service       string `json:"service"`
		ContainerID   string `json:"containerId"`
		ContainerName string `json:"containerName"`
		Image         string `json:"image"`
		State         string `json:"state"`
		Health        string `json:"health"`
		Ports         []struct {
			HostIP        string `json:"hostIp"`
			HostPort      uint16 `json:"hostPort"`
			ContainerPort uint16 `json:"containerPort"`
			Protocol      string `json:"protocol"`
		} `json:"ports"`
	}
	if err := json.Unmarshal(raw, &services); err != nil {
		t.Fatalf("decode services response %q: %v", raw, err)
	}
	if len(services) != 1 {
		t.Fatalf("services = %d, want only the seeded Compose's (others ignored, not invented)", len(services))
	}
	web := services[0]
	if web.Service != "web" || web.ContainerID != "abc123" || web.ContainerName != "storefront-web-1" ||
		web.Image != "nginx:1.27" || web.State != "running" || web.Health != "healthy" {
		t.Errorf("service facts = %+v", web)
	}
	if len(web.Ports) != 1 || web.Ports[0].HostPort != 8080 || web.Ports[0].ContainerPort != 80 || web.Ports[0].Protocol != "tcp" {
		t.Errorf("ports = %+v, want the published 8080→80/tcp", web.Ports)
	}

	// A Compose that does not exist is a 404, not an empty list.
	response, _ = server.getRaw(t, "/api/projects/1/environments/1/composes/999/services")
	defer response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Errorf("unknown compose services = %d, want %d", response.StatusCode, http.StatusNotFound)
	}
}

// seedCompose inserts a Project > Environment > Compose path directly and
// returns the Compose's frozen deploy identity (ADR-0005).
func seedCompose(t *testing.T, db *sql.DB, projectName, composeName string) string {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second).Unix()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO projects (name, description, created_at, updated_at) VALUES (?, '', ?, ?)`,
		projectName, now, now); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO environments (project_id, name, created_at) VALUES ((SELECT id FROM projects WHERE name = ?), 'production', ?)`,
		projectName, now); err != nil {
		t.Fatalf("seed environment: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO composes (environment_id, name, project_slug, env_slug, yaml_content, created_at, updated_at)
		VALUES (
			(SELECT id FROM environments WHERE project_id = (SELECT id FROM projects WHERE name = ?)),
			?, ?, ?, '', ?, ?
		)`, projectName, composeName,
		"storefront", "production", now, now); err != nil {
		t.Fatalf("seed compose: %v", err)
	}
	var (
		projectSlug string
		envSlug     string
		name        string
	)
	if err := db.QueryRowContext(ctx, `SELECT project_slug, env_slug, name FROM composes`).Scan(&projectSlug, &envSlug, &name); err != nil {
		t.Fatalf("read seeded compose identity: %v", err)
	}
	return projectSlug + "-" + envSlug + "-" + slug.Slugify(name)
}
