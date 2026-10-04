package hostmetrics

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

// ContainerSource is the port for reading containers and their raw stats off
// the Docker daemon — the second port of the system, declared here in the
// consumer and implemented over the socket in dockerexec (ADR-0003, as
// refined by ADR-0006). Tests fake it; no test talks to a real daemon.
type ContainerSource interface {
	// ListContainers lists the daemon's running containers with their
	// Compose labels, state, status text and ports (GET /containers/json).
	ListContainers(ctx context.Context) ([]ContainerInfo, error)
	// ContainerStats reads one container's cumulative counters as raw API
	// numbers (GET /containers/{id}/stats?stream=false) — never the CLI's
	// formatted strings.
	ContainerStats(ctx context.Context, id string) (ContainerStatsSample, error)
}

// The Compose labels the daemon stamps on every container a deploy creates.
const (
	// LabelComposeProject is the frozen deploy identity <p>-<e>-<c> of
	// ADR-0005 — stable across renames and redeploys, which is what makes it
	// a usable history key component.
	LabelComposeProject = "com.docker.compose.project"
	// LabelComposeService names the Service (CONTEXT.md) inside that Compose.
	LabelComposeService = "com.docker.compose.service"
)

// ContainerInfo is one running container as the daemon lists it.
type ContainerInfo struct {
	ID     string
	Name   string
	Image  string
	State  string
	Status string // the human text, e.g. "Up 2 minutes (healthy)"
	Ports  []PortMapping
	Labels map[string]string
}

// PortMapping is one published port of a container.
type PortMapping struct {
	HostIP        string
	HostPort      uint16
	ContainerPort uint16
	Protocol      string
}

// ContainerStatsSample is one container's cumulative counters in raw API
// units: CPU and system clocks in nanoseconds, memory and I/O in bytes.
// They are counters, not rates — the collector keeps the previous sweep's
// values and stores deltas.
type ContainerStatsSample struct {
	CPUTotalNanos   uint64
	CPUSystemNanos  uint64
	CPUOnline       uint64
	MemUsageBytes   uint64
	MemCacheBytes   uint64
	MemLimitBytes   uint64
	NetRXBytes      uint64 // summed over the container's interfaces
	NetTXBytes      uint64
	BlockReadBytes  uint64 // summed over io_service_bytes_recursive
	BlockWriteBytes uint64
	PidsCurrent     uint64 // pids_stats.current; 0 means the runtime reported none
}

// ServiceSample is one Service's latest observation: the identity fields the
// status rail and facts strip render, plus the gauges and delta rates the
// Monitor tab charts. Rate fields are NaN on a container's first sweep (no
// rate to report) and whenever a counter went backwards.
type ServiceSample struct {
	Project     string // the Compose's frozen <p>-<e>-<c> identity (ADR-0005)
	Service     string
	ContainerID string
	Name        string
	Image       string
	State       string
	Health      string // healthy / starting / unhealthy; "" when none
	Ports       []PortMapping
	At          time.Time

	CPUPercent    float64 // of one core; can exceed 100
	MemUsedBytes  float64 // usage − cache
	MemLimitBytes float64

	NetRXBPS      float64
	NetTXBPS      float64
	BlockReadBPS  float64
	BlockWriteBPS float64

	Pids float64 // pids_stats.current; NaN when the runtime reported none
}

// KnownProjects reports the Compose project names the panel knows — the
// frozen <p>-<e>-<c> identities on the composes table. Containers whose
// project label is not in the set are ignored, not invented.
type KnownProjects func(ctx context.Context) (map[string]bool, error)

// serviceHistoryCapacity is how many sweeps one Service's history keeps: an
// hour at the sweep cadence, the window the endpoints serve from the ring
// before the rollups take over (~170 KB for 20 Services, per the spec's
// budget).
const serviceHistoryCapacity = int(time.Hour / SweepInterval)

// ServiceCollector turns daemon sweeps into per-Service samples. Its cache
// is keyed by (compose project, service) — never by container ID (CONTEXT.md):
// a deploy mints a new container, and the series must survive it.
type ServiceCollector struct {
	source ContainerSource
	known  KnownProjects
	now    func() time.Time

	mu      sync.Mutex
	latest  map[serviceKey]serviceObservation
	minute  []ServiceSample // the sweeps the open minute has seen, for CloseMinute
	history map[serviceKey][]ServiceSample
	swept   bool
	lastErr error
}

