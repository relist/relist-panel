package requestmetrics

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"relist-panel/internal/domains"
	"relist-panel/internal/traefikconfig"
)

type httpRig struct {
	t     *testing.T
	db    *sql.DB
	store *Store
	paths traefikconfig.Paths
	mux   *http.ServeMux
	now   time.Time
}

func newHTTPRig(t *testing.T) *httpRig {
	t.Helper()
	db := openDB(t)
	r := &httpRig{t: t, db: db, store: NewStore(db), now: at(30, 0)}
	r.paths = traefikconfig.PathsFor(t.TempDir())
	s := NewService(r.store, r.paths, domains.NewStore(db), db)
	s.now = func() time.Time { return r.now }
	r.mux = http.NewServeMux()
	s.RegisterRoutes(r.mux)
	return r
}

func (r *httpRig) exec(q string, args ...any) {
	r.t.Helper()
	if _, err := r.db.Exec(q, args...); err != nil {
		r.t.Fatalf("%s: %v", q, err)
	}
}

// seedCompose makes project/environment/compose ids (p, p, c) with the
// given domain hosts.
func (r *httpRig) seedCompose(p, c int64, hosts ...string) {
	r.exec(`INSERT INTO projects (id, name, description, created_at, updated_at) VALUES (?, ?, '', 1, 1)`, p, "p"+string(rune('0'+p)))
	r.exec(`INSERT INTO environments (id, project_id, name, created_at) VALUES (?, ?, 'prod', 1)`, p, p)
	r.exec(`INSERT INTO composes (id, environment_id, name, yaml_content, created_at, updated_at) VALUES (?, ?, 'web', '', 1, 1)`, c, p)
	for i, h := range hosts {
		path := "/"
		if i > 0 && h == hosts[i-1] {
			path = "/api"
		}
		r.exec(`INSERT INTO domains (compose_id, host, path, service, port, created_at, updated_at) VALUES (?, ?, ?, 'web', 80, 1, 1)`, c, h, path)
	}
}

func (r *httpRig) get(path string) (int, map[string]any) {
	r.t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, req)
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body
}

