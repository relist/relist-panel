// Package hostmetrics samples host metrics in-process, inside the panel
// binary (ADR-0006): four /proc files per tick plus statfs on the data dir,
// kept as delta rates in a one-hour ring buffer, with per-Service container
// sweeps delegated to a ContainerSource (ADR-0003's consumer-declared port).
// History is keyed by Service, never by Container (CONTEXT.md): a deploy
// mints a new container, and the charts must survive it.
//
// The proc root is a parameter, never a constant baked into the code:
// production passes /host/proc, tests pass a t.TempDir() fixture, and
// nothing reads /proc at package init or in NewSampler.
package hostmetrics

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"relist-panel/internal/safely"
)

const (
	// DefaultProcRoot is where the install mounts the host's /proc
	// (ADR-0006). Network counters must be read from <root>/1/net/dev —
	// PID 1's network namespace is the host's, while <root>/net/dev is
	// /proc/self/net/dev and silently reports the reader's own counters
	// under the host's interface names.
	DefaultProcRoot = "/host/proc"

	// DefaultInterval is the host sampling cadence.
	DefaultInterval = 10 * time.Second

	// SweepInterval is the container sweep cadence (spec: 30–60s is plenty
	// for a panel; the 2s wall time of a stats sweep is Docker sampling a
	// delta window, not work). The loop offers SweepContainers on it, the
	// collector sizes its history by it, and the endpoints' window table
	// counts Service samples with it.
	SweepInterval = 30 * time.Second
)

// Source names used in the availability map.
const (
	SourceCPU        = "cpu"
	SourceMemory     = "memory"
	SourceDiskSpace  = "disk_space"
	SourceBlockIO    = "block_io"
	SourceNetwork    = "network"
	SourceDockerDisk = "docker_disk"
)

var hostSources = []string{SourceCPU, SourceMemory, SourceDiskSpace, SourceBlockIO, SourceNetwork, SourceDockerDisk}

// HostSample is one 10-second host observation. Gauge fields (memory, disk
// space) are NaN when their source could not be read. Rate fields are NaN
// whenever there is no honest rate: the first tick of a counter's life, a
// counter that wrapped or was reset, or an unreadable source. A missing
// reading is never a zero — "the panel cannot read this" must stay
// distinguishable from "the host is idle".
type HostSample struct {
	At              time.Time
	CPUPercent      float64
	MemUsedBytes    float64
	MemTotalBytes   float64
	DiskFreeBytes   float64
	DiskTotalBytes  float64
	DockerDiskBytes float64 // docker system df, carried forward between refreshes
	DiskReadBPS     float64 // bytes per second
	DiskWriteBPS    float64
	NetRXBPS        float64
	NetTXBPS        float64
}

// SourceStatus says whether a metrics source was readable at the last tick,
// and why not when it was not; screens render unavailable-with-a-reason
// instead of a zeroed line.
type SourceStatus struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// Config wires a Sampler to its environment.
type Config struct {
	Interval time.Duration // zero means DefaultInterval
	ProcRoot string        // zero means DefaultProcRoot
	DataDir  string        // statfs target: the panel's data dir (ADR-0002)
	Now      func() time.Time

	// SweepContainers, when set, runs every SweepInterval on its own goroutine
	// — the ~2s container sweep must never block the 10s host tick — and a
	// sweep still in flight when its turn comes round again is skipped, not
	// queued. Ticket 02 wires the container collector here.
	SweepContainers func(ctx context.Context)
	// OnMinuteClose, when set, receives a rollup averaged over the samples
	// the closed minute actually saw (ticket 03 persists it to SQLite).
	OnMinuteClose func(HostSample)
	// RefreshDockerDisk, when set, runs every 5 minutes (`docker system df`,
	// cached — 0.67s per call, too dear for a 10s cadence), on its own
	// goroutine under the same never-queue rule. Its outcome belongs in
	// SetDockerDisk.
	RefreshDockerDisk func(ctx context.Context)
}

