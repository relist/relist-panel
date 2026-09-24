package hostmetrics_test

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/hostmetrics"
)

// fakeSource is a ContainerSource over an in-memory daemon: no test talks to
// a real daemon (the socket adapter has its own tests against a fake daemon).
type fakeSource struct {
	mu         sync.Mutex
	containers []hostmetrics.ContainerInfo
	stats      map[string]hostmetrics.ContainerStatsSample
	statsErrs  map[string]error
	listErr    error

	listCalls   int
	statsCalls  int
	curInFlight int
	maxInFlight int
	onStats     func(id string)
}

func (f *fakeSource) ListContainers(ctx context.Context) ([]hostmetrics.ContainerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.containers, nil
}

func (f *fakeSource) ContainerStats(ctx context.Context, id string) (hostmetrics.ContainerStatsSample, error) {
	f.mu.Lock()
	f.statsCalls++
	f.curInFlight++
	if f.curInFlight > f.maxInFlight {
		f.maxInFlight = f.curInFlight
	}
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		f.curInFlight--
		f.mu.Unlock()
	}()
	if f.onStats != nil {
		f.onStats(id)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.statsErrs[id]; err != nil {
		return hostmetrics.ContainerStatsSample{}, err
	}
	return f.stats[id], nil
}

func (f *fakeSource) recordedStatsCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.statsCalls
}

func (f *fakeSource) recordedMaxInFlight() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.maxInFlight
}

func containerInfo(id, service string, labels map[string]string) hostmetrics.ContainerInfo {
	if labels == nil {
		labels = map[string]string{
			hostmetrics.LabelComposeProject: "p-e-c",
			hostmetrics.LabelComposeService: service,
		}
	}
	return hostmetrics.ContainerInfo{
		ID:     id,
		Name:   "p-e-c-" + service + "-1",
		Image:  "nginx:1.27",
		State:  "running",
		Status: "Up 2 minutes (healthy)",
		Ports: []hostmetrics.PortMapping{
			{HostIP: "0.0.0.0", HostPort: 8080, ContainerPort: 80, Protocol: "tcp"},
		},
		Labels: labels,
	}
}

func statsSample(cpuTotal, cpuSystem, online, memUsage, memCache, memLimit, rx, tx, read, write uint64) hostmetrics.ContainerStatsSample {
	return hostmetrics.ContainerStatsSample{
		CPUTotalNanos:   cpuTotal,
		CPUSystemNanos:  cpuSystem,
		CPUOnline:       online,
		MemUsageBytes:   memUsage,
		MemCacheBytes:   memCache,
		MemLimitBytes:   memLimit,
		NetRXBytes:      rx,
		NetTXBytes:      tx,
		BlockReadBytes:  read,
		BlockWriteBytes: write,
	}
}

func knownOnlyPec(context.Context) (map[string]bool, error) {
	return map[string]bool{"p-e-c": true}, nil
}

// The first sweep of a container's life has memory gauges (usage − cache,
// the container's own limit) but no rates to report.
func TestServiceCollectorFirstSweepHasGaugesButNoRates(t *testing.T) {
	clock := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	source := &fakeSource{
		containers: []hostmetrics.ContainerInfo{containerInfo("c1", "web", nil)},
		stats: map[string]hostmetrics.ContainerStatsSample{
			"c1": statsSample(1000, 100000, 4, 5000, 1200, 10000, 100, 50, 111, 222),
		},
	}
	collector := hostmetrics.NewServiceCollector(source, knownOnlyPec, func() time.Time { return clock })
	collector.Sweep(context.Background())

	services := collector.Services()
	if len(services) != 1 {
		t.Fatalf("Services() returned %d entries, want 1", len(services))
	}
	sample := services[0]
	if sample.Project != "p-e-c" || sample.Service != "web" {
		t.Errorf("sample is keyed by (%q, %q), want (p-e-c, web)", sample.Project, sample.Service)
	}
	if sample.ContainerID != "c1" || sample.Name != "p-e-c-web-1" || sample.Image != "nginx:1.27" || sample.State != "running" {
		t.Errorf("identity fields = %+v", sample)
	}
	if sample.Health != "healthy" {
		t.Errorf("Health = %q, want healthy (parsed from the status text)", sample.Health)
	}
	if len(sample.Ports) != 1 || sample.Ports[0] != (hostmetrics.PortMapping{HostIP: "0.0.0.0", HostPort: 8080, ContainerPort: 80, Protocol: "tcp"}) {
		t.Errorf("Ports = %+v", sample.Ports)
	}
	// Memory used is usage − cache; the limit is the container's own.
	if sample.MemUsedBytes != 3800 {
		t.Errorf("MemUsedBytes = %v, want 3800 (usage 5000 − cache 1200)", sample.MemUsedBytes)
	}
	if sample.MemLimitBytes != 10000 {
		t.Errorf("MemLimitBytes = %v, want the container's own 10000", sample.MemLimitBytes)
	}
	for name, got := range map[string]float64{
		"CPUPercent":    sample.CPUPercent,
		"NetRXBPS":      sample.NetRXBPS,
		"NetTXBPS":      sample.NetTXBPS,
		"BlockReadBPS":  sample.BlockReadBPS,
		"BlockWriteBPS": sample.BlockWriteBPS,
	} {
		if !math.IsNaN(got) {
			t.Errorf("%s = %v on the first sweep, want NaN (no rate rather than zero)", name, got)
		}
	}
}

