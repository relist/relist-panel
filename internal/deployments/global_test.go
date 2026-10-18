package deployments

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"relist-panel/internal/composes"
)

type globalHTTPFixture struct {
	db                     *sql.DB
	mux                    *http.ServeMux
	p1, p2                 int64
	e1, e2, e3             int64
	c1, c2, c3, c4         int64
	d1, d2, d3, d4, d5, d6 int64
}

func newGlobalHTTPFixture(t *testing.T, seed bool) *globalHTTPFixture {
	t.Helper()
	db, a, b := newDB(t)
	f := &globalHTTPFixture{db: db, c1: a, c2: b}
	if err := db.QueryRow(`SELECT e.project_id, e.id FROM environments e JOIN composes c ON c.environment_id = e.id WHERE c.id = ?`, a).Scan(&f.p1, &f.e1); err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) int64 {
		t.Helper()
		res, err := db.Exec(query, args...)
		if err != nil {
			t.Fatal(err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	f.e2 = exec(`INSERT INTO environments (project_id, name, created_at) VALUES (?, 'staging', 1)`, f.p1)
	f.c3 = exec(`INSERT INTO composes (environment_id, name, yaml_content, created_at, updated_at) VALUES (?, 'gamma', '', 1, 1)`, f.e2)
	f.p2 = exec(`INSERT INTO projects (name, created_at, updated_at) VALUES ('other', 1, 1)`)
	f.e3 = exec(`INSERT INTO environments (project_id, name, created_at) VALUES (?, 'production', 1)`, f.p2)
	f.c4 = exec(`INSERT INTO composes (environment_id, name, yaml_content, created_at, updated_at) VALUES (?, 'delta', '', 1, 1)`, f.e3)
	if seed {
		insert := func(compose int64, number int, status string, second int) int64 {
			return exec(`INSERT INTO deployments (compose_id, number, mode, status, queued_at) VALUES (?, ?, 'deploy', ?, ?)`, compose, number, status, globalBase.Unix()+int64(second))
		}
		f.d1 = insert(f.c1, 1, "succeeded", 1)
		f.d2 = insert(f.c2, 1, "failed", 2)
		f.d3 = insert(f.c3, 1, "canceled", 3)
		f.d4 = insert(f.c4, 1, "queued", 4)
		f.d5 = insert(f.c1, 2, "running", 4)
		f.d6 = insert(f.c2, 2, "succeeded", 5)
		exec(`UPDATE deployments SET mode = 'build', no_cache = 1, fresh_volumes = 1, step = 'deploy', steps = '["fetch","build:web","deploy"]', failed_step = 'build:web', reason = 'an earlier error', source_ref = 'abc123', source_subject = 'subject', warnings = '["warning"]', started_at = ?, finished_at = ? WHERE id = ?`, globalBase.Unix()+100, globalBase.Unix()+101, f.d6)
	}
	q := NewQueue(Deps{Store: NewStore(db)})
	f.mux = http.NewServeMux()
	NewAPI(q, composes.New(db, nil, t.TempDir())).RegisterRoutes(f.mux)
	return f
}

var globalBase = time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)

type globalWireDeployment struct {
	deploymentJSON
	ProjectID       int64  `json:"projectId"`
	ProjectName     string `json:"projectName"`
	EnvironmentID   int64  `json:"environmentId"`
	EnvironmentName string `json:"environmentName"`
	ComposeName     string `json:"composeName"`
}

type globalWirePage struct {
	Items    []globalWireDeployment `json:"items"`
	Total    int                    `json:"total"`
	Page     int                    `json:"page"`
	PageSize int                    `json:"pageSize"`
}

func (f *globalHTTPFixture) request(path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func (f *globalHTTPFixture) history(t *testing.T, query string) globalWirePage {
	t.Helper()
	rec := f.request("/api/deployments" + query)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET history %s = %d %s", query, rec.Code, rec.Body)
	}
	if rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("history content type = %q", rec.Header().Get("Content-Type"))
	}
	var page globalWirePage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode history: %v", err)
	}
	if page.Items == nil {
		t.Fatal("items must be a JSON array")
	}
	return page
}

