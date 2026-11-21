package databases

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// What started a Backup. Only scheduled ones are ever rotated or pinned.
const (
	TriggerManual     = "manual"
	TriggerScheduled  = "scheduled"
	TriggerPreRestore = "pre_restore"
	TriggerPreUpdate  = "pre_update"
	TriggerPreDrop    = "pre_drop"
)

// The state of a Backup or a restore.
const (
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
)

// Backup is one gzipped dump file of one logical database of a Database.
// LogicalName is empty for a Backup of the earlier, whole-instance format.
type Backup struct {
	ID, DatabaseID int64
	Trigger        string
	LogicalName    string
	Status         string
	Error, Log     string
	FileName       string
	Size           int64
	SHA256         string
	StartedAt      time.Time
	FinishedAt     *time.Time
	// Pinned keeps a scheduled Backup from Rotation. LocalRemovedAt is set
	// when Rotation removed the file but a copy in a Backup destination is
	// still tracked: the Backup is "Remote only".
	Pinned         bool
	LocalRemovedAt *time.Time
	// Upload is the latest copy to a Backup destination; ListBackups fills it.
	Upload *Upload
	// State is what the dump leaves out, as it was when the Backup was
	// taken; empty for Backups from before it was kept.
	State BackupState
}

// BackupState is what a one-database dump leaves out, read when the Backup
// is taken (ADR-0050): a restore into a new database can create it the way
// it was.
type BackupState struct {
	// Prev is InspectSQL's line: "<owner>\t<ACL>" (Postgres) or
	// "<charset>\t<collation>" (MySQL, MariaDB).
	Prev string `json:"prev,omitempty"`
	// Access is each login user's level on the database, for Recreate.
	Access map[string]Access `json:"access,omitempty"`
}

// The state of an Upload.
const (
	UploadRunning  = "running"
	UploadUploaded = "uploaded"
	UploadFailed   = "failed"
)

// Upload is one Backup's copy in one Backup destination. It is live while it
// is uploaded and DeletedAt is nil. DestinationName is read, never written.
type Upload struct {
	ID, BackupID, DestinationID int64
	DestinationName             string
	ObjectKey, Status, Error    string
	Encrypted                   bool
	UploadedAt, DeletedAt       *time.Time
}

// Restore is one restore of a Backup or of an uploaded file.
type Restore struct {
	ID, DatabaseID     int64
	Source             string // "backup" | "upload"
	BackupID           *int64
	PreBackupID        *int64
	UploadName         string
	Target             string // the logical database it restored into
	Status, Error, Log string
	StartedAt          time.Time
	FinishedAt         *time.Time
}

const backupColumns = `id, database_id, trigger, logical_name, status, error, log, file_name, size, sha256, started_at, finished_at, pinned, local_removed_at, state`

func scanBackup(row interface{ Scan(...any) error }) (Backup, error) {
	var b Backup
	var started int64
	var finished, removed sql.NullInt64
	var state string
	err := row.Scan(&b.ID, &b.DatabaseID, &b.Trigger, &b.LogicalName, &b.Status, &b.Error, &b.Log, &b.FileName, &b.Size, &b.SHA256, &started, &finished, &b.Pinned, &removed, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return Backup{}, ErrNotFound
	}
	if err != nil {
		return Backup{}, err
	}
	if state != "" {
		// A state that does not read is no state: the restore falls back.
		_ = json.Unmarshal([]byte(state), &b.State)
	}
	b.StartedAt = time.UnixMilli(started)
	if finished.Valid {
		t := time.UnixMilli(finished.Int64)
		b.FinishedAt = &t
	}
	if removed.Valid {
		t := time.UnixMilli(removed.Int64)
		b.LocalRemovedAt = &t
	}
	return b, nil
}

// InsertBackup stores b as running, started now.
func (s *Store) InsertBackup(ctx context.Context, b Backup) (Backup, error) {
	// scope is a column of the first schema; it is written and never read.
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO database_backups (database_id, trigger, scope, logical_name, status, started_at)
		VALUES (?, ?, '{}', ?, ?, ?)`, b.DatabaseID, b.Trigger, b.LogicalName, JobRunning, s.now().UnixMilli())
	if err != nil {
		return Backup{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Backup{}, err
	}
	return scanBackup(s.db.QueryRowContext(ctx, `SELECT `+backupColumns+` FROM database_backups WHERE id = ?`, id))
}

// SetBackupState records what Backup id's dump leaves out.
func (s *Store) SetBackupState(ctx context.Context, id int64, st BackupState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE database_backups SET state = ? WHERE id = ?`, string(raw), id)
	return err
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
	out := []Backup{}
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
	latest, err := s.latestUploads(ctx, databaseID)
	if err != nil {
		return nil, err
	}
	for i := range out {
		if u, ok := latest[out[i].ID]; ok {
			out[i].Upload = &u
		}
	}
	return out, nil
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

const restoreColumns = `id, database_id, source, backup_id, pre_backup_id, upload_name, target, status, error, log, started_at, finished_at`

