// Package metrics is where sampled metrics are persisted and served: it owns
// the 1-minute rollup tables (written when a minute closes, one small
// statement per scope), the retention delete — the panel's first
// housekeeping job — and the monitoring HTTP endpoints. The sampling itself
// lives in hostmetrics (ADR-0006); this package is its storage and read face.
package metrics

import (
	"context"
	"database/sql"
	"math"
	"time"

	"relist-panel/internal/hostmetrics"
)

// RetentionHorizon is how long rollup rows are kept: the longest window the
// screens offer (24h) plus an hour of slack, so a window's oldest bucket is
// still there when the chart asks for it.
const RetentionHorizon = 24*time.Hour + time.Hour

// Store reads and writes the metric rollups in the panel's migrated SQLite
// database, used concretely per ADR-0003 (nothing here is ever swapped out).
//
// The panel runs with SetMaxOpenConns(1), so every write here shares the one
// connection with API reads. The writes are shaped for that: one small
// statement per scope per minute, on the indexed primary keys, never while a
// row cursor is still open.
type Store struct {
	db *sql.DB
}

// NewStore constructs the rollup store over a migrated database.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

// WriteHostRollup persists one closed minute of host samples as a single
// statement. Fields that never got an honest reading are NaN and store as
// NULL — never zero. A bucket rewritten by a later close (a restart landing
// in the same wall-clock minute) replaces the earlier row.
func (s *Store) WriteHostRollup(ctx context.Context, rollup hostmetrics.HostSample) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO host_metric_rollups (
			bucket_at, cpu_percent, mem_used_bytes, mem_total_bytes,
			disk_free_bytes, disk_total_bytes, docker_disk_bytes,
			disk_read_bps, disk_write_bps, net_rx_bps, net_tx_bps
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, rollup.At.Unix(),
		nullable(rollup.CPUPercent),
		nullable(rollup.MemUsedBytes),
		nullable(rollup.MemTotalBytes),
		nullable(rollup.DiskFreeBytes),
		nullable(rollup.DiskTotalBytes),
		nullable(rollup.DockerDiskBytes),
		nullable(rollup.DiskReadBPS),
		nullable(rollup.DiskWriteBPS),
		nullable(rollup.NetRXBPS),
		nullable(rollup.NetTXBPS),
	)
	if err != nil {
		return err
	}
	return nil
}

// WriteServiceRollups persists one closed minute of per-Service rollups —
// one statement per Service, inside one short transaction so a partially
// written minute cannot be read.
func (s *Store) WriteServiceRollups(ctx context.Context, rollups []hostmetrics.ServiceSample) error {
	if len(rollups) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, rollup := range rollups {
		if _, err := tx.ExecContext(ctx, `
			INSERT OR REPLACE INTO service_metric_rollups (
				compose_project, service, bucket_at,
				cpu_percent, mem_used_bytes, mem_limit_bytes,
				net_rx_bps, net_tx_bps, block_read_bps, block_write_bps, pids
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, rollup.Project, rollup.Service, rollup.At.Unix(),
			nullable(rollup.CPUPercent),
			nullable(rollup.MemUsedBytes),
			nullable(rollup.MemLimitBytes),
			nullable(rollup.NetRXBPS),
			nullable(rollup.NetTXBPS),
			nullable(rollup.BlockReadBPS),
			nullable(rollup.BlockWriteBPS),
			nullable(rollup.Pids),
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteRollupsBefore is the retention delete: rows older than the cutoff go,
// from both scopes. It runs on every minute close — an indexed range delete
// over a bounded table, and the cheapest way to never think about it again.
func (s *Store) DeleteRollupsBefore(ctx context.Context, cutoff time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM host_metric_rollups WHERE bucket_at < ?`, cutoff.Unix()); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM service_metric_rollups WHERE bucket_at < ?`, cutoff.Unix())
	return err
}

// nullable maps NaN to SQL NULL: a field that was never honestly measured has
// no average, and a NULL column says so where a zero would lie.
func nullable(value float64) any {
	if math.IsNaN(value) {
		return nil
	}
	return value
}