type serviceKey struct {
	Project string
	Service string
}

// serviceObservation is a cache entry: the sample the screens read plus the
// cumulative counters the next sweep's deltas are computed against.
type serviceObservation struct {
	sample ServiceSample

	cpuTotal   uint64
	cpuSystem  uint64
	cpuOnline  uint64
	netRX      uint64
	netTX      uint64
	blockRead  uint64
	blockWrite uint64
}

// NewServiceCollector constructs the collector over a ContainerSource.
func NewServiceCollector(source ContainerSource, known KnownProjects, now func() time.Time) *ServiceCollector {
	if now == nil {
		now = time.Now
	}
	return &ServiceCollector{
		source:  source,
		known:   known,
		now:     now,
		latest:  make(map[serviceKey]serviceObservation),
		history: make(map[serviceKey][]ServiceSample),
	}
}

// Sweep performs the 30-second measurement: /containers/json once, then one
// stats call per attributable container, all concurrently (~2s wall for N
// containers; the wall time is Docker sampling a delta window, not work).
// It runs on its own goroutine — the sampler launches it so the 10s host
// tick never waits behind it.
func (c *ServiceCollector) Sweep(ctx context.Context) {
	containers, err := c.source.ListContainers(ctx)
	if err != nil {
		c.fail(fmt.Errorf("list containers: %w", err))
		return
	}
	known, err := c.known(ctx)
	if err != nil {
		c.fail(fmt.Errorf("list known composes: %w", err))
		return
	}

	type target struct {
		info ContainerInfo
		key  serviceKey
	}
	var targets []target
	for _, info := range containers {
		key, attributable := serviceKeyOf(info)
		if !attributable || !known[key.Project] {
			continue // ignored, not invented
		}
		targets = append(targets, target{info: info, key: key})
	}

	stats := make([]ContainerStatsSample, len(targets))
	errs := make([]error, len(targets))
	var wg sync.WaitGroup
	for i, tgt := range targets {
		wg.Add(1)
		go func(i int, tgt target) {
			defer wg.Done()
			stats[i], errs[i] = c.source.ContainerStats(ctx, tgt.info.ID)
		}(i, tgt)
	}
	wg.Wait()

	at := c.now()
	seen := make(map[serviceKey]bool, len(targets))
	c.mu.Lock()
	defer c.mu.Unlock()
	c.swept = true
	c.lastErr = nil // a sweep that got this far gives the port another chance
	for i, tgt := range targets {
		seen[tgt.key] = true
		if errs[i] != nil {
			if c.lastErr == nil {
				c.lastErr = fmt.Errorf("stats for %s: %w", tgt.info.ID, errs[i])
			}
			continue // keep the previous sample rather than inventing one
		}
		prev, had := c.latest[tgt.key]
		observation := newObservation(tgt.key, tgt.info, stats[i], at)
		if had && prev.sample.ContainerID == tgt.info.ID {
			observation.fillRates(prev)
		}
		// else: this container's first sweep — a start or a redeploy, either
		// way there is no honest rate yet and the fields stay NaN.
		c.latest[tgt.key] = observation
		c.minute = append(c.minute, observation.sample)
		// History keeps going across redeploys (one series per Service) and
		// outlives a container that left the listing — a stopped Service's
		// last hour is still its history, and the rollups continue the story
		// beyond it.
		c.history[tgt.key] = appendServiceSample(c.history[tgt.key], observation.sample)
	}
	// The sweep is the truth about what is running: a Service whose container
	// left the listing has no current container. Its history lives in the
	// rollups, not here.
	for key := range c.latest {
		if !seen[key] {
			delete(c.latest, key)
		}
	}
}

// Services snapshots the cache for the status rail and the facts strip,
// sorted by (project, service) for stable rendering.
func (c *ServiceCollector) Services() []ServiceSample {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]serviceKey, 0, len(c.latest))
	for key := range c.latest {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Project != keys[j].Project {
			return keys[i].Project < keys[j].Project
		}
		return keys[i].Service < keys[j].Service
	})
	samples := make([]ServiceSample, 0, len(keys))
	for _, key := range keys {
		samples = append(samples, c.latest[key].sample)
	}
	return samples
}

