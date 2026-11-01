package panelupdate

import (
	"context"
	"database/sql"
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
