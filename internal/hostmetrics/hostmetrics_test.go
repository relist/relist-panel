package hostmetrics

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Fixture variants for two ticks ten seconds apart. The second carries
// hand-checked counter increments so rates are exact at dt = 10s:
//
//	CPU:    busy 660→960, total 5160→5960            → 300/800    = 37.5%
//	Disk:   sda sectors +2560 read, +5120 written    → 1310720 B/s, 2621440 B/s
//	Net:    eth0 rx +30000, tx +50000                → 3000 B/s, 5000 B/s
//	Memory: MemAvailable 8192→7680 kB                → used 8912896 B (a gauge)
const (
	v2Stat     = "cpu  300 200 400 4400 600 60 0 0 70 80\n"
	v2Meminfo  = "MemTotal:       16384 kB\nMemAvailable:    7680 kB\n"
	v2Diskstat = "   8       0 sda 4321 0 126016 789 876 0 659441 987 0 0 0 0 0 0\n" +
		"   8      32 sdaa 50 0 60 7 8 0 90 1 0 0 0 0 0 0\n" +
		" 259       0 nvme0n1 1000 0 20000 100 3000 0 40000 200 0 0 0 0 0\n" +
		" 179       0 mmcblk0 500 0 1000 10 200 0 400 5 0 0 0 0 0\n" +
		" 254       1 vdb 77 0 88 9 66 0 77 8 0 0 0 0 0\n" +
		"   3       0 hda 111 0 222 3 44 0 55 6 0 0 0 0 0\n"
)

// v2Netdev is the golden set with eth0 grown by 30000 rx / 50000 tx bytes.
var v2Netdev = netdevLine("eth0", 5273331899, 698050000) +
	netdevLine("eth1", 1000, 2000) +
	netdevLine("docker0", 5000, 6000)

// A just-booted host: tiny counters across every file.
const (
	freshStat     = "cpu  12 0 8 240 0 0 0 0 0 0\n"
	freshMeminfo  = "MemTotal:        1024 kB\nMemAvailable:     512 kB\n"
	freshDiskstat = "   8       0 vda 3 0 6 1 2 0 4 1 0 0 0 0 0\n"
)

func netdevLine(name string, rx, tx uint64) string {
	return name + ": " +
		strconv.FormatUint(rx, 10) + " 1 0 0 0 0 0 0 " + strconv.FormatUint(tx, 10) + " 1 0 0 0 0 0 0\n"
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) advance(d time.Duration) {
	c.now = c.now.Add(d)
}

