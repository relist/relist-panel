package panelupdate

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// fakeHost is the host the Updater drives. Containers answers by the number
// of `up` runs so far, so a test says what each `up` left behind.
type fakeHost struct {
	mu    sync.Mutex
	calls []string
	ups   int
	// upErr is the error of the n-th `up` (1-based).
	upErr map[int]error
	// panelImage is the image of the panel Container after the n-th `up`.
	panelImage map[int]string
	// healthy says whether the n-th `up` leaves a panel that answers.
	healthy map[int]bool
	// onUp runs inside the n-th `up`, as the new Containers would.
	onUp func(n int)
	// hangUp makes the n-th `up` wait for its context, like a hung pull.
	hangUp map[int]bool
	// panelDown makes the panel Container listed as exited before the first up.
	panelDown bool
	// updaters is what UpdaterState answers by name.
	updaters map[string]UpdaterState
	states   []string // status.json state seen at each call
	dir      string
}

func (h *fakeHost) note(call string) {
	h.calls = append(h.calls, call)
	if st, err := ReadStatus(h.dir); err == nil {
		h.states = append(h.states, st.State)
	}
}

func (h *fakeHost) ComposeUp(ctx context.Context, _ Install) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ups++
	h.note("up")
	if h.hangUp[h.ups] {
		h.mu.Unlock()
		<-ctx.Done()
		h.mu.Lock()
		return ctx.Err()
	}
	if h.onUp != nil {
		h.onUp(h.ups)
	}
	return h.upErr[h.ups]
}

func (h *fakeHost) ComposeStop(_ context.Context, _ Install, service string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.note("stop " + service)
	return nil
}

func (h *fakeHost) Containers(_ context.Context, _, service string) ([]ContainerInfo, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if service == "traefik" {
		return []ContainerInfo{{ID: "t", Running: true, IPs: map[string]string{"relist-network": "172.20.0.2"}}}, nil
	}
	h.note("containers panel")
	if h.ups == 0 {
		return []ContainerInfo{{ID: "p", ImageID: "sha256:old", Running: !h.panelDown}}, nil
	}
	// An old Container still being replaced is listed next to the new one.
	return []ContainerInfo{
		{ID: "gone", ImageID: "sha256:zzz", Running: false},
		{ID: "p", ImageID: h.panelImage[h.ups], Running: true},
	}, nil
}

func (h *fakeHost) Healthz(_ context.Context, ip, domain string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ip != "172.20.0.2" || domain != "panel.example.com" {
		return errors.New("wrong address: " + ip + " " + domain)
	}
	h.note("healthz")
	if !h.healthy[h.ups] {
		return errors.New("/healthz answered 502 Bad Gateway")
	}
	return nil
}

func (h *fakeHost) UpdaterState(_ context.Context, name string) (UpdaterState, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.updaters[name], nil
}

func (h *fakeHost) has(call string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, c := range h.calls {
		if c == call {
			return true
		}
	}
	return false
}

// fakeChecker answers the Updater's re-check with a fixed result.
type fakeChecker struct {
	report *Report
	cand   *Candidate
	err    error
	runs   int
}

func (f *fakeChecker) Check(context.Context, Install) (*Report, *Candidate, error) {
	f.runs++
	return f.report, f.cand, f.err
}

type updaterRig struct {
	u       *Updater
	host    *fakeHost
	checker *fakeChecker
	in      Install
	data    string
	install string
	run     string
	dir     string
	report  *Report
	// acks are written to panel.json, one per Sleep call, as the panel would.
	acks []PanelAck
	// sleeps counts Sleep calls.
	sleeps int
}

const (
	oldInstall = "services: {panel: {image: old}}\n"
	newInstall = "services: {panel: {image: new}}\n"
	oldEnv     = "RELIST_DOMAIN=panel.example.com\n"
	newEnv     = "RELIST_DOMAIN=panel.example.com\nRELIST_PANEL_IMAGE=new\n"
	rigRun     = "20261101T120000Z"
	rigReport  = "0123456789abcdef0123456789abcdef"
)

