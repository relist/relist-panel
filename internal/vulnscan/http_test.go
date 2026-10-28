package vulnscan

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func (h *harness) mux() *http.ServeMux {
	mux := http.NewServeMux()
	h.svc.RegisterRoutes(mux)
	return mux
}

func call(t *testing.T, mux *http.ServeMux, method, path, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			var list []any
			if json.Unmarshal(rec.Body.Bytes(), &list) == nil {
				return rec.Code, map[string]any{"list": list}
			}
			t.Fatalf("%s %s: body %q", method, path, rec.Body)
		}
	}
	return rec.Code, out
}

func TestGetSettingsDefaults(t *testing.T) {
	h := newHarness(t)
	code, body := call(t, h.mux(), "GET", "/api/vulnerabilities/settings", "")
	if code != 200 || body["enabled"] != false || body["dailyTime"] != "03:00" || body["scannerImage"] != "" ||
		body["defaultScannerImage"] != DefaultScannerImage || body["afterDeploy"] != false || body["daily"] != false {
		t.Fatalf("%d %v", code, body)
	}
}

func TestPutSettingsEnablesAndSaves(t *testing.T) {
	h := newHarness(t)
	code, body := call(t, h.mux(), "PUT", "/api/vulnerabilities/settings",
		`{"enabled":true,"afterDeploy":true,"daily":true,"dailyTime":"04:30","scannerImage":""}`)
	if code != 200 || body["enabled"] != true || body["dailyTime"] != "04:30" || body["daily"] != true {
		t.Fatalf("%d %v", code, body)
	}
	st, _ := h.store.Settings(context.Background())
	if !st.Enabled || !st.AfterDeploy || !st.Daily || st.DailyMinute != 270 {
		t.Fatalf("settings = %+v", st)
	}
	code, _ = call(t, h.mux(), "PUT", "/api/vulnerabilities/settings",
		`{"enabled":false,"afterDeploy":true,"daily":true,"dailyTime":"04:30","scannerImage":""}`)
	st, _ = h.store.Settings(context.Background())
	if code != 200 || st.Enabled || !h.queueEmpty() {
		t.Fatalf("disable: %d %+v", code, st)
	}
}

func TestPutSettingsBadImageKeepsOld(t *testing.T) {
	h := newHarness(t)
	h.enable()
	h.scanner.ensureErr = errors.New("manifest unknown")
	code, body := call(t, h.mux(), "PUT", "/api/vulnerabilities/settings",
		`{"enabled":true,"afterDeploy":true,"daily":false,"dailyTime":"03:00","scannerImage":"aquasec/trivy:9.9.9@sha256:`+hex64+`"}`)
	if code != 422 || body["error"] != "manifest unknown" {
		t.Fatalf("%d %v", code, body)
	}
	st, _ := h.store.Settings(context.Background())
	if st.ScannerImage != "" || st.ActiveImage != DefaultScannerImage {
		t.Fatalf("settings = %+v", st)
	}
	code, body = call(t, h.mux(), "PUT", "/api/vulnerabilities/settings",
		`{"enabled":true,"afterDeploy":true,"daily":false,"dailyTime":"03:00","scannerImage":"aquasec/trivy:latest"}`)
	if code != 422 || body["error"] != ErrInvalidScannerImage.Error() {
		t.Fatalf("%d %v", code, body)
	}
}

func TestPutSettingsInvalidTime(t *testing.T) {
	h := newHarness(t)
	for _, v := range []string{"24:00", "3:00", "aa:bb", ""} {
		code, _ := call(t, h.mux(), "PUT", "/api/vulnerabilities/settings",
			`{"enabled":false,"afterDeploy":false,"daily":false,"dailyTime":"`+v+`","scannerImage":""}`)
		if code != 422 {
			t.Fatalf("%q: %d", v, code)
		}
	}
}

func TestScansWhileOff(t *testing.T) {
	h := newHarness(t)
	code, body := call(t, h.mux(), "POST", "/api/vulnerabilities/scans", `{}`)
	if code != 409 || body["error"] != "Scanning is off" {
		t.Fatalf("%d %v", code, body)
	}
	h.enable()
	code, body = call(t, h.mux(), "POST", "/api/vulnerabilities/scans", `{"imageId":"sha256:app"}`)
	if code != 202 || body["queued"] != float64(1) {
		t.Fatalf("%d %v", code, body)
	}
	code, body = call(t, h.mux(), "POST", "/api/vulnerabilities/scans", ``)
	if code != 202 || body["queued"] != float64(3) {
		t.Fatalf("%d %v", code, body)
	}
}

func TestDeleteCacheWhileOn(t *testing.T) {
	h := newHarness(t)
	h.enable()
	code, body := call(t, h.mux(), "DELETE", "/api/vulnerabilities/cache", "")
	if code != 409 || body["error"] != "Turn scanning off before deleting the cache" {
		t.Fatalf("%d %v", code, body)
	}
	h.svc.Disable(context.Background())
	if code, _ := call(t, h.mux(), "DELETE", "/api/vulnerabilities/cache", ""); code != 204 || h.scanner.count("deletecache") != 1 {
		t.Fatalf("%d", code)
	}
}