// Sampler is the panel's one metrics loop (ADR-0006): a single goroutine
// with a single time.Ticker at the host's cadence; the 30s container sweep,
// the minute-close rollup and the 5-minute docker df refresh ride the same
// ticker, firing when their turn comes round.
type Sampler struct {
	cfg  Config
	ring *Ring
	now  func() time.Time

	mu      sync.RWMutex
	sources map[string]SourceStatus
	// dockerDisk is the last successful `docker system df` reading, carried
	// forward into every tick between refreshes — the series is a step, not
	// a 10-second sample. NaN until the first refresh succeeds.
	dockerDisk float64

	// started guards the one sampling loop: Start is a compare-and-swap, so
	// no wiring mistake can grow a second goroutine doubling every counter.
	started atomic.Bool
	done    chan struct{}

	// Loop-owned state: only tick touches these, and tick runs on one
	// goroutine.
	prevCPU   CPUTimes
	haveCPU   bool
	prevDisk  diskTotals
	haveDisk  bool
	prevNet   netTotals
	haveNet   bool
	lastAt    time.Time
	tickN     uint64
	lastClose time.Time

	// The sweep and the df refresh run off-loop (a stats sweep takes ~2s of
	// wall time); at most one of each is ever in flight.
	sweepInFlight atomic.Bool
	dfInFlight    atomic.Bool
	hooks         sync.WaitGroup
}

type diskTotals struct{ Read, Write uint64 }
type netTotals struct{ RX, TX uint64 }

// NewSampler constructs the sampler. It reads nothing: every /proc access
// happens in Run's ticks, against cfg.ProcRoot.
func NewSampler(cfg Config) *Sampler {
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.ProcRoot == "" {
		cfg.ProcRoot = DefaultProcRoot
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	sources := make(map[string]SourceStatus, len(hostSources))
	for _, name := range hostSources {
		sources[name] = SourceStatus{Reason: "not sampled yet"}
	}
	return &Sampler{
		cfg:        cfg,
		ring:       NewRing(int(time.Hour / cfg.Interval)),
		now:        cfg.Now,
		sources:    sources,
		dockerDisk: math.NaN(),
		done:       make(chan struct{}),
	}
}

// Start launches the sampling loop on its own goroutine — once for the
// panel's lifetime. A second call on an already-started sampler is an error,
// so no wiring mistake can grow a duplicate loop.
func (s *Sampler) Start(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return fmt.Errorf("sampler already started")
	}
	go func() {
		defer close(s.done)
		s.Run(ctx)
	}()
	return nil
}

// Wait blocks until the started sampler's loop has returned and its in-flight
// hooks have drained — the shutdown order for main: cancel the context, wait,
// then exit. It blocks forever on a sampler that was never started.
func (s *Sampler) Wait() {
	<-s.done
}

// Run drives the sampler until ctx is cancelled: one goroutine, one
// time.Ticker, for the panel's lifetime. Shutdown waits out any hook still
// in flight (its context is already cancelled).
func (s *Sampler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.hooks.Wait()
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// waitIdle blocks until every off-loop hook the loop launched has finished.
// Tests drive ticks directly and settle the async hooks with it — no test
// sleeps.
func (s *Sampler) waitIdle() {
	s.hooks.Wait()
}

// Window copies the last n samples out of the ring, chronological; the
// endpoints serve 10-second-resolution windows from it.
func (s *Sampler) Window(n int) []HostSample {
	return s.ring.AppendLast(nil, n)
}

// Sources snapshots the availability map: what was readable at the last
// tick, and why not.
func (s *Sampler) Sources() map[string]SourceStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]SourceStatus, len(s.sources))
	for name, status := range s.sources {
		out[name] = status
	}
	return out
}

