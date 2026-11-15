package databases

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/audit"
	"relist-panel/internal/auth"
	"relist-panel/internal/authz"
	"relist-panel/internal/dbquery"
)

// fakeQuerier answers by the text of the SQL; "block" waits for the
// request's context like a long query does.
type fakeQuerier struct {
	mu      sync.Mutex
	runs    []dbquery.Request
	targets []dbquery.Target
	resp    map[string]dbquery.QueryResponse
	schema  []dbquery.SchemaNode
}

func (f *fakeQuerier) Run(ctx context.Context, t dbquery.Target, r dbquery.Request) dbquery.QueryResponse {
	f.mu.Lock()
	f.runs = append(f.runs, r)
	f.targets = append(f.targets, t)
	resp, ok := f.resp[r.SQL]
	f.mu.Unlock()
	if r.SQL == "block" {
		<-ctx.Done()
		return dbquery.QueryResponse{Results: []dbquery.QueryResult{}, Cancelled: true, Error: &dbquery.QueryError{Message: "Cancelled."}}
	}
	if !ok {
		return dbquery.QueryResponse{Results: []dbquery.QueryResult{{Rows: [][]dbquery.Cell{{"1"}}}}}
	}
	return resp
}

func (f *fakeQuerier) Schema(_ context.Context, t dbquery.Target, engine, database string) ([]dbquery.SchemaNode, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.targets = append(f.targets, t)
	return f.schema, nil
}

func (f *fakeQuerier) last() dbquery.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.runs[len(f.runs)-1]
}

// queryRig serves the Database routes through the real authz middleware, so
// the audit rows are the ones production writes.
type queryRig struct {
	*rig
	q      *fakeQuerier
	h      http.Handler
	mu     sync.Mutex
	events []audit.Event
}

func newQueryRig(t *testing.T) *queryRig {
	t.Helper()
	r := newRig(t)
	q := &fakeQuerier{resp: map[string]dbquery.QueryResponse{}}
	r.svc = New(Deps{Store: r.store, Host: r.host, DataDir: r.dataDir, Querier: q,
		Locked:       func(context.Context) bool { return r.locked },
		UpdateLocked: func() bool { return r.updateLocked }})
	qr := &queryRig{rig: r, q: q}
	mux := http.NewServeMux()
	NewAPI(r.svc).RegisterRoutes(mux)
	lookup := func(*http.Request) (auth.User, bool, error) { return auth.User{}, false, nil }
	mw := authz.New(r.store.db, lookup).WithRecorder(func(_ context.Context, e audit.Event) error {
		qr.mu.Lock()
		defer qr.mu.Unlock()
		qr.events = append(qr.events, e)
		return nil
	}).Middleware(mux, mux)
	qr.h = http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mw.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), auth.User{ID: 1, Email: "owner@example.com", IsAdmin: true})))
	})
	return qr
}

func (qr *queryRig) do(ctx context.Context, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	w := httptest.NewRecorder()
	qr.h.ServeHTTP(w, req)
	return w
}

func (qr *queryRig) take() []audit.Event {
	qr.mu.Lock()
	defer qr.mu.Unlock()
	out := qr.events
	qr.events = nil
	return out
}

