package panelupdate

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, s *Service, method, path, body string) (*httptest.ResponseRecorder, Status) {
	t.Helper()
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	var st Status
	_ = json.Unmarshal(rec.Body.Bytes(), &st)
	return rec, st
}

func TestStatusAndCheckRoutes(t *testing.T) {
	s, rel := newService(t, "1.3.0")
	rel.publish(manifestJSON("1.4.0"))

	rec, st := serve(t, s, "GET", "/api/panel-update/status", "")
	if rec.Code != 200 || st.Version != "1.3.0" || st.Available != nil || !st.CheckEnabled || st.Development {
		t.Fatalf("status = %d %+v", rec.Code, st)
	}
	rec, st = serve(t, s, "POST", "/api/panel-update/check", "")
	if rec.Code != 200 || st.Available == nil || st.Available.Version != "1.4.0" || len(st.Available.ManualSteps) != 1 || st.LastCheckAt == nil {
		t.Fatalf("check = %d %+v", rec.Code, st)
	}
	rel.fail.Store(true)
	rec, st = serve(t, s, "POST", "/api/panel-update/check", "")
	if rec.Code != 200 || st.Available == nil || st.LastError == "" {
		t.Fatalf("failed check = %d %+v", rec.Code, st)
	}
}

func TestSettingsRoute(t *testing.T) {
	s, _ := newService(t, "1.3.0")
	rec, st := serve(t, s, "PUT", "/api/panel-update/settings", `{"checkEnabled":false}`)
	if rec.Code != 200 || st.CheckEnabled {
		t.Fatalf("put = %d %+v", rec.Code, st)
	}
	if rec, _ := serve(t, s, "PUT", "/api/panel-update/settings", `{}`); rec.Code != 400 {
		t.Fatalf("empty body = %d", rec.Code)
	}
}

func TestStatusForDevelopmentBuild(t *testing.T) {
	s, _ := newService(t, "dev")
	_, st := serve(t, s, "GET", "/api/panel-update/status", "")
	if !st.Development || st.Message != "Development build: updates are not offered" {
		t.Fatalf("status = %+v", st)
	}
}

func TestCheckRouteWhileLocked(t *testing.T) {
	s, _ := newService(t, "1.3.0")
	s.SetLocked(func() bool { return true })
	if rec, _ := serve(t, s, "POST", "/api/panel-update/check", ""); rec.Code != 409 {
		t.Fatalf("code = %d", rec.Code)
	}
}