// SetDockerDisk records a `docker system df` refresh's outcome, however it
// was scheduled. On success the reading is carried forward into every tick
// until the next one — the Docker disk series is a step. On failure the
// source is marked unavailable with the reason while the last reading stays
// on the chart (it is minutes old, and the sources block says so); before
// the first success there is no reading at all and the field stays NaN.
func (s *Sampler) SetDockerDisk(usedBytes float64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.sources[SourceDockerDisk] = SourceStatus{Reason: err.Error()}
		return
	}
	s.dockerDisk = usedBytes
	s.sources[SourceDockerDisk] = SourceStatus{Available: true}
}

func (s *Sampler) carriedDockerDisk() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dockerDisk
}

// tick performs one sampling pass. Unexported so tests drive it directly —
// no test sleeps through a real ticker.
func (s *Sampler) tick(ctx context.Context) {
	// One bad tick is skipped, not fatal: the loop goroutine has no other
	// recover, and tickN still advances so the due counters keep their turns.
	safely.Do("host metrics sampling", func() {
		at := s.now()
		sample := nanSample(at)
		dt := at.Sub(s.lastAt).Seconds()

		s.sampleCPU(&sample)
		s.sampleMemory(&sample)
		s.sampleDiskSpace(&sample)
		s.sampleBlockIO(&sample, dt)
		s.sampleNetwork(&sample, dt)
		sample.DockerDiskBytes = s.carriedDockerDisk()

		s.ring.Push(sample)
		s.lastAt = at
	})
	s.tickN++

	// The due counters: every job rides this one ticker, when its turn
	// comes round (spec, "one goroutine, one tick"). The sweep and the df
	// refresh are slow by nature (Docker samples a delta window; the daemon
	// aggregates), so they run off-loop — the next host tick never waits
	// behind them — and a slow turn is skipped, never queued.
	if s.cfg.SweepContainers != nil && s.tickN%s.ticksPer(SweepInterval) == 0 &&
		s.sweepInFlight.CompareAndSwap(false, true) {
		s.hooks.Add(1)
		go func() {
			defer s.hooks.Done()
			defer s.sweepInFlight.Store(false)
			safely.Do("container sweep", func() { s.cfg.SweepContainers(ctx) })
		}()
	}
	if s.tickN%s.ticksPer(time.Minute) == 0 {
		s.closeMinute()
	}
	if s.cfg.RefreshDockerDisk != nil && s.tickN%s.ticksPer(5*time.Minute) == 0 &&
		s.dfInFlight.CompareAndSwap(false, true) {
		s.hooks.Add(1)
		go func() {
			defer s.hooks.Done()
			defer s.dfInFlight.Store(false)
			safely.Do("docker disk refresh", func() { s.cfg.RefreshDockerDisk(ctx) })
		}()
	}
}

// nanSample is a sample whose every reading starts as "no data": each
// sampler overwrites the fields it actually read, so a source that failed
// leaves NaN behind, never a zero wearing a real measurement's clothes.
func nanSample(at time.Time) HostSample {
	nan := math.NaN()
	return HostSample{
		At:              at,
		CPUPercent:      nan,
		MemUsedBytes:    nan,
		MemTotalBytes:   nan,
		DiskFreeBytes:   nan,
		DiskTotalBytes:  nan,
		DockerDiskBytes: nan,
		DiskReadBPS:     nan,
		DiskWriteBPS:    nan,
		NetRXBPS:        nan,
		NetTXBPS:        nan,
	}
}

// ticksPer rounds a duration to a whole number of ticks at the configured
// interval (30s → 3, a minute → 6, 5m → 30 at the 10s cadence).
func (s *Sampler) ticksPer(window time.Duration) uint64 {
	ticks := (window + s.cfg.Interval/2) / s.cfg.Interval
	if ticks < 1 {
		ticks = 1
	}
	return uint64(ticks)
}