func assertGlobalIDs(t *testing.T, page globalWirePage, total int, want ...int64) {
	t.Helper()
	got := make([]int64, len(page.Items))
	for i, item := range page.Items {
		got[i] = item.ID
	}
	if len(want) == 0 {
		want = []int64{}
	}
	if !reflect.DeepEqual(got, want) || page.Total != total {
		t.Fatalf("history IDs = %v total=%d, want %v total=%d", got, page.Total, want, total)
	}
}

// Removing the joins or treating per-Compose number as a global ID must fail.
func TestGlobalHistoryIncludesContextAndExistingDeploymentFields(t *testing.T) {
	f := newGlobalHTTPFixture(t, true)
	page := f.history(t, "")
	assertGlobalIDs(t, page, 6, f.d6, f.d5, f.d4, f.d3, f.d2, f.d1)
	if page.Page != 1 || page.PageSize != 20 {
		t.Fatalf("defaults = page %d size %d", page.Page, page.PageSize)
	}
	first := page.Items[0]
	if first.ComposeID != f.c2 || first.Number != 2 || first.ProjectID != f.p1 || first.ProjectName != "p" || first.EnvironmentID != f.e1 || first.EnvironmentName != "e" || first.ComposeName != "beta" {
		t.Fatalf("first item context = %+v", first)
	}
	if first.Mode != ModeBuild || !first.NoCache || !first.FreshVolumes || first.Status != StatusSucceeded || first.Step != "deploy" || !reflect.DeepEqual(first.Steps, []string{"fetch", "build:web", "deploy"}) || first.FailedStep != "build:web" || first.Reason != "an earlier error" || first.SourceRef != "abc123" || first.SourceSubject != "subject" || !reflect.DeepEqual(first.Warnings, []string{"warning"}) || !first.QueuedAt.Equal(globalBase.Add(5*time.Second)) || first.StartedAt == nil || !first.StartedAt.Equal(globalBase.Add(100*time.Second)) || first.FinishedAt == nil || !first.FinishedAt.Equal(globalBase.Add(101*time.Second)) {
		t.Fatalf("existing Deployment fields changed: %+v", first)
	}
	other := page.Items[2]
	if other.Number != 1 || other.ComposeID != f.c4 || other.ProjectID != f.p2 || other.ProjectName != "other" || other.EnvironmentID != f.e3 || other.EnvironmentName != "production" || other.ComposeName != "delta" {
		t.Fatalf("other Project context = %+v", other)
	}
	if other.Steps == nil || other.Warnings == nil || other.Ahead == nil || *other.Ahead != 1 {
		t.Fatalf("queued Deployment fields = %+v", other)
	}
	if page.Items[3].Number != 1 || page.Items[4].Number != 1 || page.Items[5].Number != 1 {
		t.Fatal("Compose-local numbers were altered")
	}
	if _, err := f.db.Exec(`UPDATE projects SET name = 'renamed' WHERE id = ?`, f.p1); err != nil {
		t.Fatal(err)
	}
	if got := f.history(t, "").Items[0].ProjectName; got != "renamed" {
		t.Fatalf("context name = %q, want current database name", got)
	}
}

// Omitting ID tie ordering would move the tied records between pages.
func TestGlobalHistoryPaginationAndStableTimestampTies(t *testing.T) {
	f := newGlobalHTTPFixture(t, true)
	for _, test := range []struct {
		page int
		ids  []int64
	}{
		{1, []int64{f.d6, f.d5}},
		{2, []int64{f.d4, f.d3}},
		{3, []int64{f.d2, f.d1}},
		{4, []int64{}},
	} {
		t.Run(strconv.Itoa(test.page), func(t *testing.T) {
			page := f.history(t, fmt.Sprintf("?page=%d&pageSize=2", test.page))
			assertGlobalIDs(t, page, 6, test.ids...)
			if page.Page != test.page || page.PageSize != 2 {
				t.Fatalf("pagination metadata = %+v", page)
			}
		})
	}
	assertGlobalIDs(t, f.history(t, "?pageSize=100"), 6, f.d6, f.d5, f.d4, f.d3, f.d2, f.d1)
}

