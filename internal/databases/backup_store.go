package databases

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// What started a Backup. Phase 2 never deletes any of them automatically.
const (
	TriggerManual     = "manual"
	TriggerPreRestore = "pre_restore"
	TriggerPreUpdate  = "pre_update"
)

// The state of a Backup or a restore.
const (
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
)

// Scope is what a Backup covers: the whole instance, or named logical databases.
type Scope struct {
	All       bool     `json:"all,omitempty"`
	Databases []string `json:"databases,omitempty"`
}

// Backup is one gzipped dump file of a Database.
type Backup struct {
	ID, DatabaseID int64
	Trigger        string
	Scope          Scope
	Status         string
	Error, Log     string
	FileName       string
	Size           int64
	SHA256         string
	StartedAt      time.Time
	FinishedAt     *time.Time
}

// Restore is one restore of a Backup or of an uploaded file.
type Restore struct {
	ID, DatabaseID     int64
	Source             string // "backup" | "upload"
	BackupID           *int64
	PreBackupID        *int64
	UploadName         string
	Status, Error, Log string
	StartedAt          time.Time
	FinishedAt         *time.Time
}

const backupColumns = `id, database_id, trigger, scope, status, error, log, file_name, size, sha256, started_at, finished_at`

func scanBackup(row interface{ Scan(...any) error }) (Backup, error) {
	var b Backup
	var scope string
	var started int64
	var finished sql.NullInt64
	err := row.Scan(&b.ID, &b.DatabaseID, &b.Trigger, &scope, &b.Status, &b.Error, &b.Log, &b.FileName, &b.Size, &b.SHA256, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return Backup{}, ErrNotFound
	}
	if err != nil {
		return Backup{}, err
	}
	if err := json.Unmarshal([]byte(scope), &b.Scope); err != nil {
		return Backup{}, err
	}
	b.StartedAt = time.UnixMilli(started)
	if finished.Valid {
		t := time.UnixMilli(finished.Int64)
		b.FinishedAt = &t
	}
	return b, nil
}

// InsertBackup stores b as running, started now.
func (s *Store) InsertBackup(ctx context.Context, b Backup) (Backup, error) {
	scope, err := json.Marshal(b.Scope)
	if err != nil {
		return Backup{}, err
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO database_backups (database_id, trigger, scope, status, started_at)
		VALUES (?, ?, ?, ?, ?)`, b.DatabaseID, b.Trigger, string(scope), JobRunning, s.now().UnixMilli())
	if err != nil {
		return Backup{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Backup{}, err
	}
	return scanBackup(s.db.QueryRowContext(ctx, `SELECT `+backupColumns+` FROM database_backups WHERE id = ?`, id))
}

// FinishBackup records how a running Backup ended.
func (s *Store) FinishBackup(ctx context.Context, id int64, b Backup) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE database_backups SET status = ?, error = ?, log = ?, file_name = ?, size = ?, sha256 = ?, finished_at = ?
		WHERE id = ?`, b.Status, b.Error, b.Log, b.FileName, b.Size, b.SHA256, s.now().UnixMilli(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetBackup returns a Backup of Database databaseID; ErrNotFound when it is
// another Database's.
func (s *Store) GetBackup(ctx context.Context, databaseID, id int64) (Backup, error) {
	return scanBackup(s.db.QueryRowContext(ctx,
		`SELECT `+backupColumns+` FROM database_backups WHERE id = ? AND database_id = ?`, id, databaseID))
}

// ListBackups returns a Database's Backups, newest first.
func (s *Store) ListBackups(ctx context.Context, databaseID int64) ([]Backup, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+backupColumns+` FROM database_backups WHERE database_id = ? ORDER BY started_at DESC, id DESC`, databaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Backup{}
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) DeleteBackup(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM database_backups WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const restoreColumns = `id, database_id, source, backup_id, pre_backup_id, upload_name, status, error, log, started_at, finished_at`

func scanRestore(row interface{ Scan(...any) error }) (Restore, error) {
	var r Restore
	var backup, pre, finished sql.NullInt64
	var started int64
	err := row.Scan(&r.ID, &r.DatabaseID, &r.Source, &backup, &pre, &r.UploadName, &r.Status, &r.Error, &r.Log, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return Restore{}, ErrNotFound
	}
	if err != nil {
		return Restore{}, err
	}
	if backup.Valid {
		r.BackupID = &backup.Int64
	}
	if pre.Valid {
		r.PreBackupID = &pre.Int64
	}
	r.StartedAt = time.UnixMilli(started)
	if finished.Valid {
		t := time.UnixMilli(finished.Int64)
		r.FinishedAt = &t
	}
	return r, nil
}

// InsertRestore stores r as running, started now.
func (s *Store) InsertRestore(ctx context.Context, r Restore) (Restore, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO database_restores (database_id, source, backup_id, upload_name, status, started_at)
		VALUES (?, ?, ?, ?, ?, ?)`, r.DatabaseID, r.Source, r.BackupID, r.UploadName, JobRunning, s.now().UnixMilli())
	if err != nil {
		return Restore{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Restore{}, err
	}
	return scanRestore(s.db.QueryRowContext(ctx, `SELECT `+restoreColumns+` FROM database_restores WHERE id = ?`, id))
}

// FinishRestore records how a running restore ended.
func (s *Store) FinishRestore(ctx context.Context, id int64, r Restore) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE database_restores SET status = ?, error = ?, log = ?, pre_backup_id = ?, finished_at = ?
		WHERE id = ?`, r.Status, r.Error, r.Log, r.PreBackupID, s.now().UnixMilli(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetRestorePreBackup records the pre_restore Backup as soon as it exists, so
// a restore that is interrupted still links to it.
func (s *Store) SetRestorePreBackup(ctx context.Context, id, backupID int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE database_restores SET pre_backup_id = ? WHERE id = ?`, backupID, id)
	return err
}

// ListRestores returns a Database's last 20 restores, newest first.
func (s *Store) ListRestores(ctx context.Context, databaseID int64) ([]Restore, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+restoreColumns+` FROM database_restores WHERE database_id = ? ORDER BY started_at DESC, id DESC LIMIT 20`, databaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Restore{}
	for rows.Next() {
		r, err := scanRestore(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FailRunning marks every running Backup and restore failed with msg, and
// returns the Backups it failed. The panel calls it at start: nothing that was
// running survives a restart.
func (s *Store) FailRunning(ctx context.Context, msg string) ([]Backup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+backupColumns+` FROM database_backups WHERE status = ?`, JobRunning)
	if err != nil {
		return nil, err
	}
	var out []Backup
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	now := s.now().UnixMilli()
	if _, err := s.db.ExecContext(ctx, `UPDATE database_backups SET status = ?, error = ?, finished_at = ? WHERE status = ?`, JobFailed, msg, now, JobRunning); err != nil {
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE database_restores SET status = ?, error = ?, finished_at = ? WHERE status = ?`, JobFailed, msg, now, JobRunning); err != nil {
		return nil, err
	}
	return out, nil
}
