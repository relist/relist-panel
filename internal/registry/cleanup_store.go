package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// CleanupSettings is when Registry cleanup runs and how much it keeps. Days is
// ["daily"] or weekdays ("mon".."sun"); Minute is the minute of the day in
// host time.
type CleanupSettings struct {
	Enabled   bool       `json:"enabled"`
	Keep      int        `json:"keep"`
	Days      []string   `json:"days"`
	Minute    int        `json:"minute"`
	LastRunAt *time.Time `json:"lastRunAt"`
}

// The result of a cleanup run.
const (
	RunRunning     = "running"
	RunOK          = "ok"
	RunFailed      = "failed"
	RunInterrupted = "interrupted"
)

// keptRuns is how many cleanup runs the history keeps.
const keptRuns = 10

// Run is one cleanup run. Trigger is "schedule" or "user:<name>"; Deleted
// lists repo:tag.
type Run struct {
	ID         int64      `json:"id"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt"`
	Trigger    string     `json:"trigger"`
	Deleted    []string   `json:"deleted"`
	// FreedBytes is nil when the volume's usage could not be read.
	FreedBytes *int64 `json:"freedBytes"`
	Result     string `json:"result"`
	Log        string `json:"log"`
}

func (s *Store) CleanupSettings(ctx context.Context) (CleanupSettings, error) {
	var c CleanupSettings
	var days string
	var last sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT cleanup_enabled, cleanup_keep, cleanup_days, cleanup_minute, cleanup_last_run_at
		FROM registry_settings WHERE id = 1`).Scan(&c.Enabled, &c.Keep, &days, &c.Minute, &last)
	c.Days = strings.Split(days, ",")
	if last.Valid {
		at := time.Unix(last.Int64, 0)
		c.LastRunAt = &at
	}
	return c, err
}

func (s *Store) SaveCleanupSettings(ctx context.Context, c CleanupSettings) error {
	var last any
	if c.LastRunAt != nil {
		last = c.LastRunAt.Unix()
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE registry_settings
		SET cleanup_enabled = ?, cleanup_keep = ?, cleanup_days = ?, cleanup_minute = ?, cleanup_last_run_at = ?
		WHERE id = 1`, c.Enabled, c.Keep, strings.Join(c.Days, ","), c.Minute, last)
	return err
}

// SetLastRunAt writes only when cleanup last ran (or was armed), so a
// scheduler tick never overwrites settings saved meanwhile.
func (s *Store) SetLastRunAt(ctx context.Context, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE registry_settings SET cleanup_last_run_at = ? WHERE id = 1`, at.Unix())
	return err
}

// RunResult is one run's result.
func (s *Store) RunResult(ctx context.Context, id int64) (string, error) {
	var result string
	err := s.db.QueryRowContext(ctx, `SELECT result FROM registry_cleanup_runs WHERE id = ?`, id).Scan(&result)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return result, err
}

// freedUnknown is stored in freed_bytes when usage could not be read.
const freedUnknown = -1

// InterruptRunning marks every run still recorded running as interrupted:
// with the cleanup flag held nothing runs, so they are runs the panel stopped
// in. The run named in gc_running is left to recover, which records the
// garbage collect's own logs.
func (s *Store) InterruptRunning(ctx context.Context, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE registry_cleanup_runs SET result = 'interrupted', finished_at = ?, freed_bytes = -1,
			log = log || 'the panel stopped before this run finished' || char(10)
		WHERE result = 'running'
			AND id != COALESCE((SELECT gc_running FROM registry_settings WHERE id = 1), 0)`, at.Unix())
	return err
}

// SetGCRunning records the run whose GC is in progress; 0 clears it.
func (s *Store) SetGCRunning(ctx context.Context, runID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE registry_settings SET gc_running = ? WHERE id = 1`, runID)
	return err
}

func (s *Store) GCRunning(ctx context.Context) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT gc_running FROM registry_settings WHERE id = 1`).Scan(&id)
	return id, err
}

// StartRun records a running run and drops runs beyond the last 10.
func (s *Store) StartRun(ctx context.Context, at time.Time, trigger string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO registry_cleanup_runs (started_at, trigger) VALUES (?, ?)`, at.Unix(), trigger)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM registry_cleanup_runs WHERE id NOT IN (
			SELECT id FROM registry_cleanup_runs ORDER BY id DESC LIMIT ?)`, keptRuns); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

func (s *Store) FinishRun(ctx context.Context, id int64, at time.Time, result string, deleted []string, freed int64, log string) error {
	if deleted == nil {
		deleted = []string{}
	}
	raw, err := json.Marshal(deleted)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE registry_cleanup_runs SET finished_at = ?, result = ?, deleted = ?, freed_bytes = ?, log = ?
		WHERE id = ?`, at.Unix(), result, string(raw), freed, log, id)
	return err
}

// Runs lists the newest runs first.
func (s *Store) Runs(ctx context.Context, limit int) ([]Run, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, started_at, finished_at, trigger, deleted, freed_bytes, result, log
		FROM registry_cleanup_runs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		var started int64
		var finished sql.NullInt64
		var deleted string
		var freed int64
		if err := rows.Scan(&r.ID, &started, &finished, &r.Trigger, &deleted, &freed, &r.Result, &r.Log); err != nil {
			return nil, err
		}
		if freed >= 0 {
			r.FreedBytes = &freed
		}
		r.StartedAt = time.Unix(started, 0)
		if finished.Valid {
			at := time.Unix(finished.Int64, 0)
			r.FinishedAt = &at
		}
		if err := json.Unmarshal([]byte(deleted), &r.Deleted); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
