package panelupdate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
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

// fakeRunner is the update Container, scripted.
type fakeRunner struct {
	mu        sync.Mutex
	specs     []UpdateSpec
	started   []UpdateSpec
	stopped   []string
	stdout    []byte
	code      int
	err       error
	startErr  error
	onPreview func()
}

func (f *fakeRunner) Preflight(_ context.Context, spec UpdateSpec) ([]byte, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.specs = append(f.specs, spec)
	if f.onPreview != nil {
		f.onPreview()
	}
	return f.stdout, f.code, f.err
}

func (f *fakeRunner) StartUpdater(_ context.Context, spec UpdateSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, spec)
	return f.startErr
}

func (f *fakeRunner) StopUpdater(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stopped = append(f.stopped, name)
	return f.err
}

const httpReportID = "0123456789abcdef0123456789abcdef"

type httpRig struct {
	*lockRig
	runner *fakeRunner
}

func newHTTPRig(t *testing.T) *httpRig {
	t.Helper()
	r := &httpRig{lockRig: newLockRig(t), runner: &fakeRunner{stdout: []byte(`{"id":"` + httpReportID + `","ready":true,"toVersion":"1.4.0","invariants":[]}`)}}
	r.s.d.Version = "1.3.0"
	r.s.d.Runner = r.runner
	r.s.d.Self = fakeSelf{self: Self{ImageID: "sha256:img", Labels: composeLabels()}}
	r.s.d.Now = func() time.Time { return time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC) }
	return r
}

func postJSON(t *testing.T, s *Service, path, body string) (*httptest.ResponseRecorder, map[string]json.RawMessage) {
	t.Helper()
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", path, strings.NewReader(body)))
	var out map[string]json.RawMessage
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec, out
}

func (r *httpRig) preview(t *testing.T) {
	t.Helper()
	if rec, _ := postJSON(t, r.s, "/api/panel-update/preflight", ""); rec.Code != 200 {
		t.Fatalf("preview = %d %s", rec.Code, rec.Body)
	}
}

func TestPreflightEndpointRunsContainer(t *testing.T) {
	r := newHTTPRig(t)
	rec, out := postJSON(t, r.s, "/api/panel-update/preflight", "")
	if rec.Code != 200 {
		t.Fatalf("code = %d %s", rec.Code, rec.Body)
	}
	if len(r.runner.specs) != 1 {
		t.Fatalf("specs = %v", r.runner.specs)
	}
	spec := r.runner.specs[0]
	want := "--preflight --json --project relist-panel --dir /srv/relist --file /srv/relist/docker-compose.yml --file /srv/relist/docker-compose.override.yml"
	if got := strings.Join(spec.Args, " "); got != want || spec.Name != "" || spec.Image != "sha256:img" ||
		spec.InstallDir != "/srv/relist" || spec.DataDir != r.dir || spec.Network != "relist-network" {
		t.Fatalf("spec = %+v", spec)
	}
	var report Report
	var gate Gate
	if err := json.Unmarshal(out["report"], &report); err != nil || report.ID != httpReportID || !report.Ready {
		t.Fatalf("report = %s", out["report"])
	}
	if err := json.Unmarshal(out["gate"], &gate); err != nil || !gate.InstallAvailable || gate.MigrationLocked || gate.UpdateRunning {
		t.Fatalf("gate = %s", out["gate"])
	}
}

func TestPreflightEndpointShowsLiveGate(t *testing.T) {
	r := newHTTPRig(t)
	r.migr = true
	r.s.d.DeploysBusy = func() bool { return true }
	_, out := postJSON(t, r.s, "/api/panel-update/preflight", "")
	var gate Gate
	_ = json.Unmarshal(out["gate"], &gate)
	if !gate.MigrationLocked || !gate.DeploysBusy {
		t.Fatalf("gate = %s", out["gate"])
	}
}

func TestPreflightEndpointExitThreeIsReport(t *testing.T) {
	for _, code := range []int{1, 3} {
		r := newHTTPRig(t)
		r.runner.code = code
		r.runner.stdout = []byte(`{"id":"` + httpReportID + `","upToDate":true,"invariants":[]}`)
		rec, out := postJSON(t, r.s, "/api/panel-update/preflight", "")
		if rec.Code != 200 || out["report"] == nil {
			t.Fatalf("exit %d: code = %d %s", code, rec.Code, rec.Body)
		}
	}
}

