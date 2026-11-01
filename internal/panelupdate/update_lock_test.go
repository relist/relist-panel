package panelupdate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeQueue struct {
	mu     sync.Mutex
	events []string
	idle   chan struct{} // WaitIdle blocks on it when set
}

func (q *fakeQueue) add(e string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.events = append(q.events, e)
}
func (q *fakeQueue) Hold()    { q.add("hold") }
func (q *fakeQueue) Release() { q.add("release") }
func (q *fakeQueue) CancelAllQueued(context.Context, string) (int, error) {
	q.add("cancel")
	return 0, nil
}
func (q *fakeQueue) WaitIdle(ctx context.Context) error {
	q.add("wait-idle")
	if q.idle == nil {
		return nil
	}
	select {
	case <-q.idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (q *fakeQueue) list() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Clone(q.events)
}

type fakeScans struct{ waited int }

func (f *fakeScans) WaitIdle(context.Context) error { f.waited++; return nil }

type fakeUpdater struct {
	mu       sync.Mutex
	state    map[string]UpdaterState
	removed  []string
	err      error
	started  []UpdaterSpec
	startErr error
	onStart  func(UpdaterSpec)
}

func (f *fakeUpdater) ListUpdaters(context.Context) ([]UpdaterListing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []UpdaterListing
	for name, st := range f.state {
		out = append(out, UpdaterListing{Name: name, Running: st.Running})
	}
	return out, f.err
}

func (f *fakeUpdater) StartUpdater(_ context.Context, spec UpdaterSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return f.startErr
	}
	f.started = append(f.started, spec)
	f.state[spec.Name] = UpdaterState{Exists: true, Running: true}
	if f.onStart != nil {
		f.onStart(spec)
	}
	return nil
}

func (f *fakeUpdater) UpdaterState(_ context.Context, name string) (UpdaterState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state[name], f.err
}

func (f *fakeUpdater) RemoveUpdater(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, name)
	delete(f.state, name)
	return nil
}

type lockRig struct {
	s       *Service
	queue   *fakeQueue
	scans   *fakeScans
	updater *fakeUpdater
	files   *fakeHelper
	dir     string
	now     time.Time
	migr    bool
}

func newLockRig(t *testing.T) *lockRig {
	t.Helper()
	r := &lockRig{
		queue: &fakeQueue{}, scans: &fakeScans{}, updater: &fakeUpdater{state: map[string]UpdaterState{}},
		dir: t.TempDir(), now: time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC),
		files: &fakeHelper{files: map[string]string{
			"/srv/relist/docker-compose.yml": "services: {}\n",
			"/srv/relist/.env":               "RELIST_DOMAIN=x\n",
		}},
	}
	r.s = New(Deps{
		Store: NewStore(newDB(t)), DataDir: r.dir, Queue: r.queue, Scans: r.scans, Updater: r.updater,
		Helper: r.files, Now: func() time.Time { return r.now },
		MigrationLocked: func(context.Context) (bool, error) { return r.migr, nil },
	})
	return r
}

func (r *lockRig) install() Install {
	return Install{Project: "relist-panel", Dir: "/srv/relist", ConfigFiles: []string{"/srv/relist/docker-compose.yml"}, Image: "img"}
}

