package vulnscan

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Severity string

const (
	SeverityCritical Severity = "CRITICAL"
	SeverityHigh     Severity = "HIGH"
	SeverityMedium   Severity = "MEDIUM"
	SeverityLow      Severity = "LOW"
	SeverityUnknown  Severity = "UNKNOWN"
)

// Finding is one CVE matched against one package in one Image. It is fixable
// when FixedVersion is set.
type Finding struct {
	VulnID, PkgName, PkgPath, InstalledVersion, FixedVersion string
	Severity                                                 Severity
	Title, URL                                               string
}

// Report is what one Vulnerability scan found.
type Report struct {
	OS       string
	Findings []Finding
}

// ignoredPackages are dropped at ingest. linux-libc-dev is Debian's kernel
// headers: thousands of kernel CVEs that do not apply to a container.
var ignoredPackages = map[string]bool{"linux-libc-dev": true}

type findingKey struct{ vuln, pkg, path, installed string }

// normalizeFindings drops ignored packages, keeps the first of findings that
// share a primary key, and maps a severity it does not know to UNKNOWN.
func normalizeFindings(in []Finding) []Finding {
	out := make([]Finding, 0, len(in))
	seen := make(map[findingKey]bool, len(in))
	for _, f := range in {
		if ignoredPackages[f.PkgName] {
			continue
		}
		k := findingKey{f.VulnID, f.PkgName, f.PkgPath, f.InstalledVersion}
		if seen[k] {
			continue
		}
		seen[k] = true
		switch f.Severity {
		case SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow, SeverityUnknown:
		default:
			f.Severity = SeverityUnknown
		}
		out = append(out, f)
	}
	return out
}

// RecordScan replaces an image's findings with a successful scan's, in one
// transaction, and records the first time this host saw each CVE.
func (s *Store) RecordScan(ctx context.Context, imageID, imageRef string, r Report, dbVersion string, now time.Time) error {
	findings := normalizeFindings(r.Findings)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO image_scans (image_id, image_ref, os, scanned_at, db_version, error, attempted_at)
		VALUES (?, ?, ?, ?, ?, '', ?)
		ON CONFLICT(image_id) DO UPDATE SET image_ref = excluded.image_ref, os = excluded.os, scanned_at = excluded.scanned_at,
			db_version = excluded.db_version, error = '', attempted_at = excluded.attempted_at`,
		imageID, imageRef, r.OS, now.Unix(), dbVersion, now.Unix()); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM image_findings WHERE image_id = ?`, imageID); err != nil {
		return err
	}
	insert, err := tx.PrepareContext(ctx, `INSERT INTO image_findings
		(image_id, vuln_id, pkg_name, pkg_path, installed_version, fixed_version, severity, title, url)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insert.Close()
	seen, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO vulnerability_first_seen (vuln_id, first_seen_at) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer seen.Close()
	for _, f := range findings {
		if _, err := insert.ExecContext(ctx, imageID, f.VulnID, f.PkgName, f.PkgPath, f.InstalledVersion, f.FixedVersion,
			string(f.Severity), f.Title, f.URL); err != nil {
			return err
		}
		if _, err := seen.ExecContext(ctx, f.VulnID, now.Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RecordFailure records a failed attempt. The last successful findings stay.
func (s *Store) RecordFailure(ctx context.Context, imageID, imageRef, reason string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO image_scans (image_id, image_ref, error, attempted_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(image_id) DO UPDATE SET error = excluded.error, attempted_at = excluded.attempted_at`,
		imageID, imageRef, reason, now.Unix())
	return err
}

// ScanState reports the DB version of an image's last successful scan and
// the error of its last attempt.
func (s *Store) ScanState(ctx context.Context, imageID string) (dbVersion, errText string, found bool, err error) {
	var version sql.NullString
	err = s.db.QueryRowContext(ctx, `SELECT db_version, error FROM image_scans WHERE image_id = ?`, imageID).Scan(&version, &errText)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return version.String, errText, true, nil
}

// PruneExcept deletes the results of every image not in keep. First-seen
// dates are never deleted.
func (s *Store) PruneExcept(ctx context.Context, keep []string) (int, error) {
	query := `DELETE FROM image_scans`
	args := []any{}
	if len(keep) > 0 {
		query += ` WHERE image_id NOT IN (` + placeholders(len(keep)) + `)`
		args = stringArgs(keep)
	}
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