// The second sweep thirty seconds later stores delta rates, with container
// CPU in Docker's own arithmetic (a share of the system delta, scaled by the
// container's online CPUs — a percentage of one core).
func TestServiceCollectorSecondSweepStoresDeltaRates(t *testing.T) {
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	source := &fakeSource{
		containers: []hostmetrics.ContainerInfo{containerInfo("c1", "web", nil)},
		stats: map[string]hostmetrics.ContainerStatsSample{
			"c1": statsSample(1000, 100000, 4, 5000, 1200, 10000, 100, 50, 111, 222),
		},
	}
	collector := hostmetrics.NewServiceCollector(source, knownOnlyPec, func() time.Time { return now })
	collector.Sweep(context.Background())

	now = now.Add(30 * time.Second)
	source.stats = map[string]hostmetrics.ContainerStatsSample{
		"c1": statsSample(6000, 200000, 4, 7000, 1200, 10000, 3100, 350, 3111, 522),
	}
	collector.Sweep(context.Background())

	sample := collector.Services()[0]
	// CPU: Δcontainer 5000 / Δsystem 100000 × 4 online CPUs × 100 = 20%.
	if sample.CPUPercent != 20 {
		t.Errorf("CPUPercent = %v, want 20", sample.CPUPercent)
	}
	// Byte rates over the thirty seconds the sweep actually took.
	if sample.NetRXBPS != 100 || sample.NetTXBPS != 10 {
		t.Errorf("net rates = %v/%v Bps, want 100/10", sample.NetRXBPS, sample.NetTXBPS)
	}
	if sample.BlockReadBPS != 100 || sample.BlockWriteBPS != 10 {
		t.Errorf("block rates = %v/%v Bps, want 100/10", sample.BlockReadBPS, sample.BlockWriteBPS)
	}
}

// A CPU counter going backwards has no honest rate — and a container CPU
// share is not capped at 100, because it is a percentage of one core.
func TestServiceCollectorCPUPercentEdges(t *testing.T) {
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	source := &fakeSource{
		containers: []hostmetrics.ContainerInfo{containerInfo("c1", "web", nil)},
		stats: map[string]hostmetrics.ContainerStatsSample{
			"c1": statsSample(1000, 100000, 4, 5000, 0, 10000, 0, 0, 0, 0),
		},
	}
	collector := hostmetrics.NewServiceCollector(source, knownOnlyPec, func() time.Time { return now })
	collector.Sweep(context.Background())

	// Δcontainer 100000 / Δsystem 100000 × 4 CPUs × 100 = 400%.
	now = now.Add(30 * time.Second)
	source.stats = map[string]hostmetrics.ContainerStatsSample{
		"c1": statsSample(101000, 200000, 4, 5000, 0, 10000, 0, 0, 0, 0),
	}
	collector.Sweep(context.Background())
	if got := collector.Services()[0].CPUPercent; got != 400 {
		t.Errorf("CPUPercent = %v, want 400 (multi-core saturation is normal)", got)
	}

	// A counter that went backwards: no rate.
	now = now.Add(30 * time.Second)
	source.stats = map[string]hostmetrics.ContainerStatsSample{
		"c1": statsSample(5, 10, 4, 5000, 0, 10000, 0, 0, 0, 0),
	}
	collector.Sweep(context.Background())
	if got := collector.Services()[0].CPUPercent; !math.IsNaN(got) {
		t.Errorf("CPUPercent = %v across a counter reset, want NaN", got)
	}
}