// CloseMinute hands back one rollup per Service, averaged over the samples
// the closed minute actually saw, and opens the next minute. A minute a
// restart cut short is still a row — however few samples it saw — because the
// screens' `partial` state is the honest display for the gap. A minute in
// which no sweep completed returns nothing: there is nothing to average, and
// the missing minutes are the gap the rollups should show.
func (c *ServiceCollector) CloseMinute() []ServiceSample {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.minute) == 0 {
		return nil
	}
	rollups := serviceMinuteAverages(c.minute)
	c.minute = c.minute[:0]
	return rollups
}

// ServiceWindow copies the Service's last n sweeps, chronological; the
// endpoints serve sub-hour windows from it. A Service with no sweep history
// returns nothing — the rollups are the past for a Service whose container
// has moved on.
func (c *ServiceCollector) ServiceWindow(project, service string, n int) []ServiceSample {
	c.mu.Lock()
	defer c.mu.Unlock()
	history := c.history[serviceKey{Project: project, Service: service}]
	if n > len(history) {
		n = len(history)
	}
	if n <= 0 {
		return nil
	}
	out := make([]ServiceSample, n)
	copy(out, history[len(history)-n:])
	return out
}

// Status reports the last sweep's outcome, for the endpoints' sources block.
// Before the first sweep there is no outcome to report: unavailable with a
// reason, like any source that has not spoken yet.
func (c *ServiceCollector) Status() SourceStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.swept {
		return SourceStatus{Reason: "not swept yet"}
	}
	if c.lastErr == nil {
		return SourceStatus{Available: true}
	}
	return SourceStatus{Reason: c.lastErr.Error()}
}

func (c *ServiceCollector) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.swept = true
	c.lastErr = err
}

// serviceKeyOf attributes a container to a Service off its Compose labels.
func serviceKeyOf(info ContainerInfo) (serviceKey, bool) {
	project := info.Labels[LabelComposeProject]
	service := info.Labels[LabelComposeService]
	if project == "" || service == "" {
		return serviceKey{}, false
	}
	return serviceKey{Project: project, Service: service}, true
}

// newObservation records identity, gauges and the new cumulative baseline;
// rates start at NaN and are only filled in against the same container's
// previous observation.
func newObservation(key serviceKey, info ContainerInfo, stats ContainerStatsSample, at time.Time) serviceObservation {
	observation := serviceObservation{
		cpuTotal:   stats.CPUTotalNanos,
		cpuSystem:  stats.CPUSystemNanos,
		cpuOnline:  stats.CPUOnline,
		netRX:      stats.NetRXBytes,
		netTX:      stats.NetTXBytes,
		blockRead:  stats.BlockReadBytes,
		blockWrite: stats.BlockWriteBytes,
	}
	observation.sample = ServiceSample{
		Project:       key.Project,
		Service:       key.Service,
		ContainerID:   info.ID,
		Name:          info.Name,
		Image:         info.Image,
		State:         info.State,
		Health:        ParseHealth(info.Status),
		Ports:         append([]PortMapping(nil), info.Ports...),
		At:            at,
		CPUPercent:    math.NaN(),
		MemUsedBytes:  memUsedBytes(stats),
		MemLimitBytes: float64(stats.MemLimitBytes),
		NetRXBPS:      math.NaN(),
		NetTXBPS:      math.NaN(),
		BlockReadBPS:  math.NaN(),
		BlockWriteBPS: math.NaN(),
		Pids:          pidsSample(stats.PidsCurrent),
	}
	return observation
}

// fillRates turns the previous sweep's cumulative counters into rates,
// storing nothing on observation itself — the counters to beat are already
// in its fields, set by newObservation.
func (o *serviceObservation) fillRates(prev serviceObservation) {
	dt := o.sample.At.Sub(prev.sample.At).Seconds()
	o.sample.CPUPercent = containerCPUPercent(o.cpuTotal, o.cpuSystem, o.cpuOnline, prev.cpuTotal, prev.cpuSystem)
	o.sample.NetRXBPS = deltaRate(o.netRX, prev.netRX, dt)
	o.sample.NetTXBPS = deltaRate(o.netTX, prev.netTX, dt)
	o.sample.BlockReadBPS = deltaRate(o.blockRead, prev.blockRead, dt)
	o.sample.BlockWriteBPS = deltaRate(o.blockWrite, prev.blockWrite, dt)
}

// memUsedBytes is usage − cache: reclaimable page cache is not memory in
// use, the same rule as the host's MemAvailable. A cache momentarily
// larger than usage (counters racing) saturates at zero.
func memUsedBytes(stats ContainerStatsSample) float64 {
	if stats.MemCacheBytes > stats.MemUsageBytes {
		return 0
	}
	return float64(stats.MemUsageBytes - stats.MemCacheBytes)
}

