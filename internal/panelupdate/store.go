package panelupdate

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Settings is the update check's state.
type Settings struct {
	CheckEnabled bool
	LastCheckAt  *time.Time
	LastError    string
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Settings(ctx context.Context) (Settings, error) {
	var v Settings
	var last sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT check_enabled, last_check_at, last_error
		FROM panel_update_settings WHERE id = 1`).Scan(&v.CheckEnabled, &last, &v.LastError)
	if err != nil {
		return Settings{}, err
	}
	if last.Valid {
		t := time.Unix(last.Int64, 0)
		v.LastCheckAt = &t
	}
	return v, nil
}

func (s *Store) SetCheckEnabled(ctx context.Context, on bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE panel_update_settings SET check_enabled = ? WHERE id = 1`, on)
	return err
}

// Available returns the stored manifest and signature, nil when none.
func (s *Store) Available(ctx context.Context) (manifest, sig []byte, err error) {
	err = s.db.QueryRowContext(ctx, `SELECT available_manifest, available_sig
		FROM panel_update_settings WHERE id = 1`).Scan(&manifest, &sig)
	return manifest, sig, err
}

// RecordSuccess stores a finished check. manifest and sig are the result to
// keep, nil to keep none.
func (s *Store) RecordSuccess(ctx context.Context, at time.Time, manifest, sig []byte) error {
	_, err := s.db.ExecContext(ctx, `UPDATE panel_update_settings
		SET last_check_at = ?, last_error = '', available_manifest = ?, available_sig = ? WHERE id = 1`,
		at.Unix(), manifest, sig)
	return err
}

// RecordFailure keeps the last good result and notes the error.
func (s *Store) RecordFailure(ctx context.Context, at time.Time, errText string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE panel_update_settings SET last_check_at = ?, last_error = ? WHERE id = 1`,
		at.Unix(), errText)
	return err
}

// RecordRun copies a finished run to panel_updates. It is safe to repeat: the
// run id keeps one row, and the latest state wins.
func (s *Store) RecordRun(ctx context.Context, st RunStatus, now time.Time) error {
	finished := st.UpdatedAt
	if finished == 0 {
		finished = now.Unix()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO panel_updates (run_id, from_version, to_version, state, error, started_at, finished_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (run_id) WHERE run_id <> '' DO UPDATE SET
			state = excluded.state, error = excluded.error, finished_at = excluded.finished_at`,
		st.Run, st.FromVersion, st.ToVersion, st.State, st.Error, st.StartedAt, finished)
	return err
}

// VacuumInto writes a consistent copy of the database to path, which must not
// exist (VACUUM INTO, as the Migration's database item does).
func (s *Store) VacuumInto(ctx context.Context, path string) error {
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", path); err != nil {
		return fmt.Errorf("copy the database: %w", err)
	}
	return nil
}