func TestQueryRoute(t *testing.T) {
	qr := newQueryRig(t)
	qr.create(t, "Shop", "postgres", "18")
	qr.q.resp["SELEC 1"] = dbquery.QueryResponse{Results: []dbquery.QueryResult{}, Error: &dbquery.QueryError{Message: "syntax error"}}

	w := qr.do(context.Background(), "POST", "/api/databases/1/query", `{"database":"shop","sql":"SELECT 1"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"rows":[["1"]]`) {
		t.Fatalf("run = %d %s", w.Code, w.Body)
	}
	got := qr.q.last()
	if got.Writes || got.Database != "shop" || got.SQL != "SELECT 1" || got.Engine != "postgres" {
		t.Fatalf("request = %+v", got)
	}
	if tg := qr.q.targets[0]; tg.Host != "relist-db-1" || tg.Port != 5432 || tg.User != "postgres" || tg.Password == "" {
		t.Fatalf("target = %+v", tg)
	}
	if w := qr.do(context.Background(), "POST", "/api/databases/1/query", `{"database":"shop","sql":"SELECT 2","writes":true}`); w.Code != 200 || !qr.q.last().Writes {
		t.Fatalf("writes = %d %+v", w.Code, qr.q.last())
	}
	w = qr.do(context.Background(), "POST", "/api/databases/1/query", `{"database":"shop","sql":"SELEC 1"}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"message":"syntax error"`) {
		t.Fatalf("a server error must still answer 200: %d %s", w.Code, w.Body)
	}
}

func TestQueryAuditsEveryOutcome(t *testing.T) {
	qr := newQueryRig(t)
	qr.create(t, "Shop", "postgres", "18")
	qr.q.resp["err"] = dbquery.QueryResponse{Results: []dbquery.QueryResult{}, Error: &dbquery.QueryError{Message: "boom"}}
	qr.q.resp["stop"] = dbquery.QueryResponse{Stopped: "Stopped after 1,000 rows.", Results: []dbquery.QueryResult{{Rows: make([][]dbquery.Cell, 1000), Truncated: true}}}
	qr.q.resp["ok2"] = dbquery.QueryResponse{Results: []dbquery.QueryResult{{Rows: make([][]dbquery.Cell, 2)}, {Rows: make([][]dbquery.Cell, 3)}}}

	cases := []struct {
		sql, outcome, errText string
		rows                  int
	}{
		{"ok2", "ok", "", 5},
		{"err", "error", "boom", 0},
		{"stop", "stopped", "", 1000},
	}
	for _, c := range cases {
		w := qr.do(context.Background(), "POST", "/api/databases/1/query", `{"database":"shop","sql":"`+c.sql+`","writes":true}`)
		ev := qr.take()
		if w.Code != 200 || len(ev) != 1 {
			t.Fatalf("%s: status %d, %d audit rows", c.sql, w.Code, len(ev))
		}
		d := ev[0].Detail
		if ev[0].Action != "database.query" || ev[0].Target != "Shop" || d["outcome"] != c.outcome || d["statement"] != c.sql || d["writes"] != true ||
			d["database"] != "shop" || d["rows"] != c.rows || d["error"] != c.errText {
			t.Errorf("%s: event %+v", c.sql, ev[0])
		}
		if _, ok := d["durationMs"]; !ok {
			t.Errorf("%s: no durationMs in %v", c.sql, d)
		}
	}

	// The browser aborts: the request's context ends while the run is
	// blocked. The row is still written, as cancelled.
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	w := qr.do(ctx, "POST", "/api/databases/1/query", `{"database":"shop","sql":"block"}`)
	ev := qr.take()
	if w.Code != 200 || len(ev) != 1 || ev[0].Detail["outcome"] != "cancelled" || ev[0].Action != "database.query" {
		t.Fatalf("cancelled: status %d, events %+v", w.Code, ev)
	}
}

func TestQueryRedactsStatement(t *testing.T) {
	qr := newQueryRig(t)
	qr.create(t, "Shop", "mysql", "8.4")
	qr.do(context.Background(), "POST", "/api/databases/1/query", `{"sql":"ALTER USER u IDENTIFIED BY 'hunter2'"}`)
	ev := qr.take()
	if len(ev) != 1 || ev[0].Detail["statement"] != "ALTER USER u IDENTIFIED BY '***'" {
		t.Fatalf("events = %+v", ev)
	}
	// What the browser sent to the engine is not redacted; only the record is.
	if !strings.Contains(qr.q.last().SQL, "hunter2") {
		t.Fatal("the run must get the real text")
	}
	long := strings.Repeat("é", 3000)
	qr.do(context.Background(), "POST", "/api/databases/1/query", `{"sql":"SELECT '`+long+`'"}`)
	st := qr.take()[0].Detail["statement"].(string)
	if len(st) > 4096 || !strings.HasPrefix(st, "SELECT 'é") {
		t.Fatalf("statement is %d bytes", len(st))
	}
}

