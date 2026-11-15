package directaccess

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
)

type apiRig struct {
	*rig
	h      http.Handler
	mu     sync.Mutex
	events []audit.Event
}

func newAPIRig(t *testing.T) *apiRig {
	t.Helper()
	a := &apiRig{rig: newRig(t)}
	mux := http.NewServeMux()
	NewAPI(a.svc).RegisterRoutes(mux)
	a.h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx, _ := audit.Begin(r.Context(), audit.Event{}, nil, func(_ context.Context, e audit.Event) error {
			a.mu.Lock()
			defer a.mu.Unlock()
			a.events = append(a.events, e)
			return nil
		})
		ctx = auth.WithUser(ctx, auth.User{ID: 5, Email: "admin@example.com", IsAdmin: true})
		mux.ServeHTTP(w, r.WithContext(ctx))
	})
	return a
}

func (a *apiRig) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	a.h.ServeHTTP(w, req)
	return w
}

const openBody = `{"targetKind":"database","targetId":7,"channel":"host_port","allInterfaces":false,"hostPort":0,"minutes":60}`

func TestHTTPOpenListExtendClose(t *testing.T) {
	a := newAPIRig(t)
	w := a.do("POST", "/api/direct-accesses", openBody)
	if w.Code != http.StatusCreated {
		t.Fatalf("open = %d %s", w.Code, w.Body)
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	wantExp := a.now.Add(time.Hour).UTC().Format(time.RFC3339)
	if got["id"] != float64(1) || got["targetKind"] != "database" || got["targetId"] != float64(7) || got["targetPort"] != float64(5432) ||
		got["channel"] != "host_port" || got["bind"] != "127.0.0.1" || got["hostPort"] != float64(49153) ||
		got["expiresAt"] != wantExp || got["openedAt"] == nil {
		t.Fatalf("AccessJSON = %v", got)
	}
	if row, _ := a.store.Get(context.Background(), 1); row.OpenedBy != 5 {
		t.Fatalf("OpenedBy = %d, want the session user 5", row.OpenedBy)
	}

	w = a.do("GET", "/api/direct-accesses?targetKind=database&targetId=7", "")
	var list struct{ Accesses []map[string]any }
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list.Accesses) != 1 {
		t.Fatalf("list = %d %s", w.Code, w.Body)
	}
	w = a.do("GET", "/api/direct-accesses?targetKind=database&targetId=8", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"accesses":[]`) {
		t.Fatalf("empty list = %d %s", w.Code, w.Body)
	}

	w = a.do("POST", "/api/direct-accesses/1/extend", `{"minutes":240}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), a.now.Add(4*time.Hour).UTC().Format(time.RFC3339)) {
		t.Fatalf("extend = %d %s", w.Code, w.Body)
	}
	if w := a.do("DELETE", "/api/direct-accesses/1", ""); w.Code != 204 {
		t.Fatalf("close = %d %s", w.Code, w.Body)
	}
	if w := a.do("DELETE", "/api/direct-accesses/1", ""); w.Code != 404 {
		t.Fatalf("close again = %d", w.Code)
	}
}

