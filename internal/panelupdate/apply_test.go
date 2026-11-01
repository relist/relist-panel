package panelupdate

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type applyRig struct {
	*rig
	queue   *fakeQueue
	updater *fakeUpdater
}

func newApplyRig(t *testing.T) *applyRig {
	t.Helper()
	r := &applyRig{rig: newRig(t, rigOpts{current: embeddedFile, theirs: newerFile})}
	r.queue = &fakeQueue{}
	r.updater = &fakeUpdater{state: map[string]UpdaterState{}}
	r.svc.d.Queue, r.svc.d.Updater, r.svc.d.Domain = r.queue, r.updater, "panel.example.com"
	return r
}

func (r *applyRig) apply(t *testing.T, id string, steps ...string) (string, error) {
	t.Helper()
	return r.svc.Apply(context.Background(), id, steps)
}

func refusal(t *testing.T, err error) string {
	t.Helper()
	var ref *Refusal
	if !errors.As(err, &ref) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	return ref.Message
}

func TestApplyStartsTheUpdater(t *testing.T) {
	r := newApplyRig(t)
	rep := r.preflight(t)
	run, err := r.apply(t, rep.ID, "open-443")
	if err != nil {
		t.Fatal(err)
	}
	if len(r.updater.started) != 1 {
		t.Fatalf("started %v", r.updater.started)
	}
	spec := r.updater.started[0]
	if spec.Name != "relist-panel-updater-"+run || spec.Run != run || spec.Image != "sha256:img" ||
		spec.InstallDir != "/srv/relist" || spec.DataDir != r.data || spec.Network != "relist-network" {
		t.Fatalf("spec = %+v", spec)
	}
	dir := RunDir(r.data, run)
	plan, err := ReadPlan(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := Plan{
		Run: run, Project: "relist-panel", InstallDir: "/srv/relist", ConfigFiles: []string{"/srv/relist/docker-compose.yml"},
		Domain: "panel.example.com", TargetRef: fixtureImage, TargetImageID: "sha256:target", FromImageID: "sha256:img",
	}
	if plan.Run != want.Run || plan.Project != want.Project || plan.Domain != want.Domain || plan.TargetRef != want.TargetRef ||
		plan.TargetImageID != want.TargetImageID || plan.FromImageID != want.FromImageID || plan.InstallDir != want.InstallDir ||
		strings.Join(plan.ConfigFiles, ",") != strings.Join(want.ConfigFiles, ",") {
		t.Fatalf("plan = %+v", plan)
	}
	// The candidate files are in the run directory, not read from the scratch.
	if got, _ := os.ReadFile(filepath.Join(dir, candidateInstall)); string(got) != newerFile {
		t.Fatalf("candidate Install file = %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, candidateEnvFile)); !strings.Contains(string(got), "RELIST_PANEL_IMAGE="+fixtureImage) {
		t.Fatalf("candidate .env = %q", got)
	}
	st, err := ReadStatus(dir)
	if err != nil || st.State != stateStartingUpdater || st.FromImage != "sha256:img" || st.ToVersion != "1.4.0" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	if !r.svc.Locked() || !contains(r.queue.list(), "hold") {
		t.Fatalf("the update lock is off: %v", r.queue.list())
	}
	// The scratch directory a later Preflight removes is not what the run uses.
	if err := os.RemoveAll(filepath.Join(r.data, ".panel-update", "preflight")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, candidateInstall)); err != nil {
		t.Fatal(err)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestApplyRefusals(t *testing.T) {
	t.Run("a report that is not the latest", func(t *testing.T) {
		r := newApplyRig(t)
		r.preflight(t)
		msg := refusal(t, func() error { _, err := r.apply(t, "old-id", "open-443"); return err }())
		if !strings.Contains(msg, "out of date") || len(r.updater.started) != 0 || r.svc.Locked() {
			t.Fatalf("msg = %q", msg)
		}
	})
	t.Run("an unticked manual step", func(t *testing.T) {
		r := newApplyRig(t)
		rep := r.preflight(t)
		msg := refusal(t, func() error { _, err := r.apply(t, rep.ID); return err }())
		if !strings.Contains(msg, "open-443") || r.svc.Locked() {
			t.Fatalf("msg = %q", msg)
		}
	})
	t.Run("the Install file changed after the preview", func(t *testing.T) {
		r := newApplyRig(t)
		rep := r.preflight(t)
		r.helper.files["/srv/relist/docker-compose.yml"] = embeddedFile + "# an edit\n"
		msg := refusal(t, func() error { _, err := r.apply(t, rep.ID, "open-443"); return err }())
		if !strings.Contains(msg, "changed since the preview") || r.svc.Locked() || len(r.updater.started) != 0 {
			t.Fatalf("msg = %q", msg)
		}
	})
	t.Run(".env changed after the preview", func(t *testing.T) {
		r := newApplyRig(t)
		rep := r.preflight(t)
		r.helper.files["/srv/relist/.env"] += "EXTRA=1\n"
		refusal(t, func() error { _, err := r.apply(t, rep.ID, "open-443"); return err }())
	})
	t.Run("a Migration lock", func(t *testing.T) {
		r := newApplyRig(t)
		rep := r.preflight(t)
		r.locked = true
		msg := refusal(t, func() error { _, err := r.apply(t, rep.ID, "open-443"); return err }())
		if !strings.Contains(msg, "Migration") {
			t.Fatalf("msg = %q", msg)
		}
	})
	t.Run("invariants that fail", func(t *testing.T) {
		r := newRig(t, rigOpts{current: embeddedFile, theirs: newerFile, invariants: []m{
			{"kind": "service-label", "service": "panel", "key": "nope", "value": "x"},
		}})
		r.svc.d.Queue, r.svc.d.Updater, r.svc.d.Domain = &fakeQueue{}, &fakeUpdater{state: map[string]UpdaterState{}}, "panel.example.com"
		rep := r.preflight(t)
		if rep.Ready {
			t.Fatal("the fixture should fail an invariant")
		}
		_, err := r.svc.Apply(context.Background(), rep.ID, []string{"open-443"})
		refusal(t, err)
	})
}

func TestApplyRefusesAStillRunningUpdater(t *testing.T) {
	r := newApplyRig(t)
	rep := r.preflight(t)
	r.updater.state["relist-panel-updater-20260101T000000Z"] = UpdaterState{Exists: true, Running: true}
	msg := refusal(t, func() error { _, err := r.apply(t, rep.ID, "open-443"); return err }())
	if !strings.Contains(msg, "relist-panel-updater-20260101T000000Z") || !strings.Contains(msg, "still running") {
		t.Fatalf("msg = %q", msg)
	}
	if r.svc.Locked() || len(r.updater.started) != 0 || len(r.updater.removed) != 0 {
		t.Fatalf("locked=%v started=%v removed=%v", r.svc.Locked(), r.updater.started, r.updater.removed)
	}
}

func TestApplyRemovesAStaleExitedUpdater(t *testing.T) {
	r := newApplyRig(t)
	rep := r.preflight(t)
	r.updater.state["relist-panel-updater-20260101T000000Z"] = UpdaterState{Exists: true}
	if _, err := r.apply(t, rep.ID, "open-443"); err != nil {
		t.Fatal(err)
	}
	if len(r.updater.removed) != 1 || r.updater.removed[0] != "relist-panel-updater-20260101T000000Z" {
		t.Fatalf("removed %v", r.updater.removed)
	}
}

func TestApplyAbortsWhenTheUpdaterCannotStart(t *testing.T) {
	r := newApplyRig(t)
	rep := r.preflight(t)
	r.updater.startErr = errors.New("no such image")
	_, err := r.apply(t, rep.ID, "open-443")
	if err == nil || !strings.Contains(err.Error(), "no such image") {
		t.Fatalf("err = %v", err)
	}
	if r.svc.Locked() {
		t.Fatal("the lock stayed on")
	}
	st, ok, _ := NewestRun(r.data)
	if !ok || st.State != StateInterrupted || !strings.Contains(st.Error, "no such image") {
		t.Fatalf("run = %+v", st)
	}
	if ev := r.queue.list(); ev[len(ev)-1] != "release" {
		t.Fatalf("events = %v", ev)
	}
}

func TestApplyRoute(t *testing.T) {
	r := newApplyRig(t)
	rep := r.preflight(t)
	rec, _ := serve(t, r.svc, http.MethodPost, "/api/panel-update/apply", `{"report_id":"nope","manual_steps_done":["open-443"]}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale: %d %s", rec.Code, rec.Body)
	}
	rec, _ = serve(t, r.svc, http.MethodPost, "/api/panel-update/apply", `{"manual_steps_done":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no id: %d", rec.Code)
	}
	rec, _ = serve(t, r.svc, http.MethodPost, "/api/panel-update/apply", `{"report_id":"`+rep.ID+`","manual_steps_done":["open-443"]}`)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"id":"2`) {
		t.Fatalf("apply: %d %s", rec.Code, rec.Body)
	}
}

func TestApplyBlocksAConcurrentPreflightUntilTheUpdaterStarted(t *testing.T) {
	r := newApplyRig(t)
	rep := r.preflight(t)
	var scratchAtStart bool
	r.updater.onStart = func(UpdaterSpec) {
		// The Preflight lock is still held here, and the plan is complete.
		scratchAtStart = !r.svc.pfRun.TryLock()
	}
	if _, err := r.apply(t, rep.ID, "open-443"); err != nil {
		t.Fatal(err)
	}
	if !scratchAtStart {
		t.Fatal("Apply let go of the Preflight lock before the Updater started")
	}
	if !r.svc.pfRun.TryLock() {
		t.Fatal("Apply kept the Preflight lock")
	}
	r.svc.pfRun.Unlock()
}

func TestRunInfoCarriesTheOutcome(t *testing.T) {
	r := newApplyRig(t)
	dir := RunDir(r.data, "20261101T120000Z")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteStatus(dir, RunStatus{Run: "20261101T120000Z", State: StateRollbackFailed, Error: "not healthy", RollbackError: "disk full"}, r.svc.d.Now()); err != nil {
		t.Fatal(err)
	}
	_, status := serve(t, r.svc, http.MethodGet, "/api/panel-update/status", "")
	if status.Run == nil || status.Run.State != StateRollbackFailed || !status.Run.Terminal ||
		status.Run.Error != "not healthy" || status.Run.RollbackError != "disk full" {
		t.Fatalf("run = %+v", status.Run)
	}
}

func TestPreviousImageIsTheLastSucceededRunsFromImage(t *testing.T) {
	r := newApplyRig(t)
	ctx := context.Background()
	if got, err := r.svc.PreviousImage(ctx); got != "" || err != nil {
		t.Fatalf("before any run: %q, %v", got, err)
	}
	record := func(run, state, image string) {
		t.Helper()
		if err := r.svc.d.Store.RecordRun(ctx, RunStatus{Run: run, State: state, FromImage: image, ToVersion: "1"}, r.svc.d.Now()); err != nil {
			t.Fatal(err)
		}
	}
	record("20261101T120000Z", StateSucceeded, "sha256:one")
	record("20261102T120000Z", StateRolledBack, "sha256:two")
	if got, _ := r.svc.PreviousImage(ctx); got != "sha256:one" {
		t.Fatalf("got %q: a rolled-back run's image is not the previous one", got)
	}
	record("20261103T120000Z", StateSucceeded, "sha256:three")
	if got, _ := r.svc.PreviousImage(ctx); got != "sha256:three" {
		t.Fatalf("got %q", got)
	}
}