func (r *lockRig) writeRun(t *testing.T, id, state string) {
	t.Helper()
	dir := RunDir(r.dir, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteStatus(dir, RunStatus{Run: id, State: state, FromVersion: "1.3.0", ToVersion: "1.4.0", StartedAt: r.now.Unix()}, r.now); err != nil {
		t.Fatal(err)
	}
}

func (r *lockRig) rows(t *testing.T) []string {
	t.Helper()
	rs, err := r.s.d.Store.db.Query(`SELECT run_id || ':' || state || ':' || error FROM panel_updates ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	var out []string
	for rs.Next() {
		var v string
		if err := rs.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func TestStartLockedHoldsQueueForNonTerminalRun(t *testing.T) {
	for _, state := range []string{StateQuiescing, StateWritingFiles, StateWaitingHealth, StateRollingBack} {
		r := newLockRig(t)
		r.writeRun(t, "20261101T110000Z", state)
		if err := r.s.StartLocked(t.Context()); err != nil {
			t.Fatal(err)
		}
		if !r.s.Locked() || !slices.Contains(r.queue.list(), "hold") || !slices.Contains(r.queue.list(), "cancel") {
			t.Fatalf("%s: locked = %v, events = %v", state, r.s.Locked(), r.queue.list())
		}
	}
}

func TestStartLockedLeavesFinishedRunsAlone(t *testing.T) {
	for _, state := range []string{StateSucceeded, StateRolledBack, StateRollbackFailed, StateInterrupted} {
		r := newLockRig(t)
		r.writeRun(t, "20261101T110000Z", state)
		if err := r.s.StartLocked(t.Context()); err != nil {
			t.Fatal(err)
		}
		if r.s.Locked() || len(r.queue.list()) != 0 {
			t.Fatalf("%s: locked = %v, events = %v", state, r.s.Locked(), r.queue.list())
		}
	}
	r := newLockRig(t)
	if err := r.s.StartLocked(t.Context()); err != nil || r.s.Locked() {
		t.Fatalf("no run: %v locked=%v", err, r.s.Locked())
	}
}

func TestStartLockedUsesTheNewestRun(t *testing.T) {
	r := newLockRig(t)
	r.writeRun(t, "20261101T100000Z", StateWaitingHealth)
	r.writeRun(t, "20261101T110000Z", StateSucceeded)
	if err := r.s.StartLocked(t.Context()); err != nil || r.s.Locked() {
		t.Fatalf("err = %v locked = %v", err, r.s.Locked())
	}
}

func TestMiddlewareWhileLocked(t *testing.T) {
	r := newLockRig(t)
	reached := 0
	h := r.s.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached++ }))
	do := func(method, path string) int {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader("")))
		if rec.Code == 503 && !strings.Contains(rec.Body.String(), updatingMessage) {
			t.Fatalf("503 body = %q", rec.Body.String())
		}
		return rec.Code
	}
	if do("POST", "/api/composes") != 200 {
		t.Fatal("unlocked write refused")
	}
	if err := r.s.lock(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		method, path string
		code         int
	}{
		{"POST", "/api/composes", 503},
		{"PUT", "/api/composes/1", 503},
		{"DELETE", "/api/composes/1", 503},
		{"GET", "/api/composes", 503},
		{"POST", "/api/panel-update/check", 503},
		{"POST", "/api/migration/start", 503},
		{"GET", "/healthz", 200},
		{"POST", "/api/auth/login", 200},
		{"GET", "/api/auth/status", 200},
		{"POST", "/api/auth/logout", 200},
		{"POST", "/api/auth/password", 503},
		{"POST", "/api/auth/2fa/setup", 503},
		{"POST", "/api/auth/2fa/enable", 503},
		{"POST", "/api/auth/2fa/disable", 503},
		{"GET", "/api/auth/account", 503},
		{"GET", "/api/panel-update/status", 200},
		{"GET", "/", 200},
		{"GET", "/assets/app.js", 200},
	} {
		if got := do(c.method, c.path); got != c.code {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, got, c.code)
		}
	}
}

func TestDailyCheckSkipsWriteWhileLocked(t *testing.T) {
	s, rel := newService(t, "1.3.0")
	rel.publish(manifestJSON("1.4.0"))
	s.d.Queue = &fakeQueue{}
	s.d.DataDir = t.TempDir()
	if err := s.lock(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckNow(t.Context()); !errors.Is(err, ErrBusy) {
		t.Fatalf("CheckNow = %v, want ErrBusy", err)
	}
	s.tick(t.Context())
	if rel.hits.Load() != 0 {
		t.Fatalf("the check fetched %d times while locked", rel.hits.Load())
	}
	if st, _ := s.d.Store.Settings(t.Context()); st.LastCheckAt != nil {
		t.Fatal("a check was recorded while locked")
	}
}

func TestQuiesceAndBackup(t *testing.T) {
	r := newLockRig(t)
	r.writeRun(t, "20261031T100000Z", StateSucceeded) // the previous run's backup
	run, err := r.s.QuiesceAndBackup(t.Context(), r.install(), "1.3.0", "1.4.0")
	if err != nil {
		t.Fatal(err)
	}
	if run.ID != "20261101T120000Z" || !r.s.Locked() {
		t.Fatalf("run = %+v locked = %v", run, r.s.Locked())
	}
	if got := r.queue.list(); !slices.Equal(got, []string{"hold", "cancel", "wait-idle"}) || r.scans.waited != 1 {
		t.Fatalf("events = %v scans waited = %d", got, r.scans.waited)
	}
	st, err := ReadStatus(run.Dir)
	if err != nil || st.State != StateBackingUp || st.FromVersion != "1.3.0" || st.ToVersion != "1.4.0" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	for _, f := range []string{"panel.db", "config-0.yml", "env", "backup.json", "status.json"} {
		if _, err := os.Stat(filepath.Join(run.Dir, f)); err != nil {
			t.Errorf("backup lacks %s: %v", f, err)
		}
	}
	b, err := ReadBackup(run.Dir)
	if err != nil || len(b.Config) != 1 || b.Config[0].Path != "/srv/relist/docker-compose.yml" || b.Env == nil || b.Env.Path != "/srv/relist/.env" {
		t.Fatalf("backup = %+v, %v", b, err)
	}
	if data, _ := os.ReadFile(filepath.Join(run.Dir, "env")); string(data) != "RELIST_DOMAIN=x\n" {
		t.Fatalf("env copy = %q", data)
	}
	if _, err := os.Stat(RunDir(r.dir, "20261031T100000Z")); !os.IsNotExist(err) {
		t.Fatalf("the previous run's directory stays: %v", err)
	}
	// Until MarkStarted the watcher leaves the run alone, though it has no Updater.
	r.s.watchOnce(t.Context())
	if st, _ := ReadStatus(run.Dir); st.State != StateBackingUp || !r.s.Locked() {
		t.Fatalf("watcher touched a guarded run: %+v", st)
	}
	// A second run is refused while the lock is on.
	if _, err := r.s.QuiesceAndBackup(t.Context(), r.install(), "1.3.0", "1.4.0"); !errors.Is(err, ErrUpdateRunning) {
		t.Fatalf("second run = %v", err)
	}
}

func TestQuiesceKeepsPreviousBackupWhenThisOneFails(t *testing.T) {
	r := newLockRig(t)
	r.writeRun(t, "20261031T100000Z", StateSucceeded)
	delete(r.files.files, "/srv/relist/docker-compose.yml") // the Install file cannot be read
	_, err := r.s.QuiesceAndBackup(t.Context(), r.install(), "1.3.0", "1.4.0")
	if err == nil {
		t.Fatal("want an error")
	}
	if _, serr := os.Stat(RunDir(r.dir, "20261031T100000Z")); serr != nil {
		t.Fatalf("previous backup removed: %v", serr)
	}
	st, ok, _ := NewestRun(r.dir)
	if !ok || st.State != StateInterrupted || st.Error == "" || r.s.Locked() {
		t.Fatalf("newest = %+v locked = %v", st, r.s.Locked())
	}
	if ev := r.queue.list(); ev[len(ev)-1] != "release" {
		t.Fatalf("queue not released: %v", ev)
	}
	if rows := r.rows(t); len(rows) != 1 || !strings.HasPrefix(rows[0], "20261101T120000Z:interrupted:") {
		t.Fatalf("rows = %v", rows)
	}
}

func TestQuiesceRefusedWhileMigrationLocked(t *testing.T) {
	r := newLockRig(t)
	r.migr = true
	if _, err := r.s.QuiesceAndBackup(t.Context(), r.install(), "1.3.0", "1.4.0"); !errors.Is(err, ErrMigrationLocked) {
		t.Fatalf("err = %v", err)
	}
	if r.s.Locked() || len(r.queue.list()) != 0 {
		t.Fatal("lock taken")
	}
}

func TestQuiesceWaitsForIdleQueue(t *testing.T) {
	r := newLockRig(t)
	r.queue.idle = make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := r.s.QuiesceAndBackup(ctx, r.install(), "1.3.0", "1.4.0")
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("returned before the queue was idle: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	if !r.s.Locked() {
		t.Fatal("lock off while waiting")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if r.s.Locked() {
		t.Fatal("lock left on after a failed quiesce")
	}
}

func TestPanelUpdateGateRefusesWhileLocked(t *testing.T) {
	r := newLockRig(t)
	if err := r.s.lock(t.Context()); err != nil {
		t.Fatal(err)
	}
	var rep Report
	if _, err := r.s.gate(t.Context(), &rep); err == nil || !rep.Gate.UpdateRunning {
		t.Fatalf("gate = %v %+v", err, rep.Gate)
	}
}

func TestUnlockLeavesMigrationHold(t *testing.T) {
	r := newLockRig(t)
	if err := r.s.lock(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.migr = true
	r.s.unlock(t.Context())
	if slices.Contains(r.queue.list(), "release") || r.s.Locked() {
		t.Fatalf("events = %v locked = %v", r.queue.list(), r.s.Locked())
	}
}

func TestWatcherTerminalRunReleasesLockAndRemovesUpdater(t *testing.T) {
	for _, state := range []string{StateSucceeded, StateRolledBack, StateRollbackFailed} {
		r := newLockRig(t)
		r.writeRun(t, "20261101T110000Z", StateWaitingHealth)
		if err := r.s.StartLocked(t.Context()); err != nil {
			t.Fatal(err)
		}
		name := UpdaterName("20261101T110000Z")
		r.updater.state[name] = UpdaterState{Exists: true, Running: true}
		if r.s.watchOnce(t.Context()) || !r.s.Locked() {
			t.Fatal("a running Updater must keep the lock on")
		}
		r.writeRun(t, "20261101T110000Z", state)
		r.updater.state[name] = UpdaterState{Exists: true}
		if !r.s.watchOnce(t.Context()) {
			t.Fatalf("%s: not settled", state)
		}
		if r.s.Locked() || !slices.Equal(r.updater.removed, []string{name}) || r.queue.list()[len(r.queue.list())-1] != "release" {
			t.Fatalf("%s: locked = %v removed = %v events = %v", state, r.s.Locked(), r.updater.removed, r.queue.list())
		}
		if rows := r.rows(t); len(rows) != 1 || !strings.HasPrefix(rows[0], "20261101T110000Z:"+state) {
			t.Fatalf("%s: rows = %v", state, rows)
		}
		r.s.watchOnce(t.Context()) // settled: nothing more to do
		if len(r.updater.removed) != 1 || len(r.rows(t)) != 1 {
			t.Fatalf("%s: settled run handled twice", state)
		}
	}
}

func TestWatcherWaitsForAnUpdaterThatIsStillExiting(t *testing.T) {
	r := newLockRig(t)
	r.writeRun(t, "20261101T110000Z", StateSucceeded)
	name := UpdaterName("20261101T110000Z")
	r.updater.state[name] = UpdaterState{Exists: true, Running: true}
	if r.s.watchOnce(t.Context()) || len(r.updater.removed) != 0 {
		t.Fatal("removed a running Updater")
	}
	r.updater.state[name] = UpdaterState{Exists: true}
	if !r.s.watchOnce(t.Context()) || len(r.updater.removed) != 1 {
		t.Fatalf("removed = %v", r.updater.removed)
	}
}

func TestWatcherMarksDeadUpdaterInterrupted(t *testing.T) {
	for name, state := range map[string]UpdaterState{
		"exited": {Exists: true},
		"gone":   {},
	} {
		t.Run(name, func(t *testing.T) {
			r := newLockRig(t)
			r.writeRun(t, "20261101T110000Z", StateComposeUp)
			if err := r.s.StartLocked(t.Context()); err != nil {
				t.Fatal(err)
			}
			r.updater.state[UpdaterName("20261101T110000Z")] = state
			if !r.s.watchOnce(t.Context()) {
				t.Fatal("not settled")
			}
			st, _, _ := NewestRun(r.dir)
			if st.State != StateInterrupted || !strings.Contains(st.Error, StateComposeUp) {
				t.Fatalf("status = %+v", st)
			}
			if r.s.Locked() {
				t.Fatal("lock still on")
			}
			if rows := r.rows(t); len(rows) != 1 || !strings.Contains(rows[0], ":interrupted:") {
				t.Fatalf("rows = %v", rows)
			}
		})
	}
}

func TestWatcherKeepsRunOnInspectError(t *testing.T) {
	r := newLockRig(t)
	r.writeRun(t, "20261101T110000Z", StateComposeUp)
	_ = r.s.StartLocked(t.Context())
	r.updater.err = errors.New("socket down")
	r.s.watchOnce(t.Context())
	if st, _, _ := NewestRun(r.dir); st.State != StateComposeUp || !r.s.Locked() {
		t.Fatalf("status = %+v locked = %v", st, r.s.Locked())
	}
}

func TestMarkStartedHandsRunToWatcher(t *testing.T) {
	r := newLockRig(t)
	run, err := r.s.QuiesceAndBackup(t.Context(), r.install(), "1.3.0", "1.4.0")
	if err != nil {
		t.Fatal(err)
	}
	r.s.MarkStarted(run)
	r.s.watchOnce(t.Context()) // no Updater Container: the run is dead
	if st, _, _ := NewestRun(r.dir); st.State != StateInterrupted || r.s.Locked() {
		t.Fatalf("status = %+v locked = %v", st, r.s.Locked())
	}
}

func TestStatusReportsNewestRun(t *testing.T) {
	r := newLockRig(t)
	run, err := r.s.QuiesceAndBackup(t.Context(), r.install(), "1.3.0", "1.4.0")
	if err != nil {
		t.Fatal(err)
	}
	rec, st := serve(t, r.s, "GET", "/api/panel-update/status", "")
	if rec.Code != 200 || !st.Locked || st.Run == nil || st.Run.ID != run.ID || st.Run.State != StateBackingUp || st.Run.BackupPath != run.Dir || st.Run.Terminal {
		t.Fatalf("status = %d %+v run=%+v", rec.Code, st, st.Run)
	}
}

func TestStatusFileIsAtomicAndRoundTrips(t *testing.T) {
	dir := t.TempDir()
	want := RunStatus{Run: "20261101T110000Z", State: StateComposeUp, FromVersion: "a", ToVersion: "b", StartedAt: 5}
	if err := WriteStatus(dir, want, time.Unix(9, 0)); err != nil {
		t.Fatal(err)
	}
	got, err := ReadStatus(dir)
	want.UpdatedAt = 9
	if err != nil || got != want {
		t.Fatalf("got %+v, %v", got, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".status-*")); len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
	if _, err := ReadStatus(t.TempDir()); !os.IsNotExist(err) {
		t.Fatalf("missing status.json: %v", err)
	}
}

func TestQuiesceKeepsARollbackFailedRunUntilALaterOneSucceeded(t *testing.T) {
	r := newLockRig(t)
	r.writeRun(t, "20261029T100000Z", StateRollbackFailed) // superseded by the next failure
	r.writeRun(t, "20261030T100000Z", StateRollbackFailed)
	r.writeRun(t, "20261031T100000Z", StateRolledBack)
	run, err := r.s.QuiesceAndBackup(t.Context(), r.install(), "1.3.0", "1.4.0")
	if err != nil {
		t.Fatal(err)
	}
	exists := func(id string) bool {
		_, err := os.Stat(RunDir(r.dir, id))
		return err == nil
	}
	if exists("20261029T100000Z") || !exists("20261030T100000Z") || exists("20261031T100000Z") || !exists(run.ID) {
		t.Fatal("only the newest rollback-failed run and the new one may stay")
	}
	// The new run succeeds; the next backup clears the failed one.
	r.s.AbortRun(run, "") // lock off
	if err := r.s.SetRunState(run, StateSucceeded, ""); err != nil {
		t.Fatal(err)
	}
	r.now = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	next, err := r.s.QuiesceAndBackup(t.Context(), r.install(), "1.4.0", "1.5.0")
	if err != nil {
		t.Fatal(err)
	}
	if exists("20261030T100000Z") || exists(run.ID) || !exists(next.ID) {
		t.Fatal("a later success must release the rollback-failed run")
	}
}