// A missing predicate in either COUNT or SELECT must change IDs or total.
func TestGlobalHistoryFiltersShareCountAndPagePredicates(t *testing.T) {
	f := newGlobalHTTPFixture(t, true)
	for _, test := range []struct {
		name, query string
		total       int
		ids         []int64
	}{
		{"Project", fmt.Sprintf("?projectId=%d", f.p1), 5, []int64{f.d6, f.d5, f.d3, f.d2, f.d1}},
		{"Environment", fmt.Sprintf("?environmentId=%d", f.e1), 4, []int64{f.d6, f.d5, f.d2, f.d1}},
		{"Compose", fmt.Sprintf("?composeId=%d", f.c1), 2, []int64{f.d5, f.d1}},
		{"status", "?status=succeeded", 2, []int64{f.d6, f.d1}},
		{"queued", "?status=queued", 1, []int64{f.d4}},
		{"running", "?status=running", 1, []int64{f.d5}},
		{"failed", "?status=failed", 1, []int64{f.d2}},
		{"canceled", "?status=canceled", 1, []int64{f.d3}},
		{"combined page", fmt.Sprintf("?projectId=%d&environmentId=%d&composeId=%d&status=succeeded&page=2&pageSize=1", f.p1, f.e1, f.c2), 1, []int64{}},
		{"contradictory context", fmt.Sprintf("?projectId=%d&environmentId=%d", f.p2, f.e1), 0, []int64{}},
		{"missing Project", "?projectId=999999", 0, []int64{}},
	} {
		t.Run(test.name, func(t *testing.T) { assertGlobalIDs(t, f.history(t, test.query), test.total, test.ids...) })
	}
}

// Comparing started_at, losing timezone offsets, or rounding fractional lower
// bounds down must include the wrong records at an inclusive queuedAt boundary.
func TestGlobalHistoryInclusiveQueuedAtDateRange(t *testing.T) {
	f := newGlobalHTTPFixture(t, true)
	for _, test := range []struct {
		name, from, to string
		ids            []int64
	}{
		{"inclusive", "2026-10-12T10:00:02Z", "2026-10-12T10:00:04Z", []int64{f.d5, f.d4, f.d3, f.d2}},
		{"offset", "2026-10-12T17:00:02+07:00", "2026-10-12T17:00:04+07:00", []int64{f.d5, f.d4, f.d3, f.d2}},
		{"fractional", "2026-10-12T10:00:02.001Z", "2026-10-12T10:00:04.999Z", []int64{f.d5, f.d4, f.d3}},
		{"one instant", "2026-10-12T10:00:04Z", "2026-10-12T10:00:04Z", []int64{f.d5, f.d4}},
		{"fractional empty", "2026-10-12T10:00:04.001Z", "2026-10-12T10:00:04.999Z", []int64{}},
		{"subnanosecond lower bound", "2026-10-12T10:00:04.0000000001Z", "2026-10-12T10:00:05Z", []int64{f.d6}},
		{"subnanosecond interval", "2026-10-12T10:00:04.0000000001Z", "2026-10-12T10:00:04.0000000002Z", []int64{}},
		{"from only", "2026-10-12T10:00:04Z", "", []int64{f.d6, f.d5, f.d4}},
		{"to only", "", "2026-10-12T10:00:02Z", []int64{f.d2, f.d1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := url.Values{}
			if test.from != "" {
				query.Set("from", test.from)
			}
			if test.to != "" {
				query.Set("to", test.to)
			}
			assertGlobalIDs(t, f.history(t, "?"+query.Encode()), len(test.ids), test.ids...)
		})
	}
}

