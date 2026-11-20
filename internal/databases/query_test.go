package databases

import (
	"context"
	"encoding/json"
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

	// Redis: calls records every console run; sent only those the contract
	// lets reach the server (a dangerous command waits for its confirmation).
	calls []redisCall
	sent  []string
	redis func(line string) dbquery.RedisResponse
}

type redisCall struct {
	db                int
	line              string
	writes, confirmed bool
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
	two := int64(2)
	qr.q.resp["UPDATE t SET a = 1; SELECT 1; SELEC 2"] = dbquery.QueryResponse{
		Results: []dbquery.QueryResult{{Affected: &two}, {Rows: make([][]dbquery.Cell, 1)}},
		Error:   &dbquery.QueryError{Message: "syntax error at or near \"SELEC\"", Code: "42601", StatementIndex: 2},
	}
	qr.q.resp["SELECT * FROM t"] = dbquery.QueryResponse{Stopped: "Stopped after 1,000 rows.", Results: []dbquery.QueryResult{{Rows: make([][]dbquery.Cell, 1000), Truncated: true}}}
	qr.q.resp["SELECT 1; SELECT 2"] = dbquery.QueryResponse{Results: []dbquery.QueryResult{{Rows: make([][]dbquery.Cell, 2)}, {Rows: make([][]dbquery.Cell, 3)}}}

	cases := []struct {
		sql, outcome, kinds, code string
		rows, affected, failed    int
	}{
		{"SELECT 1; SELECT 2", "ok", "SELECT ×2", "", 5, 0, 0},
		{"UPDATE t SET a = 1; SELECT 1; SELEC 2", "error", "UPDATE, SELECT, OTHER", "42601", 1, 2, 3},
		{"SELECT * FROM t", "stopped", "SELECT", "", 1000, 0, 0},
	}
	for _, c := range cases {
		w := qr.do(context.Background(), "POST", "/api/databases/1/query", `{"database":"shop","sql":"`+c.sql+`","writes":true}`)
		ev := qr.take()
		if w.Code != 200 || len(ev) != 1 {
			t.Fatalf("%s: status %d, %d audit rows", c.sql, w.Code, len(ev))
		}
		d := ev[0].Detail
		if ev[0].Action != "database.query" || ev[0].Target != "Shop" || d["outcome"] != c.outcome || d["statements"] != c.kinds || d["writes"] != true ||
			d["database"] != "shop" || d["rows"] != c.rows || d["affected"] != int64(c.affected) || d["errorCode"] != c.code || d["errorStatement"] != c.failed {
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

// A Preview is a preview whatever came of it: a run that failed before the
// engine could mark the response still answers and audits as one.
func TestFailedPreviewIsStillAPreview(t *testing.T) {
	qr := newQueryRig(t)
	qr.create(t, "Shop", "postgres", "18")
	qr.q.resp["boom"] = dbquery.QueryResponse{Results: []dbquery.QueryResult{}, Error: &dbquery.QueryError{Message: "connection refused"}}
	w := qr.do(context.Background(), "POST", "/api/databases/1/query", `{"database":"shop","sql":"boom","preview":true}`)
	ev := qr.take()
	if w.Code != 200 || len(ev) != 1 || ev[0].Detail["outcome"] != "preview" {
		t.Fatalf("failed preview: status %d, events %+v", w.Code, ev)
	}
	if !strings.Contains(w.Body.String(), `"preview":true`) {
		t.Fatalf("response lost the preview flag: %s", w.Body)
	}
}

// The audit log keeps the kinds of statement and the error's code, never
// the text or the server's message: a regex cannot find every way to write
// a password literal, and an error can quote the statement.
func TestQueryAuditKeepsNoText(t *testing.T) {
	qr := newQueryRig(t)
	qr.create(t, "Shop", "mysql", "8.4")
	for _, sql := range []string{
		"ALTER USER u IDENTIFIED BY 'hunter2'",
		"SET PASSWORD = _utf8mb4'hunter2'",
		"ALTER USER u IDENTIFIED BY X'68756e74657232'",
		"/* hunter2 */ SELECT 1",
	} {
		qr.q.resp[sql] = dbquery.QueryResponse{Results: []dbquery.QueryResult{}, Error: &dbquery.QueryError{Message: "You have an error in your SQL syntax near 'hunter2'", Code: "1064"}}
		body, _ := json.Marshal(map[string]any{"sql": sql})
		qr.do(context.Background(), "POST", "/api/databases/1/query", string(body))
		ev := qr.take()
		raw, _ := json.Marshal(ev)
		if len(ev) != 1 || strings.Contains(string(raw), "hunter2") || strings.Contains(string(raw), "68756e") {
			t.Fatalf("%q: the audit row keeps the text: %s", sql, raw)
		}
		if ev[0].Detail["errorCode"] != "1064" {
			t.Fatalf("%q: errorCode = %v", sql, ev[0].Detail["errorCode"])
		}
	}
	// What the browser sent reaches the engine whole; only the record is cut.
	if !strings.Contains(qr.q.last().SQL, "hunter2") {
		t.Fatal("the run must get the real text")
	}
}

func TestQueryHidesThePasswordInErrors(t *testing.T) {
	qr := newQueryRig(t)
	d := qr.create(t, "Shop", "postgres", "18")
	got, _ := qr.store.Get(context.Background(), d.ID)
	qr.q.resp["x"] = dbquery.QueryResponse{Results: []dbquery.QueryResult{}, Error: &dbquery.QueryError{Message: "failed for " + got.Password}}
	w := qr.do(context.Background(), "POST", "/api/databases/1/query", `{"sql":"x"}`)
	ev := qr.take()
	raw, _ := json.Marshal(ev)
	if strings.Contains(w.Body.String(), got.Password) || strings.Contains(string(raw), got.Password) {
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

func (f *fakeQuerier) RunRedis(_ context.Context, _ dbquery.Target, db int, line string, writes, confirm bool) dbquery.RedisResponse {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, redisCall{db, line, writes, confirm})
	// Like RunRedis, name the command a real server would know.
	name := strings.ToUpper(strings.Fields(line + " ")[0])
	if strings.HasPrefix(line, "FLUSHALL") && !confirm {
		return dbquery.RedisResponse{Command: name, NeedsConfirm: true}
	}
	if strings.HasPrefix(line, "SET ") && !writes {
		return dbquery.RedisResponse{Command: name, Refused: "writes are off"}
	}
	f.sent = append(f.sent, line)
	if f.redis != nil {
		return f.redis(line)
	}
	return dbquery.RedisResponse{Command: name, Reply: &dbquery.Reply{Type: "string", Value: "OK"}}
}

func (f *fakeQuerier) ScanKeys(_ context.Context, _ dbquery.Target, db int, pattern, cursor string) ([]dbquery.KeyInfo, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, redisCall{db: db, line: "SCAN " + cursor + " " + pattern})
	return []dbquery.KeyInfo{{Key: "a", Type: "string", TTLms: -1}}, "7", nil
}

func (f *fakeQuerier) ReadKey(_ context.Context, _ dbquery.Target, db int, key string) (dbquery.KeyValue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, redisCall{db: db, line: "KEY " + key})
	return dbquery.KeyValue{Key: key, Type: "string", Items: []dbquery.KeyItem{{Value: "v"}}}, nil
}

func (qr *queryRig) redisCmd(body string) *httptest.ResponseRecorder {
	return qr.do(context.Background(), "POST", "/api/databases/2/redis/command", body)
}

func newRedisRig(t *testing.T) *queryRig {
	t.Helper()
	qr := newQueryRig(t)
	qr.create(t, "Shop", "postgres", "18")
	qr.create(t, "Cache", "redis", "8")
	return qr
}

func TestRedisRouteNeedsConfirm(t *testing.T) {
	qr := newRedisRig(t)
	w := qr.redisCmd(`{"db":0,"command":"FLUSHALL","writes":true}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"needsConfirm":true`) {
		t.Fatalf("FLUSHALL = %d %s", w.Code, w.Body)
	}
	if len(qr.q.sent) != 0 || qr.q.calls[0].confirmed {
		t.Fatalf("sent %v calls %+v: nothing may be sent before the confirmation", qr.q.sent, qr.q.calls)
	}
	w = qr.redisCmd(`{"db":3,"command":"FLUSHALL","writes":true,"confirmDangerous":true}`)
	if w.Code != 200 || strings.Contains(w.Body.String(), "needsConfirm") || len(qr.q.sent) != 1 {
		t.Fatalf("confirmed = %d %s sent %v", w.Code, w.Body, qr.q.sent)
	}
	if c := qr.q.calls[1]; c.db != 3 || !c.writes || !c.confirmed {
		t.Fatalf("call = %+v", c)
	}
}

func TestRedisAuditsRefused(t *testing.T) {
	qr := newRedisRig(t)
	w := qr.redisCmd(`{"db":1,"command":"SET k v"}`)
	ev := qr.take()
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"refused":"writes are off"`) || len(ev) != 1 {
		t.Fatalf("SET = %d %s, %d audit rows", w.Code, w.Body, len(ev))
	}
	d := ev[0].Detail
	if ev[0].Action != "database.redis_command" || ev[0].Target != "Cache" || d["outcome"] != "refused" || d["command"] != "SET" ||
		d["writes"] != false || d["database"] != 1 {
		t.Fatalf("event %+v", ev[0])
	}
	qr.redisCmd(`{"db":0,"command":"GET k"}`)
	if ev := qr.take(); len(ev) != 1 || ev[0].Detail["outcome"] != "ok" || ev[0].Detail["command"] != "GET" || ev[0].Detail["errorCode"] != "" {
		t.Fatalf("GET event %+v", ev)
	}
	qr.q.redis = func(string) dbquery.RedisResponse {
		return dbquery.RedisResponse{Command: "LPUSH", Reply: &dbquery.Reply{Type: "error", Value: "WRONGTYPE nope"}}
	}
	qr.redisCmd(`{"db":0,"command":"LPUSH k x","writes":true}`)
	if ev := qr.take(); len(ev) != 1 || ev[0].Detail["outcome"] != "error" || ev[0].Detail["errorCode"] != "WRONGTYPE" {
		t.Fatalf("error event %+v", ev)
	}
}

func TestRedisAuditsACancelledRun(t *testing.T) {
	qr := newRedisRig(t)
	qr.q.redis = func(string) dbquery.RedisResponse {
		return dbquery.RedisResponse{Cancelled: true, Error: "Cancelled."}
	}
	w := qr.redisCmd(`{"db":0,"command":"BLPOP q 0","writes":true}`)
	ev := qr.take()
	if w.Code != 200 || len(ev) != 1 || ev[0].Detail["outcome"] != "cancelled" {
		t.Fatalf("status %d events %+v", w.Code, ev)
	}
}

// A console line reaches the audit log as its command's name only: no
// argument, and of an error reply only its leading code (Redis quotes the
// arguments of an unknown command in the message).
func TestRedisAuditKeepsNoArguments(t *testing.T) {
	qr := newRedisRig(t)
	qr.q.redis = func(line string) dbquery.RedisResponse {
		if strings.HasPrefix(line, "CONFIG") {
			return dbquery.RedisResponse{Command: "CONFIG SET", Reply: &dbquery.Reply{Type: "string", Value: "OK"}}
		}
		return dbquery.RedisResponse{Reply: &dbquery.Reply{Type: "error", Value: "ERR unknown command 'hunter2', with args beginning with: 'x'"}}
	}
	qr.redisCmd(`{"db":0,"command":"CONFIG SET requirepass hunter2","writes":true,"confirmDangerous":true}`)
	qr.redisCmd(`{"db":0,"command":"hunter2 x","writes":true}`)
	qr.redisCmd(`{"db":0,"command":"AUTH \"un closed hunter2","writes":true}`)
	ev := qr.take()
	raw, _ := json.Marshal(ev)
	if len(ev) != 3 || strings.Contains(string(raw), "hunter2") {
		t.Fatalf("the audit rows keep an argument: %s", raw)
	}
	if ev[0].Detail["command"] != "CONFIG SET" || ev[1].Detail["command"] != "OTHER" || ev[1].Detail["errorCode"] != "ERR" {
		t.Fatalf("events %s", raw)
	}
}

func TestRedisGates(t *testing.T) {
	qr := newRedisRig(t)
	post := func(id, body string) int {
		return qr.do(context.Background(), "POST", "/api/databases/"+id+"/redis/command", body).Code
	}
	if c := post("2", `{"db":16,"command":"GET k"}`); c != 400 {
		t.Errorf("db 16 = %d", c)
	}
	if c := post("2", `{"db":-1,"command":"GET k"}`); c != 400 {
		t.Errorf("db -1 = %d", c)
	}
	if c := post("2", `{"db":0,"command":" "}`); c != 400 {
		t.Errorf("empty = %d", c)
	}
	if c := post("1", `{"db":0,"command":"GET k"}`); c != 400 {
		t.Errorf("a SQL engine = %d", c)
	}
	if c := post("9", `{"db":0,"command":"GET k"}`); c != 404 {
		t.Errorf("unknown = %d", c)
	}
	if c := qr.do(context.Background(), "POST", "/api/databases/1/query", `{"sql":"SELECT 1"}`).Code; c != 200 {
		t.Errorf("the SQL route still serves SQL = %d", c)
	}
	qr.locked = true
	if c := post("2", `{"db":0,"command":"GET k"}`); c != 409 {
		t.Errorf("locked = %d", c)
	}
	qr.locked = false
	qr.host.containers["relist-db-2"].Running = false
	if c := post("2", `{"db":0,"command":"GET k"}`); c != 409 {
		t.Errorf("stopped = %d", c)
	}
	if len(qr.q.calls) != 0 {
		t.Errorf("refused requests reached the Querier: %+v", qr.q.calls)
	}
}

func TestRedisKeyRoutes(t *testing.T) {
	qr := newRedisRig(t)
	w := qr.do(context.Background(), "GET", "/api/databases/2/redis/keys?db=2&pattern=user:*&cursor=5", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"cursor":"7"`) || !strings.Contains(w.Body.String(), `"key":"a"`) {
		t.Fatalf("keys = %d %s", w.Code, w.Body)
	}
	if c := qr.q.calls[0]; c.db != 2 || c.line != "SCAN 5 user:*" {
		t.Fatalf("call %+v", c)
	}
	if w := qr.do(context.Background(), "GET", "/api/databases/2/redis/key?db=0&key=a", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"type":"string"`) {
		t.Fatalf("key = %d %s", w.Code, w.Body)
	}
	if w := qr.do(context.Background(), "GET", "/api/databases/2/redis/keys?db=99", ""); w.Code != 400 {
		t.Fatalf("db 99 = %d", w.Code)
	}
	if w := qr.do(context.Background(), "GET", "/api/databases/2/redis/key?db=0", ""); w.Code != 400 {
		t.Fatalf("no key = %d", w.Code)
	}
	if len(qr.take()) != 0 {
		t.Error("browsing keys is not a run")
	}
}