func TestPreflightEndpointFailures(t *testing.T) {
	r := newHTTPRig(t)
	r.runner.code = 2
	if rec, _ := postJSON(t, r.s, "/api/panel-update/preflight", ""); rec.Code != 500 {
		t.Fatalf("exit 2: code = %d", rec.Code)
	}
	r = newHTTPRig(t)
	r.runner.stdout = []byte("not json")
	if rec, _ := postJSON(t, r.s, "/api/panel-update/preflight", ""); rec.Code != 500 {
		t.Fatalf("bad output: code = %d", rec.Code)
	}
	r = newHTTPRig(t)
	r.runner.err = errors.New("no docker")
	if rec, _ := postJSON(t, r.s, "/api/panel-update/preflight", ""); rec.Code != 500 {
		t.Fatalf("runner error: code = %d", rec.Code)
	}
}

func TestPreflightEndpointInstallUnavailableIsAReport(t *testing.T) {
	r := newHTTPRig(t)
	r.s.d.Self = fakeSelf{self: Self{}}
	rec, out := postJSON(t, r.s, "/api/panel-update/preflight", "")
	var report Report
	_ = json.Unmarshal(out["report"], &report)
	if rec.Code != 200 || report.Failed == nil || report.Failed.Step != StepInstall || !strings.Contains(report.Failed.Message, reasonNotCompose) || len(r.runner.specs) != 0 {
		t.Fatalf("code = %d report = %+v", rec.Code, report)
	}
}

