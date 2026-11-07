package requestmetrics

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Resolution picks the minute or the hourly table.
type Resolution int

const (
	Minute Resolution = iota
	Hour
)

func (r Resolution) table() string {
	if r == Hour {
		return "request_metric_hourly"
	}
	return "request_metric_rollups"
}

// Cursor is where the Ingester resumes: the access log's inode, the offset
// to read from, and the time before which every minute is already written.
type Cursor struct {
	Inode          uint64
	ResumeOffset   int64
	FlushedThrough time.Time
}

// ErrInvalidRetention refuses a retention outside 1..90 days.
var ErrInvalidRetention = errors.New("retention must be between 1 and 90 days")

// Store reads and writes request metrics in the panel's migrated SQLite
// database, used concretely per ADR-0003. The panel runs with one connection,
// so writes are small transactions and never run while a row cursor is open.
type Store struct {
	db *sql.DB
}

// NewStore constructs the store over a migrated database.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

const insertRow = `INSERT OR REPLACE INTO %s
	(scope, compose_id, host, bucket_at, c1xx, c2xx, c3xx, c4xx, c5xx, cother, latency)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func execRow(ctx context.Context, tx *sql.Tx, table string, r Row) error {
	latency := r.Counts.Latency.MarshalBinary()
	if latency == nil {
		latency = []byte{} // an empty BLOB, not NULL
	}
	_, err := tx.ExecContext(ctx, strings.Replace(insertRow, "%s", table, 1),
		r.Key.Scope, r.Key.ComposeID, r.Key.Host, r.BucketAt.Unix(),
		r.Counts.C1xx, r.Counts.C2xx, r.Counts.C3xx, r.Counts.C4xx, r.Counts.C5xx, r.Counts.Cother,
		latency)
	return err
}

// WriteMinutes writes closed minute rows and the cursor in one transaction.
func (s *Store) WriteMinutes(ctx context.Context, rows []Row, c Cursor) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, r := range rows {
		if err := execRow(ctx, tx, Minute.table(), r); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO request_metric_cursor
		(id, inode, resume_offset, flushed_through) VALUES (1, ?, ?, ?)`,
		int64(c.Inode), c.ResumeOffset, c.FlushedThrough.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

// Cursor reads the saved cursor; ok is false when none was saved yet.
func (s *Store) Cursor(ctx context.Context) (Cursor, bool, error) {
	var inode, offset, through int64
	err := s.db.QueryRowContext(ctx, `SELECT inode, resume_offset, flushed_through
		FROM request_metric_cursor WHERE id = 1`).Scan(&inode, &offset, &through)
	if errors.Is(err, sql.ErrNoRows) {
		return Cursor{}, false, nil
	}
	if err != nil {
		return Cursor{}, false, err
	}
	return Cursor{Inode: uint64(inode), ResumeOffset: offset, FlushedThrough: time.Unix(through, 0).UTC()}, true, nil
}

// LatestHour is the newest hour with hourly rows.
func (s *Store) LatestHour(ctx context.Context) (time.Time, bool, error) {
	var at sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(bucket_at) FROM request_metric_hourly`).Scan(&at); err != nil {
		return time.Time{}, false, err
	}
	if !at.Valid {
		return time.Time{}, false, nil
	}
	return time.Unix(at.Int64, 0).UTC(), true, nil
}

// CompactHour merges the minute rows of [hour, hour+1h) per series into
// hourly rows. The merge happens in Go so histograms add exactly.
func (s *Store) CompactHour(ctx context.Context, hour time.Time) error {
	minutes, err := s.queryRows(ctx, Minute, nil, hour, hour.Add(time.Hour))
	if err != nil {
		return err
	}
	if len(minutes) == 0 {
		return nil
	}
	merged := map[Key]*Counts{}
	for _, r := range minutes {
		c := merged[r.Key]
		if c == nil {
			c = &Counts{}
			merged[r.Key] = c
		}
		c.Merge(&r.Counts)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, c := range merged {
		if err := execRow(ctx, tx, Hour.table(), Row{Key: k, BucketAt: hour, Counts: *c}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeleteBefore drops minute rows older than minuteCutoff and hourly rows
// older than hourCutoff.
func (s *Store) DeleteBefore(ctx context.Context, minuteCutoff, hourCutoff time.Time) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM request_metric_rollups WHERE bucket_at < ?`, minuteCutoff.Unix()); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM request_metric_hourly WHERE bucket_at < ?`, hourCutoff.Unix())
	return err
}

// Rows returns the rows of the given series from `from` on, oldest first.
func (s *Store) Rows(ctx context.Context, res Resolution, keys []Key, from time.Time) ([]Row, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	return s.queryRows(ctx, res, keys, from, time.Time{})
}

// queryRows reads [from, until) (no upper bound when until is zero), for the
// given keys or for every series when keys is nil.
func (s *Store) queryRows(ctx context.Context, res Resolution, keys []Key, from, until time.Time) ([]Row, error) {
	q := `SELECT scope, compose_id, host, bucket_at, c1xx, c2xx, c3xx, c4xx, c5xx, cother, latency
		FROM ` + res.table() + ` WHERE bucket_at >= ?`
	args := []any{from.Unix()}
	if !until.IsZero() {
		q += ` AND bucket_at < ?`
		args = append(args, until.Unix())
	}
	if keys != nil {
		clauses := make([]string, len(keys))
		for i, k := range keys {
			clauses[i] = `(scope = ? AND compose_id = ? AND host = ?)`
			args = append(args, k.Scope, k.ComposeID, k.Host)
		}
		q += ` AND (` + strings.Join(clauses, ` OR `) + `)`
	}
	q += ` ORDER BY bucket_at, scope, compose_id, host`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var r Row
		var bucket int64
		var blob []byte
		if err := rows.Scan(&r.Key.Scope, &r.Key.ComposeID, &r.Key.Host, &bucket,
			&r.Counts.C1xx, &r.Counts.C2xx, &r.Counts.C3xx, &r.Counts.C4xx, &r.Counts.C5xx, &r.Counts.Cother, &blob); err != nil {
			return nil, err
		}
		h, err := UnmarshalHistogram(blob)
		if err != nil {
			return nil, err
		}
		r.Counts.Latency = h
		r.BucketAt = time.Unix(bucket, 0).UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// RetentionDays is how many days hourly rows are kept.
func (s *Store) RetentionDays(ctx context.Context) (int, error) {
	var days int
	err := s.db.QueryRowContext(ctx, `SELECT retention_days FROM request_metric_settings WHERE id = 1`).Scan(&days)
	return days, err
}

// SaveRetention sets the days hourly rows are kept, 1..90.
func (s *Store) SaveRetention(ctx context.Context, days int) error {
	if days < 1 || days > 90 {
		return ErrInvalidRetention
	}
	_, err := s.db.ExecContext(ctx, `UPDATE request_metric_settings SET retention_days = ? WHERE id = 1`, days)
	return err
}