func seedFindings(h *harness) {
	ctx := context.Background()
	h.store.RecordScan(ctx, "sha256:app", "app:1", Report{Findings: []Finding{
		f("CVE-1", "openssl", SeverityCritical, "3.1"),
		f("CVE-2", "zlib", SeverityHigh, ""),
	}}, "db1", t0)
	h.store.RecordScan(ctx, "sha256:other", "other:1", Report{Findings: []Finding{
		f("CVE-1", "openssl", SeverityCritical, ""),
	}}, "db1", t0)
}

func TestImagesListUsers(t *testing.T) {
	h := newHarness(t)
	h.enable()
	seedFindings(h)
	code, body := call(t, h.mux(), "GET", "/api/vulnerabilities/images", "")
	list := body["list"].([]any)
	if code != 200 || len(list) != 3 {
		t.Fatalf("%d %v", code, body)
	}
	app := list[0].(map[string]any)
	counts := app["counts"].(map[string]any)
	users := app["users"].([]any)
	if app["imageId"] != "sha256:app" || counts["critical"] != float64(1) || counts["fixable"] != float64(1) ||
		users[0].(map[string]any)["service"] != "web" || app["scannedAt"] == nil {
		t.Fatalf("app = %v", app)
	}
	scanner := list[2].(map[string]any)
	if scanner["users"].([]any)[0].(map[string]any)["kind"] != "scanner" || scanner["scannedAt"] != nil || scanner["counts"] != nil {
		t.Fatalf("scanner = %v", scanner)
	}
	code, body = call(t, h.mux(), "GET", "/api/vulnerabilities/images/sha256:app/findings", "")
	findings := body["list"].([]any)
	if code != 200 || len(findings) != 2 || findings[0].(map[string]any)["vulnId"] != "CVE-1" || findings[0].(map[string]any)["new"] != true {
		t.Fatalf("%d %v", code, body)
	}
}

func TestSummaryCompose(t *testing.T) {
	h := newHarness(t)
	h.enable()
	seedFindings(h)
	h.inUse.images[1].Users[0].ComposeID = 3 // both images now belong to Compose 3
	code, body := call(t, h.mux(), "GET", "/api/vulnerabilities/summary?composeId=3", "")
	counts := body["counts"].(map[string]any)
	if code != 200 || body["enabled"] != true || counts["critical"] != float64(1) || counts["high"] != float64(1) {
		t.Fatalf("%d %v", code, body)
	}
	services := body["services"].([]any)
	if len(services) != 2 || services[1].(map[string]any)["service"] != "db" ||
		services[1].(map[string]any)["counts"].(map[string]any)["critical"] != float64(1) {
		t.Fatalf("services = %v", services)
	}
	code, body = call(t, h.mux(), "GET", "/api/vulnerabilities/summary?projectId=1", "")
	if code != 200 || body["counts"].(map[string]any)["critical"] != float64(1) || body["services"] != nil {
		t.Fatalf("%d %v", code, body)
	}
	code, body = call(t, h.mux(), "GET", "/api/vulnerabilities/summary?projectId=99", "")
	if code != 200 || body["counts"].(map[string]any)["critical"] != float64(0) {
		t.Fatalf("%d %v", code, body)
	}
}

func TestSummaryOffHidesCounts(t *testing.T) {
	h := newHarness(t)
	seedFindings(h)
	code, body := call(t, h.mux(), "GET", "/api/vulnerabilities/summary", "")
	if code != 200 || body["enabled"] != false || body["counts"] != nil {
		t.Fatalf("%d %v", code, body)
	}
}

func TestCVEsQuery(t *testing.T) {
	h := newHarness(t)
	h.enable()
	seedFindings(h)
	code, body := call(t, h.mux(), "GET", "/api/vulnerabilities/cves?severity=CRITICAL&fixable=1&page=1&pageSize=10", "")
	items := body["items"].([]any)
	if code != 200 || body["total"] != float64(1) || items[0].(map[string]any)["vulnId"] != "CVE-1" ||
		len(items[0].(map[string]any)["imageIds"].([]any)) != 2 {
		t.Fatalf("%d %v", code, body)
	}
	code, body = call(t, h.mux(), "GET", "/api/vulnerabilities/cves?composeId=4", "")
	if code != 200 || body["total"] != float64(1) {
		t.Fatalf("%d %v", code, body)
	}
	if code, _ := call(t, h.mux(), "GET", "/api/vulnerabilities/cves?page=abc", ""); code != 400 {
		t.Fatalf("bad page: %d", code)
	}
}