func TestHTTPErrorMapping(t *testing.T) {
	a := newAPIRig(t)
	body := func(f string) string { return strings.Replace(openBody, `"minutes":60`, f, 1) }
	for name, tc := range map[string]struct {
		body string
		code int
		msg  string
	}{
		"minutes":   {body(`"minutes":30`), 400, "minutes"},
		"port":      {strings.Replace(openBody, `"hostPort":0`, `"hostPort":80`, 1), 400, "port"},
		"kind":      {strings.Replace(openBody, `"database"`, `"service"`, 1), 400, "not supported yet"},
		"channel":   {strings.Replace(openBody, `"host_port"`, `"tailscale"`, 1), 400, "not supported yet"},
		"no target": {strings.Replace(openBody, `"targetId":7`, `"targetId":99`, 1), 404, ""},
		"bad json":  {`{`, 400, ""},
		"unknown":   {`{"bogus":1}`, 400, ""},
		"oversized": {`{"channel":"` + strings.Repeat("a", 70000) + `"}`, 400, ""},
	} {
		w := a.do("POST", "/api/direct-accesses", tc.body)
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.msg) {
			t.Errorf("%s: %d %s, want %d containing %q", name, w.Code, w.Body, tc.code, tc.msg)
		}
	}

	a.targets.eps["database:7"] = Endpoint{Name: "Shop", Network: "relist-network", Host: "relist-db-7", Port: 5432, Running: false}
	if w := a.do("POST", "/api/direct-accesses", openBody); w.Code != 409 || !strings.Contains(w.Body.String(), "start the Database first") {
		t.Errorf("stopped: %d %s", w.Code, w.Body)
	}
	a.targets.eps["database:7"] = Endpoint{Name: "Shop", Network: "relist-network", Host: "relist-db-7", Port: 5432, Running: true}
	a.locked = true
	if w := a.do("POST", "/api/direct-accesses", openBody); w.Code != 409 {
		t.Errorf("locked: %d", w.Code)
	}
	a.locked = false
	a.host.startErr = ErrPortInUse
	if w := a.do("POST", "/api/direct-accesses", openBody); w.Code != 409 || !strings.Contains(w.Body.String(), "already in use") {
		t.Errorf("port in use: %d %s", w.Code, w.Body)
	}
	a.host.startErr = nil
	a.do("POST", "/api/direct-accesses", openBody)
	if w := a.do("POST", "/api/direct-accesses", openBody); w.Code != 409 || !strings.Contains(w.Body.String(), "already has a Direct access") {
		t.Errorf("second open: %d %s", w.Code, w.Body)
	}
	if w := a.do("POST", "/api/direct-accesses/1/extend", `{"minutes":30}`); w.Code != 400 {
		t.Errorf("extend minutes: %d", w.Code)
	}
	if w := a.do("POST", "/api/direct-accesses/77/extend", `{"minutes":60}`); w.Code != 404 || !strings.Contains(w.Body.String(), "this Direct access has ended") {
		t.Errorf("extend missing: %d %s", w.Code, w.Body)
	}
	if w := a.do("GET", "/api/direct-accesses", ""); w.Code != 400 {
		t.Errorf("list without target: %d", w.Code)
	}
}

func TestHTTPAudit(t *testing.T) {
	a := newAPIRig(t)
	// The audit pending event is read through a recording hook: capture the
	// Target and Details the handler filled.
	var pending *audit.Pending
	mux := http.NewServeMux()
	NewAPI(a.svc).RegisterRoutes(mux)
	do := func(method, path, body string) {
		ctx, p := audit.Begin(context.Background(), audit.Event{}, nil, func(context.Context, audit.Event) error { return nil })
		pending = p
		req := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(auth.WithUser(ctx, auth.User{ID: 5, IsAdmin: true}))
		mux.ServeHTTP(httptest.NewRecorder(), req)
	}
	do("POST", "/api/direct-accesses", strings.Replace(openBody, `"allInterfaces":false`, `"allInterfaces":true`, 1))
	e := pending.Event()
	if e.Target != "Shop" || e.Detail["target"] != "database:7" || e.Detail["channel"] != "host_port" || e.Detail["bind"] != "0.0.0.0" ||
		e.Detail["port"] != 49153 || e.Detail["minutes"] != 60 {
		t.Fatalf("open audit = target %q details %v", e.Target, e.Detail)
	}
	do("POST", "/api/direct-accesses/1/extend", `{"minutes":240}`)
	e = pending.Event()
	if e.Target != "Shop" || e.Detail["minutes"] != 240 {
		t.Fatalf("extend audit = %q %v", e.Target, e.Detail)
	}
	do("DELETE", "/api/direct-accesses/1", "")
	if e = pending.Event(); e.Target != "Shop" {
		t.Fatalf("close audit = %q", e.Target)
	}
}
