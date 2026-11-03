// Package accesslog keeps Traefik's Access log (ADR-0027): it rotates the file
// Traefik writes under <data dir>/.traefik/access, deletes rotated files past
// the retention period, and serves Traefik's own log and the files to the
// Traefik page.
package accesslog

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrInvalidRetention = errors.New("retention must be at least 1 day")

type Settings struct {
	RetentionDays int
	KeepForever   bool
	LastRotatedAt *time.Time
	LastError     string
}

type Store struct{ db *sql.DB }

func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func (s *Store) Get(ctx context.Context) (Settings, error) {
	var v Settings
	var lastRotated sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT retention_days, keep_forever, last_rotated_at, last_error FROM access_log_settings WHERE id = 1`,
	).Scan(&v.RetentionDays, &v.KeepForever, &lastRotated, &v.LastError)
	if err != nil {
		return Settings{}, err
	}
	if lastRotated.Valid {
		t := time.Unix(lastRotated.Int64, 0).UTC()
		v.LastRotatedAt = &t
	}
	return v, nil
}

func (s *Store) SaveRetention(ctx context.Context, days int, forever bool) error {
	if days < 1 {
		return ErrInvalidRetention
	}
	_, err := s.db.ExecContext(ctx, `UPDATE access_log_settings SET retention_days = ?, keep_forever = ? WHERE id = 1`, days, forever)
	return err
}

// RecordRun stores a run's error, "" for none, and when it rotated a file;
// a nil rotatedAt keeps the previous rotation time.
func (s *Store) RecordRun(ctx context.Context, rotatedAt *time.Time, runErr string) error {
	if rotatedAt == nil {
		_, err := s.db.ExecContext(ctx, `UPDATE access_log_settings SET last_error = ? WHERE id = 1`, runErr)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE access_log_settings SET last_rotated_at = ?, last_error = ? WHERE id = 1`, rotatedAt.Unix(), runErr)
	return err
}
