package traefikconfig

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeTraefik scripts a Traefik: State answers from states (the last one
// repeats), and health from healthy.
type fakeTraefik struct {
	mu          sync.Mutex
	states      []State
	stateCalls  int
	restarts    int
	logs        string
	dry         DryRunResult
	dryErr      error
	dryRuns     int
	removed     int
	healthy     func(restarts int) error
	restartHook func()
}

func (f *fakeTraefik) State(context.Context) (State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := min(f.stateCalls, len(f.states)-1)
	f.stateCalls++
	return f.states[i], nil
}

func (f *fakeTraefik) Restart(context.Context) error {
	f.mu.Lock()
	f.restarts++
	hook := f.restartHook
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	return nil
}

func (f *fakeTraefik) LogsSince(context.Context, time.Time, int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logs, nil
}

func (f *fakeTraefik) DryRun(_ context.Context, _, dir, _ string, _ time.Duration) (DryRunResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dryRuns++
	if _, err := os.Stat(dir + "/traefik.yml"); err != nil {
		return DryRunResult{}, err
	}
	return f.dry, f.dryErr
}

func (f *fakeTraefik) RemoveDryRuns(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed++
	return nil
}

func (f *fakeTraefik) Healthz(context.Context, string, string) error {
	f.mu.Lock()
	h, n := f.healthy, f.restarts
	f.mu.Unlock()
	if h == nil {
		return nil
	}
	return h(n)
}

func (f *fakeTraefik) restartCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.restarts
}

var running = State{Running: true, RestartCount: 0, Image: "sha256:img", IP: "10.0.0.2", StartedAt: time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)}

type testRunner struct {
	*Runner
	fake *fakeTraefik
	p    Paths
}

func newTestRunner(t *testing.T, override string) testRunner {
	t.Helper()
	p := PathsFor(t.TempDir())
	os.MkdirAll(p.Dir, 0o755)
	eff, problems, err := Generate([]byte(override))
	if err != nil || len(problems) > 0 {
		t.Fatalf("Generate: %v %+v", err, problems)
	}
	os.WriteFile(p.Override(), []byte(override), 0o644)
	os.WriteFile(p.Effective(), eff, 0o644)
	fake := &fakeTraefik{states: []State{running}, dry: DryRunResult{Running: true, Log: "INF ok"}}
	var mu sync.Mutex
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	r := &Runner{
		Paths: p, Traefik: fake, Domain: "panel.example.com",
		Gate: func(context.Context) error { return nil },
		Now: func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return now
		},
		Sleep: func(_ context.Context, d time.Duration) error {
			mu.Lock()
			now = now.Add(d)
			mu.Unlock()
			return nil
		},
	}
	return testRunner{Runner: r, fake: fake, p: p}
}

func (tr testRunner) override(t *testing.T) string {
	t.Helper()
	b, _ := os.ReadFile(tr.p.Override())
	return string(b)
}

// apply starts an apply of override and waits for it to end.
func (tr testRunner) apply(t *testing.T, override string) Run {
	t.Helper()
	cur, _ := os.ReadFile(tr.p.Override())
	if _, err := tr.Start(context.Background(), KindApply, []byte(override), Revision(cur)); err != nil {
		t.Fatalf("Start: %v", err)
	}
	return tr.wait(t)
}