func (r *httpRig) put(path, body string) (int, map[string]any) {
	r.t.Helper()
	req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	r.mux.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func (r *httpRig) write(rows ...Row) {
	r.t.Helper()
	if err := r.store.WriteMinutes(context.Background(), rows, Cursor{}); err != nil {
		r.t.Fatal(err)
	}
}

func (r *httpRig) writeEffective(override string) {
	r.t.Helper()
	out, _, err := traefikconfig.Generate([]byte(override))
	if err != nil {
		r.t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(r.paths.Effective()), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(r.paths.Effective(), out, 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func num(v any) float64 { f, _ := v.(float64); return f }

func TestTotalMetrics(t *testing.T) {
	r := newHTTPRig(t)
	r.write(
		mkRow(total, at(20, 0), 10, 10*time.Millisecond, 10*time.Millisecond),
		mkRow(total, at(21, 0), 90, 400*time.Millisecond),
		mkRow(Key{Scope: ScopePanel}, at(21, 0), 5, time.Millisecond),
	)
	code, body := r.get("/api/requests/metrics?window=1h")
	if code != 200 {
		t.Fatalf("code = %d, %v", code, body)
	}
	sum := body["summary"].(map[string]any)
	if num(sum["requests"]) != 100 || num(sum["c2xx"]) != 100 {
		t.Fatalf("summary = %v", sum)
	}
	// 3 observations: 10ms, 10ms, 400ms -> p50 ~10ms, p99 ~400ms (within a bucket).
	if p50 := num(sum["p50Ms"]); p50 < 9 || p50 > 11 {
		t.Errorf("p50 = %v", sum["p50Ms"])
	}
	if p99 := num(sum["p99Ms"]); p99 < 360 || p99 > 440 {
		t.Errorf("p99 = %v", sum["p99Ms"])
	}
	buckets := body["buckets"].([]any)
	if len(buckets) != 2 || num(buckets[0].(map[string]any)["at"]) != float64(at(20, 0).UnixMilli()) {
		t.Fatalf("buckets = %v", buckets)
	}
	if body["partial"] != true || num(body["retentionDays"]) != 1 {
		t.Errorf("partial/retention = %v / %v", body["partial"], body["retentionDays"])
	}
	if _, ok := body["hosts"]; ok {
		t.Error("host-wide response must not carry hosts")
	}

	_, panel := r.get("/api/requests/panel/metrics?window=1h")
	if num(panel["summary"].(map[string]any)["requests"]) != 5 {
		t.Fatalf("panel summary = %v", panel["summary"])
	}
}

func TestEmptyBucketHasNullPercentiles(t *testing.T) {
	r := newHTTPRig(t)
	r.write(mkRow(total, at(20, 0), 0))
	_, body := r.get("/api/requests/metrics?window=1h")
	b := body["buckets"].([]any)[0].(map[string]any)
	if b["p50Ms"] != nil || b["p90Ms"] != nil || b["p99Ms"] != nil {
		t.Fatalf("bucket = %v", b)
	}
	if body["summary"].(map[string]any)["p99Ms"] != nil {
		t.Fatalf("summary = %v", body["summary"])
	}
}

func TestHourlyWindowReadsHourlyRows(t *testing.T) {
	r := newHTTPRig(t)
	if code, _ := r.put("/api/requests/settings", `{"retentionDays":7}`); code != 200 {
		t.Fatal(code)
	}
	r.now = t0.Add(48 * time.Hour)
	// Minute rows exist too; the 7d window must ignore them.
	r.write(mkRow(total, r.now.Add(-10*time.Minute), 1))
	hour := r.now.Add(-3 * time.Hour).Truncate(time.Hour)
	tx, _ := r.db.Begin()
	if err := execRow(context.Background(), tx, Hour.table(), mkRow(total, hour, 600, 20*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	code, body := r.get("/api/requests/metrics?window=7d")
	if code != 200 {
		t.Fatalf("code = %d %v", code, body)
	}
	buckets := body["buckets"].([]any)
	if len(buckets) != 1 || num(buckets[0].(map[string]any)["c2xx"]) != 600 {
		t.Fatalf("buckets = %v", buckets)
	}
}

func TestWindowBeyondRetention(t *testing.T) {
	r := newHTTPRig(t)
	r.put("/api/requests/settings", `{"retentionDays":7}`)
	for _, w := range []string{"30d", "90d", "2h", "", "7D"} {
		if code, body := r.get("/api/requests/metrics?window=" + w); code != 400 {
			t.Errorf("window %q = %d %v, want 400", w, code, body)
		}
	}
	if code, _ := r.get("/api/requests/metrics?window=7d"); code != 200 {
		t.Errorf("7d with retention 7 = %d", code)
	}
	if code, _ := r.get("/api/requests/metrics?window=24h"); code != 200 {
		t.Errorf("24h = %d", code)
	}
}

const composePath = "/api/projects/1/environments/1/composes/1/requests/metrics"

func TestComposeHosts(t *testing.T) {
	r := newHTTPRig(t)
	r.seedCompose(1, 1, "A.test", "A.test", "b.test")
	a := Key{Scope: ScopeHost, ComposeID: 1, Host: "a.test"}
	b := Key{Scope: ScopeHost, ComposeID: 1, Host: "b.test"}
	gone := Key{Scope: ScopeHost, ComposeID: 1, Host: "deleted.test"}
	r.write(mkRow(a, at(20, 0), 4, time.Millisecond), mkRow(b, at(20, 0), 6, time.Millisecond), mkRow(gone, at(20, 0), 100, time.Millisecond))

	code, body := r.get(composePath + "?window=1h")
	if code != 200 {
		t.Fatalf("code = %d %v", code, body)
	}
	hosts := body["hosts"].([]any)
	if len(hosts) != 2 || hosts[0] != "a.test" || hosts[1] != "b.test" {
		t.Fatalf("hosts = %v", hosts)
	}
	if got := num(body["summary"].(map[string]any)["requests"]); got != 10 {
		t.Errorf("all hosts = %v, want 10 (current hosts only)", got)
	}
	buckets := body["buckets"].([]any)
	if len(buckets) != 1 || num(buckets[0].(map[string]any)["c2xx"]) != 10 {
		t.Errorf("merged buckets = %v", buckets)
	}
	_, one := r.get(composePath + "?window=1h&host=A.TEST")
	if got := num(one["summary"].(map[string]any)["requests"]); got != 4 {
		t.Errorf("host a = %v, want 4", got)
	}
	if code, _ := r.get(composePath + "?window=1h&host=c.test"); code != 400 {
		t.Errorf("unknown host = %d, want 400", code)
	}
	if code, _ := r.get(composePath + "?window=1h&host=deleted.test"); code != 400 {
		t.Errorf("deleted host = %d, want 400", code)
	}
}

func TestComposeWithoutDomains(t *testing.T) {
	r := newHTTPRig(t)
	r.seedCompose(1, 1)
	code, body := r.get(composePath + "?window=1h")
	if hosts, ok := body["hosts"].([]any); code != 200 || !ok || len(hosts) != 0 {
		t.Fatalf("code = %d body = %v, want hosts: []", code, body)
	}
	if num(body["summary"].(map[string]any)["requests"]) != 0 {
		t.Fatal("no domains, no requests")
	}
}

func TestComposeRouteOwnership(t *testing.T) {
	r := newHTTPRig(t)
	r.seedCompose(1, 1, "a.test")
	r.seedCompose(2, 2, "z.test")
	// Compose 2 belongs to project 2; under project 1's path it does not exist.
	if code, _ := r.get("/api/projects/1/environments/1/composes/2/requests/metrics?window=1h"); code != 404 {
		t.Errorf("foreign compose = %d, want 404", code)
	}
	if code, _ := r.get("/api/projects/1/environments/2/composes/1/requests/metrics?window=1h"); code != 404 {
		t.Errorf("foreign environment = %d, want 404", code)
	}
	if code, _ := r.get("/api/projects/1/environments/1/composes/9/requests/metrics?window=1h"); code != 404 {
		t.Errorf("missing compose = %d, want 404", code)
	}
	if code, _ := r.get("/api/projects/x/environments/1/composes/1/requests/metrics?window=1h"); code != 400 {
		t.Errorf("bad id = %d, want 400", code)
	}
}

func TestAccessLogFlags(t *testing.T) {
	r := newHTTPRig(t)
	_, body := r.get("/api/requests/metrics?window=1h")
	if body["accessLogEnabled"] != false || body["accessLogFiltered"] != false {
		t.Fatalf("no effective file: %v", body)
	}
	r.writeEffective("accessLog: {}\n")
	_, body = r.get("/api/requests/metrics?window=1h")
	if body["accessLogEnabled"] != true || body["accessLogFiltered"] != false {
		t.Fatalf("on: %v", body)
	}
	r.writeEffective("accessLog: {filters: {statusCodes: ['500']}}\n")
	_, body = r.get("/api/requests/metrics?window=1h")
	if body["accessLogEnabled"] != true || body["accessLogFiltered"] != true {
		t.Fatalf("filtered: %v", body)
	}
}

func TestSettingsPut(t *testing.T) {
	r := newHTTPRig(t)
	if code, body := r.put("/api/requests/settings", `{"retentionDays":30}`); code != 200 || num(body["retentionDays"]) != 30 {
		t.Fatalf("put 30 = %d %v", code, body)
	}
	if code, body := r.get("/api/requests/settings"); code != 200 || num(body["retentionDays"]) != 30 {
		t.Fatalf("get = %d %v", code, body)
	}
	for _, bad := range []string{`{"retentionDays":0}`, `{"retentionDays":91}`, `{}`, `not json`} {
		if code, _ := r.put("/api/requests/settings", bad); code != 400 {
			t.Errorf("put %s = %d, want 400", bad, code)
		}
	}
	if code, body := r.get("/api/requests/settings"); num(body["retentionDays"]) != 30 {
		t.Errorf("a refused put changed the setting: %d %v", code, body)
	}
}

// A minute the panel collected (it has a total row) but in which the series
// had no request is a zero bucket, not a missing one; a minute without a
// total row (the panel was down) stays absent.
func TestQuietBucketsAreZero(t *testing.T) {
	r := newHTTPRig(t)
	r.seedCompose(1, 1, "a.test")
	host := Key{Scope: ScopeHost, ComposeID: 1, Host: "a.test"}
	r.write(
		mkRow(total, at(20, 0), 5, time.Millisecond),
		mkRow(host, at(20, 0), 5, time.Millisecond),
		mkRow(total, at(21, 0), 0),
		mkRow(total, at(22, 0), 3, time.Millisecond),
		// 10:23 not collected: no total row.
		mkRow(total, at(24, 0), 0),
	)
	for _, path := range []string{
		"/api/requests/panel/metrics?window=1h",
		"/api/projects/1/environments/1/composes/1/requests/metrics?window=1h",
	} {
		_, body := r.get(path)
		buckets := body["buckets"].([]any)
		var ats []float64
		for _, b := range buckets {
			ats = append(ats, num(b.(map[string]any)["at"]))
		}
		want := []float64{float64(at(20, 0).UnixMilli()), float64(at(21, 0).UnixMilli()), float64(at(22, 0).UnixMilli()), float64(at(24, 0).UnixMilli())}
		if len(ats) != len(want) {
			t.Fatalf("%s: bucket times = %v, want %v", path, ats, want)
		}
		for i := range want {
			if ats[i] != want[i] {
				t.Fatalf("%s: bucket times = %v, want %v", path, ats, want)
			}
		}
		quiet := buckets[1].(map[string]any)
		if num(quiet["c2xx"]) != 0 || quiet["p50Ms"] != nil {
			t.Errorf("%s: quiet bucket = %v", path, quiet)
		}
	}
}
