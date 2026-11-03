package accesslog

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"relist-panel/internal/database"
)

type testService struct {
	mux     *http.ServeMux
	traefik *fakeTraefik
	dataDir string
}

func newService(t *testing.T) testService {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	dataDir := t.TempDir()
	traefik := &fakeTraefik{}
	mux := http.NewServeMux()
	New(db, dataDir, traefik).RegisterRoutes(mux)
	return testService{mux: mux, traefik: traefik, dataDir: dataDir}
}

func (ts testService) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	ts.mux.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return out
}

func TestLogsFound(t *testing.T) {
	ts := newService(t)
	ts.traefik.logs = "INF up\n"
	rec := ts.do("GET", "/api/traefik/logs", "")
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := decode(t, rec); got["logs"] != "INF up\n" || got["found"] != true {
		t.Fatalf("body = %v", got)
	}
}

func TestLogsNotFound(t *testing.T) {
	ts := newService(t)
	ts.traefik.err = ErrNoTraefik
	rec := ts.do("GET", "/api/traefik/logs", "")
	if got := decode(t, rec); rec.Code != 200 || got["logs"] != "" || got["found"] != false {
		t.Fatalf("status = %d, body = %v", rec.Code, got)
	}
	ts.traefik.err = errors.New("docker down")
	if rec := ts.do("GET", "/api/traefik/logs", ""); rec.Code != 500 {
		t.Fatalf("other error status = %d, want 500", rec.Code)
	}
}

func TestNilTraefikIsNotFound(t *testing.T) {
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mux := http.NewServeMux()
	New(db, t.TempDir(), nil).RegisterRoutes(mux)
	for _, path := range []string{"/api/traefik/logs", "/api/traefik/status"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if got := decode(t, rec); rec.Code != 200 || got["found"] != false {
			t.Errorf("%s: status = %d, body = %v", path, rec.Code, got)
		}
	}
}

func TestStatusParsesArgs(t *testing.T) {
	ts := newService(t)
	ts.traefik.args = []string{
		"--log.level=INFO",
		"--accesslog.filepath=/letsencrypt/access/access.log",
		"--entrypoints.websecure.forwardedheaders.trustedips=10.0.0.0/8",
	}
	got := decode(t, ts.do("GET", "/api/traefik/status", ""))
	if got["found"] != true || got["accessLogEnabled"] != true || got["trustedIps"] != "10.0.0.0/8" {
		t.Fatalf("body = %v", got)
	}
	ts.traefik.args = []string{"--log.level=INFO"}
	got = decode(t, ts.do("GET", "/api/traefik/status", ""))
	if got["found"] != true || got["accessLogEnabled"] != false || got["trustedIps"] != "" {
		t.Fatalf("body = %v", got)
	}
	ts.traefik.err = ErrNoTraefik
	got = decode(t, ts.do("GET", "/api/traefik/status", ""))
	if got["found"] != false || got["accessLogEnabled"] != false {
		t.Fatalf("body = %v", got)
	}
}

func TestListAccessLogs(t *testing.T) {
	ts := newService(t)
	dir := Dir(ts.dataDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "access.log", "abc", testNow)
	write(t, dir, "access-2026-11-02.log.gz", "de", testNow)
	rec := ts.do("GET", "/api/traefik/access-logs", "")
	var body struct {
		Files []struct {
			Name string  `json:"name"`
			Date *string `json:"date"`
			Size int64   `json:"size"`
		} `json:"files"`
		TotalSize int64 `json:"totalSize"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Files) != 2 || body.Files[0].Name != "access.log" || body.Files[0].Date != nil ||
		body.Files[1].Date == nil || *body.Files[1].Date != "2026-11-02" || body.TotalSize != 5 {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestListAccessLogsEmpty(t *testing.T) {
	got := decode(t, newService(t).do("GET", "/api/traefik/access-logs", ""))
	if files, ok := got["files"].([]any); !ok || len(files) != 0 || got["totalSize"] != float64(0) {
		t.Fatalf("body = %v, want an empty files array", got)
	}
}

func TestDownloadAccessLog(t *testing.T) {
	ts := newService(t)
	dir := Dir(ts.dataDir)
	os.MkdirAll(dir, 0o755)
	write(t, dir, "access-2026-11-02.log.gz", "gzbytes", testNow)
	rec := ts.do("GET", "/api/traefik/access-logs/access-2026-11-02.log.gz", "")
	if rec.Code != 200 || rec.Body.String() != "gzbytes" {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="access-2026-11-02.log.gz"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/gzip" {
		t.Fatalf("Content-Type = %q", got)
	}
}

func TestDownloadRejectsBadNames(t *testing.T) {
	ts := newService(t)
	dir := Dir(ts.dataDir)
	os.MkdirAll(dir, 0o755)
	write(t, dir, "notes.txt", "secret", testNow)
	os.WriteFile(filepath.Join(ts.dataDir, "panel.db.copy"), []byte("secret"), 0o600)
	for _, path := range []string{
		"/api/traefik/access-logs/..%2Fpanel.db",
		"/api/traefik/access-logs/access.log%2F..%2F..%2Fpanel.db.copy",
		"/api/traefik/access-logs/notes.txt",
		"/api/traefik/access-logs/access-2026-01-01.log.gz",
	} {
		rec := ts.do("GET", path, "")
		if rec.Code != 404 || strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("%s: status = %d, body = %q", path, rec.Code, rec.Body.String())
		}
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	ts := newService(t)
	got := decode(t, ts.do("GET", "/api/traefik/access-log-settings", ""))
	if got["retentionDays"] != float64(90) || got["keepForever"] != false || got["lastRotatedAt"] != nil || got["lastError"] != "" {
		t.Fatalf("defaults = %v", got)
	}
	rec := ts.do("PUT", "/api/traefik/access-log-settings", `{"retentionDays":30,"keepForever":true}`)
	if got := decode(t, rec); rec.Code != 200 || got["retentionDays"] != float64(30) || got["keepForever"] != true {
		t.Fatalf("PUT status = %d, body = %v", rec.Code, got)
	}
	rec = ts.do("PUT", "/api/traefik/access-log-settings", `{"retentionDays":0,"keepForever":true}`)
	if got := decode(t, rec); rec.Code != 400 || got["field"] != "retentionDays" {
		t.Fatalf("PUT 0 status = %d, body = %v", rec.Code, got)
	}
	if rec := ts.do("PUT", "/api/traefik/access-log-settings", `{"retentionDays":`); rec.Code != 400 {
		t.Fatalf("malformed status = %d", rec.Code)
	}
	if got := decode(t, ts.do("GET", "/api/traefik/access-log-settings", "")); got["retentionDays"] != float64(30) {
		t.Fatalf("after bad PUTs = %v", got)
	}
}

// A symlink in the access directory is never followed, even under a valid
// name: List skips links, so download refuses them too.
func TestDownloadRefusesSymlink(t *testing.T) {
	ts := newService(t)
	dir := Dir(ts.dataDir)
	os.MkdirAll(dir, 0o755)
	secret := filepath.Join(ts.dataDir, "panel.db.copy")
	os.WriteFile(secret, []byte("secret"), 0o600)
	if err := os.Symlink(secret, filepath.Join(dir, "access-2026-11-02.log")); err != nil {
		t.Fatal(err)
	}
	rec := ts.do("GET", "/api/traefik/access-logs/access-2026-11-02.log", "")
	if rec.Code != 404 || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
}