// newUpdaterRig lays out an Install directory holding the old files, a data
// directory with a real panel.db and a saved preflight report, and an Updater
// whose re-check finds the same thing.
func newUpdaterRig(t *testing.T, withEnv bool) *updaterRig {
	t.Helper()
	r := &updaterRig{data: t.TempDir(), install: t.TempDir(), run: rigRun}
	r.dir = RunDir(r.data, r.run)
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	compose := filepath.Join(r.install, "docker-compose.yml")
	override := filepath.Join(r.install, "docker-compose.override.yml")
	write(compose, oldInstall)
	write(override, "services: {}\n")
	if withEnv {
		write(filepath.Join(r.install, ".env"), oldEnv)
	}
	r.writeDB(t, "database before the update")
	r.in = Install{Project: "relist-panel", Dir: r.install, ConfigFiles: []string{compose, override}}
	files, err := ReadLocalFiles(r.in)
	if err != nil {
		t.Fatal(err)
	}
	r.report = &Report{
		ID: rigReport, FromVersion: "1.3.0", ToVersion: "1.4.0", Ready: true, Fingerprint: "fp1",
		ManualSteps: []ManualStep{}, Invariants: []InvariantResult{}, Recreated: []string{},
	}
	if err := WriteReport(r.data, r.report); err != nil {
		t.Fatal(err)
	}
	r.checker = &fakeChecker{
		report: &Report{ID: "fresh", FromVersion: "1.3.0", ToVersion: "1.4.0", Ready: true, Fingerprint: "fp1"},
		cand:   &Candidate{In: r.in, Files: files, Install: []byte(newInstall), Env: []byte(newEnv), ImageID: "sha256:new"},
	}
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	r.host = &fakeHost{
		dir: r.dir, upErr: map[int]error{}, updaters: map[string]UpdaterState{},
		panelImage: map[int]string{1: "sha256:new", 2: "sha256:old"},
		healthy:    map[int]bool{1: true, 2: true},
	}
	r.u = &Updater{
		DataDir: r.data, Host: r.host, Checker: r.checker, Domain: "panel.example.com",
		Now:           func() time.Time { return now },
		HealthTimeout: 300 * time.Millisecond, HealthEvery: 5 * time.Millisecond,
		Sleep: func(ctx context.Context, _ time.Duration) error {
			r.sleeps++
			if len(r.acks) > 0 {
				if err := WriteAck(r.dir, r.acks[0]); err != nil {
					t.Error(err)
				}
				r.acks = r.acks[1:]
			}
			time.Sleep(time.Millisecond)
			return ctx.Err()
		},
	}
	// The panel answers ready at once unless a test says otherwise.
	r.acks = []PanelAck{{Run: r.run, State: AckReady, Heartbeat: 1}}
	return r
}

