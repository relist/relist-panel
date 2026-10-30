package composes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

const zeroDowntimePath = "/api/projects/7/environments/3/composes/1/zero-downtime"

func serve(t *testing.T, svc *Service, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

func TestZeroDowntimeHTTPList(t *testing.T) {
	svc, runner, _, db := newDeployService(t, zdYAML)
	seedDomain(t, db, "web")
	seedDomain(t, db, "db")
	switchOn(t, db, "web")
	runner.config = []byte(`{"services":{"web":{},"db":{"healthcheck":{"test":["CMD","true"]}},"worker":{}}}`)
	rec := serve(t, svc, http.MethodGet, zeroDowntimePath, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET = %d %s", rec.Code, rec.Body)
	}
	var got []ZeroDowntimeStatus
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := []ZeroDowntimeStatus{
		{Service: "web", Enabled: true, Reasons: []string{"has no healthcheck"}},
		{Service: "db", Enabled: false, Reasons: []string{}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GET = %+v, want %+v (YAML order, Services with a Domain only)", got, want)
	}
}

func TestZeroDowntimeHTTPSet(t *testing.T) {
	svc, _, _, db := newDeployService(t, zdYAML)
	seedDomain(t, db, "web")
	if rec := serve(t, svc, http.MethodPut, zeroDowntimePath+"/web", `{"enabled":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("PUT on = %d %s", rec.Code, rec.Body)
	}
	if enabled, _ := svc.switches.Enabled(t.Context(), 1); !reflect.DeepEqual(enabled, []string{"web"}) {
		t.Fatalf("enabled = %v", enabled)
	}
	if rec := serve(t, svc, http.MethodPut, zeroDowntimePath+"/web", `{"enabled":false}`); rec.Code != http.StatusNoContent {
		t.Fatalf("PUT off = %d %s", rec.Code, rec.Body)
	}
	if enabled, _ := svc.switches.Enabled(t.Context(), 1); len(enabled) != 0 {
		t.Fatalf("enabled after off = %v", enabled)
	}
}

func TestZeroDowntimeHTTPSetWithoutDomain(t *testing.T) {
	svc, _, _, _ := newDeployService(t, zdYAML)
	rec := serve(t, svc, http.MethodPut, zeroDowntimePath+"/worker", `{"enabled":true}`)
	if rec.Code != http.StatusUnprocessableEntity || strings.TrimSpace(rec.Body.String()) != `{"error":"worker has no domain"}` {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
	}
}

func TestZeroDowntimeHTTPUnknownCompose(t *testing.T) {
	svc, _, _, _ := newDeployService(t, zdYAML)
	rec := serve(t, svc, http.MethodPut, "/api/projects/7/environments/3/composes/99/zero-downtime/web", `{"enabled":true}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body)
	}
}

func TestZeroDowntimeHTTPEnableDespiteReasons(t *testing.T) {
	svc, runner, _, db := newDeployService(t, zdYAML)
	seedDomain(t, db, "web")
	runner.config = []byte(`{"services":{"web":{}}}`)
	if rec := serve(t, svc, http.MethodPut, zeroDowntimePath+"/web", `{"enabled":true}`); rec.Code != http.StatusNoContent {
		t.Fatalf("PUT = %d %s, want 204 even though web has no healthcheck", rec.Code, rec.Body)
	}
}