// writeProcTree lays out a /proc root the sampler can read: stat, meminfo and
// diskstats at the root, net/dev under PID 1 — production reads
// /host/proc/1/net/dev because /host/proc/net/dev would be the panel's own
// namespace (ADR-0006).
func writeProcTree(t *testing.T, root, stat, meminfo, diskstats, netdev string) {
	t.Helper()
	files := map[string]string{
		"stat":      stat,
		"meminfo":   meminfo,
		"diskstats": diskstats,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s fixture: %v", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "1", "net"), 0o755); err != nil {
		t.Fatalf("mkdir 1/net: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "1", "net", "dev"), []byte(netdev), 0o644); err != nil {
		t.Fatalf("write net/dev fixture: %v", err)
	}
}

func newTestSampler(t *testing.T, clock *fakeClock, mutate func(*Config)) *Sampler {
	t.Helper()
	root := t.TempDir()
	writeProcTree(t, root, loadFixture(t, "stat.golden"), loadFixture(t, "meminfo.golden"),
		loadFixture(t, "diskstats.golden"), loadFixture(t, "netdev.golden"))
	cfg := Config{
		ProcRoot: root,
		DataDir:  t.TempDir(),
		Now:      clock.Now,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return NewSampler(cfg)
}

// Before the first tick nothing has been read — every source is unavailable
// with a reason, not "available" by assumption.
func TestSamplerSourcesBeforeFirstTick(t *testing.T) {
	s := newTestSampler(t, &fakeClock{now: baseTime()}, nil)
	sources := s.Sources()
	for _, name := range []string{SourceCPU, SourceMemory, SourceDiskSpace, SourceBlockIO, SourceNetwork} {
		status, ok := sources[name]
		if !ok {
			t.Errorf("sources[%q] missing", name)
		} else if status.Available {
			t.Errorf("sources[%q] available before the first tick", name)
		} else if status.Reason == "" {
			t.Errorf("sources[%q] unavailable without a reason", name)
		}
	}
}

// The first tick has gauges but no rates to report: a rate needs two ticks,
// and reporting zero would dress "not measured yet" up as "the host is idle".
func TestSamplerFirstTickHasGaugesButNoRates(t *testing.T) {
	clock := &fakeClock{now: baseTime()}
	s := newTestSampler(t, clock, nil)
	s.tick(context.Background())

	sources := s.Sources()
	for _, name := range []string{SourceCPU, SourceMemory, SourceDiskSpace, SourceBlockIO, SourceNetwork} {
		if !sources[name].Available {
			t.Errorf("sources[%q] unavailable after first tick: %s", name, sources[name].Reason)
		}
	}

	window := s.Window(1)
	if len(window) != 1 {
		t.Fatalf("Window(1) returned %d samples, want 1", len(window))
	}
	sample := window[0]
	for name, got := range map[string]float64{
		"CPUPercent":   sample.CPUPercent,
		"DiskReadBPS":  sample.DiskReadBPS,
		"DiskWriteBPS": sample.DiskWriteBPS,
		"NetRXBPS":     sample.NetRXBPS,
		"NetTXBPS":     sample.NetTXBPS,
	} {
		if !math.IsNaN(got) {
			t.Errorf("%s = %v on the first tick, want NaN (no rate rather than zero)", name, got)
		}
	}
	if sample.MemUsedBytes != (16384-8192)*1024 {
		t.Errorf("MemUsedBytes = %v, want %v", sample.MemUsedBytes, (16384-8192)*1024)
	}
	if sample.MemTotalBytes != 16384*1024 {
		t.Errorf("MemTotalBytes = %v, want %v", sample.MemTotalBytes, 16384*1024)
	}
	if sample.DiskTotalBytes <= 0 || sample.DiskFreeBytes <= 0 || sample.DiskFreeBytes > sample.DiskTotalBytes {
		t.Errorf("statfs on the data dir produced total=%v free=%v", sample.DiskTotalBytes, sample.DiskFreeBytes)
	}
}

// Ten seconds later, with bigger counters in the fixture files, every rate is
// the exact hand-computed delta over the ten seconds that passed.
func TestSamplerSecondTickStoresDeltaRates(t *testing.T) {
	clock := &fakeClock{now: baseTime()}
	s := newTestSampler(t, clock, nil)
	s.tick(context.Background())

	clock.advance(10 * time.Second)
	writeProcTree(t, s.cfg.ProcRoot, v2Stat, v2Meminfo, v2Diskstat, v2Netdev)
	s.tick(context.Background())

	sample := s.Window(1)[0]
	if sample.CPUPercent != 37.5 {
		t.Errorf("CPUPercent = %v, want 37.5", sample.CPUPercent)
	}
	if sample.DiskReadBPS != 131072 {
		t.Errorf("DiskReadBPS = %v, want 131072", sample.DiskReadBPS)
	}
	if sample.DiskWriteBPS != 262144 {
		t.Errorf("DiskWriteBPS = %v, want 262144", sample.DiskWriteBPS)
	}
	if sample.NetRXBPS != 3000 {
		t.Errorf("NetRXBPS = %v, want 3000", sample.NetRXBPS)
	}
	if sample.NetTXBPS != 5000 {
		t.Errorf("NetTXBPS = %v, want 5000", sample.NetTXBPS)
	}
	if sample.MemUsedBytes != (16384-7680)*1024 {
		t.Errorf("MemUsedBytes = %v, want %v", sample.MemUsedBytes, (16384-7680)*1024)
	}
}

// A freshly booted host whose counters did not move between ticks: the file
// was readable, so zero is the honest rate — unlike the first tick's NaN.
func TestSamplerFreshlyBootedZeroDeltaReportsZero(t *testing.T) {
	clock := &fakeClock{now: baseTime()}
	root := t.TempDir()
	writeProcTree(t, root, freshStat, freshMeminfo, freshDiskstat, netdevLine("eth0", 100, 80))
	s := NewSampler(Config{ProcRoot: root, DataDir: t.TempDir(), Now: clock.Now})

	s.tick(context.Background())
	clock.advance(10 * time.Second)
	writeProcTree(t, root, freshStat, freshMeminfo, freshDiskstat, netdevLine("eth0", 100, 80))
	s.tick(context.Background())

	sample := s.Window(1)[0]
	for name, got := range map[string]float64{
		"CPUPercent":   sample.CPUPercent,
		"DiskReadBPS":  sample.DiskReadBPS,
		"DiskWriteBPS": sample.DiskWriteBPS,
		"NetRXBPS":     sample.NetRXBPS,
		"NetTXBPS":     sample.NetTXBPS,
	} {
		if got != 0 {
			t.Errorf("%s = %v on a zero delta, want 0", name, got)
		}
	}
	for _, name := range []string{SourceCPU, SourceMemory, SourceBlockIO, SourceNetwork} {
		if !s.Sources()[name].Available {
			t.Errorf("sources[%q] unavailable: %s", name, s.Sources()[name].Reason)
		}
	}
}

// A counter that goes backwards (wrapped, or a device that was removed and
// re-added) has no honest rate: NaN for that tick, and the source itself is
// still readable.
func TestSamplerCounterWrapReportsNoRate(t *testing.T) {
	clock := &fakeClock{now: baseTime()}
	s := newTestSampler(t, clock, nil)

	writeProcTree(t, s.cfg.ProcRoot, v2Stat, loadFixture(t, "meminfo.golden"),
		v2Diskstat, v2Netdev)
	s.tick(context.Background())

	clock.advance(10 * time.Second)
	writeProcTree(t, s.cfg.ProcRoot, loadFixture(t, "stat.golden"), loadFixture(t, "meminfo.golden"),
		loadFixture(t, "diskstats.golden"), loadFixture(t, "netdev.golden"))
	s.tick(context.Background())

	sample := s.Window(1)[0]
	for name, got := range map[string]float64{
		"CPUPercent":   sample.CPUPercent,
		"DiskReadBPS":  sample.DiskReadBPS,
		"DiskWriteBPS": sample.DiskWriteBPS,
		"NetRXBPS":     sample.NetRXBPS,
		"NetTXBPS":     sample.NetTXBPS,
	} {
		if !math.IsNaN(got) {
			t.Errorf("%s = %v across a counter wrap, want NaN", name, got)
		}
	}
	for _, name := range []string{SourceCPU, SourceBlockIO, SourceNetwork} {
		if !s.Sources()[name].Available {
			t.Errorf("sources[%q] marked unavailable by a mere wrap: %s", name, s.Sources()[name].Reason)
		}
	}
}

// One missing source never blanks the screen: the tick still lands in the
// ring with its other fields intact, and the failure lands in the sources
// map with a reason.
func TestSamplerUnreadableSourceKeepsSampleIntact(t *testing.T) {
	clock := &fakeClock{now: baseTime()}
	s := newTestSampler(t, clock, nil)
	s.tick(context.Background())

	// CPU becomes unreadable, everything else still reads.
	if err := os.WriteFile(filepath.Join(s.cfg.ProcRoot, "stat"), []byte("garbage\n"), 0o644); err != nil {
		t.Fatalf("corrupt stat fixture: %v", err)
	}
	clock.advance(10 * time.Second)
	s.tick(context.Background())

	cpu := s.Sources()[SourceCPU]
	if cpu.Available || cpu.Reason == "" {
		t.Errorf("sources[cpu] = %+v, want unavailable with a reason", cpu)
	}
	sample := s.Window(1)[0]
	if !math.IsNaN(sample.CPUPercent) {
		t.Errorf("CPUPercent = %v with the source unreadable, want NaN", sample.CPUPercent)
	}
	if sample.MemUsedBytes != (16384-8192)*1024 {
		t.Errorf("MemUsedBytes = %v, want the memory gauge to survive the cpu failure", sample.MemUsedBytes)
	}

	// Now memory goes away too; the earlier cpu failure sticks.
	if err := os.Remove(filepath.Join(s.cfg.ProcRoot, "meminfo")); err != nil {
		t.Fatalf("remove meminfo fixture: %v", err)
	}
	clock.advance(10 * time.Second)
	s.tick(context.Background())

	mem := s.Sources()[SourceMemory]
	if mem.Available || mem.Reason == "" {
		t.Errorf("sources[memory] = %+v, want unavailable with a reason", mem)
	}
	if got := s.Window(1)[0]; !math.IsNaN(got.MemTotalBytes) {
		t.Errorf("MemTotalBytes = %v after meminfo went away, want NaN", got.MemTotalBytes)
	}
	if got := len(s.Window(360)); got != 3 {
		t.Errorf("ring holds %d samples after three ticks, want 3", got)
	}
}

func TestSamplerSourcesSnapshotIsACopy(t *testing.T) {
	s := newTestSampler(t, &fakeClock{now: baseTime()}, nil)
	sources := s.Sources()
	sources[SourceCPU] = SourceStatus{Reason: "clobbered"}
	if s.Sources()[SourceCPU].Reason == "clobbered" {
		t.Error("mutating the returned sources map changed the sampler's own")
	}
}

// The minute close averages the samples that minute actually saw: the first
// tick's missing CPU rate drops out instead of counting as zero, and the
// memory gauges all count.
func TestSamplerMinuteRollupAveragesSeenSamples(t *testing.T) {
	clock := &fakeClock{now: baseTime()}
	var rollups []HostSample
	s := newTestSampler(t, clock, func(cfg *Config) {
		cfg.OnMinuteClose = func(rollup HostSample) { rollups = append(rollups, rollup) }
	})

	// Tick i (1..6) grows user by 100 and idle by 300 per tick, so from the
	// second tick on every delta is busy 100 / total 400 = 25%. MemAvailable
	// is (8192−1024i) kB, i.e. used 9216..14336 kB.
	for i := 1; i <= 6; i++ {
		stat := "cpu  " + strconv.FormatUint(uint64(560+100*i), 10) + " 200 300 " +
			strconv.FormatUint(uint64(4000+300*i), 10) + " 500 60 0 0 70 80\n"
		meminfo := "MemTotal:       16384 kB\nMemAvailable:   " + strconv.FormatUint(uint64(8192-1024*i), 10) + " kB\n"
		writeProcTree(t, s.cfg.ProcRoot, stat, meminfo,
			loadFixture(t, "diskstats.golden"), loadFixture(t, "netdev.golden"))
		s.tick(context.Background())
		clock.advance(10 * time.Second)
	}

	if len(rollups) != 1 {
		t.Fatalf("minute closed %d times in six ticks, want 1", len(rollups))
	}
	rollup := rollups[0]
	// CPU: the first tick had no rate, so the average runs over ticks 2..6 —
	// five samples of 25%, not six samples of 20.83%.
	if rollup.CPUPercent != 25 {
		t.Errorf("rollup CPUPercent = %v, want 25 (first tick's NaN dropped)", rollup.CPUPercent)
	}
	if rollup.MemUsedBytes != 11776*1024 {
		t.Errorf("rollup MemUsedBytes = %v, want %v", rollup.MemUsedBytes, 11776*1024)
	}
	if !rollup.At.Equal(baseTime()) {
		t.Errorf("rollup At = %v, want the closed minute's start %v", rollup.At, baseTime())
	}
}

func TestSamplerDueCountersFireWhenTheirTurnComesRound(t *testing.T) {
	clock := &fakeClock{now: baseTime()}
	var sweeps, closes, refreshes int
	s := newTestSampler(t, clock, func(cfg *Config) {
		cfg.SweepContainers = func(context.Context) { sweeps++ }
		cfg.OnMinuteClose = func(HostSample) { closes++ }
		cfg.RefreshDockerDisk = func(context.Context) { refreshes++ }
	})

	for i := 0; i < 30; i++ {
		s.tick(context.Background())
		clock.advance(10 * time.Second)
		s.waitIdle() // async hooks: let each turn finish before the next
	}

	if sweeps != 10 {
		t.Errorf("container sweep fired %d times in 30 ticks, want every 3rd (10)", sweeps)
	}
	if closes != 5 {
		t.Errorf("minute close fired %d times in 30 ticks, want every 6th (5)", closes)
	}
	if refreshes != 1 {
		t.Errorf("docker df refresh fired %d times in 30 ticks, want every 30th (1)", refreshes)
	}
}

// The 30s sweep must never block the 10s host tick: it runs on its own
// goroutine, and a sweep still in flight is skipped when its turn comes
// round again, not queued up.
func TestContainerSweepDoesNotBlockHostTick(t *testing.T) {
	clock := &fakeClock{now: baseTime()}
	entered := make(chan struct{})
	release := make(chan struct{})
	source := &blockingSource{onStats: func() {
		close(entered)
		<-release
	}}
	known := func(context.Context) (map[string]bool, error) { return map[string]bool{"p-e-c": true}, nil }
	collector := NewServiceCollector(source, known, clock.Now)

	s := newTestSampler(t, clock, func(cfg *Config) {
		cfg.SweepContainers = collector.Sweep
	})

	// Ticks 1 and 2 are not the sweep's turn; tick 3 launches it.
	s.tick(context.Background())
	clock.advance(10 * time.Second)
	s.tick(context.Background())
	clock.advance(10 * time.Second)
	s.tick(context.Background())
	<-entered

	// Three more host ticks land in the ring while the sweep is still stuck,
	// and tick 6 — the sweep's turn again — is skipped, not queued.
	for i := 0; i < 3; i++ {
		clock.advance(10 * time.Second)
		s.tick(context.Background())
	}
	if got := len(s.Window(360)); got != 6 {
		t.Fatalf("ring holds %d samples while a sweep is in flight, want 6 — the host tick blocked", got)
	}
	if got := source.statsCalls(); got != 1 {
		t.Errorf("%d stats calls started while one sweep was in flight, want 1 (skipped, not queued)", got)
	}

	close(release)
	s.waitIdle()
	if got := len(collector.Services()); got != 1 {
		t.Errorf("Services() returned %d entries after the sweep finished, want 1", got)
	}
}

// blockingSource is the minimal fake for the non-blocking test: one known
// container whose single stats call hangs until the test releases it.
type blockingSource struct {
	onStats func()
	mu      sync.Mutex
	calls   int
}

func (b *blockingSource) ListContainers(context.Context) ([]ContainerInfo, error) {
	return []ContainerInfo{{
		ID:     "c1",
		Name:   "p-e-c-web-1",
		Image:  "nginx:1.27",
		State:  "running",
		Status: "Up 2 minutes (healthy)",
		Labels: map[string]string{LabelComposeProject: "p-e-c", LabelComposeService: "web"},
	}}, nil
}

func (b *blockingSource) ContainerStats(context.Context, string) (ContainerStatsSample, error) {
	b.mu.Lock()
	b.calls++
	b.mu.Unlock()
	b.onStats()
	return ContainerStatsSample{MemUsageBytes: 100, MemCacheBytes: 10, MemLimitBytes: 1000}, nil
}

func (b *blockingSource) statsCalls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls
}

func TestSamplerRunWaitsForInFlightHooksOnShutdown(t *testing.T) {
	clock := &fakeClock{now: baseTime()}
	started := make(chan struct{})
	release := make(chan struct{})
	s := newTestSampler(t, clock, func(cfg *Config) {
		cfg.SweepContainers = func(context.Context) {
			close(started)
			<-release
		}
	})

	// Tick three times so the 30s sweep's turn comes round and its hook is
	// in flight (blocked on release). The due counters are wall-clock aligned,
	// so the ticker path itself is not testable at speed — the drain
	// behaviour below is what needs the real Run.
	s.tick(context.Background())
	s.tick(context.Background())
	s.tick(context.Background())
	<-started

	// Run on an already-cancelled context goes straight to shutdown; it must
	// wait out the in-flight sweep rather than leave it behind.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("Run returned while its sweep was still in flight")
	case <-time.After(50 * time.Millisecond):
		// still waiting on the sweep, as it must
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the in-flight sweep finished")
	}
}