func scanRestore(row interface{ Scan(...any) error }) (Restore, error) {
	var r Restore
	var backup, pre, finished sql.NullInt64
	var started int64
	err := row.Scan(&r.ID, &r.DatabaseID, &r.Source, &backup, &pre, &r.UploadName, &r.Target, &r.Status, &r.Error, &r.Log, &started, &finished)
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
		INSERT INTO database_restores (database_id, source, backup_id, upload_name, target, status, started_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, r.DatabaseID, r.Source, r.BackupID, r.UploadName, r.Target, JobRunning, s.now().UnixMilli())
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

const uploadColumns = `u.id, u.backup_id, u.destination_id, d.name, u.object_key, u.status, u.error, u.encrypted, u.uploaded_at, u.deleted_at`

func scanUpload(row interface{ Scan(...any) error }) (Upload, error) {
	var u Upload
	var up, del sql.NullInt64
	if err := row.Scan(&u.ID, &u.BackupID, &u.DestinationID, &u.DestinationName, &u.ObjectKey, &u.Status, &u.Error, &u.Encrypted, &up, &del); err != nil {
		return Upload{}, err
	}
	if up.Valid {
		t := time.UnixMilli(up.Int64)
		u.UploadedAt = &t
	}
	if del.Valid {
		t := time.UnixMilli(del.Int64)
		u.DeletedAt = &t
	}
	return u, nil
}

// latestUploads is each Backup of the Database's latest upload row.
func (s *Store) latestUploads(ctx context.Context, databaseID int64) (map[int64]Upload, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+uploadColumns+` FROM database_backup_uploads u
		JOIN backup_destinations d ON d.id = u.destination_id
		JOIN database_backups b ON b.id = u.backup_id
		WHERE b.database_id = ? ORDER BY u.id`, databaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]Upload{}
	for rows.Next() {
		u, err := scanUpload(rows)
		if err != nil {
			return nil, err
		}
		out[u.BackupID] = u // later rows win: the latest attempt
	}
	return out, rows.Err()
}

// InsertUpload stores u (ID is set here) and returns the stored row.
func (s *Store) InsertUpload(ctx context.Context, u Upload) (Upload, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO database_backup_uploads (backup_id, destination_id, object_key, status, encrypted) VALUES (?, ?, ?, ?, ?)`,
		u.BackupID, u.DestinationID, u.ObjectKey, u.Status, u.Encrypted)
	if err != nil {
		return Upload{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Upload{}, err
	}
	return scanUpload(s.db.QueryRowContext(ctx, `
		SELECT `+uploadColumns+` FROM database_backup_uploads u JOIN backup_destinations d ON d.id = u.destination_id WHERE u.id = ?`, id))
}

// FinishUpload records how a running upload ended; at is when an uploaded one finished.
func (s *Store) FinishUpload(ctx context.Context, id int64, status, errMsg string, at time.Time) error {
	var uploaded any
	if status == UploadUploaded {
		uploaded = at.UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE database_backup_uploads SET status = ?, error = ?, uploaded_at = ? WHERE id = ?`, status, errMsg, uploaded, id)
	return err
}

// LiveUpload is a Backup's copy that is in a destination now: uploaded and
// not deleted. nil when there is none.
func (s *Store) LiveUpload(ctx context.Context, backupID int64) (*Upload, error) {
	u, err := scanUpload(s.db.QueryRowContext(ctx, `
		SELECT `+uploadColumns+` FROM database_backup_uploads u JOIN backup_destinations d ON d.id = u.destination_id
		WHERE u.backup_id = ? AND u.status = 'uploaded' AND u.deleted_at IS NULL ORDER BY u.id DESC LIMIT 1`, backupID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// LiveUploads is every live copy of the Database's Backups, in the destination
// destID when it is not 0, else in any.
func (s *Store) LiveUploads(ctx context.Context, databaseID, destID int64) ([]Upload, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+uploadColumns+` FROM database_backup_uploads u
		JOIN backup_destinations d ON d.id = u.destination_id
		JOIN database_backups b ON b.id = u.backup_id
		WHERE b.database_id = ? AND u.status = 'uploaded' AND u.deleted_at IS NULL AND (? = 0 OR u.destination_id = ?)
		ORDER BY u.id`, databaseID, destID, destID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Upload
	for rows.Next() {
		u, err := scanUpload(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// MarkUploadDeleted records that a copy was removed from its destination.
func (s *Store) MarkUploadDeleted(ctx context.Context, id int64, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE database_backup_uploads SET deleted_at = ? WHERE id = ?`, at.UnixMilli(), id)
	return err
}

// DeleteUpload removes an upload row (a failed attempt that is being retried).
func (s *Store) DeleteUpload(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM database_backup_uploads WHERE id = ?`, id)
	return err
}

// FailRunningOf fails the Backups, restores and Backup copies of Database
// id still marked running: a job of its that panicked left them behind.
func (s *Store) FailRunningOf(ctx context.Context, id int64, msg string) error {
	now := s.now().UnixMilli()
	if _, err := s.db.ExecContext(ctx, `UPDATE database_backups SET status = ?, error = ?, finished_at = ? WHERE status = ? AND database_id = ?`,
		JobFailed, msg, now, JobRunning, id); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE database_restores SET status = ?, error = ?, finished_at = ? WHERE status = ? AND database_id = ?`,
		JobFailed, msg, now, JobRunning, id); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE database_backup_uploads SET status = 'failed', error = ?
		WHERE status = 'running' AND backup_id IN (SELECT id FROM database_backups WHERE database_id = ?)`, msg, id)
	return err
}

// FailRunningUploads marks every running upload failed with msg.
func (s *Store) FailRunningUploads(ctx context.Context, msg string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE database_backup_uploads SET status = 'failed', error = ? WHERE status = 'running'`, msg)
	return err
}
