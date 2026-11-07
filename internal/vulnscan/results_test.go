package vulnscan

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func f(id, pkg string, sev Severity, fixed string) Finding {
	return Finding{VulnID: id, PkgName: pkg, InstalledVersion: "1.0", FixedVersion: fixed, Severity: sev, Title: id + " title"}
}

func TestNormalizeDropsLibcDevAndCollapses(t *testing.T) {
	got := normalizeFindings([]Finding{
		f("CVE-1", "openssl", "HIGH", "1.1"),
		f("CVE-1", "openssl", "HIGH", "1.2"), // same primary key: first wins
		f("CVE-2", "linux-libc-dev", "CRITICAL", ""),
		f("CVE-3", "zlib", "weird", ""),
	})
	if len(got) != 2 {
		t.Fatalf("got %d findings: %+v", len(got), got)
	}
	if got[0].FixedVersion != "1.1" {
		t.Fatalf("first duplicate not kept: %+v", got[0])
	}
	if got[1].Severity != SeverityUnknown {
		t.Fatalf("unknown severity = %q", got[1].Severity)
	}
}

func countFindings(t *testing.T, s *Store, imageID string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM image_findings WHERE image_id = ?`, imageID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestRecordScanReplacesFindings(t *testing.T) {
	ctx := context.Background()
	s := NewStore(newDB(t))
	r := Report{OS: "alpine 3.22", Findings: []Finding{f("CVE-1", "a", "HIGH", ""), f("CVE-2", "b", "LOW", "")}}
	if err := s.RecordScan(ctx, "sha256:i", "app:1", r, "db1", t0); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordScan(ctx, "sha256:i", "app:1", Report{Findings: r.Findings[:1]}, "db2", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := countFindings(t, s, "sha256:i"); n != 1 {
		t.Fatalf("findings = %d, want 1", n)
	}
	version, errText, found, err := s.ScanState(ctx, "sha256:i")
	if err != nil || !found || version != "db2" || errText != "" {
		t.Fatalf("ScanState = %q %q %v %v", version, errText, found, err)
	}
}

func TestRecordFailureKeepsFindings(t *testing.T) {
	ctx := context.Background()
	s := NewStore(newDB(t))
	s.RecordScan(ctx, "sha256:i", "app:1", Report{Findings: []Finding{f("CVE-1", "a", "HIGH", "")}}, "db1", t0)
	if err := s.RecordFailure(ctx, "sha256:i", "app:1", "boom", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if n := countFindings(t, s, "sha256:i"); n != 1 {
		t.Fatalf("findings = %d, want 1", n)
	}
	results, err := s.ImageResults(ctx, []string{"sha256:i"})
	if err != nil {
		t.Fatal(err)
	}
	r := results["sha256:i"]
	if r.Error != "boom" || r.ScannedAt == nil || !r.ScannedAt.Equal(t0) || !r.AttemptedAt.Equal(t0.Add(time.Hour)) || r.Counts.High != 1 {
		t.Fatalf("result = %+v", r)
	}
	// A failure on a never-scanned image still records the attempt.
	if err := s.RecordFailure(ctx, "sha256:new", "new:1", "nope", t0); err != nil {
		t.Fatal(err)
	}
	results, _ = s.ImageResults(ctx, []string{"sha256:new"})
	if results["sha256:new"].Error != "nope" || results["sha256:new"].ScannedAt != nil {
		t.Fatalf("new = %+v", results["sha256:new"])
	}
}

func TestFirstSeenSurvivesPrune(t *testing.T) {
	ctx := context.Background()
	s := NewStore(newDB(t))
	s.RecordScan(ctx, "sha256:a", "a", Report{Findings: []Finding{f("CVE-1", "x", "HIGH", "")}}, "db", t0)
	if n, err := s.PruneExcept(ctx, nil); err != nil || n != 1 {
		t.Fatalf("PruneExcept = %d, %v", n, err)
	}
	if n := countFindings(t, s, "sha256:a"); n != 0 {
		t.Fatalf("findings not cascaded: %d", n)
	}
	s.RecordScan(ctx, "sha256:b", "b", Report{Findings: []Finding{f("CVE-1", "x", "HIGH", "")}}, "db", t0.Add(48*time.Hour))
	rows, err := s.Findings(ctx, "sha256:b", t0.Add(48*time.Hour))
	if err != nil || len(rows) != 1 || !rows[0].FirstSeenAt.Equal(t0) {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
}

func TestPruneExceptKeeps(t *testing.T) {
	ctx := context.Background()
	s := NewStore(newDB(t))
	s.RecordScan(ctx, "sha256:a", "a", Report{}, "db", t0)
	s.RecordScan(ctx, "sha256:b", "b", Report{}, "db", t0)
	if n, err := s.PruneExcept(ctx, []string{"sha256:a"}); err != nil || n != 1 {
		t.Fatalf("PruneExcept = %d, %v", n, err)
	}
	if _, _, found, _ := s.ScanState(ctx, "sha256:a"); !found {
		t.Fatal("kept image pruned")
	}
}

func TestCountsUniqueAcrossImages(t *testing.T) {
	ctx := context.Background()
	s := NewStore(newDB(t))
	s.RecordScan(ctx, "sha256:a", "a", Report{Findings: []Finding{f("CVE-1", "x", "CRITICAL", ""), f("CVE-2", "y", "HIGH", "")}}, "db", t0)
	s.RecordScan(ctx, "sha256:b", "b", Report{Findings: []Finding{f("CVE-1", "x", "CRITICAL", "2.0"), f("CVE-3", "z", "LOW", "")}}, "db", t0)
	c, err := s.Counts(ctx, []string{"sha256:a", "sha256:b"})
	if err != nil {
		t.Fatal(err)
	}
	if c != (Counts{Critical: 1, High: 1, Low: 1, Fixable: 1}) {
		t.Fatalf("counts = %+v", c)
	}
	c, _ = s.Counts(ctx, []string{"sha256:a"})
	if c != (Counts{Critical: 1, High: 1}) {
		t.Fatalf("counts a = %+v", c)
	}
	c, _ = s.Counts(ctx, nil)
	if c != (Counts{}) {
		t.Fatalf("empty scope counts = %+v", c)
	}
}

func TestFindingsNewFlag(t *testing.T) {
	ctx := context.Background()
	s := NewStore(newDB(t))
	now := t0.Add(30 * 24 * time.Hour)
	s.RecordScan(ctx, "sha256:a", "a", Report{Findings: []Finding{f("CVE-OLD", "x", "LOW", "")}}, "db", now.Add(-8*24*time.Hour))
	s.RecordScan(ctx, "sha256:a", "a", Report{Findings: []Finding{f("CVE-OLD", "x", "LOW", ""), f("CVE-NEW", "y", "CRITICAL", "")}}, "db", now.Add(-6*24*time.Hour))
	rows, err := s.Findings(ctx, "sha256:a", now)
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	if rows[0].VulnID != "CVE-NEW" || !rows[0].New || rows[1].VulnID != "CVE-OLD" || rows[1].New {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestCVEsFiltersAndPagination(t *testing.T) {
	ctx := context.Background()
	s := NewStore(newDB(t))
	now := t0.Add(30 * 24 * time.Hour)
	s.RecordScan(ctx, "sha256:a", "a", Report{Findings: []Finding{
		f("CVE-1", "openssl", "CRITICAL", "3.0"),
		f("CVE-2", "zlib", "HIGH", ""),
		f("CVE-3", "curl", "MEDIUM", ""),
	}}, "db", now.Add(-20*24*time.Hour))
	s.RecordScan(ctx, "sha256:b", "b", Report{Findings: []Finding{
		f("CVE-1", "openssl", "HIGH", ""),
		f("CVE-4", "glibc", "LOW", "2.1"),
		f("CVE-5", "bash", "UNKNOWN", ""),
	}}, "db", now.Add(-1*24*time.Hour))
	scope := []string{"sha256:a", "sha256:b"}

	rows, total, err := s.CVEs(ctx, CVEQuery{ImageIDs: scope, Page: 1, PageSize: 50}, now)
	if err != nil || total != 5 || len(rows) != 5 {
		t.Fatalf("all = %d %d %v", total, len(rows), err)
	}
	if rows[0].VulnID != "CVE-1" || rows[0].Severity != SeverityCritical || !rows[0].Fixable || len(rows[0].ImageIDs) != 2 {
		t.Fatalf("first = %+v", rows[0])
	}
	_, total, _ = s.CVEs(ctx, CVEQuery{ImageIDs: scope, Severities: []Severity{"CRITICAL", "HIGH"}, Page: 1, PageSize: 50}, now)
	if total != 2 {
		t.Fatalf("severity total = %d", total)
	}
	_, total, _ = s.CVEs(ctx, CVEQuery{ImageIDs: scope, FixableOnly: true, Page: 1, PageSize: 50}, now)
	if total != 2 {
		t.Fatalf("fixable total = %d", total)
	}
	rows, total, _ = s.CVEs(ctx, CVEQuery{ImageIDs: scope, NewOnly: true, Page: 1, PageSize: 50}, now)
	if total != 2 || rows[0].VulnID != "CVE-4" {
		t.Fatalf("new = %d %+v", total, rows)
	}
	rows, total, _ = s.CVEs(ctx, CVEQuery{ImageIDs: scope, Q: "OPENSSL", Page: 1, PageSize: 50}, now)
	if total != 1 || rows[0].VulnID != "CVE-1" {
		t.Fatalf("q = %d %+v", total, rows)
	}
	rows, total, _ = s.CVEs(ctx, CVEQuery{ImageIDs: scope, Page: 2, PageSize: 2}, now)
	if total != 5 || len(rows) != 2 || rows[0].VulnID != "CVE-3" {
		t.Fatalf("page 2 = %d %+v", total, rows)
	}
	rows, total, _ = s.CVEs(ctx, CVEQuery{ImageIDs: []string{"sha256:b"}, Page: 1, PageSize: 50}, now)
	if total != 3 || rows[0].Severity != SeverityHigh {
		t.Fatalf("scope b = %d %+v", total, rows)
	}
}

func TestRecordScanLarge(t *testing.T) {
	ctx := context.Background()
	s := NewStore(newDB(t))
	findings := make([]Finding, 6000)
	for i := range findings {
		findings[i] = f(fmt.Sprintf("CVE-2026-%05d", i), fmt.Sprintf("pkg%d", i%300), "MEDIUM", "")
	}
	start := time.Now()
	if err := s.RecordScan(ctx, "sha256:big", "node:24", Report{Findings: findings}, "db", t0); err != nil {
		t.Fatal(err)
	}
	// One transaction keeps 6000 findings well under 2s (0.1s normally);
	// a row per commit would not. The race detector slows everything
	// ~40x, so only the count is checked under it.
	if d := time.Since(start); d > 2*time.Second && !raceEnabled {
		t.Fatalf("RecordScan took %s", d)
	}
	if n := countFindings(t, s, "sha256:big"); n != 6000 {
		t.Fatalf("findings = %d", n)
	}
}