func TestSamplerRunStopsWhenContextIsCancelled(t *testing.T) {
	clock := &fakeClock{now: baseTime()}
	s := newTestSampler(t, clock, func(cfg *Config) {
		cfg.Interval = 5 * time.Millisecond
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
}

// The docker df reading is carried forward between refreshes — the series is
// a step, not a 10-second sample — and a failed refresh marks the source
// unavailable with its reason while the last reading stays on the chart.
func TestSamplerSetDockerDiskCarriesReadingForward(t *testing.T) {
	clock := &fakeClock{now: baseTime()}
	s := newTestSampler(t, clock, nil)

	// Before the first refresh succeeds there is no reading at all.
	s.tick(context.Background())
	sample := s.Window(1)[0]
	if !math.IsNaN(sample.DockerDiskBytes) {
		t.Fatalf("DockerDiskBytes = %v before any refresh, want NaN", sample.DockerDiskBytes)
	}
	if status := s.Sources()[SourceDockerDisk]; status.Available || status.Reason == "" {
		t.Fatalf("sources[%q] = %+v before any refresh, want unavailable with a reason", SourceDockerDisk, status)
	}

	s.SetDockerDisk(3.2e9, nil)
	clock.advance(10 * time.Second)
	s.tick(context.Background())
	clock.advance(10 * time.Second)
	s.tick(context.Background())
	// The first sample predates the refresh and is honestly NaN; every tick
	// after it carries the reading forward.
	if got := s.Window(3)[0].DockerDiskBytes; !math.IsNaN(got) {
		t.Errorf("pre-refresh DockerDiskBytes = %v, want NaN", got)
	}
	for i, sample := range s.Window(3)[1:] {
		if sample.DockerDiskBytes != 3.2e9 {
			t.Errorf("sample %d DockerDiskBytes = %v, want the carried-forward 3.2e9", i+1, sample.DockerDiskBytes)
		}
	}
	if status := s.Sources()[SourceDockerDisk]; !status.Available {
		t.Errorf("sources[%q] = %+v after a good refresh, want available", SourceDockerDisk, status)
	}

	// A failed refresh: the source goes unavailable, the step keeps its last
	// honest value (the sources block says why it is stale).
	s.SetDockerDisk(0, errors.New("docker api /system/df: 500"))
	clock.advance(10 * time.Second)
	s.tick(context.Background())
	if got := s.Window(1)[0].DockerDiskBytes; got != 3.2e9 {
		t.Errorf("DockerDiskBytes = %v after a failed refresh, want the last reading 3.2e9", got)
	}
	if status := s.Sources()[SourceDockerDisk]; status.Available || status.Reason != "docker api /system/df: 500" {
		t.Errorf("sources[%q] = %+v after a failed refresh, want unavailable with that reason", SourceDockerDisk, status)
	}

	// The rollup average sees the carried reading too.
	rollup := MinuteAverage(s.ring.AppendLast(nil, len(s.ring.buf)+1))
	if rollup.DockerDiskBytes != 3.2e9 {
		t.Errorf("MinuteAverage DockerDiskBytes = %v, want 3.2e9", rollup.DockerDiskBytes)
	}
}

// Start launches the loop exactly once for the panel's lifetime: a second
// call is refused rather than growing a duplicate goroutine, and Wait
// returns once the cancelled loop has drained.
func TestSamplerStartsOnceAndWaitsForShutdown(t *testing.T) {
	s := newTestSampler(t, &fakeClock{now: baseTime()}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	if err := s.Start(ctx); err != nil {
		t.Fatalf("first Start() error = %v", err)
	}
	if err := s.Start(ctx); err == nil {
		t.Fatal("second Start() = nil error, want a refusal (no duplicate loops)")
	}

	cancel()
	select {
	case <-s.done:
		// the loop returned; Wait below is instant
	case <-time.After(5 * time.Second):
		t.Fatal("sampler loop did not return after its context was cancelled")
	}

	waited := make(chan struct{})
	go func() {
		s.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("Wait did not return after the loop finished")
	}
}

// A4: a panic in an off-loop hook must not take the panel down. The sweep is
// logged and dropped, its in-flight flag resets, and the next turn runs.
func TestSweepPanicIsRecoveredAndNextTickSamples(t *testing.T) {
	clock := &fakeClock{now: baseTime()}
	var sweeps int
	s := newTestSampler(t, clock, func(cfg *Config) {
		cfg.SweepContainers = func(context.Context) {
			sweeps++
			panic("unexpected Docker stats payload")
		}
	})

	for i := 0; i < 6; i++ {
		s.tick(context.Background())
		clock.advance(10 * time.Second)
		s.waitIdle()
	}

	if s.sweepInFlight.Load() {
		t.Error("sweepInFlight still set after a panicking sweep")
	}
	if sweeps != 2 {
		t.Errorf("sweep ran %d times in 6 ticks, want 2 (a panicked sweep does not stop the next turn)", sweeps)
	}
	if got := len(s.Window(10)); got != 6 {
		t.Errorf("window has %d samples after 6 ticks, want 6", got)
	}
}

func TestDockerDiskRefreshPanicIsRecovered(t *testing.T) {
	clock := &fakeClock{now: baseTime()}
	var refreshes int
	s := newTestSampler(t, clock, func(cfg *Config) {
		cfg.RefreshDockerDisk = func(context.Context) {
			refreshes++
			panic("docker system df returned nonsense")
		}
	})

	for i := 0; i < 60; i++ {
		s.tick(context.Background())
		clock.advance(10 * time.Second)
		s.waitIdle()
	}

	if s.dfInFlight.Load() {
		t.Error("dfInFlight still set after a panicking refresh")
	}
	if refreshes != 2 {
		t.Errorf("refresh ran %d times in 60 ticks, want 2", refreshes)
	}
}

// OnMinuteClose runs on the loop itself; a panic in it must not skip the
// rest of the tick (the df refresh rides the same tick as the sixth close).
func TestMinuteClosePanicIsRecoveredAndTickContinues(t *testing.T) {
	clock := &fakeClock{now: baseTime()}
	var closes, refreshes int
	s := newTestSampler(t, clock, func(cfg *Config) {
		cfg.OnMinuteClose = func(HostSample) {
			closes++
			panic("persist failed badly")
		}
		cfg.RefreshDockerDisk = func(context.Context) { refreshes++ }
	})

	for i := 0; i < 30; i++ {
		s.tick(context.Background())
		clock.advance(10 * time.Second)
		s.waitIdle()
	}

	if closes != 5 {
		t.Errorf("minute close ran %d times in 30 ticks, want 5", closes)
	}
	if refreshes != 1 {
		t.Errorf("docker df refresh ran %d times in 30 ticks, want 1 (the panic must not skip it)", refreshes)
	}
}

// A panic in the tick's own sampling skips that tick; the next one records.
func TestTickSamplingPanicSkipsOnlyThatTick(t *testing.T) {
	clock := &fakeClock{now: baseTime()}
	calls := 0
	s := newTestSampler(t, clock, func(cfg *Config) {
		cfg.Now = func() time.Time {
			calls++
			if calls == 1 {
				panic("clock exploded")
			}
			return clock.Now()
		}
	})

	s.tick(context.Background())
	if got := len(s.Window(10)); got != 0 {
		t.Fatalf("window has %d samples after the panicked tick, want 0", got)
	}
	clock.advance(10 * time.Second)
	s.tick(context.Background())
	if got := len(s.Window(10)); got != 1 {
		t.Fatalf("window has %d samples after the next tick, want 1", got)
	}
}