// The redeploy continuity test: the same Service seen under a new container
// ID keeps exactly one series, because the key is (compose, service) — never
// the container ID (CONTEXT.md, ADR-0006). This is the test that would have
// caught keying on container ID.
func TestServiceCollectorRedeployKeepsOneSeries(t *testing.T) {
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	source := &fakeSource{
		containers: []hostmetrics.ContainerInfo{containerInfo("c1", "web", nil)},
		stats: map[string]hostmetrics.ContainerStatsSample{
			"c1": statsSample(1000, 100000, 4, 5000, 1200, 10000, 100, 50, 111, 222),
		},
	}
	collector := hostmetrics.NewServiceCollector(source, knownOnlyPec, func() time.Time { return now })
	collector.Sweep(context.Background())

	// Redeploy: new container, same Service, fresh counters starting near zero.
	now = now.Add(30 * time.Second)
	source.containers = []hostmetrics.ContainerInfo{containerInfo("c2", "web", nil)}
	source.stats = map[string]hostmetrics.ContainerStatsSample{
		"c2": statsSample(50, 60, 4, 400, 100, 8000, 7, 3, 5, 6),
	}
	collector.Sweep(context.Background())

	services := collector.Services()
	if len(services) != 1 {
		t.Fatalf("a redeploy forked the series: %d entries for one service, want 1", len(services))
	}
	sample := services[0]
	if sample.ContainerID != "c2" {
		t.Errorf("cache kept container %q, want the redeployed c2", sample.ContainerID)
	}
	if sample.MemUsedBytes != 300 || sample.MemLimitBytes != 8000 {
		t.Errorf("gauges = %v/%v, want the new container's 300/8000", sample.MemUsedBytes, sample.MemLimitBytes)
	}
	for name, got := range map[string]float64{
		"CPUPercent":    sample.CPUPercent,
		"NetRXBPS":      sample.NetRXBPS,
		"BlockWriteBPS": sample.BlockWriteBPS,
	} {
		if !math.IsNaN(got) {
			t.Errorf("%s = %v across a redeploy, want NaN (fresh container, no rate yet)", name, got)
		}
	}
}

// A container whose labels name a Compose the panel does not know — or that
// carries no Compose labels at all (Traefik, the panel itself) — is ignored,
// and costs no stats call.
func TestServiceCollectorIgnoresUnknownComposes(t *testing.T) {
	source := &fakeSource{
		containers: []hostmetrics.ContainerInfo{
			containerInfo("c1", "web", map[string]string{
				hostmetrics.LabelComposeProject: "a-ghost-compose",
				hostmetrics.LabelComposeService: "web",
			}),
			containerInfo("c2", "traefik", map[string]string{}),
		},
		stats: map[string]hostmetrics.ContainerStatsSample{
			"c1": statsSample(1, 2, 4, 3, 0, 4, 0, 0, 0, 0),
			"c2": statsSample(1, 2, 4, 3, 0, 4, 0, 0, 0, 0),
		},
	}
	collector := hostmetrics.NewServiceCollector(source, knownOnlyPec, time.Now)
	collector.Sweep(context.Background())

	if got := len(collector.Services()); got != 0 {
		t.Errorf("Services() returned %d entries for unknown composes, want 0 (ignored, not invented)", got)
	}
	if got := source.recordedStatsCalls(); got != 0 {
		t.Errorf("%d stats calls were made for ignored containers, want 0", got)
	}
	if got := source.listCalls; got != 1 {
		t.Errorf("ListContainers called %d times in one sweep, want once", got)
	}
}

