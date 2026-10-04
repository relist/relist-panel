package httpapi_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"relist-panel/internal/hostmetrics"
)

// The metrics endpoints' HTTP-seam doubles: the running sampler's read sides
// as fakes, so no test in this package reads the real /proc or talks to a
// real daemon.

type fakeHostHistory struct {
	window  []hostmetrics.HostSample
	sources map[string]hostmetrics.SourceStatus
}

func newFakeHostHistory() *fakeHostHistory {
	return &fakeHostHistory{
		sources: map[string]hostmetrics.SourceStatus{
			hostmetrics.SourceCPU: {Available: true},
		},
	}
}

func (f *fakeHostHistory) Window(n int) []hostmetrics.HostSample {
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
	services       []hostmetrics.ServiceSample
	windows        map[string][]hostmetrics.ServiceSample
	status         hostmetrics.SourceStatus
	windowRequests []string
}

func newFakeServiceFacts() *fakeServiceFacts {
	return &fakeServiceFacts{
		windows: make(map[string][]hostmetrics.ServiceSample),
		status:  hostmetrics.SourceStatus{Available: true},
	}
}

func (f *fakeServiceFacts) Services() []hostmetrics.ServiceSample {
	return f.services
}

func (f *fakeServiceFacts) ServiceWindow(project, service string, n int) []hostmetrics.ServiceSample {
	f.windowRequests = append(f.windowRequests, project+"/"+service)
	window := f.windows[project+"/"+service]
	if n > len(window) {
		n = len(window)
	}
	return window[len(window)-n:]
}

func (f *fakeServiceFacts) Status() hostmetrics.SourceStatus {
	return f.status
}

// The metrics routes ride the same mux as everything else, so the existing
// auth middleware covers them: 401 unauthenticated, 200 with a session.
func TestMetricsRoutesRequireAuthentication(t *testing.T) {
	server := newTestServer(t)

	metricsPaths := []string{
		"/api/host/metrics?window=1h",
		"/api/projects/1/environments/1/composes/1/services/web/metrics?window=1h",
		"/api/projects/1/environments/1/composes/1/services",
	}
	for _, path := range metricsPaths {
		response := server.request(t, http.MethodGet, path, nil)
		response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s unauthenticated = %d, want %d", path, response.StatusCode, http.StatusUnauthorized)
		}
	}

	setupAndLogin(t, server)
	project := createProject(t, server, "Storefront", "The customer-facing application")
	environment := defaultEnvironment(t, server, project.ID)
	compose := createCompose(t, server, project.ID, environment.ID, "web")

	realPaths := []string{
		"/api/host/metrics?window=1h",
		"/api/projects/" + idString(project.ID) + "/environments/" + idString(environment.ID) +
			"/composes/" + idString(compose.ID) + "/services/web/metrics?window=1h",
		"/api/projects/" + idString(project.ID) + "/environments/" + idString(environment.ID) +
			"/composes/" + idString(compose.ID) + "/services",
	}
	for _, path := range realPaths {
		response := server.request(t, http.MethodGet, path, nil)
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("%s with session = %d, want %d", path, response.StatusCode, http.StatusOK)
		}
	}

	// An unknown Compose is a 404, not an empty list wearing a 200.
	unknown := server.request(t, http.MethodGet,
		"/api/projects/"+idString(project.ID)+"/environments/"+idString(environment.ID)+"/composes/999/services", nil)
	defer unknown.Body.Close()
	if unknown.StatusCode != http.StatusNotFound {
		t.Errorf("unknown compose services = %d, want %d", unknown.StatusCode, http.StatusNotFound)
	}
}

func idString(id int64) string {
	return strconv.FormatInt(id, 10)
}

