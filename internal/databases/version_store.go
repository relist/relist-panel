package databases

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// How a Change version moves a Database to another major version.
const (
	// VersionMethodDumpRestore dumps every logical database and the roles and
	// restores them into the new version on a new volume (Postgres).
	VersionMethodDumpRestore = "dump_restore"
	// VersionMethodInPlace starts the new image on the same volume (MariaDB, Redis).
	VersionMethodInPlace = "in_place"
)

// VersionChange is one Change version of a Database. FromVolume is set once a
// dump-and-restore change succeeds: the volume left behind as previous
// version data until FromVolumeRemovedAt.
type VersionChange struct {
	ID, DatabaseID         int64
	FromVersion, ToVersion string
	Method, Status         string
	Error, Log             string
	FromVolume, ToVolume   string
	FromVolumeRemovedAt    *time.Time
	StartedAt              time.Time
	FinishedAt             *time.Time
}

// VersionCommit is what a succeeded change writes in one transaction.
// KeepRunning leaves the row running: an in-place change switches the
// record before the new image's first start, and SucceedVersionChange ends
// it after.
type VersionCommit struct {
	ChangeID, DatabaseID                          int64
	Version, Volume, AppliedSpec, FromVolume, Log string
	KeepRunning                                   bool
}

const versionColumns = `id, database_id, from_version, to_version, method, status, error, log, from_volume, to_volume, from_volume_removed_at, started_at, finished_at`

func scanVersionChange(row interface{ Scan(...any) error }) (VersionChange, error) {
	var c VersionChange
	var started int64
	var removed, finished sql.NullInt64
	err := row.Scan(&c.ID, &c.DatabaseID, &c.FromVersion, &c.ToVersion, &c.Method, &c.Status, &c.Error, &c.Log,
		&c.FromVolume, &c.ToVolume, &removed, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return VersionChange{}, ErrNotFound
	}
	if err != nil {
		return VersionChange{}, err
	}
	c.StartedAt = time.UnixMilli(started)
	if removed.Valid {
		t := time.UnixMilli(removed.Int64)
		c.FromVolumeRemovedAt = &t
	}
	if finished.Valid {
		t := time.UnixMilli(finished.Int64)
		c.FinishedAt = &t
	}
	return c, nil
}

