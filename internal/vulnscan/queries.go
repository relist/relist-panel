package vulnscan

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// newWithin is how recently a CVE must have been first seen to count as new.
const newWithin = 7 * 24 * time.Hour

// Counts are unique CVEs per highest severity in a scope. Fixable counts the
// CVEs with a fixed version for at least one finding in the scope.
type Counts struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Unknown  int `json:"unknown"`
	Fixable  int `json:"fixable"`
}

type ImageResult struct {
	ImageID, ImageRef, OS, Error string
	ScannedAt, AttemptedAt       *time.Time
	Counts                       Counts
}

type FindingRow struct {
	Finding
	FirstSeenAt time.Time
	New         bool
}

type CVEQuery struct {
	ImageIDs             []string
	Severities           []Severity
	FixableOnly, NewOnly bool
	Q                    string
	Page, PageSize       int
}

type CVERow struct {
	VulnID      string
	Severity    Severity
	Title       string
	Fixable     bool
	FirstSeenAt time.Time
	New         bool
	ImageIDs    []string
}

// severityRank orders severities, most severe first.
const severityRank = `CASE severity WHEN 'CRITICAL' THEN 0 WHEN 'HIGH' THEN 1 WHEN 'MEDIUM' THEN 2 WHEN 'LOW' THEN 3 ELSE 4 END`

var rankSeverity = []Severity{SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow, SeverityUnknown}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func stringArgs(values []string) []any {
	args := make([]any, len(values))
	for i, v := range values {
		args[i] = v
	}
	return args
}

func (s *Store) Counts(ctx context.Context, imageIDs []string) (Counts, error) {
	var c Counts
	if len(imageIDs) == 0 {
		return c, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT MIN(`+severityRank+`), MAX(fixed_version != '') FROM image_findings
		WHERE image_id IN (`+placeholders(len(imageIDs))+`) GROUP BY vuln_id`, stringArgs(imageIDs)...)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var rank int
		var fixable bool
		if err := rows.Scan(&rank, &fixable); err != nil {
			return c, err
		}
		switch rank {
		case 0:
			c.Critical++
		case 1:
			c.High++
		case 2:
			c.Medium++
		case 3:
			c.Low++
		default:
			c.Unknown++
		}
		if fixable {
			c.Fixable++
		}
	}
	return c, rows.Err()
}

func (s *Store) ImageResults(ctx context.Context, imageIDs []string) (map[string]ImageResult, error) {
	out := map[string]ImageResult{}
	if len(imageIDs) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT image_id, image_ref, os, error, scanned_at, attempted_at FROM image_scans
		WHERE image_id IN (`+placeholders(len(imageIDs))+`)`, stringArgs(imageIDs)...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r ImageResult
		var scanned sql.NullInt64
		var attempted int64
		if err := rows.Scan(&r.ImageID, &r.ImageRef, &r.OS, &r.Error, &scanned, &attempted); err != nil {
			rows.Close()
			return nil, err
		}
		r.ScannedAt = unixPtr(scanned)
		at := time.Unix(attempted, 0)
		r.AttemptedAt = &at
		out[r.ImageID] = r
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for id, r := range out {
		if r.Counts, err = s.Counts(ctx, []string{id}); err != nil {
			return nil, err
		}
		out[id] = r
	}
	return out, nil
}

func (s *Store) Findings(ctx context.Context, imageID string, now time.Time) ([]FindingRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT f.vuln_id, f.pkg_name, f.pkg_path, f.installed_version, f.fixed_version,
		f.severity, f.title, f.url, COALESCE(fs.first_seen_at, 0)
		FROM image_findings f LEFT JOIN vulnerability_first_seen fs ON fs.vuln_id = f.vuln_id
		WHERE f.image_id = ? ORDER BY `+severityRank+`, f.vuln_id, f.pkg_name`, imageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []FindingRow{}
	for rows.Next() {
		var r FindingRow
		var seen int64
		if err := rows.Scan(&r.VulnID, &r.PkgName, &r.PkgPath, &r.InstalledVersion, &r.FixedVersion,
			&r.Severity, &r.Title, &r.URL, &seen); err != nil {
			return nil, err
		}
		r.FirstSeenAt = time.Unix(seen, 0)
		r.New = now.Sub(r.FirstSeenAt) < newWithin
		out = append(out, r)
	}
	return out, rows.Err()
}

// CVEs lists the unique CVEs across the scope's images, most severe first,
// with the total that match before paging.
func (s *Store) CVEs(ctx context.Context, q CVEQuery, now time.Time) ([]CVERow, int, error) {
	if len(q.ImageIDs) == 0 {
		return []CVERow{}, 0, nil
	}
	where := `f.image_id IN (` + placeholders(len(q.ImageIDs)) + `)`
	args := stringArgs(q.ImageIDs)
	if q.Q != "" {
		where += ` AND (lower(f.vuln_id) LIKE ? OR lower(f.pkg_name) LIKE ? OR lower(f.title) LIKE ?)`
		like := "%" + strings.ToLower(q.Q) + "%"
		args = append(args, like, like, like)
	}
	having := []string{}
	if len(q.Severities) > 0 {
		ranks := make([]string, 0, len(q.Severities))
		for _, sev := range q.Severities {
			for i, known := range rankSeverity {
				if known == sev {
					ranks = append(ranks, string(rune('0'+i)))
				}
			}
		}
		if len(ranks) == 0 {
			return []CVERow{}, 0, nil
		}
		having = append(having, `best IN (`+strings.Join(ranks, ",")+`)`)
	}
	if q.FixableOnly {
		having = append(having, `fixable = 1`)
	}
	if q.NewOnly {
		having = append(having, `seen > ?`)
	}
	grouped := `SELECT f.vuln_id AS vid, MIN(` + severityRank + `) AS best, MAX(f.title) AS title, MAX(f.fixed_version != '') AS fixable,
		COALESCE(MAX(fs.first_seen_at), 0) AS seen, GROUP_CONCAT(DISTINCT f.image_id) AS images
		FROM image_findings f LEFT JOIN vulnerability_first_seen fs ON fs.vuln_id = f.vuln_id
		WHERE ` + where + ` GROUP BY f.vuln_id`
	if len(having) > 0 {
		grouped += ` HAVING ` + strings.Join(having, " AND ")
	}
	if q.NewOnly {
		args = append(args, now.Add(-newWithin).Unix())
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (`+grouped+`)`, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	page, size := max(q.Page, 1), q.PageSize
	if size <= 0 {
		size = 50
	}
	rows, err := s.db.QueryContext(ctx, grouped+` ORDER BY best, vid LIMIT ? OFFSET ?`,
		append(args, size, (page-1)*size)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []CVERow{}
	for rows.Next() {
		var r CVERow
		var best int
		var seen int64
		var images string
		if err := rows.Scan(&r.VulnID, &best, &r.Title, &r.Fixable, &seen, &images); err != nil {
			return nil, 0, err
		}
		r.Severity = rankSeverity[min(best, len(rankSeverity)-1)]
		r.FirstSeenAt = time.Unix(seen, 0)
		r.New = now.Sub(r.FirstSeenAt) < newWithin
		r.ImageIDs = strings.Split(images, ",")
		out = append(out, r)
	}
	return out, total, rows.Err()
}
