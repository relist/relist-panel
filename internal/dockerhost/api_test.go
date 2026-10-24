package dockerhost

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type testEngine struct {
	calls      []Kind
	pruneErr   error
	reportErr  error
	candidates map[Kind][]Candidate
	listErr    error
	refuse     map[string]string // candidate name -> refusal
	failOn     string            // candidate name whose Remove errors
	removed    []string
}

func (e *testEngine) Candidates(_ context.Context, k Kind) ([]Candidate, error) {
	return e.candidates[k], e.listErr
}
func (e *testEngine) Remove(_ context.Context, _ Kind, c Candidate) (bool, string, error) {
	if c.Name == e.failOn {
		return false, "", errors.New("socket closed")
	}
	if reason := e.refuse[c.Name]; reason != "" {
		return false, reason, nil
	}
	e.removed = append(e.removed, c.Name)
	return true, "", nil
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
	NewAPI(engine, guard, nil, nil).RegisterRoutes(mux)
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
	for _, kind := range []Kind{Containers, Networks, BuildCache} {
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
	if len(engine.calls) != 3 {
		t.Fatalf("prunes: %v", engine.calls)
	}
}

func TestAPIDoesNotPruneImagesNeededByQueuedDeployments(t *testing.T) {
	engine := &testEngine{}
	mux := http.NewServeMux()
	NewAPI(engine, NewGuard(), func(context.Context) (bool, error) { return true, nil }, nil).RegisterRoutes(mux)
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
	NewAPI(engine, guard, nil, nil).RegisterRoutes(mux)
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
	NewAPI(engine, NewGuard(), nil, nil).RegisterRoutes(mux)
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

func ownersOf(o Ownership, err error) func(context.Context) (Ownership, error) {
	return func(context.Context) (Ownership, error) { return o, err }
}

func panelOwners() Ownership {
	return Ownership{Projects: map[string]string{"relist-c2": "shop"}, Volumes: map[string]string{"ext": "shop"}, Images: map[string]string{}}
}

func volumeEngine() *testEngine {
	size := uint64(100)
	return &testEngine{candidates: map[Kind][]Candidate{AllVolumes: {
		{Name: "panel_data", Labels: map[string]string{"com.docker.compose.project": "relist-c2"}, Size: &size},
		{Name: "ext"},
		{Name: "foreign", Size: &size},
		{Name: "busy", Size: &size},
	}}}
}

func post(mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/docker/prune", strings.NewReader(body)))
	return w
}

func TestPrunePlanRoute(t *testing.T) {
	engine := volumeEngine()
	mux := http.NewServeMux()
	NewAPI(engine, NewGuard(), nil, ownersOf(panelOwners(), nil)).RegisterRoutes(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/docker/prune-plan?kind=all-volumes", nil))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var plan PrunePlan
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if got := names(plan.Delete); len(got) != 2 || got[0] != "foreign" || len(plan.Skip) != 2 || plan.Skip[0].Compose != "shop" {
		t.Fatalf("plan: %s", w.Body.String())
	}
	for _, kind := range []string{"containers", "networks", "build-cache"} {
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/docker/prune-plan?kind="+kind, nil))
		if w.Code != 200 || w.Body.String() != `{"kind":"`+kind+`","delete":[],"skip":[]}`+"\n" {
			t.Fatalf("%s: %d %s", kind, w.Code, w.Body.String())
		}
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/docker/prune-plan?kind=system", nil))
	if w.Code != 400 {
		t.Fatalf("unknown kind: %d", w.Code)
	}
	if len(engine.removed) != 0 {
		t.Fatal("plan deleted")
	}
}

func TestPruneDeletesOnlyThePlanAndReportsSkipped(t *testing.T) {
	engine := volumeEngine()
	mux := http.NewServeMux()
	NewAPI(engine, NewGuard(), nil, ownersOf(panelOwners(), nil)).RegisterRoutes(mux)
	w := post(mux, `{"kind":"all-volumes","confirm":"DELETE VOLUMES"}`)
	if w.Code != 200 || !reflect.DeepEqual(engine.removed, []string{"foreign", "busy"}) {
		t.Fatalf("%d %s removed=%v", w.Code, w.Body.String(), engine.removed)
	}
	var result PruneResult
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if result.SpaceReclaimed != 200 || len(result.Skipped) != 2 {
		t.Fatalf("result: %s", w.Body.String())
	}
}

func TestPruneIncludePanelNeedsStrongerWord(t *testing.T) {
	engine := volumeEngine()
	mux := http.NewServeMux()
	NewAPI(engine, NewGuard(), nil, ownersOf(panelOwners(), nil)).RegisterRoutes(mux)
	if w := post(mux, `{"kind":"all-volumes","confirm":"DELETE VOLUMES","includePanel":true}`); w.Code != 400 || len(engine.removed) != 0 {
		t.Fatalf("weak word: %d %s", w.Code, w.Body.String())
	}
	if w := post(mux, `{"kind":"all-volumes","confirm":"DELETE PANEL DATA"}`); w.Code != 400 {
		t.Fatalf("strong word without includePanel: %d", w.Code)
	}
	if w := post(mux, `{"kind":"containers","confirm":"DELETE PANEL DATA","includePanel":true}`); w.Code != 400 {
		t.Fatalf("includePanel on containers: %d", w.Code)
	}
	w := post(mux, `{"kind":"all-volumes","confirm":"DELETE PANEL DATA","includePanel":true}`)
	if w.Code != 200 || len(engine.removed) != 4 {
		t.Fatalf("include panel: %d %s removed=%v", w.Code, w.Body.String(), engine.removed)
	}
}

func TestPruneRefusalIsSkippedNotBlocked(t *testing.T) {
	engine := volumeEngine()
	engine.refuse = map[string]string{"foreign": "volume is in use"}
	guard := NewGuard()
	mux := http.NewServeMux()
	NewAPI(engine, guard, nil, ownersOf(panelOwners(), nil)).RegisterRoutes(mux)
	w := post(mux, `{"kind":"all-volumes","confirm":"DELETE VOLUMES"}`)
	if w.Code != 200 || guard.Blocked() || !strings.Contains(w.Body.String(), `"name":"foreign"`) || !strings.Contains(w.Body.String(), "volume is in use") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	if !reflect.DeepEqual(engine.removed, []string{"busy"}) {
		t.Fatalf("removed %v", engine.removed)
	}
}

func TestPruneTransportErrorBlocks(t *testing.T) {
	engine := volumeEngine()
	engine.failOn = "busy"
	guard := NewGuard()
	mux := http.NewServeMux()
	NewAPI(engine, guard, nil, ownersOf(panelOwners(), nil)).RegisterRoutes(mux)
	w := post(mux, `{"kind":"all-volumes","confirm":"DELETE VOLUMES"}`)
	if w.Code != 502 || !guard.Blocked() || !strings.Contains(w.Body.String(), "foreign") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestPruneOwnersFailureDeletesNothing(t *testing.T) {
	engine := volumeEngine()
	guard := NewGuard()
	mux := http.NewServeMux()
	NewAPI(engine, guard, nil, ownersOf(Ownership{}, errors.New("db down"))).RegisterRoutes(mux)
	if w := post(mux, `{"kind":"all-volumes","confirm":"DELETE VOLUMES"}`); w.Code != 503 || len(engine.removed) != 0 || guard.Blocked() {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/docker/prune-plan?kind=all-volumes", nil))
	if w.Code != 503 {
		t.Fatalf("plan: %d", w.Code)
	}
}