func (r *updaterRig) writeDB(t *testing.T, marker string) {
	t.Helper()
	path := filepath.Join(r.data, "panel.db")
	_ = os.Remove(path)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE marker (v TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO marker VALUES (?)`, marker); err != nil {
		t.Fatal(err)
	}
}

func (r *updaterRig) marker(t *testing.T) string {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(r.data, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v string
	if err := db.QueryRow(`SELECT v FROM marker`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func (r *updaterRig) read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (r *updaterRig) compose() string { return filepath.Join(r.install, "docker-compose.yml") }
func (r *updaterRig) env() string     { return filepath.Join(r.install, ".env") }

func (r *updaterRig) apply(t *testing.T) RunStatus {
	t.Helper()
	st, err := r.u.Apply(context.Background(), ApplyArgs{Run: r.run, Report: rigReport, In: r.in})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return st
}

func TestApplySucceeds(t *testing.T) {
	r := newUpdaterRig(t, true)
	st := r.apply(t)
	if st.State != StateSucceeded || st.Error != "" || st.FromVersion != "1.3.0" || st.ToVersion != "1.4.0" {
		t.Fatalf("status = %+v", st)
	}
	if got := r.read(t, r.compose()); got != newInstall {
		t.Fatalf("Install file = %q", got)
	}
	if got := r.read(t, r.env()); got != newEnv {
		t.Fatalf(".env = %q", got)
	}
	if got := r.marker(t); got != "database before the update" {
		t.Fatalf("panel.db = %q: a successful update must not restore it", got)
	}
	if on, _ := ReadStatus(r.dir); on.State != StateSucceeded {
		t.Fatalf("status.json = %+v", on)
	}
	// The backup holds a real copy of panel.db and every file.
	if _, err := os.Stat(filepath.Join(r.dir, backupDB)); err != nil {
		t.Fatal(err)
	}
	// compose-up while `up` runs, waiting-health while the health is polled.
	if r.host.states[0] != StateComposeUp || r.host.states[1] != StateWaitingHealth {
		t.Fatalf("states = %v", r.host.states)
	}
	if strings.Join(r.host.calls, ",") != "containers panel,up,containers panel,healthz" {
		t.Fatalf("calls = %v", r.host.calls)
	}
}

func TestApplyPanelAnswersAndSucceeds(t *testing.T) {
	r := newUpdaterRig(t, true)
	r.acks = []PanelAck{
		{Run: r.run, State: AckWaiting, WaitingOn: "Deployment api #3", Heartbeat: 1},
		{Run: r.run, State: AckWaiting, WaitingOn: "Deployment api #3", Heartbeat: 2},
		{Run: r.run, State: AckReady, Heartbeat: 3},
	}
	var seen []string
	inner := r.u.Sleep
	r.u.Sleep = func(ctx context.Context, d time.Duration) error {
		if st, err := ReadStatus(r.dir); err == nil && st.WaitingOn != "" {
			seen = append(seen, st.WaitingOn)
		}
		return inner(ctx, d)
	}
	st := r.apply(t)
	if st.State != StateSucceeded || st.WaitingOn != "" {
		t.Fatalf("status = %+v", st)
	}
	if len(seen) == 0 || seen[0] != "Deployment api #3" {
		t.Fatalf("status.json never showed what the run waits on: %v", seen)
	}
	if r.host.has("stop panel") {
		t.Fatal("stopped a panel that answered")
	}
	if got := r.read(t, filepath.Join(r.dir, backupMeta)); got == "" {
		t.Fatal("no backup")
	}
}

func TestApplyIgnoresAckOfOtherRun(t *testing.T) {
	r := newUpdaterRig(t, true)
	// An earlier run's answer, ready and with moving heartbeats.
	r.acks = nil
	for i := 0; i < 20; i++ {
		r.acks = append(r.acks, PanelAck{Run: "20250101T000000Z", State: AckReady, Heartbeat: int64(i)})
	}
	st := r.apply(t)
	if !r.host.has("stop panel") || st.Note != PanelNoAnswerNote || st.State != StateSucceeded {
		t.Fatalf("status = %+v calls %v", st, r.host.calls)
	}
}

func TestApplyHeartbeatNotClock(t *testing.T) {
	r := newUpdaterRig(t, true)
	// More waiting acks than the answer window holds polls: the heartbeat
	// counter moves each time while the file looks an hour old.
	r.acks = nil
	for i := 1; i <= 15; i++ {
		r.acks = append(r.acks, PanelAck{Run: r.run, State: AckWaiting, WaitingOn: "a vulnerability scan", Heartbeat: int64(i)})
	}
	r.acks = append(r.acks, PanelAck{Run: r.run, State: AckReady, Heartbeat: 16})
	inner := r.u.Sleep
	r.u.Sleep = func(ctx context.Context, d time.Duration) error {
		err := inner(ctx, d)
		old := time.Now().Add(-time.Hour)
		_ = os.Chtimes(filepath.Join(r.dir, ackFile), old, old)
		return err
	}
	st := r.apply(t)
	if st.State != StateSucceeded || st.Note != "" || r.host.has("stop panel") {
		t.Fatalf("status = %+v calls %v", st, r.host.calls)
	}
}

func TestApplyStalledHeartbeatPanelRunningKeepsWaiting(t *testing.T) {
	r := newUpdaterRig(t, true)
	r.acks = nil
	for i := 0; i < 15; i++ { // beyond the 10 polls of the window
		r.acks = append(r.acks, PanelAck{Run: r.run, State: AckWaiting, WaitingOn: "Deployment api #3", Heartbeat: 1})
	}
	r.acks = append(r.acks, PanelAck{Run: r.run, State: AckReady, Heartbeat: 2})
	st := r.apply(t)
	if st.State != StateSucceeded || st.Note != "" || r.host.has("stop panel") {
		t.Fatalf("status = %+v calls %v", st, r.host.calls)
	}
}

func TestApplyStalledHeartbeatPanelGone(t *testing.T) {
	r := newUpdaterRig(t, true)
	r.host.panelDown = true
	r.acks = []PanelAck{{Run: r.run, State: AckWaiting, WaitingOn: "Deployment api #3", Heartbeat: 1}}
	st := r.apply(t)
	if st.State != StateSucceeded || st.Note != PanelNoAnswerNote {
		t.Fatalf("status = %+v", st)
	}
	if r.host.has("stop panel") {
		t.Fatal("stopped a panel that was already gone")
	}
}

func TestApplyRefusedIsAborted(t *testing.T) {
	r := newUpdaterRig(t, true)
	r.acks = []PanelAck{{Run: r.run, State: AckRefused, Reason: "a server Migration holds the lock", Heartbeat: 1}}
	st := r.apply(t)
	if st.State != StateAborted || st.Error != "a server Migration holds the lock" {
		t.Fatalf("status = %+v", st)
	}
	if got := r.read(t, r.compose()); got != oldInstall || r.read(t, r.env()) != oldEnv {
		t.Fatal("the Install was written")
	}
	if r.host.ups != 0 {
		t.Fatal("ran up")
	}
	if _, err := os.Stat(filepath.Join(r.dir, backupDB)); err == nil {
		t.Fatal("took a backup of a refused run")
	}
}

func TestApplyNoAnswerStopsPanel(t *testing.T) {
	r := newUpdaterRig(t, true)
	r.acks = nil
	st := r.apply(t)
	if st.State != StateSucceeded || st.Note != PanelNoAnswerNote || !r.host.has("stop panel") {
		t.Fatalf("status = %+v calls %v", st, r.host.calls)
	}
	// At least the answer window passed in poll intervals.
	if r.sleeps < 10 {
		t.Fatalf("slept %d times", r.sleeps)
	}
	if on, _ := ReadStatus(r.dir); on.Note != PanelNoAnswerNote {
		t.Fatalf("status.json = %+v", on)
	}
}

func TestApplySignalBeforeBackupAborts(t *testing.T) {
	r := newUpdaterRig(t, true)
	sig := make(chan os.Signal, 1)
	sig <- os.Interrupt
	r.u.Signals = sig
	st := r.apply(t)
	if st.State != StateAborted || st.Error != "cancelled" {
		t.Fatalf("status = %+v", st)
	}
	if r.read(t, r.compose()) != oldInstall || r.host.ups != 0 {
		t.Fatal("the host was changed")
	}
	if _, err := os.Stat(filepath.Join(r.dir, backupDB)); err == nil {
		t.Fatal("took a backup after the cancel")
	}
}

func TestApplySignalAfterBackupIgnored(t *testing.T) {
	r := newUpdaterRig(t, true)
	sig := make(chan os.Signal, 1)
	r.u.Signals = sig
	r.host.onUp = func(int) { sig <- os.Interrupt }
	st := r.apply(t)
	if st.State != StateSucceeded {
		t.Fatalf("status = %+v", st)
	}
}

func TestApplyRecheckMismatchAborts(t *testing.T) {
	r := newUpdaterRig(t, true)
	r.checker.report.Fingerprint = "fp2"
	st := r.apply(t)
	if st.State != StateAborted || !strings.Contains(st.Error, "changed since the preflight") {
		t.Fatalf("status = %+v", st)
	}
	on, err := ReadStatus(r.dir)
	if err != nil || on.State != StateAborted {
		t.Fatalf("status.json = %+v, %v", on, err)
	}
	entries, _ := os.ReadDir(r.dir)
	if len(entries) != 1 || len(r.host.calls) != 0 {
		t.Fatalf("a refused re-check wrote more than status.json: %v %v", entries, r.host.calls)
	}
}

func TestApplyRecheckNotReadyAborts(t *testing.T) {
	r := newUpdaterRig(t, true)
	r.checker.report.Ready = false
	r.checker.report.Failed = &Failure{Step: StepConfig, Message: "boom"}
	st := r.apply(t)
	if st.State != StateAborted || !strings.Contains(st.Error, "boom") {
		t.Fatalf("status = %+v", st)
	}
}

func TestApplyStepNotDoneAborts(t *testing.T) {
	r := newUpdaterRig(t, true)
	r.report.ManualSteps = []ManualStep{{ID: "open-443", Text: "Open port 443."}}
	if err := WriteReport(r.data, r.report); err != nil {
		t.Fatal(err)
	}
	st := r.apply(t)
	if st.State != StateAborted || !strings.Contains(st.Error, "open-443") {
		t.Fatalf("status = %+v", st)
	}
	if r.checker.runs != 0 {
		t.Fatal("re-checked before the manual steps were confirmed")
	}
	// Ticked, it goes on.
	r2 := newUpdaterRig(t, true)
	r2.report.ManualSteps = []ManualStep{{ID: "open-443", Text: "Open port 443."}}
	if err := WriteReport(r2.data, r2.report); err != nil {
		t.Fatal(err)
	}
	st2, err := r2.u.Apply(context.Background(), ApplyArgs{Run: r2.run, Report: rigReport, In: r2.in, StepsDone: []string{"open-443"}})
	if err != nil || st2.State != StateSucceeded {
		t.Fatalf("status = %+v, %v", st2, err)
	}
}

func TestApplyMissingReportAborts(t *testing.T) {
	r := newUpdaterRig(t, true)
	st, err := r.u.Apply(context.Background(), ApplyArgs{Run: r.run, Report: "ffffffffffffffffffffffffffffffff", In: r.in})
	if err != nil || st.State != StateAborted {
		t.Fatalf("status = %+v, %v", st, err)
	}
}

func TestApplyMarksStaleRunInterrupted(t *testing.T) {
	r := newUpdaterRig(t, true)
	stale := "20261101T110000Z"
	if err := os.MkdirAll(RunDir(r.data, stale), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteStatus(RunDir(r.data, stale), RunStatus{Run: stale, State: StateQuiescing}, time.Now()); err != nil {
		t.Fatal(err)
	}
	st := r.apply(t)
	if st.State != StateSucceeded {
		t.Fatalf("status = %+v", st)
	}
	// The stale run was settled before this one started; the new run's
	// backup then pruned its directory, so settle is visible in the logs only.
}

func TestApplyStaleRunMarkedInterruptedOnDisk(t *testing.T) {
	r := newUpdaterRig(t, true)
	stale := "20261101T110000Z"
	if err := os.MkdirAll(RunDir(r.data, stale), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteStatus(RunDir(r.data, stale), RunStatus{Run: stale, State: StateQuiescing}, time.Now()); err != nil {
		t.Fatal(err)
	}
	r.checker.err = errors.New("stop here") // end the run right after step 1
	_, _ = r.u.Apply(context.Background(), ApplyArgs{Run: r.run, Report: rigReport, In: r.in})
	on, err := ReadStatus(RunDir(r.data, stale))
	if err != nil || on.State != StateInterrupted {
		t.Fatalf("stale run = %+v, %v", on, err)
	}
}

func TestApplyRefusesWhileStaleUpdaterRuns(t *testing.T) {
	r := newUpdaterRig(t, true)
	stale := "20261101T110000Z"
	if err := os.MkdirAll(RunDir(r.data, stale), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteStatus(RunDir(r.data, stale), RunStatus{Run: stale, State: StateQuiescing}, time.Now()); err != nil {
		t.Fatal(err)
	}
	r.host.updaters[UpdaterName(stale)] = UpdaterState{Exists: true, Running: true}
	_, err := r.u.Apply(context.Background(), ApplyArgs{Run: r.run, Report: rigReport, In: r.in})
	if err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("err = %v", err)
	}
	if _, serr := os.Stat(r.dir); serr == nil {
		t.Fatal("wrote a run directory while another Updater runs")
	}
	if on, _ := ReadStatus(RunDir(r.data, stale)); on.State != StateQuiescing {
		t.Fatalf("stale run = %+v", on)
	}
}

func TestApplyRefusesExistingRunDir(t *testing.T) {
	r := newUpdaterRig(t, true)
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := r.u.Apply(context.Background(), ApplyArgs{Run: r.run, Report: rigReport, In: r.in})
	if !errors.Is(err, ErrRunExists) {
		t.Fatalf("err = %v", err)
	}
	if r.checker.runs != 0 {
		t.Fatal("re-checked in a run directory that exists")
	}
}

func TestApplyHealthTimeoutRollsBack(t *testing.T) {
	r := newUpdaterRig(t, true)
	r.host.healthy[1] = false
	// The new panel opened the database before it failed health.
	r.host.onUp = func(n int) {
		if n == 1 {
			r.writeDB(t, "database after migrating")
			_ = os.WriteFile(filepath.Join(r.data, "panel.db-wal"), []byte("wal"), 0o600)
			_ = os.WriteFile(filepath.Join(r.data, "panel.db-shm"), []byte("shm"), 0o600)
		}
	}
	st := r.apply(t)
	if st.State != StateRolledBack || !strings.Contains(st.Error, "not healthy through Traefik within") ||
		!strings.Contains(st.Error, "502") {
		t.Fatalf("status = %+v", st)
	}
	if got := r.read(t, r.compose()); got != oldInstall {
		t.Fatalf("Install file = %q", got)
	}
	if got := r.read(t, r.env()); got != oldEnv {
		t.Fatalf(".env = %q", got)
	}
	if got := r.marker(t); got != "database before the update" {
		t.Fatalf("panel.db = %q", got)
	}
	for _, sibling := range []string{"panel.db-wal", "panel.db-shm"} {
		if _, err := os.Stat(filepath.Join(r.data, sibling)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s survived the restore: %v", sibling, err)
		}
	}
	calls := strings.Join(r.host.calls, ",")
	if i, j := strings.Index(calls, "stop panel"), strings.LastIndex(calls, "up"); i < 0 || j < i {
		t.Fatalf("calls = %s", calls)
	}
	if on, _ := ReadStatus(r.dir); on.State != StateRolledBack {
		t.Fatalf("status.json = %+v", on)
	}
}

func TestApplyRollbackRemovesEnvItCreated(t *testing.T) {
	r := newUpdaterRig(t, false)
	r.host.healthy[1] = false
	st := r.apply(t)
	if st.State != StateRolledBack {
		t.Fatalf("status = %+v", st)
	}
	if _, err := os.Stat(r.env()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf(".env the update created is still there: %v", err)
	}
}

func TestApplyComposeUpFailureRollsBack(t *testing.T) {
	r := newUpdaterRig(t, true)
	r.host.upErr[1] = errors.New("pull access denied")
	st := r.apply(t)
	if st.State != StateRolledBack || !strings.Contains(st.Error, "pull access denied") {
		t.Fatalf("status = %+v", st)
	}
}

func TestApplyRestoreFailureIsRollbackFailed(t *testing.T) {
	r := newUpdaterRig(t, true)
	r.host.healthy[1] = false
	r.host.onUp = func(n int) {
		if n == 1 {
			_ = os.Remove(filepath.Join(r.dir, backupDB))
		}
	}
	st := r.apply(t)
	if st.State != StateRollbackFailed {
		t.Fatalf("status = %+v", st)
	}
	if !strings.Contains(st.Error, "within") || !strings.Contains(st.RollbackError, "restore panel.db") {
		t.Fatalf("both errors expected: %+v", st)
	}
	if on, _ := ReadStatus(r.dir); on.State != StateRollbackFailed || on.RollbackError == "" {
		t.Fatalf("status.json = %+v", on)
	}
	if r.host.ups != 1 {
		t.Fatalf("ups = %d", r.host.ups)
	}
}

func TestApplyRollbackHealthFailureIsRollbackFailed(t *testing.T) {
	r := newUpdaterRig(t, true)
	r.host.healthy[1] = false
	r.host.healthy[2] = false
	st := r.apply(t)
	if st.State != StateRollbackFailed || !strings.Contains(st.RollbackError, "the old version") {
		t.Fatalf("status = %+v", st)
	}
}

func TestApplyRollbackWaitsForTheOldImage(t *testing.T) {
	r := newUpdaterRig(t, true)
	r.host.healthy[1] = false
	r.host.panelImage[2] = "sha256:new" // the old panel never came back
	st := r.apply(t)
	if st.State != StateRollbackFailed || !strings.Contains(st.RollbackError, "sha256:old") {
		t.Fatalf("status = %+v", st)
	}
}

func TestApplyHealthNeedsTheTargetImage(t *testing.T) {
	r := newUpdaterRig(t, true)
	// Traefik answers, but through the old panel Container.
	r.host.panelImage[1] = "sha256:old"
	st := r.apply(t)
	if st.State != StateRolledBack || !strings.Contains(st.Error, "sha256:new") {
		t.Fatalf("status = %+v", st)
	}
}

func TestApplyKeepsFileModes(t *testing.T) {
	r := newUpdaterRig(t, true)
	if err := os.Chmod(r.env(), 0o640); err != nil {
		t.Fatal(err)
	}
	if st := r.apply(t); st.State != StateSucceeded {
		t.Fatalf("status = %+v", st)
	}
	info, err := os.Stat(r.env())
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf(".env mode = %v, %v", info.Mode(), err)
	}
}

func TestRestoreDatabaseLeavesPanelDBWhenAWalCannotGo(t *testing.T) {
	r := newUpdaterRig(t, true)
	if err := os.MkdirAll(r.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := vacuumInto(context.Background(), filepath.Join(r.data, "panel.db"), filepath.Join(r.dir, backupDB)); err != nil {
		t.Fatal(err)
	}
	r.writeDB(t, "database now")
	before := r.read(t, filepath.Join(r.data, "panel.db"))
	// A non-empty directory named panel.db-wal cannot be removed as a file.
	wal := filepath.Join(r.data, "panel.db-wal")
	if err := os.MkdirAll(filepath.Join(wal, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := restoreDatabase(r.data, r.dir); err == nil {
		t.Fatal("want an error")
	}
	if r.read(t, filepath.Join(r.data, "panel.db")) != before {
		t.Fatal("panel.db changed: it must be untouched")
	}
}

func TestApplyHungComposeUpRollsBack(t *testing.T) {
	r := newUpdaterRig(t, true)
	r.host.hangUp = map[int]bool{1: true}
	r.u.UpTimeout = 30 * time.Millisecond
	st := r.apply(t)
	if st.State != StateRolledBack || !strings.Contains(st.Error, "did not finish within") {
		t.Fatalf("status = %+v", st)
	}
	if r.read(t, r.compose()) != oldInstall || r.read(t, r.env()) != oldEnv {
		t.Fatal("the files were not restored")
	}
}

func TestApplyRefusesSymlinkedFiles(t *testing.T) {
	for name, link := range map[string]func(r *updaterRig) string{
		"Install file": func(r *updaterRig) string { return r.compose() },
		"env":          func(r *updaterRig) string { return r.env() },
	} {
		t.Run(name, func(t *testing.T) {
			r := newUpdaterRig(t, true)
			path := link(r)
			target := filepath.Join(t.TempDir(), "elsewhere")
			if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			st := r.apply(t)
			if st.State != StateAborted || !strings.Contains(st.Error, "symlink") {
				t.Fatalf("status = %+v", st)
			}
			if r.host.ups != 0 {
				t.Fatalf("the host was touched: %v", r.host.calls)
			}
			if got, _ := os.Readlink(path); got != target || r.read(t, target) != "outside" {
				t.Fatal("the symlink or its target changed")
			}
		})
	}
}

func TestApplyFilesChangedWhileWaitingAborts(t *testing.T) {
	r := newUpdaterRig(t, true)
	r.acks = []PanelAck{{Run: r.run, State: AckWaiting, WaitingOn: "x", Heartbeat: 1}, {Run: r.run, State: AckReady, Heartbeat: 2}}
	inner := r.u.Sleep
	r.u.Sleep = func(ctx context.Context, d time.Duration) error {
		_ = os.WriteFile(r.env(), []byte("RELIST_DOMAIN=changed\n"), 0o600)
		return inner(ctx, d)
	}
	st := r.apply(t)
	if st.State != StateAborted || !strings.Contains(st.Error, ".env changed") {
		t.Fatalf("status = %+v", st)
	}
	if r.host.ups != 0 || r.read(t, r.compose()) != oldInstall {
		t.Fatal("wrote to the Install")
	}
}

func TestVacuumIntoWhileOpen(t *testing.T) {
	r := newUpdaterRig(t, true)
	path := filepath.Join(r.data, "panel.db")
	// The panel's own connection, holding a read transaction open.
	held, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	tx, err := held.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM marker`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "copy.db")
	if err := vacuumInto(context.Background(), path, dst); err != nil {
		t.Fatal(err)
	}
	copyDB, err := sql.Open("sqlite", "file:"+dst)
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	var v string
	if err := copyDB.QueryRow(`SELECT v FROM marker`).Scan(&v); err != nil || v != "database before the update" {
		t.Fatalf("copy has %q, %v", v, err)
	}
}