// pidsSample records pids_stats.current. A live container always holds at
// least one PID, so a zero means the runtime reported nothing — NaN, never a
// zero wearing a measurement's clothes.
func pidsSample(current uint64) float64 {
	if current == 0 {
		return math.NaN()
	}
	return float64(current)
}

// appendServiceSample appends to a Service's history and trims it to the
// fixed capacity, so a long-lived panel's history stays a bounded ring
// however long a Service keeps running.
func appendServiceSample(history []ServiceSample, sample ServiceSample) []ServiceSample {
	history = append(history, sample)
	if len(history) > serviceHistoryCapacity {
		history = history[len(history)-serviceHistoryCapacity:]
	}
	return history
}

// containerCPUPercent applies Docker's own arithmetic: the container's CPU
// delta over the system-wide delta, scaled by the container's online CPUs.
// It is a percentage of one core and can exceed 100%. A system clock that
// stood still or went backwards has no honest denominator and reports NaN.
func containerCPUPercent(curTotal, curSystem, curOnline, prevTotal, prevSystem uint64) float64 {
	if curTotal < prevTotal || curSystem < prevSystem || curSystem == prevSystem {
		return math.NaN()
	}
	dCPU := curTotal - prevTotal
	if dCPU == 0 {
		return 0 // the container's clock ran, the container did nothing
	}
	return float64(dCPU) / float64(curSystem-prevSystem) * float64(curOnline) * 100
}

// serviceMinuteAverages groups one minute's samples by Service and averages
// each metric field over the samples that carried it — NaN fields (a first
// sweep with no rates yet, an unreported pid count) drop out instead of
// poisoning the row, and a field no sample carried stays NaN. The identity
// fields are not averaged: a rollup describes the Service, and for them the
// latest sample wins (a redeploy mid-minute leaves the new container's name).
func serviceMinuteAverages(samples []ServiceSample) []ServiceSample {
	order := make([]serviceKey, 0, len(samples))
	grouped := make(map[serviceKey][]ServiceSample, len(samples))
	for _, sample := range samples {
		key := serviceKey{Project: sample.Project, Service: sample.Service}
		if _, seen := grouped[key]; !seen {
			order = append(order, key)
		}
		grouped[key] = append(grouped[key], sample)
	}

	rollups := make([]ServiceSample, 0, len(order))
	for _, key := range order {
		seen := grouped[key]
		latest := seen[len(seen)-1]
		rollups = append(rollups, ServiceSample{
			Project:       key.Project,
			Service:       key.Service,
			ContainerID:   latest.ContainerID,
			Name:          latest.Name,
			Image:         latest.Image,
			State:         latest.State,
			Health:        latest.Health,
			Ports:         latest.Ports,
			At:            seen[0].At.Truncate(time.Minute),
			CPUPercent:    averageField(seen, func(s ServiceSample) float64 { return s.CPUPercent }),
			MemUsedBytes:  averageField(seen, func(s ServiceSample) float64 { return s.MemUsedBytes }),
			MemLimitBytes: averageField(seen, func(s ServiceSample) float64 { return s.MemLimitBytes }),
			NetRXBPS:      averageField(seen, func(s ServiceSample) float64 { return s.NetRXBPS }),
			NetTXBPS:      averageField(seen, func(s ServiceSample) float64 { return s.NetTXBPS }),
			BlockReadBPS:  averageField(seen, func(s ServiceSample) float64 { return s.BlockReadBPS }),
			BlockWriteBPS: averageField(seen, func(s ServiceSample) float64 { return s.BlockWriteBPS }),
			Pids:          averageField(seen, func(s ServiceSample) float64 { return s.Pids }),
		})
	}
	return rollups
}

// ParseHealth extracts a container's health from its status text —
// "Up 2 minutes (healthy)" — so no docker compose ps process is ever
// spawned for it. Anything that is not a health note (an exit code, say)
// reads as "".
func ParseHealth(status string) string {
	open := strings.LastIndex(status, "(")
	end := strings.LastIndex(status, ")")
	if open < 0 || end < open {
		return ""
	}
	inner := status[open+1 : end]
	switch {
	case inner == "healthy" || inner == "unhealthy":
		return inner
	case strings.HasPrefix(inner, "health:"):
		return strings.TrimSpace(strings.TrimPrefix(inner, "health:"))
	}
	return ""
}
