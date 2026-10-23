package dockerhost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type testEngine struct {
	calls     []Kind
	pruneErr  error
	reportErr error
}

func (e *testEngine) Report(context.Context) (Report, error) {
	if e.reportErr != nil {
		return Report{}, e.reportErr
	}
	return Report{Version: "28.0", Usage: []Usage{}}, nil
}
func (e *testEngine) Prune(_ context.Context, k Kind) (PruneResult, error) {
	e.calls = append(e.calls, k)
	if e.pruneErr != nil {
		return PruneResult{}, e.pruneErr
	}
	return PruneResult{Kind: k, SpaceReclaimed: 1024, Deleted: []string{"removed"}}, nil
}
func TestAPIRefusesUnconfirmedUnknownAndBusyPrunes(t *testing.T) {
	engine := &testEngine{}
	guard := NewGuard()
	mux := http.NewServeMux()
	NewAPI(engine, guard, nil).RegisterRoutes(mux)
	for _, body := range []string{`{}`, `{"kind":"images","confirm":"wrong"}`, `{"kind":"system","confirm":"PRUNE"}`, `{"kind":"all-volumes","confirm":"PRUNE"}`, `{"kind":"images","confirm":"PRUNE","force":true}`, `{"kind":"images","confirm":"PRUNE"} {}`} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/docker/prune", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("body %s: %d %s", body, w.Code, w.Body.String())
		}
	}
	if len(engine.calls) != 0 {
		t.Fatal("invalid input reached Docker")
	}
	release, _ := guard.Acquire(context.Background())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/docker/prune", strings.NewReader(`{"kind":"images","confirm":"PRUNE"}`)))
	if w.Code != 409 || len(engine.calls) != 0 {
		t.Fatalf("busy: %d %s", w.Code, w.Body.String())
	}
	release()
	for _, kind := range []Kind{Images, UnusedImages, Containers, Networks, BuildCache, Volumes, AllVolumes} {
		confirm := "PRUNE"
		if kind == Volumes || kind == AllVolumes {
			confirm = "DELETE VOLUMES"
		}
		body, _ := json.Marshal(map[string]string{"kind": string(kind), "confirm": confirm})
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/docker/prune", strings.NewReader(string(body))))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"spaceReclaimed":1024`) {
			t.Fatalf("confirmed %s: %d %s", kind, w.Code, w.Body.String())
		}
	}
	if len(engine.calls) != 7 {
		t.Fatalf("prunes: %v", engine.calls)
	}
}

func TestAPIDoesNotPruneImagesNeededByQueuedDeployments(t *testing.T) {
	engine := &testEngine{}
	mux := http.NewServeMux()
	NewAPI(engine, NewGuard(), func(context.Context) (bool, error) { return true, nil }).RegisterRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/docker/prune", strings.NewReader(`{"kind":"unused-images","confirm":"PRUNE"}`)))
	if w.Code != 409 || len(engine.calls) != 0 {
		t.Fatalf("queued prune: %d %s", w.Code, w.Body.String())
	}
}

func TestAPIUncertainResultRequiresConfirmedRecovery(t *testing.T) {
	engine := &testEngine{pruneErr: ErrPruneUncertain}
	guard := NewGuard()
	mux := http.NewServeMux()
	NewAPI(engine, guard, nil).RegisterRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/docker/prune", strings.NewReader(`{"kind":"containers","confirm":"PRUNE"}`)))
	if w.Code != 502 || !guard.Blocked() {
		t.Fatalf("uncertain result did not pause: %d %s", w.Code, w.Body.String())
	}
	if _, err := guard.Acquire(context.Background()); err == nil {
		t.Fatal("uncertain deletion overlapped work")
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/docker", nil))
	if !strings.Contains(w.Body.String(), `"maintenanceBlocked":true`) {
		t.Fatalf("pause absent from report: %s", w.Body.String())
	}
	for _, body := range []string{`{}`, `{"confirm":"PRUNE"}`, `{"confirm":"RESUME DOCKER","force":true}`, `{"confirm":"RESUME DOCKER"} {}`} {
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/docker/recover", strings.NewReader(body)))
		if w.Code != 400 || !guard.Blocked() {
			t.Fatalf("invalid recovery: %d %s", w.Code, w.Body.String())
		}
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/docker/recover", strings.NewReader(`{"confirm":"RESUME DOCKER"}`)))
	if w.Code != 200 || guard.Blocked() {
		t.Fatalf("recovery: %d %s", w.Code, w.Body.String())
	}
}

func TestFailedForcedRefreshDoesNotRestoreOlderCachedReport(t *testing.T) {
	engine := &testEngine{}
	mux := http.NewServeMux()
	NewAPI(engine, NewGuard(), nil).RegisterRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/docker", nil))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	engine.reportErr = ErrPruneUncertain
	for _, path := range []string{"/api/docker?refresh=1", "/api/docker"} {
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 502 {
			t.Fatalf("failed daemon report returned cached success: %s %d", path, w.Code)
		}
	}
}