func TestPreflightEndpointRefusedWhileUpdating(t *testing.T) {
	r := newHTTPRig(t)
	if err := r.s.lock(t.Context()); err != nil {
		t.Fatal(err)
	}
	if rec, _ := postJSON(t, r.s, "/api/panel-update/preflight", ""); rec.Code != 409 || len(r.runner.specs) != 0 {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestApplyEndpointStartsDetachedAndReturns(t *testing.T) {
	r := newHTTPRig(t)
	r.preview(t)
	rec, out := postJSON(t, r.s, "/api/panel-update/apply", `{"report_id":"`+httpReportID+`","manual_steps_done":["a","b"]}`)
	if rec.Code != 202 {
		t.Fatalf("code = %d %s", rec.Code, rec.Body)
	}
	var run string
	_ = json.Unmarshal(out["run"], &run)
	if run != "20261101T120000Z" || len(r.runner.started) != 1 {
		t.Fatalf("run = %q started = %v", run, r.runner.started)
	}
	spec := r.runner.started[0]
	want := "--report " + httpReportID + " --run 20261101T120000Z --steps-done a,b --project relist-panel --dir /srv/relist --file /srv/relist/docker-compose.yml --file /srv/relist/docker-compose.override.yml"
	if spec.Name != "relist-panel-updater-20261101T120000Z" || strings.Join(spec.Args, " ") != want {
		t.Fatalf("spec = %+v", spec)
	}
}

func TestApplyEndpointPassesAManifestURLOverride(t *testing.T) {
	r := newHTTPRig(t)
	r.s.d.ManifestURL = "http://registry.test/m.json"
	r.preview(t)
	if r.runner.specs[0].Env[0] != "RELIST_UPDATE_MANIFEST_URL=http://registry.test/m.json" {
		t.Fatalf("env = %v", r.runner.specs[0].Env)
	}
	r2 := newHTTPRig(t)
	r2.s.d.ManifestURL = DefaultManifestURL
	r2.preview(t)
	if len(r2.runner.specs[0].Env) != 0 {
		t.Fatalf("env = %v", r2.runner.specs[0].Env)
	}
}

func TestApplyEndpointRefusesStaleReport(t *testing.T) {
	r := newHTTPRig(t)
	if rec, _ := postJSON(t, r.s, "/api/panel-update/apply", `{"report_id":"`+httpReportID+`"}`); rec.Code != 409 {
		t.Fatalf("no preview yet: code = %d", rec.Code)
	}
	r.preview(t)
	if rec, _ := postJSON(t, r.s, "/api/panel-update/apply", `{"report_id":"ffffffffffffffffffffffffffffffff"}`); rec.Code != 409 || len(r.runner.started) != 0 {
		t.Fatalf("other report: code = %d", rec.Code)
	}
	if rec, _ := postJSON(t, r.s, "/api/panel-update/apply", `{"report_id":"`+httpReportID+`","manual_steps_done":["a,b"]}`); rec.Code != 400 {
		t.Fatalf("bad step id: code = %d", rec.Code)
	}
	if rec, _ := postJSON(t, r.s, "/api/panel-update/apply", `{}`); rec.Code != 400 {
		t.Fatalf("empty body: code = %d", rec.Code)
	}
}

func TestApplyEndpointRefusesRunningRun(t *testing.T) {
	r := newHTTPRig(t)
	r.preview(t)
	r.writeRun(t, "20261101T110000Z", StateBackingUp)
	if rec, _ := postJSON(t, r.s, "/api/panel-update/apply", `{"report_id":"`+httpReportID+`"}`); rec.Code != 409 || len(r.runner.started) != 0 {
		t.Fatalf("code = %d", rec.Code)
	}
	// A finished run does not block the next one.
	r.writeRun(t, "20261101T110000Z", StateRolledBack)
	if rec, _ := postJSON(t, r.s, "/api/panel-update/apply", `{"report_id":"`+httpReportID+`"}`); rec.Code != 202 {
		t.Fatalf("after a finished run: code = %d", rec.Code)
	}
}

func TestApplyEndpointStartFailure(t *testing.T) {
	r := newHTTPRig(t)
	r.preview(t)
	r.runner.startErr = errors.New("name in use")
	if rec, _ := postJSON(t, r.s, "/api/panel-update/apply", `{"report_id":"`+httpReportID+`"}`); rec.Code != 500 {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestCancelEndpoint(t *testing.T) {
	r := newHTTPRig(t)
	r.writeRun(t, "20261101T110000Z", StateQuiescing)
	rec, _ := postJSON(t, r.s, "/api/panel-update/cancel", "")
	if rec.Code != 202 || len(r.runner.stopped) != 1 || r.runner.stopped[0] != "relist-panel-updater-20261101T110000Z" {
		t.Fatalf("code = %d stopped = %v", rec.Code, r.runner.stopped)
	}
	r = newHTTPRig(t)
	r.writeRun(t, "20261101T110000Z", StateComposeUp)
	rec, _ = postJSON(t, r.s, "/api/panel-update/cancel", "")
	if rec.Code != 409 || len(r.runner.stopped) != 0 || !strings.Contains(rec.Body.String(), "Only a waiting update can be cancelled") {
		t.Fatalf("code = %d body = %s", rec.Code, rec.Body)
	}
	r = newHTTPRig(t)
	if rec, _ = postJSON(t, r.s, "/api/panel-update/cancel", ""); rec.Code != 409 {
		t.Fatalf("no run: code = %d", rec.Code)
	}
}

func TestStatusCarriesWaitingOnAndNote(t *testing.T) {
	r := newHTTPRig(t)
	dir := RunDir(r.dir, "20261101T110000Z")
	_ = os.MkdirAll(dir, 0o700)
	_ = WriteStatus(dir, RunStatus{Run: "20261101T110000Z", State: StateQuiescing, WaitingOn: "Deployment api #3", Note: PanelNoAnswerNote}, r.s.d.Now())
	_, st := serve(t, r.s, "GET", "/api/panel-update/status", "")
	if st.Run == nil || st.Run.WaitingOn != "Deployment api #3" || st.Run.Note != PanelNoAnswerNote {
		t.Fatalf("run = %+v", st.Run)
	}
}

// Two applies inside the Updater's re-check window (two tabs, or the page and
// install.sh) must not both start: a running Updater of any run refuses the next.
func TestApplyEndpointRefusesWhileAnUpdaterRuns(t *testing.T) {
	r := newHTTPRig(t)
	r.preview(t)
	r.updater.state["relist-panel-updater-20261101T115959Z"] = UpdaterState{Exists: true, Running: true}
	rec, _ := postJSON(t, r.s, "/api/panel-update/apply", `{"report_id":"`+httpReportID+`"}`)
	if rec.Code != 409 || len(r.runner.started) != 0 || !strings.Contains(rec.Body.String(), "relist-panel-updater-20261101T115959Z") {
		t.Fatalf("code = %d body = %s started = %v", rec.Code, rec.Body, r.runner.started)
	}
	// An Updater that has exited does not block.
	r.updater.state["relist-panel-updater-20261101T115959Z"] = UpdaterState{Exists: true}
	if rec, _ := postJSON(t, r.s, "/api/panel-update/apply", `{"report_id":"`+httpReportID+`"}`); rec.Code != 202 {
		t.Fatalf("with only an exited Updater: code = %d", rec.Code)
	}
}