// defaultEnvironment reads a fresh Project's auto-provisioned environment.
func defaultEnvironment(t *testing.T, server *testServer, projectID int64) environmentResponse {
	t.Helper()
	response := server.request(t, http.MethodGet, "/api/projects/"+idString(projectID)+"/environments", nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("list environments status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	var environments []environmentResponse
	if err := json.NewDecoder(response.Body).Decode(&environments); err != nil {
		t.Fatalf("decode environments: %v", err)
	}
	if len(environments) != 1 {
		t.Fatalf("environments = %+v, want the auto-provisioned default", environments)
	}
	return environments[0]
}

func createCompose(t *testing.T, server *testServer, projectID, environmentID int64, name string) composeResponse {
	t.Helper()
	response := postJSON(t, server,
		"/api/projects/"+idString(projectID)+"/environments/"+idString(environmentID)+"/composes",
		map[string]any{"name": name, "yamlContent": "services: {web: {image: nginx:1.27}}"})
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create compose status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	var compose composeResponse
	if err := json.NewDecoder(response.Body).Decode(&compose); err != nil {
		t.Fatalf("decode created compose: %v", err)
	}
	return compose
}

// An unknown window is a 400 with a message — never a silent default. A chart
// that asked for a day and quietly got an hour would lie worse than an error.
func TestMetricsRoutesRejectUnknownWindows(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)

	for _, window := range []string{"", "2h", "7d", "1H", "hour"} {
		path := "/api/host/metrics"
		if window != "" {
			path += "?window=" + window
		}
		response := server.request(t, http.MethodGet, path, nil)
		defer response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Errorf("window %q status = %d, want %d", window, response.StatusCode, http.StatusBadRequest)
		}
		var body struct {
			Error string `json:"error"`
		}
		if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
			t.Fatalf("decode error body: %v", err)
		}
		if !strings.Contains(body.Error, "15m") {
			t.Errorf("window %q error = %q, want it to name the valid windows", window, body.Error)
		}
	}
}

// The host response reports which store served it and how fully it covers
// the window: a fake history shorter than the window is `partial`.
func TestHostMetricsResponseNamesStoreAndPartialState(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)

	base := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	// Two samples is far short of a 15m window's ninety.
	server.history.window = []hostmetrics.HostSample{
		{
			At: base, CPUPercent: 12.5, MemUsedBytes: 100, MemTotalBytes: 400,
			DiskFreeBytes: 50, DiskTotalBytes: 200, DockerDiskBytes: 30,
			DiskReadBPS: 1, DiskWriteBPS: 2, NetRXBPS: 3, NetTXBPS: 4,
		},
		{At: base.Add(10 * time.Second), CPUPercent: 20},
	}

	response := server.request(t, http.MethodGet, "/api/host/metrics?window=15m", nil)
	defer response.Body.Close()
	var body struct {
		Window          string `json:"window"`
		From            string `json:"from"`
		IntervalSeconds int    `json:"intervalSeconds"`
		Partial         bool   `json:"partial"`
		Sources         map[string]struct {
			Available bool   `json:"available"`
			Reason    string `json:"reason"`
		} `json:"sources"`
		Samples []struct {
			At      int64    `json:"at"`
			CPU     *float64 `json:"cpu"`
			MemUsed *float64 `json:"memUsed"`
		} `json:"samples"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode host metrics response: %v", err)
	}
	if body.Window != "15m" || body.From != "ring" || body.IntervalSeconds != 10 {
		t.Errorf("envelope = %s/%s/%d, want 15m/ring/10", body.Window, body.From, body.IntervalSeconds)
	}
	if !body.Partial {
		t.Error("Partial = false with 2 of 90 samples, want true")
	}
	if len(body.Samples) != 2 {
		t.Fatalf("samples = %d, want 2", len(body.Samples))
	}
	if body.Samples[0].CPU == nil || *body.Samples[0].CPU != 12.5 {
		t.Errorf("first sample cpu = %v, want 12.5", body.Samples[0].CPU)
	}
	if cpu, ok := body.Sources["cpu"]; !ok || !cpu.Available {
		t.Errorf("sources[cpu] = %+v, want available", body.Sources["cpu"])
	}
}
