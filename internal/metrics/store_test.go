package metrics_test

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/database"
	"relist-panel/internal/hostmetrics"
	"relist-panel/internal/metrics"
)

func openMigratedDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("open migrated database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func hostRollup(at time.Time) hostmetrics.HostSample {
	nan := math.NaN()
	return hostmetrics.HostSample{
		At:              at,
		CPUPercent:      37.5,
		MemUsedBytes:    8192 * 1024,
		MemTotalBytes:   16384 * 1024,
		DiskFreeBytes:   1000,
		DiskTotalBytes:  2000,
		DockerDiskBytes: nan, // never measured — must store as NULL, not zero
		DiskReadBPS:     nan, // a rateless minute stores NULL too
		DiskWriteBPS:    0,   // an honest zero stays a zero
		NetRXBPS:        3000,
		NetTXBPS:        5000,
	}
}

// Every metric column round-trips through the table, with NaN stored as NULL:
// a field the minute never honestly saw has no average, and NULL says so.
func TestWriteHostRollupStoresNaNAsNull(t *testing.T) {
	db := openMigratedDB(t)
	store := metrics.NewStore(db)
	at := time.Date(2026, 9, 24, 10, 1, 0, 0, time.UTC)

	if err := store.WriteHostRollup(context.Background(), hostRollup(at)); err != nil {
		t.Fatalf("WriteHostRollup() error = %v", err)
	}

	var (
		bucketAt        int64
		cpu             sql.NullFloat64
		memUsed         sql.NullFloat64
		diskTotal       sql.NullFloat64
		dockerDiskBytes sql.NullFloat64
		diskReadBPS     sql.NullFloat64
		diskWriteBPS    sql.NullFloat64
	)
	if err := db.QueryRow(`
		SELECT bucket_at, cpu_percent, mem_used_bytes, disk_total_bytes,
		       docker_disk_bytes, disk_read_bps, disk_write_bps
		FROM host_metric_rollups
	`).Scan(&bucketAt, &cpu, &memUsed, &diskTotal, &dockerDiskBytes, &diskReadBPS, &diskWriteBPS); err != nil {
		t.Fatalf("read host rollup row: %v", err)
	}
	if bucketAt != at.Unix() {
		t.Errorf("bucket_at = %d, want %d", bucketAt, at.Unix())
	}
	if cpu.Float64 != 37.5 || !cpu.Valid {
		t.Errorf("cpu_percent = %+v, want 37.5", cpu)
	}
	if memUsed.Float64 != 8192*1024 || !memUsed.Valid {
		t.Errorf("mem_used_bytes = %+v, want %d", memUsed, 8192*1024)
	}
	if diskTotal.Float64 != 2000 || !diskTotal.Valid {
		t.Errorf("disk_total_bytes = %+v, want 2000", diskTotal)
	}
	if dockerDiskBytes.Valid {
		t.Errorf("docker_disk_bytes = %v, want NULL (never measured)", dockerDiskBytes.Float64)
	}
	if diskReadBPS.Valid {
		t.Errorf("disk_read_bps = %v, want NULL (a rateless minute)", diskReadBPS.Float64)
	}
	if !diskWriteBPS.Valid || diskWriteBPS.Float64 != 0 {
		t.Errorf("disk_write_bps = %+v, want a valid 0 (an honest zero)", diskWriteBPS)
	}
}

// A minute re-closed by a restart — the same wall-clock bucket written again
// — replaces the earlier row instead of failing the write.
func TestWriteHostRollupReplacesSameBucket(t *testing.T) {
	db := openMigratedDB(t)
	store := metrics.NewStore(db)
	at := time.Date(2026, 9, 24, 10, 1, 0, 0, time.UTC)

	first := hostRollup(at)
	first.CPUPercent = 10
	if err := store.WriteHostRollup(context.Background(), first); err != nil {
		t.Fatalf("first WriteHostRollup() error = %v", err)
	}
	second := hostRollup(at)
	second.CPUPercent = 20
	if err := store.WriteHostRollup(context.Background(), second); err != nil {
		t.Fatalf("second WriteHostRollup() error = %v", err)
	}

	var rows int
	var cpu float64
	if err := db.QueryRow(`SELECT COUNT(*), MIN(cpu_percent) FROM host_metric_rollups`).Scan(&rows, &cpu); err != nil {
		t.Fatalf("count host rollups: %v", err)
	}
	if rows != 1 {
		t.Fatalf("host rollup rows = %d, want 1 (the bucket is the key)", rows)
	}
	if cpu != 20 {
		t.Errorf("cpu_percent = %v, want the rewritten 20", cpu)
	}
}

