// Package vulnscan scans the images in use for known vulnerabilities with a
// locked-down Scanner container, one at a time, behind the Deployment queue
// (ADR-0021).
package vulnscan

import (
	"context"
	"database/sql"
	"time"
)

// DefaultScannerImage is the Scanner image used while the operator has not
// chosen one. A release that bumps it upgrades every install that kept the
// default.
const DefaultScannerImage = "aquasec/trivy:0.76.1@sha256:790fd66d1697395d8693d1acaa1ea702406a20f109b6fc7bac8db623eca6c06c"

type Settings struct {
	Enabled, AfterDeploy, Daily bool
	DailyMinute                 int    // minutes after local midnight, 0..1439
	ScannerImage                string // "" = DefaultScannerImage
	ActiveImage                 string // the reference last pulled and verified
	LastDailyAt                 *time.Time
	DBVersion                   string
	DBUpdatedAt                 *time.Time
	DBError                     string
}

// EffectiveImage is the Scanner image reference to run.
func (s Settings) EffectiveImage() string {
	if s.ScannerImage == "" {
		return DefaultScannerImage
	}
	return s.ScannerImage
}

type Store struct {
	db *sql.DB
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func (s *Store) Settings(ctx context.Context) (Settings, error) {
	var v Settings
	var lastDaily, dbUpdated sql.NullInt64
	var dbVersion sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT enabled, after_deploy, daily, daily_minute, scanner_image, active_image,
		last_daily_at, db_version, db_updated_at, db_error FROM scan_settings WHERE id = 1`).Scan(
		&v.Enabled, &v.AfterDeploy, &v.Daily, &v.DailyMinute, &v.ScannerImage, &v.ActiveImage,
		&lastDaily, &dbVersion, &dbUpdated, &v.DBError)
	if err != nil {
		return Settings{}, err
	}
	v.LastDailyAt = unixPtr(lastDaily)
	v.DBUpdatedAt = unixPtr(dbUpdated)
	v.DBVersion = dbVersion.String
	return v, nil
}

// SaveSettings writes the operator's choices. The DB state and the daily and
// active-image bookkeeping are written by their own methods.
func (s *Store) SaveSettings(ctx context.Context, v Settings) error {
	_, err := s.db.ExecContext(ctx, `UPDATE scan_settings SET enabled = ?, after_deploy = ?, daily = ?, daily_minute = ?, scanner_image = ?
		WHERE id = 1`, v.Enabled, v.AfterDeploy, v.Daily, v.DailyMinute, v.ScannerImage)
	return err
}

func (s *Store) SetActiveImage(ctx context.Context, ref string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE scan_settings SET active_image = ? WHERE id = 1`, ref)
	return err
}

// SetDB records a DB update. A failure (errText != "") keeps the version and
// time of the last good update, so scans carry on with the old DB.
func (s *Store) SetDB(ctx context.Context, version string, at time.Time, errText string) error {
	if errText != "" {
		_, err := s.db.ExecContext(ctx, `UPDATE scan_settings SET db_error = ? WHERE id = 1`, errText)
		return err
	}
	_, err := s.db.ExecContext(ctx, `UPDATE scan_settings SET db_version = ?, db_updated_at = ?, db_error = '' WHERE id = 1`,
		version, at.Unix())
	return err
}

func (s *Store) SetLastDaily(ctx context.Context, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE scan_settings SET last_daily_at = ? WHERE id = 1`, at.Unix())
	return err
}

func unixPtr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.Unix(v.Int64, 0)
	return &t
}