func (tr testRunner) wait(t *testing.T) Run {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tr.WaitIdle(ctx); err != nil {
		t.Fatalf("WaitIdle: %v", err)
	}
	run, err := tr.Status()
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func historyNames(t *testing.T, p Paths) []string {
	t.Helper()
	entries, _ := os.ReadDir(p.HistoryDir())
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestApplyOK(t *testing.T) {
	tr := newTestRunner(t, "log: {level: INFO}\n")
	var seen []string
	tr.onState = func(s string) { seen = append(seen, s) }
	run := tr.apply(t, "log: {level: DEBUG}\n")
	if run.State != StateOK {
		t.Fatalf("run = %+v", run)
	}
	if want := []string{StateDryRun, StateWriting, StateRestarting, StateChecking, StateOK}; strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Errorf("states = %v", seen)
	}
	if tr.override(t) != "log: {level: DEBUG}\n" {
		t.Errorf("override = %q", tr.override(t))
	}
	eff, _ := os.ReadFile(tr.p.Effective())
	if !strings.Contains(string(eff), "DEBUG") || run.EffectiveSHA256 != sha(eff) {
		t.Errorf("effective sha = %s, file sha = %s", run.EffectiveSHA256, sha(eff))
	}
	names := historyNames(t, tr.p)
	if len(names) != 1 {
		t.Fatalf("history = %v", names)
	}
	old, _ := os.ReadFile(tr.p.HistoryDir() + "/" + names[0] + "/traefik.override.yml")
	if string(old) != "log: {level: INFO}\n" {
		t.Errorf("history keeps %q, want the old override", old)
	}
}

func TestApplyValidateFails(t *testing.T) {
	tr := newTestRunner(t, "")
	_, err := tr.Start(context.Background(), KindApply, []byte("bogusTop: 1\n"), Revision([]byte("")))
	var pe *ProblemsError
	if !errors.As(err, &pe) || len(pe.Problems) != 1 {
		t.Fatalf("err = %v", err)
	}
	_, err = tr.Start(context.Background(), KindApply, []byte("a: [oops\n"), Revision([]byte("")))
	if !errors.As(err, &pe) || pe.Problems[0].Line != 1 {
		t.Fatalf("bad YAML err = %v", err)
	}
	if tr.override(t) != "" || tr.fake.dryRuns != 0 {
		t.Fatalf("something ran: override %q, dry-runs %d", tr.override(t), tr.fake.dryRuns)
	}
}

func TestApplyDryRunFails(t *testing.T) {
	tr := newTestRunner(t, "")
	tr.fake.dry = DryRunResult{Running: false, Log: `{"level":"error","message":"Command error"}`}
	run := tr.apply(t, "log: {level: DEBUG}\n")
	if run.State != StateRefused || !strings.Contains(run.Log, "Command error") {
		t.Fatalf("run = %+v", run)
	}
	if tr.override(t) != "" || tr.fake.restartCount() != 0 || len(historyNames(t, tr.p)) != 0 {
		t.Fatalf("override %q, restarts %d, history %v", tr.override(t), tr.fake.restartCount(), historyNames(t, tr.p))
	}
	if _, err := os.Stat(tr.p.DryRunDir(run.ID)); !os.IsNotExist(err) {
		t.Errorf("dry-run dir left behind: %v", err)
	}
}

func TestApplyExitAfterRestart(t *testing.T) {
	tr := newTestRunner(t, "")
	stopped := running
	stopped.Running = false
	// Before the restart: running. After it: exited, until the rollback restart.
	tr.fake.restartHook = func() {
		tr.fake.mu.Lock()
		if tr.fake.restarts == 1 {
			tr.fake.states = []State{stopped}
		} else {
			tr.fake.states = []State{running}
		}
		tr.fake.stateCalls = 0
		tr.fake.mu.Unlock()
	}
	run := tr.apply(t, "log: {level: DEBUG}\n")
	if run.State != StateRolledBack || tr.fake.restartCount() != 2 {
		t.Fatalf("run = %+v, restarts = %d", run, tr.fake.restartCount())
	}
	if tr.override(t) != "" {
		t.Errorf("override not rolled back: %q", tr.override(t))
	}
}

func TestApplyRestartLoop(t *testing.T) {
	tr := newTestRunner(t, "")
	looping := running
	looping.RestartCount = 1
	tr.fake.restartHook = func() {
		tr.fake.mu.Lock()
		if tr.fake.restarts == 1 {
			tr.fake.states = []State{running, looping}
		} else {
			tr.fake.states = []State{looping}
		}
		tr.fake.stateCalls = 0
		tr.fake.mu.Unlock()
	}
	// Not answering while it restarts in a loop; answering again after the
	// rollback.
	tr.fake.healthy = func(restarts int) error {
		if restarts == 1 {
			return errors.New("down")
		}
		return nil
	}
	if run := tr.apply(t, "log: {level: DEBUG}\n"); run.State != StateRolledBack || !strings.Contains(run.Reason, "restart") {
		t.Fatalf("run = %+v", run)
	}
}

func TestApplyCommandErrorInLog(t *testing.T) {
	tr := newTestRunner(t, "")
	tr.fake.restartHook = func() {
		tr.fake.mu.Lock()
		if tr.fake.restarts == 1 {
			tr.fake.logs = "Command error"
		} else {
			tr.fake.logs = ""
		}
		tr.fake.mu.Unlock()
	}
	tr.fake.healthy = func(restarts int) error {
		if restarts == 1 {
			return errors.New("down")
		}
		return nil
	}
	if run := tr.apply(t, "log: {level: DEBUG}\n"); run.State != StateRolledBack || !strings.Contains(run.Reason, "Command error") {
		t.Fatalf("run = %+v", run)
	}
}

func TestApplyOtherErrNotFailure(t *testing.T) {
	tr := newTestRunner(t, "")
	tr.fake.logs = "2026-11-04T10:00:01Z ERR acme: rate limited\n"
	run := tr.apply(t, "log: {level: DEBUG}\n")
	if run.State != StateOK || !strings.Contains(run.Log, "rate limited") {
		t.Fatalf("run = %+v", run)
	}
}

func TestApplyHealthzTimeout(t *testing.T) {
	tr := newTestRunner(t, "")
	tr.fake.healthy = func(restarts int) error {
		if restarts == 1 {
			return errors.New("connection refused")
		}
		return nil
	}
	run := tr.apply(t, "log: {level: DEBUG}\n")
	if run.State != StateRolledBack || !strings.Contains(run.Reason, "60s") {
		t.Fatalf("run = %+v", run)
	}
}

func TestRollbackFails(t *testing.T) {
	tr := newTestRunner(t, "")
	tr.fake.healthy = func(int) error { return errors.New("down") }
	run := tr.apply(t, "log: {level: DEBUG}\n")
	if run.State != StateRollbackFailed || tr.fake.restartCount() != 2 {
		t.Fatalf("run = %+v, restarts = %d", run, tr.fake.restartCount())
	}
}

func TestApplyBusy(t *testing.T) {
	tr := newTestRunner(t, "")
	release := make(chan struct{})
	tr.fake.restartHook = func() { <-release }
	if _, err := tr.Start(context.Background(), KindApply, []byte("log: {level: DEBUG}\n"), Revision(nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Start(context.Background(), KindApply, []byte("log: {level: WARN}\n"), Revision(nil)); !errors.Is(err, ErrBusy) {
		t.Fatalf("second Start err = %v", err)
	}
	close(release)
	tr.wait(t)
}

func TestApplyRevisionMismatch(t *testing.T) {
	tr := newTestRunner(t, "log: {level: INFO}\n")
	if _, err := tr.Start(context.Background(), KindApply, []byte("log: {level: DEBUG}\n"), Revision([]byte("other"))); !errors.Is(err, ErrRevision) {
		t.Fatalf("err = %v", err)
	}
	if tr.override(t) != "log: {level: INFO}\n" {
		t.Fatalf("override changed")
	}
}

func TestApplyGateRefused(t *testing.T) {
	tr := newTestRunner(t, "")
	locked := errors.New("a Panel update is running")
	tr.Gate = func(context.Context) error { return locked }
	if _, err := tr.Start(context.Background(), KindApply, []byte(""), Revision(nil)); !errors.Is(err, locked) {
		t.Fatalf("err = %v", err)
	}
}

func TestRestartKind(t *testing.T) {
	tr := newTestRunner(t, "log: {level: WARN}\n")
	if _, err := tr.Start(context.Background(), KindRestart, nil, ""); err != nil {
		t.Fatal(err)
	}
	run := tr.wait(t)
	if run.State != StateOK || run.Kind != KindRestart || tr.fake.restartCount() != 1 || tr.fake.dryRuns != 1 {
		t.Fatalf("run = %+v, restarts %d, dry-runs %d", run, tr.fake.restartCount(), tr.fake.dryRuns)
	}
	if len(historyNames(t, tr.p)) != 0 || tr.override(t) != "log: {level: WARN}\n" {
		t.Fatalf("history %v, override %q", historyNames(t, tr.p), tr.override(t))
	}
}

// A Restart dry-runs the files on disk first: when they fail (a new base from
// a Panel update, or a hand edit), the running Traefik is left alone.
func TestRestartRefusedWhenFilesFailDryRun(t *testing.T) {
	tr := newTestRunner(t, "log: {level: WARN}\n")
	tr.fake.dry = DryRunResult{Running: false, Log: `{"level":"error","message":"Command error"}`}
	if _, err := tr.Start(context.Background(), KindRestart, nil, ""); err != nil {
		t.Fatal(err)
	}
	run := tr.wait(t)
	if run.State != StateRefused || tr.fake.restartCount() != 0 || run.Log == "" {
		t.Fatalf("run = %+v, restarts %d", run, tr.fake.restartCount())
	}
}

func TestResumeFromEachState(t *testing.T) {
	for _, c := range []struct {
		state   string
		healthy bool
		want    string
	}{
		{StateDryRun, true, StateRefused},
		{StateWriting, true, StateOK},
		{StateRestarting, true, StateOK},
		{StateChecking, true, StateOK},
		{StateChecking, false, StateRolledBack},
		{StateRollingBack, true, StateRolledBack},
	} {
		tr := newTestRunner(t, "log: {level: WARN}\n")
		backup := "20261104T100000Z-abcdefgh"
		os.MkdirAll(tr.p.HistoryDir()+"/"+backup, 0o755)
		os.WriteFile(tr.p.HistoryDir()+"/"+backup+"/traefik.override.yml", []byte("log: {level: WARN}\n"), 0o644)
		eff, _ := os.ReadFile(tr.p.Effective())
		os.WriteFile(tr.p.HistoryDir()+"/"+backup+"/traefik.yml", eff, 0o644)
		if err := tr.save(Run{ID: "abcdefgh", Kind: KindApply, State: c.state, Backup: backup}); err != nil {
			t.Fatal(err)
		}
		if !c.healthy {
			tr.fake.healthy = func(restarts int) error {
				if restarts == 0 {
					return errors.New("down")
				}
				return nil
			}
		}
		if err := tr.Resume(context.Background()); err != nil {
			t.Fatalf("%s: Resume: %v", c.state, err)
		}
		run := tr.wait(t)
		if run.State != c.want || tr.Busy() {
			t.Errorf("%s (healthy %v): ended %s, busy %v", c.state, c.healthy, run.State, tr.Busy())
		}
	}
}

func TestResumeRemovesStaleDryRuns(t *testing.T) {
	tr := newTestRunner(t, "")
	if err := tr.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	if tr.fake.removed != 1 {
		t.Fatalf("RemoveDryRuns called %d times", tr.fake.removed)
	}
}

func TestWaitIdle(t *testing.T) {
	tr := newTestRunner(t, "")
	release := make(chan struct{})
	tr.fake.restartHook = func() { <-release }
	if _, err := tr.Start(context.Background(), KindApply, []byte("log: {level: DEBUG}\n"), Revision(nil)); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := tr.WaitIdle(ctx); err == nil {
		t.Fatal("WaitIdle returned while the run was going")
	}
	close(release)
	tr.wait(t)
}

func TestPendingRestart(t *testing.T) {
	tr := newTestRunner(t, "")
	tr.apply(t, "log: {level: DEBUG}\n")
	if pending, err := tr.PendingRestart(context.Background()); err != nil || pending {
		t.Fatalf("after ok: pending = %v, err = %v", pending, err)
	}
	eff, _, _ := Generate([]byte("log: {level: WARN}\n"))
	os.WriteFile(tr.p.Effective(), eff, 0o644)
	if pending, _ := tr.PendingRestart(context.Background()); !pending {
		t.Fatal("file changed but not pending")
	}

	fresh := newTestRunner(t, "")
	future := running
	future.StartedAt = time.Now().Add(time.Hour)
	fresh.fake.states = []State{future}
	if pending, _ := fresh.PendingRestart(context.Background()); pending {
		t.Fatal("pending although Traefik started after the file was written")
	}
	fresh.fake.states = []State{running} // started 2026-11-04 00:00, before the file
	if pending, _ := fresh.PendingRestart(context.Background()); !pending {
		t.Fatal("not pending although the file is newer than Traefik's start")
	}
}

func TestStatusCarriesBootstrapWarning(t *testing.T) {
	tr := newTestRunner(t, "")
	if err := RecordBootstrapWarning(tr.p, "could not read flags"); err != nil {
		t.Fatal(err)
	}
	run, err := tr.Status()
	if err != nil || run.BootstrapWarning != "could not read flags" {
		t.Fatalf("run = %+v, err = %v", run, err)
	}
	tr.apply(t, "log: {level: DEBUG}\n")
	if run, _ := tr.Status(); run.BootstrapWarning != "could not read flags" {
		t.Fatalf("an apply dropped the bootstrap warning: %+v", run)
	}
}

func TestAck(t *testing.T) {
	tr := newTestRunner(t, "")
	run := tr.apply(t, "log: {level: DEBUG}\n")
	if run.Acked {
		t.Fatal("acked before Ack")
	}
	if err := tr.Ack(run.ID); err != nil {
		t.Fatal(err)
	}
	if run, _ := tr.Status(); !run.Acked {
		t.Fatal("not acked")
	}
}