// The sweep is measured once per sweep: /containers/json exactly once, then
// every container's stats fetched concurrently.
func TestServiceCollectorFetchesStatsInParallel(t *testing.T) {
	services := []string{"web", "api", "db"}
	var containers []hostmetrics.ContainerInfo
	stats := map[string]hostmetrics.ContainerStatsSample{}
	for i, service := range services {
		containers = append(containers, containerInfo(string(rune('c'+i))+"1", service, nil))
		stats[string(rune('c'+i))+"1"] = statsSample(1, 2, 4, 3, 0, 4, 0, 0, 0, 0)
	}
	arrived := make(chan string, 3)
	release := make(chan struct{})
	source := &fakeSource{
		containers: containers,
		stats:      stats,
		onStats: func(id string) {
			arrived <- id
			<-release
		},
	}
	collector := hostmetrics.NewServiceCollector(source, knownOnlyPec, time.Now)

	done := make(chan struct{})
	go func() {
		collector.Sweep(context.Background())
		close(done)
	}()
	for i := 0; i < 3; i++ {
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of 3 stats calls arrived: they are not running concurrently", i)
		}
	}
	close(release)
	<-done

	if got := source.recordedMaxInFlight(); got != 3 {
		t.Errorf("max concurrent stats calls = %d, want 3", got)
	}
	if got := source.listCalls; got != 1 {
		t.Errorf("ListContainers called %d times in one sweep, want once", got)
	}
	// The cache is sorted by (project, service), whatever order the daemon
	// listed the containers in.
	var names []string
	for _, sample := range collector.Services() {
		names = append(names, sample.Service)
	}
	if names[0] != "api" || names[1] != "db" || names[2] != "web" {
		t.Errorf("Services() ordered %v, want [api db web]", names)
	}
}

// A failed sweep keeps the previous data rather than inventing or wiping it,
// and reports why through Status.
func TestServiceCollectorFailedSweepKeepsPreviousData(t *testing.T) {
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	source := &fakeSource{
		containers: []hostmetrics.ContainerInfo{containerInfo("c1", "web", nil)},
		stats: map[string]hostmetrics.ContainerStatsSample{
			"c1": statsSample(1000, 100000, 4, 5000, 1200, 10000, 100, 50, 111, 222),
		},
	}
	collector := hostmetrics.NewServiceCollector(source, knownOnlyPec, func() time.Time { return now })
	collector.Sweep(context.Background())
	if !collector.Status().Available {
		t.Fatalf("Status() = %+v after a good sweep, want available", collector.Status())
	}

	// A stats failure for one container leaves that container's previous
	// sample in place.
	source.statsErrs = map[string]error{"c1": errors.New("daemon vanished mid-read")}
	now = now.Add(30 * time.Second)
	collector.Sweep(context.Background())
	status := collector.Status()
	if status.Available || status.Reason == "" {
		t.Errorf("Status() = %+v after a failed stats call, want unavailable with a reason", status)
	}
	if got := collector.Services()[0].ContainerID; got != "c1" {
		t.Errorf("cache kept %q after a stats failure, want the previous c1", got)
	}

	// A failed listing keeps everything from the last good sweep.
	source.statsErrs = nil
	source.listErr = errors.New("dockerd is restarting")
	now = now.Add(30 * time.Second)
	collector.Sweep(context.Background())
	if got := len(collector.Services()); got != 1 {
		t.Errorf("a failed listing wiped %d cached services, want the previous data kept", got-1)
	}

	// And a later good sweep clears the failure and refreshes the data.
	source.listErr = nil
	source.containers = []hostmetrics.ContainerInfo{containerInfo("c9", "web", nil)}
	source.stats = map[string]hostmetrics.ContainerStatsSample{
		"c9": statsSample(9, 9, 4, 900, 0, 9000, 9, 9, 9, 9),
	}
	now = now.Add(30 * time.Second)
	collector.Sweep(context.Background())
	if !collector.Status().Available {
		t.Errorf("Status() = %+v after a good sweep, want the failure cleared", collector.Status())
	}
	if got := collector.Services()[0].ContainerID; got != "c9" {
		t.Errorf("cache kept %q, want the refreshed c9", got)
	}
}

func TestParseHealth(t *testing.T) {
	for status, want := range map[string]string{
		"Up 2 minutes (healthy)":          "healthy",
		"Up 3 seconds (health: starting)": "starting",
		"Up 5 minutes (unhealthy)":        "unhealthy",
		"Up 2 minutes":                    "",
		"Exited (0) 5 minutes ago":        "",
		"Restarting (1) 40 seconds ago":   "",
		"Up 24 hours (health: failing)":   "failing",
	} {
		if got := hostmetrics.ParseHealth(status); got != want {
			t.Errorf("ParseHealth(%q) = %q, want %q", status, got, want)
		}
	}
}