func TestQueryHidesThePasswordInErrors(t *testing.T) {
	qr := newQueryRig(t)
	d := qr.create(t, "Shop", "postgres", "18")
	got, _ := qr.store.Get(context.Background(), d.ID)
	qr.q.resp["x"] = dbquery.QueryResponse{Results: []dbquery.QueryResult{}, Error: &dbquery.QueryError{Message: "failed for " + got.Password}}
	w := qr.do(context.Background(), "POST", "/api/databases/1/query", `{"sql":"x"}`)
	ev := qr.take()
	if strings.Contains(w.Body.String(), got.Password) || strings.Contains(ev[0].Detail["error"].(string), got.Password) {
		t.Fatalf("password leaked: %s / %v", w.Body, ev[0].Detail)
	}
}

func TestQueryGates(t *testing.T) {
	qr := newQueryRig(t)
	qr.create(t, "Shop", "postgres", "18")
	qr.create(t, "Cache", "redis", "8")
	run := func(id, body string) int {
		return qr.do(context.Background(), "POST", "/api/databases/"+id+"/query", body).Code
	}
	if c := run("2", `{"sql":"GET k"}`); c != 400 {
		t.Errorf("redis = %d", c)
	}
	if c := run("1", `{"sql":"  "}`); c != 400 {
		t.Errorf("empty sql = %d", c)
	}
	if c := run("1", `{"sql":`); c != 400 {
		t.Errorf("bad json = %d", c)
	}
	if c := run("9", `{"sql":"SELECT 1"}`); c != 404 {
		t.Errorf("unknown = %d", c)
	}
	big := `{"sql":"SELECT 1","pad":"` + strings.Repeat("a", 1<<20) + `"}`
	if c := run("1", big); c != 413 {
		t.Errorf("1 MiB body = %d", c)
	}
	qr.locked = true
	if c := run("1", `{"sql":"SELECT 1"}`); c != 409 {
		t.Errorf("locked = %d", c)
	}
	qr.locked = false
	qr.host.containers["relist-db-1"].Running = false
	if c := run("1", `{"sql":"SELECT 1"}`); c != 409 {
		t.Errorf("stopped = %d", c)
	}
	if len(qr.take()) != 0 {
		t.Error("a refused request is not a run: no audit row")
	}
	if n := len(qr.q.runs); n != 0 {
		t.Errorf("%d runs reached the Querier", n)
	}
}

func TestQueryTombstoneIs404(t *testing.T) {
	qr := newQueryRig(t)
	d := qr.create(t, "Shop", "postgres", "18")
	now := time.Now()
	if _, err := qr.store.db.Exec("UPDATE databases SET deleted_at = ? WHERE id = ?", now.UnixMilli(), d.ID); err != nil {
		t.Fatal(err)
	}
	if c := qr.do(context.Background(), "POST", "/api/databases/1/query", `{"sql":"SELECT 1"}`).Code; c != 404 {
		t.Errorf("tombstone = %d", c)
	}
}

func TestQuerySchemaRoute(t *testing.T) {
	qr := newQueryRig(t)
	qr.create(t, "Shop", "postgres", "18")
	qr.q.schema = []dbquery.SchemaNode{{Name: "public", Tables: []string{"items"}}}
	w := qr.do(context.Background(), "GET", "/api/databases/1/query/schema?database=shop", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"tables":["items"]`) {
		t.Fatalf("schema = %d %s", w.Code, w.Body)
	}
	if len(qr.take()) != 0 {
		t.Error("the schema read is not audited")
	}
}