// closeMinute hands OnMinuteClose a rollup averaged over the samples
// accumulated since the previous close — exactly the samples that minute
// saw, however few.
func (s *Sampler) closeMinute() {
	if s.cfg.OnMinuteClose == nil {
		return
	}
	seen := s.ring.AppendSince(nil, s.lastClose)
	if len(seen) == 0 {
		return
	}
	s.lastClose = seen[len(seen)-1].At
	safely.Do("minute close", func() { s.cfg.OnMinuteClose(MinuteAverage(seen)) })
}

func (s *Sampler) sampleCPU(sample *HostSample) {
	text, err := s.readProc("stat")
	if err != nil {
		s.markUnavailable(SourceCPU, err)
		return
	}
	times, err := ParseCPUStat(text)
	if err != nil {
		s.markUnavailable(SourceCPU, fmt.Errorf("parse %s: %w", s.procPath("stat"), err))
		return
	}
	s.markAvailable(SourceCPU)
	if s.haveCPU {
		sample.CPUPercent = cpuPercent(times, s.prevCPU)
	} else {
		sample.CPUPercent = math.NaN()
	}
	s.prevCPU, s.haveCPU = times, true
}

func (s *Sampler) sampleMemory(sample *HostSample) {
	text, err := s.readProc("meminfo")
	if err != nil {
		s.markUnavailable(SourceMemory, err)
		return
	}
	info, err := ParseMemInfo(text)
	if err != nil {
		s.markUnavailable(SourceMemory, fmt.Errorf("parse %s: %w", s.procPath("meminfo"), err))
		return
	}
	s.markAvailable(SourceMemory)
	sample.MemUsedBytes = float64(info.TotalBytes - info.AvailableBytes)
	sample.MemTotalBytes = float64(info.TotalBytes)
}

// sampleDiskSpace reads the data dir's filesystem with statfs — a host
// filesystem by ADR-0002's identical-path bind mount. Bavail, not Bfree:
// what an unprivileged process can actually still claim.
func (s *Sampler) sampleDiskSpace(sample *HostSample) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(s.cfg.DataDir, &st); err != nil {
		s.markUnavailable(SourceDiskSpace, fmt.Errorf("statfs %s: %w", s.cfg.DataDir, err))
		return
	}
	s.markAvailable(SourceDiskSpace)
	sample.DiskTotalBytes = float64(st.Blocks) * float64(st.Bsize)
	sample.DiskFreeBytes = float64(st.Bavail) * float64(st.Bsize)
}

func (s *Sampler) sampleBlockIO(sample *HostSample, dt float64) {
	text, err := s.readProc("diskstats")
	if err != nil {
		s.markUnavailable(SourceBlockIO, err)
		return
	}
	devices, err := ParseDiskStats(text)
	if err != nil {
		s.markUnavailable(SourceBlockIO, fmt.Errorf("parse %s: %w", s.procPath("diskstats"), err))
		return
	}
	s.markAvailable(SourceBlockIO)
	var totals diskTotals
	for _, device := range devices {
		totals.Read += device.ReadBytes
		totals.Write += device.WriteBytes
	}
	if s.haveDisk {
		sample.DiskReadBPS = deltaRate(totals.Read, s.prevDisk.Read, dt)
		sample.DiskWriteBPS = deltaRate(totals.Write, s.prevDisk.Write, dt)
	} else {
		sample.DiskReadBPS, sample.DiskWriteBPS = math.NaN(), math.NaN()
	}
	s.prevDisk, s.haveDisk = totals, true
}