func serviceRollup(project, service string, at time.Time, cpu float64) hostmetrics.ServiceSample {
	return hostmetrics.ServiceSample{
		Project:       project,
		Service:       service,
		At:            at,
		CPUPercent:    cpu,
		MemUsedBytes:  3800,
		MemLimitBytes: 10000,
		NetRXBPS:      math.NaN(),
		NetTXBPS:      math.NaN(),
		BlockReadBPS:  math.NaN(),
		BlockWriteBPS: math.NaN(),
		Pids:          math.NaN(),
	}
}

// One closed minute of two Services lands as two rows, one statement each,
// with NaN fields as NULL. An empty minute writes nothing.
func TestWriteServiceRollups(t *testing.T) {
	db := openMigratedDB(t)
	store := metrics.NewStore(db)
	at := time.Date(2026, 9, 24, 10, 1, 0, 0, time.UTC)

	if err := store.WriteServiceRollups(context.Background(), nil); err != nil {
		t.Fatalf("WriteServiceRollups(nil) error = %v", err)
	}

	rollups := []hostmetrics.ServiceSample{
		serviceRollup("p-e-c", "web", at, 20),
		serviceRollup("p-e-c", "db", at, math.NaN()),
	}
	if err := store.WriteServiceRollups(context.Background(), rollups); err != nil {
		t.Fatalf("WriteServiceRollups() error = %v", err)
	}

	rows, err := db.Query(`SELECT service, cpu_percent, mem_limit_bytes, net_rx_bps FROM service_metric_rollups ORDER BY service`)
	if err != nil {
		t.Fatalf("read service rollups: %v", err)
	}
	defer rows.Close()
	type row struct {
		service string
		cpu     sql.NullFloat64
		limit   sql.NullFloat64
		netRX   sql.NullFloat64
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.service, &r.cpu, &r.limit, &r.netRX); err != nil {
			t.Fatalf("scan service rollup: %v", err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate service rollups: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("service rollup rows = %d, want 2", len(got))
	}
	if got[0].service != "db" || got[1].service != "web" {
		t.Errorf("services = %q, %q; want db, web", got[0].service, got[1].service)
	}
	if got[0].cpu.Valid {
		t.Errorf("db cpu_percent = %v, want NULL (no sample carried a rate)", got[0].cpu.Float64)
	}
	if !got[1].cpu.Valid || got[1].cpu.Float64 != 20 {
		t.Errorf("web cpu_percent = %+v, want 20", got[1].cpu)
	}
	if !got[1].limit.Valid || got[1].limit.Float64 != 10000 {
		t.Errorf("web mem_limit_bytes = %+v, want 10000", got[1].limit)
	}
	if got[1].netRX.Valid {
		t.Errorf("web net_rx_bps = %v, want NULL", got[1].netRX.Float64)
	}
}

// The retention delete: rows older than the cutoff go from both scopes, the
// live window stays intact.
func TestDeleteRollupsBefore(t *testing.T) {
	db := openMigratedDB(t)
	store := metrics.NewStore(db)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	expired := now.Add(-26 * time.Hour)
	inWindow := now.Add(-2 * time.Hour)
	if err := store.WriteHostRollup(ctx, hostRollup(expired)); err != nil {
		t.Fatalf("write expired host rollup: %v", err)
	}
	if err := store.WriteHostRollup(ctx, hostRollup(inWindow)); err != nil {
		t.Fatalf("write windowed host rollup: %v", err)
	}
	serviceRows := []hostmetrics.ServiceSample{
		serviceRollup("p-e-c", "web", expired, 1),
		serviceRollup("p-e-c", "web", inWindow, 2),
	}
	if err := store.WriteServiceRollups(ctx, serviceRows); err != nil {
		t.Fatalf("write service rollups: %v", err)
	}

	if err := store.DeleteRollupsBefore(ctx, now.Add(-metrics.RetentionHorizon)); err != nil {
		t.Fatalf("DeleteRollupsBefore() error = %v", err)
	}

	var hostKept int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM host_metric_rollups`).Scan(&hostKept); err != nil {
		t.Fatalf("count host rollups: %v", err)
	}
	var serviceKept int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM service_metric_rollups`).Scan(&serviceKept); err != nil {
		t.Fatalf("count service rollups: %v", err)
	}
	if hostKept != 1 || serviceKept != 1 {
		t.Fatalf("kept host=%d service=%d rows, want one per scope (the window stays intact)", hostKept, serviceKept)
	}
	var keptAt int64
	if err := db.QueryRow(`SELECT MAX(bucket_at) FROM service_metric_rollups`).Scan(&keptAt); err != nil {
		t.Fatalf("read kept bucket: %v", err)
	}
	if keptAt != inWindow.Unix() {
		t.Errorf("kept bucket_at = %d, want %d (only the expired row went)", keptAt, inWindow.Unix())
	}
}

