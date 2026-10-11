package deployments

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/composes"
	"relist-panel/internal/sources"
)

// recorder keeps an ordered, goroutine-safe list of what the fakes saw.
type recorder struct {
	mu     sync.Mutex
	events []string
}

func (r *recorder) add(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, fmt.Sprintf(format, args...))
}

func (r *recorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

func (r *recorder) count(prefix string) int {
	n := 0
	for _, e := range r.all() {
		if strings.HasPrefix(e, prefix) {
			n++
		}
	}
	return n
}

type fakeComposes struct {
	rec      *recorder
	mu       sync.Mutex
	dirs     map[int64]string
	yaml     map[int64]string
	gone     map[int64]bool
	services []string
	deployed []composes.DeployOptions
	prepared []string // YAML PrepareBuild saw
	cleanups int
	onDeploy func(ctx context.Context, id int64, opts composes.DeployOptions) ([]string, error)
}

func (f *fakeComposes) Target(_ context.Context, id int64) (composes.DeployTarget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gone[id] {
		return composes.DeployTarget{}, fmt.Errorf("load compose %d: %w", id, sql.ErrNoRows)
	}
	dir := f.dirs[id]
	return composes.DeployTarget{
		ProjectID: 7,
		Compose:   composes.Compose{ID: id, YAMLContent: f.yaml[id]},
		Request:   composes.RunRequest{ProjectName: fmt.Sprintf("p%d", id), ComposeFilePath: filepath.Join(dir, "docker-compose.yml")},
	}, nil
}

func (f *fakeComposes) CheckDeployable(context.Context, composes.DeployTarget) error { return nil }

func (f *fakeComposes) PrepareBuild(_ context.Context, t composes.DeployTarget) (composes.BuildPlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prepared = append(f.prepared, t.Compose.YAMLContent)
	dir := composes.ComposeDir(t.Request)
	return composes.BuildPlan{
		Request:  composes.RunRequest{ProjectName: t.Request.ProjectName, ComposeFilePath: filepath.Join(dir, ".build-compose.yml")},
		Services: f.services,
		Cleanup: func() {
			f.mu.Lock()
			f.cleanups++
			f.mu.Unlock()
		},
	}, nil
}

func (f *fakeComposes) RunDeploy(ctx context.Context, t composes.DeployTarget, opts composes.DeployOptions) ([]string, error) {
	f.mu.Lock()
	f.deployed = append(f.deployed, opts)
	hook := f.onDeploy
	f.mu.Unlock()
	f.rec.add("deploy:%d", t.Compose.ID)
	if opts.Out != nil {
		fmt.Fprintln(opts.Out, "deploy output")
	}
	if hook != nil {
		return hook(ctx, t.Compose.ID, opts)
	}
	return nil, nil
}

type fakeFetcher struct {
	rec     *recorder
	onFetch func(ctx context.Context, src sources.Source, dir string, out io.Writer) (sources.Ref, error)
}

func (f *fakeFetcher) Fetch(ctx context.Context, src sources.Source, dir string, out io.Writer) (sources.Ref, error) {
	f.rec.add("fetch")
	if f.onFetch != nil {
		return f.onFetch(ctx, src, dir, out)
	}
	return sources.Ref{SHA: "abc123", Subject: "first commit"}, nil
}

type fakeBuilder struct {
	rec     *recorder
	mu      sync.Mutex
	reqs    []composes.RunRequest
	opts    []composes.BuildOptions
	onBuild func(ctx context.Context, opts composes.BuildOptions) error
}

func (b *fakeBuilder) Build(ctx context.Context, req composes.RunRequest, opts composes.BuildOptions) error {
	b.mu.Lock()
	b.reqs = append(b.reqs, req)
	b.opts = append(b.opts, opts)
	b.mu.Unlock()
	b.rec.add("build:%s", opts.Service)
	if opts.Out != nil {
		fmt.Fprintf(opts.Out, "building %s\n", opts.Service)
	}
	if b.onBuild != nil {
		return b.onBuild(ctx, opts)
	}
	return nil
}

type fakeGate struct {
	rec       *recorder
	onAcquire func(ctx context.Context) error
}

func (g *fakeGate) Acquire(ctx context.Context, projectID int64) (func(), error) {
	g.rec.add("gate+")
	if g.onAcquire != nil {
		if err := g.onAcquire(ctx); err != nil {
			g.rec.add("gate-failed")
			return nil, err
		}
	}
	return func() { g.rec.add("gate-") }, nil
}

type harness struct {
	t       *testing.T
	store   *Store
	srcs    *sources.Store
	rec     *recorder
	comp    *fakeComposes
	fetch   *fakeFetcher
	build   *fakeBuilder
	gate    *fakeGate
	q       *Queue
	a, b    int64
	timeout time.Duration
	keep    int
	cancel  context.CancelFunc
	done    chan struct{}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	db, a, b := newDB(t)
	rec := &recorder{}
	h := &harness{
		t: t, store: NewStore(db), srcs: sources.NewStore(db), rec: rec, a: a, b: b,
		fetch: &fakeFetcher{rec: rec}, build: &fakeBuilder{rec: rec}, gate: &fakeGate{rec: rec},
		comp: &fakeComposes{
			rec:  rec,
			dirs: map[int64]string{a: t.TempDir(), b: t.TempDir()},
			yaml: map[int64]string{a: "yaml-a", b: "yaml-b"},
			gone: map[int64]bool{}, services: []string{"web", "api"},
		},
		timeout: time.Minute, keep: 20,
	}
	return h
}

func (h *harness) newQueue() {
	h.q = NewQueue(Deps{
		Store: h.store, Sources: h.srcs, Composes: h.comp, Fetcher: h.fetch, Builder: h.build, Gate: h.gate,
		Timeout: h.timeout, Keep: h.keep,
	})
}

// start runs the worker; it is stopped and awaited at test cleanup.
func (h *harness) start() {
	h.t.Helper()
	if h.q == nil {
		h.newQueue()
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.done = make(chan struct{})
	go func() {
		defer close(h.done)
		h.q.Run(ctx)
	}()
	h.t.Cleanup(h.stop)
}

func (h *harness) stop() {
	h.cancel()
	select {
	case <-h.done:
	case <-time.After(10 * time.Second):
		h.t.Error("Run did not return")
	}
}

func (h *harness) gitSource(id int64) {
	h.t.Helper()
	err := h.srcs.Save(context.Background(), sources.Source{ComposeID: id, Kind: sources.KindGit, RepoURL: "git@example.com:a/b.git", Branch: "main"})
	if err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) zipSource(id int64) {
	h.t.Helper()
	err := h.srcs.Save(context.Background(), sources.Source{ComposeID: id, Kind: sources.KindZip, ZipName: "site.zip"})
	if err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) enqueue(id int64, mode Mode) Deployment {
	h.t.Helper()
	if h.q == nil {
		h.newQueue()
	}
	d, _, err := h.q.Enqueue(context.Background(), id, Request{Mode: mode})
	if err != nil {
		h.t.Fatalf("Enqueue: %v", err)
	}
	return d
}

func (h *harness) get(id int64, n int) Deployment {
	h.t.Helper()
	d, err := h.store.Get(context.Background(), id, n)
	if err != nil {
		h.t.Fatalf("Get: %v", err)
	}
	return d
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (h *harness) waitSettled(id int64, n int) Deployment {
	h.t.Helper()
	var d Deployment
	eventually(h.t, fmt.Sprintf("deployment %d to settle", n), func() bool {
		d = h.get(id, n)
		return d.Status != StatusQueued && d.Status != StatusRunning
	})
	return d
}

func (h *harness) waitEvent(prefix string, n int) {
	h.t.Helper()
	eventually(h.t, "event "+prefix, func() bool { return h.rec.count(prefix) >= n })
}

func recv(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestRunsOneAtATimeFIFO(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	h.gitSource(h.b)
	h.comp.services = []string{"web"}
	release := make(chan struct{})
	h.build.onBuild = func(ctx context.Context, opts composes.BuildOptions) error {
		<-release
		return nil
	}
	a := h.enqueue(h.a, ModeBuild)
	b := h.enqueue(h.b, ModeBuild)
	h.start()
	h.waitEvent("build:web", 1)
	if got := h.get(h.b, b.Number).Status; got != StatusQueued {
		t.Fatalf("B status = %s while A builds, want queued", got)
	}
	close(release)
	if got := h.waitSettled(h.a, a.Number).Status; got != StatusSucceeded {
		t.Fatalf("A = %s", got)
	}
	if got := h.waitSettled(h.b, b.Number).Status; got != StatusSucceeded {
		t.Fatalf("B = %s", got)
	}
	want := []string{"fetch", "build:web", "gate+", "deploy:1", "gate-", "fetch", "build:web", "gate+", "deploy:2", "gate-"}
	// Composes a and b have ids from the fixture; compare by shape.
	got := h.rec.all()
	for i := range got {
		got[i] = strings.TrimRight(got[i], "0123456789")
	}
	for i := range want {
		want[i] = strings.TrimRight(want[i], "0123456789")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestBuildModeSteps(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	d := h.enqueue(h.a, ModeBuild)
	h.start()
	got := h.waitSettled(h.a, d.Number)
	if got.Status != StatusSucceeded {
		t.Fatalf("status = %s reason %q", got.Status, got.Reason)
	}
	if want := []string{"fetch", "build:web", "build:api", "deploy"}; !reflect.DeepEqual(got.Steps, want) {
		t.Fatalf("steps = %v, want %v", got.Steps, want)
	}
	if got.SourceRef != "abc123" || got.SourceSubject != "first commit" {
		t.Fatalf("source = %q %q", got.SourceRef, got.SourceSubject)
	}
	if len(h.build.opts) != 2 || h.build.opts[0].Service != "web" || h.build.opts[1].Service != "api" {
		t.Fatalf("builds = %+v", h.build.opts)
	}
	for _, r := range h.build.reqs {
		if filepath.Base(r.ComposeFilePath) != ".build-compose.yml" {
			t.Fatalf("build request = %+v, want the plan's", r)
		}
	}
	if len(h.comp.deployed) != 1 || h.comp.deployed[0].Build {
		t.Fatalf("deploys = %+v, want one with Build false", h.comp.deployed)
	}
	if h.comp.cleanups != 1 {
		t.Fatalf("cleanups = %d, want 1", h.comp.cleanups)
	}
	log, err := os.ReadFile(LogPath(h.comp.dirs[h.a], d.Number))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"==> fetch\n", "==> build:web\n", "building api\n", "==> deploy\n", "deploy output\n"} {
		if !strings.Contains(string(log), want) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}
	info, _ := os.Stat(LogPath(h.comp.dirs[h.a], d.Number))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("log mode = %v, want 0600", info.Mode().Perm())
	}
	dirInfo, _ := os.Stat(filepath.Dir(LogPath(h.comp.dirs[h.a], d.Number)))
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf(".deployments mode = %v, want 0700", dirInfo.Mode().Perm())
	}
}

func TestNoCachePassedToBuilds(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	h.newQueue()
	d, _, err := h.q.Enqueue(context.Background(), h.a, Request{Mode: ModeBuild, NoCache: true, FreshVolumes: true})
	if err != nil {
		t.Fatal(err)
	}
	h.start()
	h.waitSettled(h.a, d.Number)
	if !h.build.opts[0].NoCache || !h.comp.deployed[0].FreshVolumes {
		t.Fatalf("opts = %+v %+v", h.build.opts, h.comp.deployed)
	}
}

func TestDeployModeUsesBuildTrue(t *testing.T) {
	h := newHarness(t)
	d := h.enqueue(h.a, ModeDeploy)
	h.start()
	got := h.waitSettled(h.a, d.Number)
	if got.Status != StatusSucceeded {
		t.Fatalf("status = %s %q", got.Status, got.Reason)
	}
	if h.rec.count("fetch") != 0 || len(h.build.opts) != 0 {
		t.Fatalf("events = %v", h.rec.all())
	}
	if !h.comp.deployed[0].Build || !reflect.DeepEqual(got.Steps, []string{"deploy"}) {
		t.Fatalf("deploys = %+v steps = %v", h.comp.deployed, got.Steps)
	}
}

func TestSkipBuildMode(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	d := h.enqueue(h.a, ModeSkipBuild)
	h.start()
	got := h.waitSettled(h.a, d.Number)
	if got.Status != StatusSucceeded || h.comp.deployed[0].Build || !reflect.DeepEqual(got.Steps, []string{"deploy"}) {
		t.Fatalf("got %+v deploys %+v", got, h.comp.deployed)
	}
	if h.rec.count("fetch") != 0 || len(h.build.opts) != 0 {
		t.Fatalf("events = %v", h.rec.all())
	}
}

func TestZipSourceRecordsZipName(t *testing.T) {
	h := newHarness(t)
	h.zipSource(h.a)
	d := h.enqueue(h.a, ModeBuild)
	h.start()
	got := h.waitSettled(h.a, d.Number)
	if got.Status != StatusSucceeded {
		t.Fatalf("status = %s %q", got.Status, got.Reason)
	}
	if h.rec.count("fetch") != 0 {
		t.Fatal("zip Source must not fetch")
	}
	if got.SourceRef != "site.zip" || got.SourceSubject != "" {
		t.Fatalf("source = %q %q", got.SourceRef, got.SourceSubject)
	}
	if want := []string{"build:web", "build:api", "deploy"}; !reflect.DeepEqual(got.Steps, want) {
		t.Fatalf("steps = %v, want %v", got.Steps, want)
	}
}

func TestNoBuildServicesLogsAndDeploys(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	h.comp.services = nil
	d := h.enqueue(h.a, ModeBuild)
	h.start()
	got := h.waitSettled(h.a, d.Number)
	if got.Status != StatusSucceeded {
		t.Fatalf("status = %s %q", got.Status, got.Reason)
	}
	if want := []string{"fetch", "deploy"}; !reflect.DeepEqual(got.Steps, want) {
		t.Fatalf("steps = %v, want %v", got.Steps, want)
	}
	log, _ := os.ReadFile(LogPath(h.comp.dirs[h.a], d.Number))
	if !strings.Contains(string(log), "no Service has build:") {
		t.Fatalf("log = %s", log)
	}
	if h.comp.cleanups != 1 {
		t.Fatalf("cleanups = %d", h.comp.cleanups)
	}
}

func TestStopsAtFirstFailedBuild(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	h.build.onBuild = func(ctx context.Context, opts composes.BuildOptions) error {
		return errors.New("web build failed\nsecond line")
	}
	d := h.enqueue(h.a, ModeBuild)
	h.start()
	got := h.waitSettled(h.a, d.Number)
	if got.Status != StatusFailed || got.FailedStep != "build:web" || got.Reason != "web build failed" {
		t.Fatalf("got %s %q %q", got.Status, got.FailedStep, got.Reason)
	}
	if len(h.build.opts) != 1 || len(h.comp.deployed) != 0 || h.rec.count("gate") != 0 {
		t.Fatalf("events = %v", h.rec.all())
	}
	if h.comp.cleanups != 1 {
		t.Fatalf("cleanups = %d", h.comp.cleanups)
	}
}

func TestFetchFailureAndMissingBranchAndMissingSource(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	h.fetch.onFetch = func(context.Context, sources.Source, string, io.Writer) (sources.Ref, error) {
		return sources.Ref{}, errors.New("clone failed")
	}
	d := h.enqueue(h.a, ModeBuild)
	h.start()
	got := h.waitSettled(h.a, d.Number)
	if got.Status != StatusFailed || got.FailedStep != "fetch" || got.Reason != "clone failed" {
		t.Fatalf("fetch failure = %+v", got)
	}

	if err := h.srcs.Save(context.Background(), sources.Source{ComposeID: h.b, Kind: sources.KindGit, RepoURL: "x", Branch: ""}); err != nil {
		t.Fatal(err)
	}
	e := h.enqueue(h.b, ModeBuild)
	got = h.waitSettled(h.b, e.Number)
	if got.Status != StatusFailed || got.FailedStep != "fetch" || got.Reason != "no branch configured" {
		t.Fatalf("no branch = %+v", got)
	}

	if err := h.srcs.Delete(context.Background(), h.b); err != nil {
		t.Fatal(err)
	}
	f := h.enqueue(h.b, ModeSkipBuild)
	got = h.waitSettled(h.b, f.Number)
	if got.Status != StatusFailed || got.FailedStep != "" || got.Reason != "compose has no source" {
		t.Fatalf("no source = %+v", got)
	}
	if len(h.comp.deployed) != 0 {
		t.Fatal("nothing may deploy")
	}
}

func TestGateOnlyForDeployStep(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	d := h.enqueue(h.a, ModeBuild)
	h.start()
	h.waitSettled(h.a, d.Number)
	want := []string{"fetch", "build:web", "build:api", "gate+", "deploy:" + fmt.Sprint(h.a), "gate-"}
	if got := h.rec.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestQueueCancelQueued(t *testing.T) {
	h := newHarness(t)
	d := h.enqueue(h.a, ModeDeploy)
	if err := h.q.Cancel(context.Background(), h.a, d.Number); err != nil {
		t.Fatal(err)
	}
	if got := h.get(h.a, d.Number).Status; got != StatusCanceled {
		t.Fatalf("status = %s", got)
	}
	h.start()
	// A later Deployment proves the worker moved past the canceled one.
	e := h.enqueue(h.b, ModeDeploy)
	h.waitSettled(h.b, e.Number)
	if got := h.rec.all(); !reflect.DeepEqual(got, []string{"gate+", "deploy:" + fmt.Sprint(h.b), "gate-"}) {
		t.Fatalf("events = %v", got)
	}
}

func TestCancelWhileBuilding(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	h.build.onBuild = func(ctx context.Context, opts composes.BuildOptions) error {
		<-ctx.Done()
		return ctx.Err()
	}
	d := h.enqueue(h.a, ModeBuild)
	h.start()
	h.waitEvent("build:web", 1)
	if !h.q.IsRunning(h.a) {
		t.Fatal("IsRunning = false while building")
	}
	if err := h.q.Cancel(context.Background(), h.a, d.Number); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	got := h.get(h.a, d.Number)
	if got.Status != StatusCanceled {
		t.Fatalf("status = %s %q, want canceled (Cancel waits for exit)", got.Status, got.Reason)
	}
	if h.rec.count("gate") != 0 || len(h.comp.deployed) != 0 {
		t.Fatalf("events = %v", h.rec.all())
	}
	eventually(t, "IsRunning to clear", func() bool { return !h.q.IsRunning(h.a) })
}

func TestCancelDuringDeployRefused(t *testing.T) {
	h := newHarness(t)
	inDeploy, release := make(chan struct{}), make(chan struct{})
	h.comp.onDeploy = func(ctx context.Context, _ int64, _ composes.DeployOptions) ([]string, error) {
		close(inDeploy)
		<-release
		return nil, nil
	}
	d := h.enqueue(h.a, ModeDeploy)
	h.start()
	recv(t, inDeploy, "deploy to start")
	if err := h.q.Cancel(context.Background(), h.a, d.Number); !errors.Is(err, ErrNotCancelable) {
		t.Fatalf("Cancel err = %v, want ErrNotCancelable", err)
	}
	close(release)
	if got := h.waitSettled(h.a, d.Number).Status; got != StatusSucceeded {
		t.Fatalf("status = %s", got)
	}
	if err := h.q.Cancel(context.Background(), h.a, d.Number); !errors.Is(err, ErrNotCancelable) {
		t.Fatalf("Cancel after finish err = %v", err)
	}
}

func TestCancelWhileWaitingForGate(t *testing.T) {
	h := newHarness(t)
	waiting := make(chan struct{})
	h.gate.onAcquire = func(ctx context.Context) error {
		close(waiting)
		<-ctx.Done()
		return ctx.Err()
	}
	d := h.enqueue(h.a, ModeDeploy)
	h.start()
	recv(t, waiting, "worker to wait on the gate")
	h.q.CancelForComposes(context.Background(), []int64{h.a})
	got := h.get(h.a, d.Number)
	if got.Status != StatusCanceled {
		t.Fatalf("status = %s %q", got.Status, got.Reason)
	}
	if len(h.comp.deployed) != 0 {
		t.Fatal("RunDeploy must not run")
	}
}

func TestComposeGoneAfterGate(t *testing.T) {
	h := newHarness(t)
	waiting, proceed := make(chan struct{}), make(chan struct{})
	h.gate.onAcquire = func(ctx context.Context) error {
		close(waiting)
		<-proceed
		return nil
	}
	d := h.enqueue(h.a, ModeDeploy)
	h.start()
	recv(t, waiting, "gate wait")
	h.comp.mu.Lock()
	h.comp.gone[h.a] = true
	h.comp.mu.Unlock()
	close(proceed)
	got := h.waitSettled(h.a, d.Number)
	if got.Status != StatusCanceled || got.Reason != "compose deleted" {
		t.Fatalf("got %s %q", got.Status, got.Reason)
	}
	if len(h.comp.deployed) != 0 {
		t.Fatal("RunDeploy must not run")
	}
	if want := []string{"gate+", "gate-"}; !reflect.DeepEqual(h.rec.all(), want) {
		t.Fatalf("events = %v, want gate released", h.rec.all())
	}
}

func TestDeployStepSurvivesShutdown(t *testing.T) {
	h := newHarness(t)
	inDeploy, release := make(chan struct{}), make(chan struct{})
	var deployCtx context.Context
	h.comp.onDeploy = func(ctx context.Context, _ int64, _ composes.DeployOptions) ([]string, error) {
		deployCtx = ctx
		close(inDeploy)
		<-release
		return nil, nil
	}
	d := h.enqueue(h.a, ModeDeploy)
	h.start()
	recv(t, inDeploy, "deploy to start")
	h.cancel()
	select {
	case <-h.done:
		t.Fatal("Run returned while the Deploy step was running")
	case <-time.After(50 * time.Millisecond):
	}
	if deployCtx.Err() != nil {
		t.Fatalf("deploy ctx canceled: %v", deployCtx.Err())
	}
	close(release)
	recv(t, h.done, "Run to return")
	if got := h.get(h.a, d.Number).Status; got != StatusSucceeded {
		t.Fatalf("status = %s", got)
	}
}

func TestShutdownDuringBuildFailsAsRestarted(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	h.build.onBuild = func(ctx context.Context, opts composes.BuildOptions) error {
		<-ctx.Done()
		return ctx.Err()
	}
	d := h.enqueue(h.a, ModeBuild)
	h.start()
	h.waitEvent("build:web", 1)
	h.cancel()
	recv(t, h.done, "Run to return")
	got := h.get(h.a, d.Number)
	if got.Status != StatusFailed || got.Reason != "panel restarted" || got.FailedStep != "build:web" {
		t.Fatalf("got %s %q %q", got.Status, got.Reason, got.FailedStep)
	}
}

func TestTryLockSourceFailsDuringBuild(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	h.newQueue()
	unlock, ok := h.q.TryLockSource(h.a)
	if !ok {
		t.Fatal("idle lock refused")
	}
	unlock()

	building, release := make(chan struct{}), make(chan struct{})
	h.build.onBuild = func(ctx context.Context, opts composes.BuildOptions) error {
		if opts.Service == "web" {
			close(building)
			<-release
		}
		return nil
	}
	d := h.enqueue(h.a, ModeBuild)
	h.start()
	recv(t, building, "build to start")
	if _, ok := h.q.TryLockSource(h.a); ok {
		t.Fatal("TryLockSource succeeded during a build")
	}
	if _, ok := h.q.TryLockSource(h.b); !ok {
		t.Fatal("another Compose's lock must be free")
	}
	close(release)
	h.waitSettled(h.a, d.Number)
	eventually(t, "lock to free", func() bool {
		unlock, ok := h.q.TryLockSource(h.a)
		if ok {
			unlock()
		}
		return ok
	})
}

func TestWorkerWaitsForUploadLockAndCanBeCanceled(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	h.newQueue()
	unlock, ok := h.q.TryLockSource(h.a)
	if !ok {
		t.Fatal("lock refused")
	}
	defer unlock()
	d := h.enqueue(h.a, ModeBuild)
	h.start()
	eventually(t, "deployment to start", func() bool { return h.get(h.a, d.Number).Status == StatusRunning })
	if err := h.q.Cancel(context.Background(), h.a, d.Number); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if got := h.get(h.a, d.Number).Status; got != StatusCanceled {
		t.Fatalf("status = %s", got)
	}
	if h.rec.count("fetch") != 0 {
		t.Fatal("must not fetch without the lock")
	}
}

func TestCancelForComposesWaitsForExit(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	exited := make(chan struct{})
	h.build.onBuild = func(ctx context.Context, opts composes.BuildOptions) error {
		<-ctx.Done()
		time.Sleep(30 * time.Millisecond) // the process takes a moment to die
		close(exited)
		return ctx.Err()
	}
	h.enqueue(h.a, ModeBuild)
	h.start()
	h.waitEvent("build:web", 1)
	h.q.CancelForComposes(context.Background(), []int64{h.a})
	select {
	case <-exited:
	default:
		t.Fatal("CancelForComposes returned before the builder returned")
	}
}

func TestCancelForComposesDoesNotWaitDuringDeploy(t *testing.T) {
	h := newHarness(t)
	inDeploy, release := make(chan struct{}), make(chan struct{})
	h.comp.onDeploy = func(ctx context.Context, _ int64, _ composes.DeployOptions) ([]string, error) {
		close(inDeploy)
		<-release
		return nil, nil
	}
	d := h.enqueue(h.a, ModeDeploy)
	h.start()
	recv(t, inDeploy, "deploy to start")
	returned := make(chan struct{})
	go func() {
		h.q.CancelForComposes(context.Background(), []int64{h.a})
		close(returned)
	}()
	recv(t, returned, "CancelForComposes to return during deploy")
	close(release)
	if got := h.waitSettled(h.a, d.Number).Status; got != StatusSucceeded {
		t.Fatalf("status = %s, want succeeded", got)
	}
}

func TestCancelForComposesCancelsQueued(t *testing.T) {
	h := newHarness(t)
	d := h.enqueue(h.a, ModeDeploy)
	h.q.CancelForComposes(context.Background(), []int64{h.a, h.b})
	if got := h.get(h.a, d.Number).Status; got != StatusCanceled {
		t.Fatalf("status = %s", got)
	}
}

func TestTimeout(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	h.timeout = 50 * time.Millisecond
	h.build.onBuild = func(ctx context.Context, opts composes.BuildOptions) error {
		<-ctx.Done()
		return ctx.Err()
	}
	d := h.enqueue(h.a, ModeBuild)
	h.start()
	got := h.waitSettled(h.a, d.Number)
	if got.Status != StatusFailed || got.Reason != "timeout" || got.FailedStep != "build:web" {
		t.Fatalf("got %s %q %q", got.Status, got.Reason, got.FailedStep)
	}
}

func TestRestartRecovery(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	old := enqueue(t, h.store, h.a, t0)
	if err := h.store.Start(ctx, old.ID, []string{"deploy"}, t0); err != nil {
		t.Fatal(err)
	}
	next := enqueue(t, h.store, h.b, t0.Add(time.Second))
	h.start()
	h.waitSettled(h.b, next.Number)
	got := h.get(h.a, old.Number)
	if got.Status != StatusFailed || got.Reason != "panel restarted" {
		t.Fatalf("old = %s %q", got.Status, got.Reason)
	}
	if h.get(h.b, next.Number).Status != StatusSucceeded {
		t.Fatal("queued deployment did not run")
	}
}

func TestSnapshotAtStart(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	d := h.enqueue(h.a, ModeBuild)
	h.comp.mu.Lock()
	h.comp.yaml[h.a] = "edited after enqueue"
	h.comp.mu.Unlock()
	h.start()
	h.waitSettled(h.a, d.Number)
	if !reflect.DeepEqual(h.comp.prepared, []string{"edited after enqueue"}) {
		t.Fatalf("PrepareBuild saw %v", h.comp.prepared)
	}
}

func TestComposeDeletedMidRun(t *testing.T) {
	h := newHarness(t)
	gone := h.enqueue(h.a, ModeDeploy)
	h.comp.gone[h.a] = true
	next := h.enqueue(h.b, ModeDeploy)
	h.start()
	if got := h.waitSettled(h.b, next.Number).Status; got != StatusSucceeded {
		t.Fatalf("next = %s", got)
	}
	got := h.get(h.a, gone.Number)
	if got.Status != StatusCanceled || got.Reason != "compose deleted" {
		t.Fatalf("gone = %s %q", got.Status, got.Reason)
	}
}

func TestComposeDeletedWhileRunningFinishesQuietly(t *testing.T) {
	h := newHarness(t)
	h.comp.onDeploy = func(ctx context.Context, id int64, _ composes.DeployOptions) ([]string, error) {
		// The cascade removes the row under the running worker.
		if id != h.a {
			return nil, nil
		}
		if _, err := h.store.db.Exec(`DELETE FROM composes WHERE id = ?`, id); err != nil {
			t.Error(err)
		}
		return nil, nil
	}
	h.enqueue(h.a, ModeDeploy)
	next := h.enqueue(h.b, ModeDeploy)
	h.start()
	if got := h.waitSettled(h.b, next.Number).Status; got != StatusSucceeded {
		t.Fatalf("next = %s", got)
	}
}

func TestEnqueueReturnsExistingAndRejectsBadMode(t *testing.T) {
	h := newHarness(t)
	h.newQueue()
	first, created, err := h.q.Enqueue(context.Background(), h.a, Request{Mode: ModeDeploy})
	if err != nil || !created {
		t.Fatal(err, created)
	}
	second, created, err := h.q.Enqueue(context.Background(), h.a, Request{Mode: ModeSkipBuild})
	if err != nil || created || second.Number != first.Number || second.Mode != ModeDeploy {
		t.Fatalf("second = %+v created=%v err=%v", second, created, err)
	}
	if _, _, err := h.q.Enqueue(context.Background(), h.b, Request{Mode: "bogus"}); err == nil {
		t.Fatal("bogus mode accepted")
	}
}

func TestPrunesAfterFinish(t *testing.T) {
	h := newHarness(t)
	h.keep = 2
	h.start()
	for i := 1; i <= 3; i++ {
		d := h.enqueue(h.a, ModeDeploy)
		h.waitSettled(h.a, d.Number)
	}
	logDir := filepath.Join(h.comp.dirs[h.a], ".deployments")
	var names []string
	eventually(t, "prune", func() bool {
		list, _ := h.store.List(context.Background(), h.a, 10)
		entries, _ := os.ReadDir(logDir)
		names = names[:0]
		for _, e := range entries {
			names = append(names, e.Name())
		}
		return len(list) == 2 && len(names) == 2
	})
	if want := []string{"2.log", "3.log"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("logs = %v, want %v", names, want)
	}
}
