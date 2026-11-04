package traefikconfig

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type routeRig struct {
	testRunner
	mux *http.ServeMux
}

func newRouteRig(t *testing.T, override string) routeRig {
	t.Helper()
	tr := newTestRunner(t, override)
	mux := http.NewServeMux()
	NewService(tr.Runner).RegisterRoutes(mux)
	return routeRig{testRunner: tr, mux: mux}
}

func (rr routeRig) do(t *testing.T, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var reader *strings.Reader
	if s, ok := body.(string); ok {
		reader = strings.NewReader(s)
	} else {
		b, _ := json.Marshal(body)
		reader = strings.NewReader(string(b))
	}
	rec := httptest.NewRecorder()
	rr.mux.ServeHTTP(rec, httptest.NewRequest(method, path, reader))
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func TestConfigRoute(t *testing.T) {
	rr := newRouteRig(t, "")
	later := running
	later.StartedAt = time.Now().Add(time.Hour) // started after traefik.yml was written
	rr.fake.states = []State{later}
	code, got := rr.do(t, "GET", "/api/traefik/config", "")
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	form, _ := got["form"].(map[string]any)
	if form["logLevel"] != "INFO" || got["revision"] != Revision(nil) || got["base"] != string(Base()) {
		t.Fatalf("body = %v", got)
	}
	if !strings.HasPrefix(got["effective"].(string), GeneratedHeader) || got["pendingRestart"] != false || got["lastApply"] != nil {
		t.Fatalf("body = %v", got)
	}
}

func TestPreviewRouteForm(t *testing.T) {
	rr := newRouteRig(t, "# mine\n")
	code, got := rr.do(t, "POST", "/api/traefik/config/preview",
		map[string]any{"override": "# mine\n", "form": Form{ACMEEmail: "a@b.c", LogLevel: "INFO"}})
	if code != 200 || !strings.Contains(got["override"].(string), "a@b.c") || !strings.Contains(got["effective"].(string), "a@b.c") {
		t.Fatalf("status %d body %v", code, got)
	}
	if errs := got["errors"].([]any); len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
}

func TestPreviewRouteBadYAML(t *testing.T) {
	rr := newRouteRig(t, "")
	code, got := rr.do(t, "POST", "/api/traefik/config/preview", map[string]any{"override": "a: [oops\n"})
	errs, _ := got["errors"].([]any)
	if code != 200 || len(errs) != 1 || errs[0].(map[string]any)["line"] != float64(1) || got["form"] != nil {
		t.Fatalf("status %d body %v", code, got)
	}
}

func TestTestRoute(t *testing.T) {
	rr := newRouteRig(t, "")
	code, got := rr.do(t, "POST", "/api/traefik/config/test", map[string]any{"override": "log: {level: DEBUG}\n"})
	if code != 200 || got["ok"] != true {
		t.Fatalf("status %d body %v", code, got)
	}
	rr.fake.dry = DryRunResult{Running: false, Log: "Command error"}
	_, got = rr.do(t, "POST", "/api/traefik/config/test", map[string]any{"override": "log: {level: DEBUG}\n"})
	if got["ok"] != false || !strings.Contains(got["log"].(string), "Command error") {
		t.Fatalf("body %v", got)
	}
}

func TestApplyRoute(t *testing.T) {
	rr := newRouteRig(t, "")
	code, got := rr.do(t, "POST", "/api/traefik/config/apply", map[string]any{"override": "log: {level: DEBUG}\n", "revision": Revision(nil)})
	if code != 202 || got["id"] == "" {
		t.Fatalf("status %d body %v", code, got)
	}
	run := rr.wait(t)
	code, got = rr.do(t, "GET", "/api/traefik/config/apply", "")
	if code != 200 || got["id"] != run.ID || got["state"] != StateOK {
		t.Fatalf("status %d body %v", code, got)
	}
	if code, _ := rr.do(t, "POST", "/api/traefik/config/apply/ack", map[string]any{"id": run.ID}); code != 204 {
		t.Fatalf("ack status %d", code)
	}
	if run, _ := rr.Status(); !run.Acked {
		t.Fatal("not acked")
	}
}

func TestApplyRoute409(t *testing.T) {
	rr := newRouteRig(t, "log: {level: WARN}\n")
	code, _ := rr.do(t, "POST", "/api/traefik/config/apply", map[string]any{"override": "log: {level: DEBUG}\n", "revision": "stale"})
	if code != 409 {
		t.Fatalf("status %d", code)
	}
}

func TestApplyRouteLockedKey(t *testing.T) {
	rr := newRouteRig(t, "")
	code, got := rr.do(t, "POST", "/api/traefik/config/apply", map[string]any{"override": "entryPoints: {web: {address: ':81'}}\n", "revision": Revision(nil)})
	errs, _ := got["errors"].([]any)
	if code != 400 || len(errs) != 1 || errs[0].(map[string]any)["key"] != "entryPoints.web.address" {
		t.Fatalf("status %d body %v", code, got)
	}
}

func TestApplyRouteGate(t *testing.T) {
	rr := newRouteRig(t, "")
	rr.Gate = func(context.Context) error { return fmt.Errorf("%w: a Panel update is running", ErrLocked) }
	if code, _ := rr.do(t, "POST", "/api/traefik/config/apply", map[string]any{"override": "", "revision": Revision(nil)}); code != 503 {
		t.Fatalf("status %d", code)
	}
	if code, _ := rr.do(t, "POST", "/api/traefik/restart", ""); code != 503 {
		t.Fatalf("restart status %d", code)
	}
}

func TestRestartRoute(t *testing.T) {
	rr := newRouteRig(t, "")
	if code, got := rr.do(t, "POST", "/api/traefik/restart", ""); code != 202 || got["id"] == "" {
		t.Fatalf("status %d body %v", code, got)
	}
	if run := rr.wait(t); run.Kind != KindRestart {
		t.Fatalf("run = %+v", run)
	}
}

func TestHistoryRoutes(t *testing.T) {
	rr := newRouteRig(t, "log: {level: WARN}\n")
	rr.apply(t, "log: {level: DEBUG}\n")
	rec := httptest.NewRecorder()
	rr.mux.ServeHTTP(rec, httptest.NewRequest("GET", "/api/traefik/config/history", nil))
	var entries []HistoryEntry
	if err := json.Unmarshal(rec.Body.Bytes(), &entries); err != nil || len(entries) != 1 {
		t.Fatalf("history = %s (%v)", rec.Body.String(), err)
	}
	if code, _ := rr.do(t, "POST", "/api/traefik/config/history/"+entries[0].Name+"/restore", ""); code != 202 {
		t.Fatalf("restore status %d", code)
	}
	rr.wait(t)
	for _, bad := range []string{"..%2Fx", "nope"} {
		if code, _ := rr.do(t, "POST", "/api/traefik/config/history/"+bad+"/restore", ""); code != 404 {
			t.Errorf("%s: status %d", bad, code)
		}
	}
}