// fakeSource is the collector's ContainerSource over an in-memory daemon.
type fakeSource struct {
	mu         sync.Mutex
	containers []hostmetrics.ContainerInfo
	stats      map[string]hostmetrics.ContainerStatsSample
	listErr    error
}

func (f *fakeSource) ListContainers(context.Context) ([]hostmetrics.ContainerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.containers, nil
}

func (f *fakeSource) ContainerStats(_ context.Context, id string) (hostmetrics.ContainerStatsSample, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stats == nil {
		return hostmetrics.ContainerStatsSample{}, errors.New("no stats recorded")
	}
	stats, ok := f.stats[id]
	if !ok {
		return hostmetrics.ContainerStatsSample{}, errors.New("no stats for " + id)
	}
	return stats, nil
}

// The full minute-close path: a sweep's samples end up as rollup rows in both
// scopes, a minute cut short by a restart still writes its row, and the
// recorder runs the retention delete.
func TestRecorderOnMinuteCloseWritesBothScopes(t *testing.T) {
	db := openMigratedDB(t)
	store := metrics.NewStore(db)
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)

	source := &fakeSource{
		containers: []hostmetrics.ContainerInfo{{
			ID:     "c1",
			Name:   "p-e-c-web-1",
			Image:  "nginx:1.27",
			State:  "running",
			Status: "Up 2 minutes (healthy)",
			Labels: map[string]string{
				hostmetrics.LabelComposeProject: "p-e-c",
				hostmetrics.LabelComposeService: "web",
			},
		}},
		stats: map[string]hostmetrics.ContainerStatsSample{
			"c1": {CPUTotalNanos: 1000, CPUSystemNanos: 100000, CPUOnline: 4, MemUsageBytes: 5000, MemCacheBytes: 1200, MemLimitBytes: 10000, PidsCurrent: 7},
		},
	}
	collector := hostmetrics.NewServiceCollector(source, func(context.Context) (map[string]bool, error) {
		return map[string]bool{"p-e-c": true}, nil
	}, func() time.Time { return now })
	recorder := metrics.NewRecorder(store, collector, func() time.Time { return now })

	// An expired row the retention delete must sweep out on the next close.
	if err := store.WriteHostRollup(context.Background(), hostRollup(now.Add(-26*time.Hour))); err != nil {
		t.Fatalf("write expired rollup: %v", err)
	}

	// Minute one: two sweeps, thirty seconds apart.
	collector.Sweep(context.Background())
	now = now.Add(30 * time.Second)
	source.stats["c1"] = hostmetrics.ContainerStatsSample{CPUTotalNanos: 6000, CPUSystemNanos: 200000, CPUOnline: 4, MemUsageBytes: 7000, MemCacheBytes: 1200, MemLimitBytes: 10000, NetRXBytes: 3000, PidsCurrent: 9}
	collector.Sweep(context.Background())
	recorder.OnMinuteClose(hostRollup(now.Truncate(time.Minute)))

	// Minute two, cut short: a restart happened, one sweep is all it saw.
	now = now.Add(30 * time.Second)
	collector.Sweep(context.Background())
	recorder.OnMinuteClose(hostRollup(now.Truncate(time.Minute)))

	var hostBuckets []int64
	rows, err := db.Query(`SELECT bucket_at FROM host_metric_rollups ORDER BY bucket_at`)
	if err != nil {
		t.Fatalf("read host buckets: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var bucket int64
		if err := rows.Scan(&bucket); err != nil {
			t.Fatalf("scan host bucket: %v", err)
		}
		hostBuckets = append(hostBuckets, bucket)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate host buckets: %v", err)
	}
	if len(hostBuckets) != 2 {
		t.Fatalf("host buckets = %v, want the two closed minutes (and the expired row deleted)", hostBuckets)
	}

	serviceRows, err := db.Query(`SELECT bucket_at, cpu_percent, mem_used_bytes, pids FROM service_metric_rollups ORDER BY bucket_at`)
	if err != nil {
		t.Fatalf("read service rollups: %v", err)
	}
	defer serviceRows.Close()
	type serviceRow struct {
		bucket  int64
		cpu     sql.NullFloat64
		memUsed sql.NullFloat64
		pids    sql.NullFloat64
	}
	var got []serviceRow
	for serviceRows.Next() {
		var r serviceRow
		if err := serviceRows.Scan(&r.bucket, &r.cpu, &r.memUsed, &r.pids); err != nil {
			t.Fatalf("scan service rollup: %v", err)
		}
		got = append(got, r)
	}
	if err := serviceRows.Err(); err != nil {
		t.Fatalf("iterate service rollups: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("service rollup rows = %d, want one per closed minute — including the cut-short one", len(got))
	}

	minuteOne, minuteTwo := got[0], got[1]
	if minuteOne.bucket != time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC).Unix() {
		t.Errorf("minute one bucket = %d, want 10:00:00", minuteOne.bucket)
	}
	// CPU averaged over the two sweeps the minute saw: NaN (first sweep)
	// dropped, then 20% — the average is 20.
	if !minuteOne.cpu.Valid || minuteOne.cpu.Float64 != 20 {
		t.Errorf("minute one cpu = %+v, want 20", minuteOne.cpu)
	}
	// Memory used averaged over both sweeps: (3800 + 5800) / 2.
	if !minuteOne.memUsed.Valid || minuteOne.memUsed.Float64 != 4800 {
		t.Errorf("minute one mem_used = %+v, want 4800", minuteOne.memUsed)
	}
	// Pids averaged over (7, 9).
	if !minuteOne.pids.Valid || minuteOne.pids.Float64 != 8 {
		t.Errorf("minute one pids = %+v, want 8", minuteOne.pids)
	}
	if minuteTwo.bucket != time.Date(2026, 9, 24, 10, 1, 0, 0, time.UTC).Unix() {
		t.Errorf("minute two bucket = %d, want 10:01:00", minuteTwo.bucket)
	}
	// The cut-short minute still wrote a row — averaged over the one sweep
	// it saw. That sweep's system clock had not moved since the previous
	// sweep, so there is no honest CPU denominator and the column stays NULL;
	// the gauges carried real values.
	if minuteTwo.cpu.Valid {
		t.Errorf("minute two cpu = %+v, want NULL (no honest rate from a stalled system clock)", minuteTwo.cpu)
	}
	if !minuteTwo.memUsed.Valid || minuteTwo.memUsed.Float64 != 5800 {
		t.Errorf("minute two mem_used = %+v, want 5800", minuteTwo.memUsed)
	}
	if !minuteTwo.pids.Valid || minuteTwo.pids.Float64 != 9 {
		t.Errorf("minute two pids = %+v, want 9", minuteTwo.pids)
	}
}