// Silently accepting invalid scalars or overflow would return misleading pages.
func TestGlobalHistoryRejectsInvalidQuery(t *testing.T) {
	f := newGlobalHTTPFixture(t, false)
	queries := []string{
		"unknown=1", "pageSize=101", "page=9223372036854775807&pageSize=100", "page=999999999999999999999999999999999999",
		"status=finished", "status=QUEUED", "status=queued,failed", "status=queued&status=failed",
		"from=yesterday", "to=2026-10-12", "from=2026-10-12T10:00:05Z&to=2026-10-12T10:00:04Z",
		"from=2026-10-12T1:00:00Z", "to=2026-10-12T10:00:00,5Z", "from=2026-10-12T10:00:00%2B24:00", "to=2026-10-12T10:00:00%2B00:60",
		"from=2026-10-12T10:00:04.002Z&to=2026-10-12T10:00:04.001Z",
		"from=2026-10-12T10:00:04.0000000002Z&to=2026-10-12T10:00:04.0000000001Z",
		"from=2026-10-12T10:00:00Z&from=2026-10-12T10:00:01Z", "page=1;pageSize=2", "%zz=1",
	}
	for _, key := range []string{"page", "pageSize", "projectId", "environmentId", "composeId"} {
		for _, value := range []string{"", "0", "-1", "1.5", "abc", "%2B1", "%201", "9223372036854775808"} {
			queries = append(queries, key+"="+value)
		}
		queries = append(queries, key+"=1&"+key+"=2")
	}
	for _, key := range []string{"status", "from", "to"} {
		queries = append(queries, key+"=")
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			rec := f.request("/api/deployments?" + query)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("invalid query = %d %s, want 400", rec.Code, rec.Body)
			}
			var body map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] == "" {
				t.Fatalf("invalid query error body = %s (%v)", rec.Body, err)
			}
		})
	}
}

func TestGlobalHistoryEmptyItemsAndQueueAreArrays(t *testing.T) {
	f := newGlobalHTTPFixture(t, false)
	page := f.history(t, "")
	assertGlobalIDs(t, page, 0)
	if page.Page != 1 || page.PageSize != 20 {
		t.Fatalf("empty metadata = %+v", page)
	}
	queue := f.request("/api/deployments/queue")
	if queue.Code != http.StatusOK || strings.TrimSpace(queue.Body.String()) != "[]" {
		t.Fatalf("empty queue = %d %s, want []", queue.Code, queue.Body)
	}
}

// Adding history filters or sorting by Compose must not change host queue FIFO.
func TestGlobalQueueEnrichesContextAndKeepsRunningFirstFIFO(t *testing.T) {
	f := newGlobalHTTPFixture(t, false)
	for _, row := range []struct {
		compose int64
		status  string
		at      int64
	}{
		{f.c3, "queued", globalBase.Unix()},
		{f.c4, "running", globalBase.Unix() + 10},
		{f.c1, "queued", globalBase.Unix()},
		{f.c2, "succeeded", globalBase.Unix() - 10},
	} {
		if _, err := f.db.Exec(`INSERT INTO deployments (compose_id, number, mode, status, step, queued_at) VALUES (?, 1, 'build', ?, 'fetch', ?)`, row.compose, row.status, row.at); err != nil {
			t.Fatal(err)
		}
	}
	rec := f.request("/api/deployments/queue?projectId=999&status=succeeded")
	if rec.Code != http.StatusOK {
		t.Fatalf("queue = %d %s", rec.Code, rec.Body)
	}
	var items []struct {
		ComposeID       int64  `json:"composeId"`
		ComposeName     string `json:"composeName"`
		Number          int    `json:"number"`
		Status          Status `json:"status"`
		Step            string `json:"step"`
		ProjectID       int64  `json:"projectId"`
		ProjectName     string `json:"projectName"`
		EnvironmentID   int64  `json:"environmentId"`
		EnvironmentName string `json:"environmentName"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("queue length = %d, want 3", len(items))
	}
	for i, want := range []struct {
		compose, project, environment             int64
		composeName, projectName, environmentName string
		status                                    Status
	}{
		{f.c4, f.p2, f.e3, "delta", "other", "production", StatusRunning},
		{f.c3, f.p1, f.e2, "gamma", "p", "staging", StatusQueued},
		{f.c1, f.p1, f.e1, "alpha", "p", "e", StatusQueued},
	} {
		got := items[i]
		if got.ComposeID != want.compose || got.ProjectID != want.project || got.EnvironmentID != want.environment || got.ComposeName != want.composeName || got.ProjectName != want.projectName || got.EnvironmentName != want.environmentName || got.Status != want.status || got.Number != 1 || got.Step != "fetch" {
			t.Fatalf("queue item %d = %+v, want %+v with number 1 step fetch", i, got, want)
		}
	}
}