func TestStatusDBStale(t *testing.T) {
	h := newHarness(t)
	h.enable()
	h.scanner.cacheBytes = 1500
	h.svc.d.Now = func() time.Time { return t0.Add(8 * 24 * time.Hour) }
	code, body := call(t, h.mux(), "GET", "/api/vulnerabilities/status", "")
	db := body["db"].(map[string]any)
	if code != 200 || db["stale"] != true || db["version"] != "db1" || body["queued"] != float64(0) ||
		body["cacheBytes"] != float64(1500) || body["timeZone"] == "" || body["running"] != nil {
		t.Fatalf("%d %v", code, body)
	}
	h.svc.d.Now = func() time.Time { return t0.Add(time.Hour) }
	_, body = call(t, h.mux(), "GET", "/api/vulnerabilities/status", "")
	if body["db"].(map[string]any)["stale"] != false {
		t.Fatalf("fresh DB stale: %v", body)
	}
	h.store.SetDB(context.Background(), "", t0, "network down")
	_, body = call(t, h.mux(), "GET", "/api/vulnerabilities/status", "")
	if db := body["db"].(map[string]any); db["stale"] != true || db["error"] != "network down" {
		t.Fatalf("failed update not stale: %v", body)
	}
}

func hubFake(t *testing.T, status int, body string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/repositories/aquasec/trivy/tags" || r.URL.Query().Get("page_size") != "100" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestScannerTagsHub(t *testing.T) {
	h := newHarness(t)
	h.svc.d.Now = func() time.Time { return time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC) }
	srv := hubFake(t, 200, `{"results":[
	 {"name":"latest","digest":"sha256:`+hex64+`","tag_last_pushed":"2026-10-21T11:16:41Z"},
	 {"name":"0.76.1","digest":"sha256:`+hex64+`","tag_last_pushed":"2026-10-21T11:16:41Z"},
	 {"name":"0.76.0","digest":"sha256:`+hex64+`","tag_last_pushed":"2026-10-14T11:45:44Z"}]}`)
	h.svc.WithHub(srv.URL, srv.Client())
	code, body := call(t, h.mux(), "GET", "/api/vulnerabilities/scanner-tags", "")
	tags := body["tags"].([]any)
	if code != 200 || len(tags) != 2 || tags[0].(map[string]any)["tag"] != "0.76.1" || tags[0].(map[string]any)["young"] != true {
		t.Fatalf("%d %v", code, body)
	}
	if tags[0].(map[string]any)["ref"] != "aquasec/trivy:0.76.1@sha256:"+hex64 {
		t.Fatalf("ref = %v", tags[0])
	}
}

func TestPickTags(t *testing.T) {
	now := time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC)
	day := func(d int) time.Time { return now.Add(-time.Duration(d) * 24 * time.Hour) }
	raw := []hubTag{
		{Name: "latest", PushedAt: day(1)},
		{Name: "canary", PushedAt: day(1)},
		{Name: "0.77.0-rc1", PushedAt: day(2)},
		{Name: "0.76.1", PushedAt: day(7)},
		{Name: "v0.76.0", PushedAt: day(13)},
		{Name: "0.75.0", PushedAt: day(27)},
		{Name: "0.74.0", PushedAt: day(75)},
	}
	got := pickTags(raw, now)
	names := []string{}
	for _, tag := range got {
		names = append(names, tag.Tag)
	}
	if strings.Join(names, ",") != "0.76.1,v0.76.0,0.75.0" {
		t.Fatalf("tags = %v", names)
	}
	if !got[0].Young || !got[1].Young || got[2].Young || got[2].AgeDays != 27 {
		t.Fatalf("got = %+v", got)
	}
	if only := pickTags(raw[5:], now); len(only) != 1 || only[0].Tag != "0.75.0" {
		t.Fatalf("no recent tags: %+v", only)
	}
}

func TestScannerTagsNonHub(t *testing.T) {
	h := newHarness(t)
	h.store.db.Exec(`UPDATE scan_settings SET scanner_image = ?`, "ghcr.io/aquasecurity/trivy:0.76.1@sha256:"+hex64)
	code, _ := call(t, h.mux(), "GET", "/api/vulnerabilities/scanner-tags", "")
	if code != 400 {
		t.Fatalf("code = %d", code)
	}
}

func TestScannerTagsHubDown(t *testing.T) {
	h := newHarness(t)
	srv := hubFake(t, 500, `oops`)
	h.svc.WithHub(srv.URL, srv.Client())
	code, _ := call(t, h.mux(), "GET", "/api/vulnerabilities/scanner-tags", "")
	if code != 502 {
		t.Fatalf("code = %d", code)
	}
}

func TestScannerRefs(t *testing.T) {
	got := ScannerRefs("aquasec/trivy:0.76.1@sha256:" + hex64)
	if len(got) != 2 || got[0] != "aquasec/trivy:0.76.1" || got[1] != "aquasec/trivy@sha256:"+hex64 {
		t.Fatalf("refs = %v", got)
	}
	got = ScannerRefs("localhost:5000/trivy@sha256:" + hex64)
	if len(got) != 1 || got[0] != "localhost:5000/trivy@sha256:"+hex64 {
		t.Fatalf("refs = %v", got)
	}
}