func (s *Sampler) sampleNetwork(sample *HostSample, dt float64) {
	rel := filepath.Join("1", "net", "dev") // PID 1's namespace is the host's
	text, err := s.readProc(rel)
	if err != nil {
		s.markUnavailable(SourceNetwork, err)
		return
	}
	interfaces, err := ParseNetDev(text)
	if err != nil {
		s.markUnavailable(SourceNetwork, fmt.Errorf("parse %s: %w", s.procPath(rel), err))
		return
	}
	s.markAvailable(SourceNetwork)
	var totals netTotals
	for _, iface := range interfaces {
		totals.RX += iface.RXBytes
		totals.TX += iface.TXBytes
	}
	if s.haveNet {
		sample.NetRXBPS = deltaRate(totals.RX, s.prevNet.RX, dt)
		sample.NetTXBPS = deltaRate(totals.TX, s.prevNet.TX, dt)
	} else {
		sample.NetRXBPS, sample.NetTXBPS = math.NaN(), math.NaN()
	}
	s.prevNet, s.haveNet = totals, true
}

// procPath resolves a path under the configured proc root.
func (s *Sampler) procPath(rel string) string {
	return filepath.Join(s.cfg.ProcRoot, rel)
}

// readProc reads one /proc file under the configured root.
func (s *Sampler) readProc(rel string) (string, error) {
	path := s.procPath(rel)
	text, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	return string(text), nil
}

// cpuPercent turns two cumulative jiffie counters into a busy percentage. A
// total that stood still is 0% — the host did nothing. A counter that went
// backwards has no honest rate and reports NaN.
func cpuPercent(cur, prev CPUTimes) float64 {
	if cur.Total < prev.Total || cur.Busy < prev.Busy {
		return math.NaN()
	}
	dTotal := cur.Total - prev.Total
	if dTotal == 0 {
		return 0
	}
	return float64(cur.Busy-prev.Busy) / float64(dTotal) * 100
}

// deltaRate converts a cumulative byte counter and its previous reading into
// bytes per second over dt. Equal counters are a real zero (nothing moved);
// a counter that went backwards — a wrap, or a device removed and re-added —
// has no honest rate and reports NaN.
func deltaRate(cur, prev uint64, dt float64) float64 {
	if cur < prev || dt <= 0 {
		return math.NaN()
	}
	return float64(cur-prev) / dt
}

// MinuteAverage averages each field over the samples that carry it. NaN
// fields (a first tick with no rate yet, an unreadable source) drop out
// instead of poisoning the row; a field no sample carried stays NaN.
func MinuteAverage(samples []HostSample) HostSample {
	if len(samples) == 0 {
		return HostSample{}
	}
	average := HostSample{At: samples[0].At.Truncate(time.Minute)}
	average.CPUPercent = averageField(samples, func(s HostSample) float64 { return s.CPUPercent })
	average.MemUsedBytes = averageField(samples, func(s HostSample) float64 { return s.MemUsedBytes })
	average.MemTotalBytes = averageField(samples, func(s HostSample) float64 { return s.MemTotalBytes })
	average.DiskFreeBytes = averageField(samples, func(s HostSample) float64 { return s.DiskFreeBytes })
	average.DiskTotalBytes = averageField(samples, func(s HostSample) float64 { return s.DiskTotalBytes })
	average.DockerDiskBytes = averageField(samples, func(s HostSample) float64 { return s.DockerDiskBytes })
	average.DiskReadBPS = averageField(samples, func(s HostSample) float64 { return s.DiskReadBPS })
	average.DiskWriteBPS = averageField(samples, func(s HostSample) float64 { return s.DiskWriteBPS })
	average.NetRXBPS = averageField(samples, func(s HostSample) float64 { return s.NetRXBPS })
	average.NetTXBPS = averageField(samples, func(s HostSample) float64 { return s.NetTXBPS })
	return average
}

func averageField[T any](samples []T, get func(T) float64) float64 {
	var (
		sum float64
		n   int
	)
	for _, sample := range samples {
		if value := get(sample); !math.IsNaN(value) {
			sum += value
			n++
		}
	}
	if n == 0 {
		return math.NaN()
	}
	return sum / float64(n)
}

func (s *Sampler) markAvailable(source string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sources[source] = SourceStatus{Available: true}
}

func (s *Sampler) markUnavailable(source string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sources[source] = SourceStatus{Reason: err.Error()}
}
