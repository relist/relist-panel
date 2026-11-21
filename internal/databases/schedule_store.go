package databases

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// ErrNotScheduled is a pin of a Backup Rotation never touches.
var ErrNotScheduled = errors.New("only scheduled Backups can be pinned")

// Schedule is when and what a Database backs up by itself, and how Rotation
// keeps the results. A Database with no row reads DefaultSchedule.
type Schedule struct {
	DatabaseID int64
	Enabled    bool
	Days       []string // "daily" or "mon".."sun"
	Minute     int      // minute of the day, panel time zone
	All        bool
	Databases  []string
	Local      Keep
	Remote     Keep
	// DestinationID is the Backup destination its scheduled Backups are
	// copied to; nil = none.
	DestinationID *int64
	LastRunAt     *time.Time
	LastError     string
}

// DefaultSchedule is what a Database with no schedule row has: off, daily at
// 03:00, every database, keep the last 7 locally and the last 30 remotely.
func DefaultSchedule(id int64) Schedule {
	return Schedule{DatabaseID: id, Days: []string{"daily"}, Minute: 180, All: true, Databases: []string{},
		Local: Keep{Count: new(7)}, Remote: Keep{Count: new(30)}}
}

const scheduleColumns = `database_id, enabled, days, minute, all_databases, databases, local_keep_count, local_keep_days, remote_keep_count, remote_keep_days, last_run_at, last_error, destination_id,
	local_mode, local_gfs_days, local_gfs_weeks, local_gfs_months, local_gfs_years, remote_mode, remote_gfs_days, remote_gfs_weeks, remote_gfs_months, remote_gfs_years`

func nullInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