// InsertVersionChange records a running change and returns it.
func (s *Store) InsertVersionChange(ctx context.Context, c VersionChange) (VersionChange, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO database_version_changes (database_id, from_version, to_version, method, status, started_at)
		VALUES (?, ?, ?, ?, ?, ?)`, c.DatabaseID, c.FromVersion, c.ToVersion, c.Method, JobRunning, s.now().UnixMilli())
	if err != nil {
		return VersionChange{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return VersionChange{}, err
	}
	return s.GetVersionChange(ctx, c.DatabaseID, id)
}

func (s *Store) execOne(ctx context.Context, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetVersionChangeLog stores the step log so far, for the page to poll.
func (s *Store) SetVersionChangeLog(ctx context.Context, id int64, log string) error {
	return s.execOne(ctx, `UPDATE database_version_changes SET log = ? WHERE id = ?`, log, id)
}

// SetVersionChangeTarget records the volume the change is creating, so a
// failure or a restart can find and remove it.
func (s *Store) SetVersionChangeTarget(ctx context.Context, id int64, toVolume string) error {
	return s.execOne(ctx, `UPDATE database_version_changes SET to_volume = ? WHERE id = ?`, toVolume, id)
}

// FailVersionChange ends a change as failed. The Database is not touched.
func (s *Store) FailVersionChange(ctx context.Context, id int64, errMsg, log string) error {
	return s.execOne(ctx, `UPDATE database_version_changes SET status = ?, error = ?, log = ?, finished_at = ? WHERE id = ?`,
		JobFailed, errMsg, log, s.now().UnixMilli(), id)
}

// CommitVersionChange switches the Database to the new version, volume and
// applied settings and ends the change as succeeded, in one transaction.
func (s *Store) CommitVersionChange(ctx context.Context, c VersionCommit) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := s.now().UnixMilli()
	res, err := tx.ExecContext(ctx, `UPDATE databases SET version = ?, volume = ?, applied_spec = ?, last_error = '', updated_at = ? WHERE id = ?`,
		c.Version, c.Volume, c.AppliedSpec, now, c.DatabaseID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	status, finished := JobSucceeded, sql.NullInt64{Int64: now, Valid: true}
	if c.KeepRunning {
		status, finished = JobRunning, sql.NullInt64{}
	}
	res, err = tx.ExecContext(ctx, `UPDATE database_version_changes SET status = ?, from_volume = ?, log = ?, finished_at = ? WHERE id = ? AND database_id = ?`,
		status, c.FromVolume, c.Log, finished, c.ChangeID, c.DatabaseID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// SucceedVersionChange ends a change committed with KeepRunning.
func (s *Store) SucceedVersionChange(ctx context.Context, id int64, log string) error {
	return s.execOne(ctx, `UPDATE database_version_changes SET status = ?, log = ?, finished_at = ? WHERE id = ?`,
		JobSucceeded, log, s.now().UnixMilli(), id)
}

// ListVersionChanges lists a Database's changes, newest first.
func (s *Store) ListVersionChanges(ctx context.Context, databaseID int64) ([]VersionChange, error) {
	return s.versionChanges(ctx, `SELECT `+versionColumns+` FROM database_version_changes WHERE database_id = ? ORDER BY id DESC`, databaseID)
}

// RunningVersionChanges lists the changes still marked running, oldest
// first: at start-up, the ones a restart interrupted.
func (s *Store) RunningVersionChanges(ctx context.Context) ([]VersionChange, error) {
	return s.versionChanges(ctx, `SELECT `+versionColumns+` FROM database_version_changes WHERE status = ? ORDER BY id`, JobRunning)
}

func (s *Store) versionChanges(ctx context.Context, query string, args ...any) ([]VersionChange, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VersionChange{}
	for rows.Next() {
		c, err := scanVersionChange(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetVersionChange reads one change of a Database; another Database's is ErrNotFound.
func (s *Store) GetVersionChange(ctx context.Context, databaseID, id int64) (VersionChange, error) {
	return scanVersionChange(s.db.QueryRowContext(ctx,
		`SELECT `+versionColumns+` FROM database_version_changes WHERE id = ? AND database_id = ?`, id, databaseID))
}

// MarkFromVolumeRemoved records that a change's previous version data is gone.
func (s *Store) MarkFromVolumeRemoved(ctx context.Context, id int64) error {
	return s.execOne(ctx, `UPDATE database_version_changes SET from_volume_removed_at = ? WHERE id = ?`, s.now().UnixMilli(), id)
}

// FailRunningVersionChanges fails every change a restart interrupted.
func (s *Store) FailRunningVersionChanges(ctx context.Context, msg string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE database_version_changes SET status = ?, error = ?, finished_at = ? WHERE status = ?`,
		JobFailed, msg, s.now().UnixMilli(), JobRunning)
	return err
}

// FailedTargetVolumes names the volumes failed changes were creating. Each
// name is unique to its change, so none is ever a Database's live data.
func (s *Store) FailedTargetVolumes(ctx context.Context) ([]string, error) {
	return s.strings(ctx, `SELECT to_volume FROM database_version_changes WHERE status = ? AND to_volume <> '' ORDER BY id`, JobFailed)
}

func (s *Store) strings(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// PreviousVolumes maps each Database to the previous version data it still keeps.
func (s *Store) PreviousVolumes(ctx context.Context) (map[int64][]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT database_id, from_volume FROM database_version_changes
		WHERE status = ? AND from_volume <> '' AND from_volume_removed_at IS NULL ORDER BY id`, JobSucceeded)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]string{}
	for rows.Next() {
		var id int64
		var v string
		if err := rows.Scan(&id, &v); err != nil {
			return nil, err
		}
		out[id] = append(out[id], v)
	}
	return out, rows.Err()
}