func intFrom(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

func scanSchedule(row interface{ Scan(...any) error }) (Schedule, error) {
	var sc Schedule
	var days, dbs string
	var lc, ld, rc, rd, last, dest sql.NullInt64
	var lgd, lgw, lgm, lgy, rgd, rgw, rgm, rgy sql.NullInt64
	var lMode, rMode string
	if err := row.Scan(&sc.DatabaseID, &sc.Enabled, &days, &sc.Minute, &sc.All, &dbs, &lc, &ld, &rc, &rd, &last, &sc.LastError, &dest,
		&lMode, &lgd, &lgw, &lgm, &lgy, &rMode, &rgd, &rgw, &rgm, &rgy); err != nil {
		return Schedule{}, err
	}
	if err := json.Unmarshal([]byte(days), &sc.Days); err != nil {
		return Schedule{}, err
	}
	if err := json.Unmarshal([]byte(dbs), &sc.Databases); err != nil {
		return Schedule{}, err
	}
	if sc.Databases == nil {
		sc.Databases = []string{}
	}
	sc.Local = Keep{Mode: lMode, Count: intFrom(lc), Days: intFrom(ld),
		GFSDays: intFrom(lgd), GFSWeeks: intFrom(lgw), GFSMonths: intFrom(lgm), GFSYears: intFrom(lgy)}
	sc.Remote = Keep{Mode: rMode, Count: intFrom(rc), Days: intFrom(rd),
		GFSDays: intFrom(rgd), GFSWeeks: intFrom(rgw), GFSMonths: intFrom(rgm), GFSYears: intFrom(rgy)}
	if dest.Valid {
		sc.DestinationID = &dest.Int64
	}
	if last.Valid {
		t := time.UnixMilli(last.Int64)
		sc.LastRunAt = &t
	}
	return sc, nil
}

// GetSchedule returns Database id's schedule, DefaultSchedule when it has none.
func (s *Store) GetSchedule(ctx context.Context, id int64) (Schedule, error) {
	sc, err := scanSchedule(s.db.QueryRowContext(ctx, `SELECT `+scheduleColumns+` FROM database_backup_schedules WHERE database_id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return DefaultSchedule(id), nil
	}
	return sc, err
}

// SaveSchedule upserts sc. Turning it on (off or absent before) arms it:
// last_run_at is now, so it runs at the next due time, not at once.
func (s *Store) SaveSchedule(ctx context.Context, sc Schedule) error {
	days, _ := json.Marshal(sc.Days)
	if sc.Databases == nil {
		sc.Databases = []string{}
	}
	dbs, _ := json.Marshal(sc.Databases)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var wasEnabled bool
	var last sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT enabled, last_run_at FROM database_backup_schedules WHERE database_id = ?`, sc.DatabaseID).Scan(&wasEnabled, &last)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if sc.Enabled && !wasEnabled {
		last = sql.NullInt64{Int64: s.now().UnixMilli(), Valid: true}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO database_backup_schedules (`+scheduleColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(database_id) DO UPDATE SET enabled = excluded.enabled, days = excluded.days, minute = excluded.minute,
			all_databases = excluded.all_databases, databases = excluded.databases,
			local_keep_count = excluded.local_keep_count, local_keep_days = excluded.local_keep_days,
			remote_keep_count = excluded.remote_keep_count, remote_keep_days = excluded.remote_keep_days,
			destination_id = excluded.destination_id, last_run_at = excluded.last_run_at,
			local_mode = excluded.local_mode, local_gfs_days = excluded.local_gfs_days, local_gfs_weeks = excluded.local_gfs_weeks,
			local_gfs_months = excluded.local_gfs_months, local_gfs_years = excluded.local_gfs_years,
			remote_mode = excluded.remote_mode, remote_gfs_days = excluded.remote_gfs_days, remote_gfs_weeks = excluded.remote_gfs_weeks,
			remote_gfs_months = excluded.remote_gfs_months, remote_gfs_years = excluded.remote_gfs_years`,
		sc.DatabaseID, sc.Enabled, string(days), sc.Minute, sc.All, string(dbs),
		nullInt(sc.Local.Count), nullInt(sc.Local.Days), nullInt(sc.Remote.Count), nullInt(sc.Remote.Days), last, sc.LastError, sc.DestinationID,
		sc.Local.Mode, nullInt(sc.Local.GFSDays), nullInt(sc.Local.GFSWeeks), nullInt(sc.Local.GFSMonths), nullInt(sc.Local.GFSYears),
		sc.Remote.Mode, nullInt(sc.Remote.GFSDays), nullInt(sc.Remote.GFSWeeks), nullInt(sc.Remote.GFSMonths), nullInt(sc.Remote.GFSYears)); err != nil {
		return err
	}
	return tx.Commit()
}

// ListEnabledSchedules returns every enabled schedule, by database id.
func (s *Store) ListEnabledSchedules(ctx context.Context) ([]Schedule, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+scheduleColumns+` FROM database_backup_schedules WHERE enabled = 1 ORDER BY database_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Schedule
	for rows.Next() {
		sc, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// SetScheduleRun records when a schedule last ran and how it went ("" = fine).
func (s *Store) SetScheduleRun(ctx context.Context, id int64, at time.Time, lastErr string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE database_backup_schedules SET last_run_at = ?, last_error = ? WHERE database_id = ?`, at.UnixMilli(), lastErr, id)
	return err
}

// SetPinned marks a Backup as kept by Rotation, or not.
func (s *Store) SetPinned(ctx context.Context, backupID int64, pinned bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE database_backups SET pinned = ? WHERE id = ?`, pinned, backupID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetLocalRemoved records that Rotation removed a Backup's file.
func (s *Store) SetLocalRemoved(ctx context.Context, backupID int64, at time.Time) error {
	res, err := s.db.ExecContext(ctx, `UPDATE database_backups SET local_removed_at = ? WHERE id = ?`, at.UnixMilli(), backupID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSchedule removes a Database's schedule row.
func (s *Store) DeleteSchedule(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM database_backup_schedules WHERE database_id = ?`, id)
	return err
}

// RemoveScheduledDatabase takes a dropped logical database out of the named
// list of Database id's schedule, so a later run does not fail with "no such
// database". A schedule that covers all databases, or no schedule, is left
// alone.
func (s *Store) RemoveScheduledDatabase(ctx context.Context, databaseID int64, name string) error {
	var all bool
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT all_databases, databases FROM database_backup_schedules WHERE database_id = ?`, databaseID).Scan(&all, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if all {
		return nil
	}
	var names []string
	if err := json.Unmarshal([]byte(raw), &names); err != nil {
		return err
	}
	kept := make([]string, 0, len(names))
	for _, n := range names {
		if n != name {
			kept = append(kept, n)
		}
	}
	if len(kept) == len(names) {
		return nil
	}
	out, _ := json.Marshal(kept)
	_, err = s.db.ExecContext(ctx, `UPDATE database_backup_schedules SET databases = ? WHERE database_id = ?`, string(out), databaseID)
	return err
}
