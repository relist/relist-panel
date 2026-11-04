package deployments

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/composes"
	"relist-panel/internal/sources"
	"relist-panel/internal/sshkeys"
	"relist-panel/internal/zerodowntime"
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
	rec             *recorder
	mu              sync.Mutex
	dirs            map[int64]string
	yaml            map[int64]string
	gone            map[int64]bool
	services        []string
	deployed        []composes.DeployOptions
	prepared        []string // YAML PrepareBuild saw
	snapshotSecrets []string
	cleanups        int
	onDeploy        func(ctx context.Context, id int64, opts composes.DeployOptions) ([]string, error)
	// zeroDowntime is what Capture records as the snapshot's rollout list.
	zeroDowntime []string
	problems     []zerodowntime.Problem
	problemsErr  error
	checked      int
	rolledOut    []string
	onRollout    func(ctx context.Context, service string) ([]string, error)
	marked       int
	// builderID is what Capture records as the snapshot's Builder.
	builderID int64
	targets   []composes.DeployTarget // what RunDeploy saw
}

func (f *fakeComposes) ZeroDowntimeProblems(context.Context, composes.DeployTarget) ([]zerodowntime.Problem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checked++
	return f.problems, f.problemsErr
}

func (f *fakeComposes) RunRollout(ctx context.Context, t composes.DeployTarget, service string, opts composes.RolloutOptions) ([]string, error) {
	f.mu.Lock()
	f.rolledOut = append(f.rolledOut, service)
	hook := f.onRollout
	f.mu.Unlock()
	f.rec.add("rollout:%s", service)
	if opts.Deadline.IsZero() {
		return nil, errors.New("rollout without a deadline")
	}
	if hook != nil {
		return hook(ctx, service)
	}
	return nil, nil
}

func (f *fakeComposes) MarkDeployed(context.Context, composes.DeployTarget) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.marked++
	f.rec.add("marked")
	return nil
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

// Capture records the Services with build: as the real one does, which is what
// a Deployment's build steps are planned from.
func (f *fakeComposes) Capture(_ context.Context, t composes.DeployTarget) (composes.DeployTarget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t.Snapshot = &composes.ExecutionSnapshot{Services: f.services, Secrets: f.snapshotSecrets, ZeroDowntime: f.zeroDowntime, BuilderID: f.builderID}
	return t, nil
}

func (f *fakeComposes) PrepareBuild(_ context.Context, t composes.DeployTarget) (composes.BuildPlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prepared = append(f.prepared, t.Compose.YAMLContent)
	dir := composes.ComposeDir(t.Request)
	return composes.BuildPlan{
		Request:  composes.RunRequest{ProjectName: t.Request.ProjectName, ComposeFilePath: filepath.Join(dir, ".build-compose.yml"), Builder: t.Request.Builder},
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
	f.targets = append(f.targets, t)
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
	rec       *recorder
	onFetch   func(ctx context.Context, src sources.Source, dir string, out io.Writer) (sources.Ref, error)
	onResolve func(context.Context, sources.Source, string) (string, error)
}

func (f *fakeFetcher) ResolveRevision(ctx context.Context, src sources.Source, dir string) (string, error) {
	if f.onResolve != nil {
		return f.onResolve(ctx, src, dir)
	}
	return "abc123", nil
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
	// onAcquireVolumes runs before the exclusive volumes hold is granted.
	onAcquireVolumes func(ctx context.Context) error
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

func (g *fakeGate) AcquireVolumes(ctx context.Context, projectID int64) (func(), error) {
	g.rec.add("volumes+")
	if g.onAcquireVolumes != nil {
		if err := g.onAcquireVolumes(ctx); err != nil {
			g.rec.add("volumes-failed")
			return nil, err
		}
	}
	return func() { g.rec.add("volumes-") }, nil
}

type harness struct {
	t       *testing.T
	db      *sql.DB
	store   *Store
	srcs    *sources.Store
	rec     *recorder
	comp    *fakeComposes
	fetch   *fakeFetcher
	build   *fakeBuilder
	gate    *fakeGate
	pool    *fakePool // nil leaves Deps.Builders unset
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
		t: t, db: db, store: NewStore(db), srcs: sources.NewStore(db), rec: rec, a: a, b: b,
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
	d := Deps{
		Store: h.store, Sources: h.srcs, Composes: h.comp, Fetcher: h.fetch, Builder: h.build, Gate: h.gate,
		Timeout: h.timeout, Keep: h.keep,
	}
	if h.pool != nil {
		d.Builders = h.pool
	}
	h.q = NewQueue(d)
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
	key, err := sshkeys.NewStore(h.db).Create(context.Background(), fmt.Sprintf("harness-%d", id))
	if err != nil {
		h.t.Fatal(err)
	}
	err = h.srcs.Save(context.Background(), sources.Source{ComposeID: id, Kind: sources.KindGit, RepoURL: "git@example.com:a/b.git", Branch: "main", SSHKeyID: &key.ID})
	if err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) zipSource(id int64) {
	h.t.Helper()
	if err := os.MkdirAll(filepath.Join(h.comp.dirs[id], "src"), 0o755); err != nil {
		h.t.Fatal(err)
	}
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
	want := []string{"gate+", "gate-", "gate+", "gate-", "fetch", "build:web", "gate+", "deploy:1", "gate-", "fetch", "build:web", "gate+", "deploy:2", "gate-"}
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
	if len(h.build.opts) != 1 || len(h.comp.deployed) != 0 || h.rec.count("gate") != 2 {
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
	want := []string{"gate+", "gate-", "fetch", "build:web", "build:api", "gate+", "deploy:" + fmt.Sprint(h.a), "gate-"}
	if got := h.rec.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

// Only a fresh-volumes Deploy deletes volumes, so only it takes the exclusive
// volumes hold, after the gate and released before it.
func TestFreshVolumesDeployTakesTheVolumesHoldAfterTheGate(t *testing.T) {
	h := newHarness(t)
	h.newQueue()
	d, _, err := h.q.Enqueue(context.Background(), h.a, Request{Mode: ModeDeploy, FreshVolumes: true})
	if err != nil {
		t.Fatal(err)
	}
	h.start()
	h.waitSettled(h.a, d.Number)
	want := []string{"gate+", "gate-", "gate+", "volumes+", "deploy:" + fmt.Sprint(h.a), "volumes-", "gate-"}
	if got := h.rec.all(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestPlainDeployNeverTakesTheVolumesHold(t *testing.T) {
	h := newHarness(t)
	d := h.enqueue(h.a, ModeDeploy)
	h.start()
	h.waitSettled(h.a, d.Number)
	for _, event := range h.rec.all() {
		if strings.HasPrefix(event, "volumes") {
			t.Fatalf("a plain Deploy took the volumes hold: %v", h.rec.all())
		}
	}
}

// Waiting for the volumes hold is interruptible and gives the gate back.
func TestCancelWhileWaitingForTheVolumesHold(t *testing.T) {
	h := newHarness(t)
	h.newQueue()
	d, _, err := h.q.Enqueue(context.Background(), h.a, Request{Mode: ModeDeploy, FreshVolumes: true})
	if err != nil {
		t.Fatal(err)
	}
	waiting := make(chan struct{})
	h.gate.onAcquireVolumes = func(ctx context.Context) error {
		close(waiting)
		<-ctx.Done()
		return ctx.Err()
	}
	h.start()
	recv(t, waiting, "worker to wait on the volumes hold")
	h.q.CancelForComposes(context.Background(), []int64{h.a})
	if got := h.get(h.a, d.Number); got.Status != StatusCanceled {
		t.Fatalf("status = %s %q, want canceled", got.Status, got.Reason)
	}
	if len(h.comp.deployed) != 0 {
		t.Fatal("RunDeploy must not run")
	}
	events := h.rec.all()
	if events[len(events)-1] != "gate-" {
		t.Fatalf("events = %v, want the gate released last", events)
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
	if got := h.rec.all(); !reflect.DeepEqual(got, []string{"gate+", "gate-", "gate+", "gate-", "gate+", "deploy:" + fmt.Sprint(h.b), "gate-"}) {
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
	if h.rec.count("gate") != 2 || len(h.comp.deployed) != 0 {
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
	d := h.enqueue(h.a, ModeDeploy)
	waiting := make(chan struct{})
	h.gate.onAcquire = func(ctx context.Context) error {
		close(waiting)
		<-ctx.Done()
		return ctx.Err()
	}
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
	d := h.enqueue(h.a, ModeDeploy)
	waiting, proceed := make(chan struct{}), make(chan struct{})
	h.gate.onAcquire = func(ctx context.Context) error {
		close(waiting)
		<-proceed
		return nil
	}
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
	if want := []string{"gate+", "gate-", "gate+", "gate-"}; !reflect.DeepEqual(h.rec.all(), want) {
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

func TestSnapshotAtEnqueue(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	d := h.enqueue(h.a, ModeBuild)
	h.comp.mu.Lock()
	h.comp.yaml[h.a] = "edited after enqueue"
	h.comp.mu.Unlock()
	h.start()
	h.waitSettled(h.a, d.Number)
	if !reflect.DeepEqual(h.comp.prepared, []string{"yaml-a"}) {
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
	second, created, err := h.q.Enqueue(context.Background(), h.a, Request{Mode: ModeDeploy})
	if err != nil || created || second.Number != first.Number || second.Mode != ModeDeploy {
		t.Fatalf("second = %+v created=%v err=%v", second, created, err)
	}
	if _, _, err := h.q.Enqueue(context.Background(), h.b, Request{Mode: "bogus"}); err == nil {
		t.Fatal("bogus mode accepted")
	}
}

func TestEnqueueRefusesDifferentOptions(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name          string
		first, second Request
	}{
		{"fresh then plain", Request{Mode: ModeDeploy, FreshVolumes: true}, Request{Mode: ModeDeploy}},
		{"plain then fresh", Request{Mode: ModeDeploy}, Request{Mode: ModeDeploy, FreshVolumes: true}},
		{"noCache differs", Request{Mode: ModeBuild}, Request{Mode: ModeBuild, NoCache: true}},
		{"mode differs", Request{Mode: ModeDeploy}, Request{Mode: ModeSkipBuild}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			h.newQueue()
			first, created, err := h.q.Enqueue(ctx, h.a, c.first)
			if err != nil || !created {
				t.Fatal(err, created)
			}
			_, created, err = h.q.Enqueue(ctx, h.a, c.second)
			var conflict *QueuedConflictError
			if !errors.As(err, &conflict) || created {
				t.Fatalf("err = %v created = %v, want a QueuedConflictError", err, created)
			}
			if conflict.Queued.Number != first.Number || conflict.Queued.FreshVolumes != c.first.FreshVolumes ||
				conflict.Queued.NoCache != c.first.NoCache || conflict.Queued.Mode != c.first.Mode {
				t.Fatalf("conflict.Queued = %+v, want the first request's options", conflict.Queued)
			}
			if got := h.get(h.a, first.Number); got.FreshVolumes != c.first.FreshVolumes {
				t.Fatalf("queued Deployment changed: %+v", got)
			}
		})
	}

	t.Run("message", func(t *testing.T) {
		h := newHarness(t)
		h.newQueue()
		d, _, err := h.q.Enqueue(ctx, h.a, Request{Mode: ModeDeploy, FreshVolumes: true})
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = h.q.Enqueue(ctx, h.a, Request{Mode: ModeDeploy})
		want := fmt.Sprintf("deployment #%d is already queued with mode=deploy, noCache=false, freshVolumes=true; cancel it first", d.Number)
		if err == nil || err.Error() != want {
			t.Fatalf("err = %v, want %q", err, want)
		}
	})

	t.Run("same request twice", func(t *testing.T) {
		h := newHarness(t)
		h.newQueue()
		r := Request{Mode: ModeBuild, NoCache: true, FreshVolumes: true}
		first, created, err := h.q.Enqueue(ctx, h.a, r)
		if err != nil || !created {
			t.Fatal(err, created)
		}
		second, created, err := h.q.Enqueue(ctx, h.a, r)
		if err != nil || created || second.Number != first.Number {
			t.Fatalf("second = %+v created=%v err=%v", second, created, err)
		}
	})
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

func TestCancelOfOldNumberLeavesNewerQueued(t *testing.T) {
	h := newHarness(t)
	h.newQueue()
	ctx := context.Background()
	old := enqueue(t, h.store, h.a, t0)
	run(t, h.store, old, StatusSucceeded, t0)
	newer := h.enqueue(h.a, ModeDeploy)
	if err := h.q.Cancel(ctx, h.a, old.Number); !errors.Is(err, ErrNotCancelable) {
		t.Fatalf("Cancel(old) = %v, want ErrNotCancelable", err)
	}
	if got := h.get(h.a, newer.Number).Status; got != StatusQueued {
		t.Fatalf("newer = %s, want queued", got)
	}
}

func TestSourceEditsAfterEnqueueDoNotChangeRun(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	h.newQueue()
	unlock, ok := h.q.TryLockSource(h.a)
	if !ok {
		t.Fatal("lock refused")
	}
	var seen string
	h.fetch.onFetch = func(_ context.Context, src sources.Source, _ string, _ io.Writer) (sources.Ref, error) {
		seen = src.Branch
		return sources.Ref{SHA: "x"}, nil
	}
	d := h.enqueue(h.a, ModeBuild)
	h.start()
	eventually(t, "deployment to start", func() bool { return h.get(h.a, d.Number).Status == StatusRunning })
	err := h.srcs.Save(context.Background(), sources.Source{ComposeID: h.a, Kind: sources.KindGit, RepoURL: "git@example.com:a/b.git", Branch: "release"})
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if got := h.waitSettled(h.a, d.Number); got.Status != StatusSucceeded {
		t.Fatalf("status = %s %q", got.Status, got.Reason)
	}
	if seen != "main" {
		t.Fatalf("fetched branch %q, want captured main", seen)
	}
}

func TestDeployModeWithSourceNeverBuilds(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	d := h.enqueue(h.a, ModeDeploy)
	h.start()
	got := h.waitSettled(h.a, d.Number)
	if got.Status != StatusSucceeded || h.comp.deployed[0].Build {
		t.Fatalf("got %+v deploys %+v", got, h.comp.deployed)
	}
	text, _, err := ReadLog(LogPath(h.comp.dirs[h.a], d.Number), 0, 1<<20)
	if err != nil || !strings.Contains(text, "compose has a source; deploying without building") {
		t.Fatalf("log = %q, %v", text, err)
	}
}

func TestPanicInDeploySettlesFailedAndWorkerContinues(t *testing.T) {
	h := newHarness(t)
	h.comp.onDeploy = func(_ context.Context, id int64, _ composes.DeployOptions) ([]string, error) {
		if id == h.a {
			panic("boom")
		}
		return nil, nil
	}
	da := h.enqueue(h.a, ModeDeploy)
	db := h.enqueue(h.b, ModeDeploy)
	h.start()
	got := h.waitSettled(h.a, da.Number)
	if got.Status != StatusFailed || got.Reason != "internal error" {
		t.Fatalf("got %s %q", got.Status, got.Reason)
	}
	if next := h.waitSettled(h.b, db.Number); next.Status != StatusSucceeded {
		t.Fatalf("next = %s %q", next.Status, next.Reason)
	}
	if h.rec.count("gate+") != h.rec.count("gate-") {
		t.Fatalf("gate leaked: %v", h.rec.all())
	}
	if h.q.IsRunning(h.a) {
		t.Fatal("run state not cleared")
	}
}

func (h *harness) postDeployment(mode string) *httptest.ResponseRecorder {
	h.t.Helper()
	h.newQueue()
	mux := http.NewServeMux()
	NewAPI(h.q, apiComposes{h.comp}).RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	url := "/api/projects/1/environments/1/composes/" + strconv.FormatInt(h.a, 10) + "/deployments"
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, url, strings.NewReader(`{"mode":"`+mode+`"}`)))
	return rec
}

func TestBuildWithoutKeyIs400(t *testing.T) {
	h := newHarness(t)
	if err := h.srcs.Save(context.Background(), sources.Source{ComposeID: h.a, Kind: sources.KindGit, RepoURL: "git@example.com:a/b.git", Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	rec := h.postDeployment("build")
	var body map[string]string
	if rec.Code != http.StatusBadRequest || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["error"] != "Choose an SSH key for this Source" {
		t.Fatalf("build without key = %d %s", rec.Code, rec.Body)
	}
	if rec := h.postDeployment("skip-build"); rec.Code == http.StatusBadRequest {
		t.Fatalf("skip-build refused: %s", rec.Body)
	}
}

func TestZipBuildIsNotRefusedForKey(t *testing.T) {
	h := newHarness(t)
	h.zipSource(h.a)
	if rec := h.postDeployment("build"); rec.Code == http.StatusBadRequest {
		t.Fatalf("zip build refused: %s", rec.Body)
	}
}

func TestQueuedBuildKeepsKeyAfterDelete(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	src, err := h.srcs.Get(context.Background(), h.a)
	if err != nil || src.PrivateKey == "" {
		t.Fatalf("source key: %v %q", err, src.PrivateKey)
	}
	var got string
	h.fetch.onFetch = func(_ context.Context, s sources.Source, _ string, _ io.Writer) (sources.Ref, error) {
		got = s.PrivateKey
		return sources.Ref{SHA: "abc123"}, nil
	}
	d := h.enqueue(h.a, ModeBuild)
	if err := sshkeys.NewStore(h.db).Delete(context.Background(), *src.SSHKeyID); err != nil {
		t.Fatal(err)
	}
	h.start()
	if done := h.waitSettled(h.a, d.Number); done.Status != StatusSucceeded {
		t.Fatalf("status = %s %q", done.Status, done.Reason)
	}
	if got != src.PrivateKey {
		t.Fatalf("fetch saw key %q, want the queued one", got)
	}
}

func TestLegacyResumeWithoutKeyFailsAtFetch(t *testing.T) {
	h := newHarness(t)
	if err := h.srcs.Save(context.Background(), sources.Source{ComposeID: h.a, Kind: sources.KindGit, RepoURL: "git@example.com:a/b.git", Branch: "main"}); err != nil {
		t.Fatal(err)
	}
	d, _, err := h.store.Enqueue(context.Background(), h.a, ModeBuild, false, false, t0)
	if err != nil {
		t.Fatal(err)
	}
	h.start()
	got := h.waitSettled(h.a, d.Number)
	if got.Status != StatusFailed || got.FailedStep != "fetch" || got.Reason != "Choose an SSH key for this Source" {
		t.Fatalf("legacy no key = %+v", got)
	}
}

func TestGitBuildStoresAllStepsBeforeFetching(t *testing.T) {
	h := newHarness(t)
	h.gitSource(h.a)
	fetching, release := make(chan struct{}), make(chan struct{})
	h.fetch.onFetch = func(context.Context, sources.Source, string, io.Writer) (sources.Ref, error) {
		close(fetching)
		<-release
		return sources.Ref{SHA: "abc123", Subject: "first commit"}, nil
	}
	d := h.enqueue(h.a, ModeBuild)
	h.start()
	recv(t, fetching, "fetch to start")
	got := h.get(h.a, d.Number)
	close(release)
	if want := []string{"fetch", "build:web", "build:api", "deploy"}; !reflect.DeepEqual(got.Steps, want) {
		t.Fatalf("steps while fetching = %v, want %v", got.Steps, want)
	}
	if settled := h.waitSettled(h.a, d.Number); settled.Status != StatusSucceeded {
		t.Fatalf("status = %s %q", settled.Status, settled.Reason)
	}
}

func TestRolloutStepsRunInGateAndRecordWarnings(t *testing.T) {
	h := newHarness(t)
	h.comp.zeroDowntime = []string{"web"}
	h.comp.onRollout = func(context.Context, string) ([]string, error) {
		return []string{"web's Traefik labels changed"}, nil
	}
	d := h.enqueue(h.a, ModeDeploy)
	h.start()
	got := h.waitSettled(h.a, d.Number)
	if got.Status != StatusSucceeded || !reflect.DeepEqual(got.Steps, []string{"deploy", "rollout:web"}) {
		t.Fatalf("got %s %q steps %v", got.Status, got.Reason, got.Steps)
	}
	if !reflect.DeepEqual(got.Warnings, []string{"web's Traefik labels changed"}) {
		t.Fatalf("warnings = %v", got.Warnings)
	}
	if !reflect.DeepEqual(h.comp.deployed[0].Exclude, []string{"web"}) {
		t.Fatalf("deploy opts = %+v, want web excluded", h.comp.deployed[0])
	}
	events := strings.Join(h.rec.all(), ",")
	if want := fmt.Sprintf("gate+,deploy:%d,rollout:web,marked,gate-", h.a); !strings.Contains(events, want) {
		t.Fatalf("events = %s, want %s", events, want)
	}
}

func TestRolloutFailureFailsDeployment(t *testing.T) {
	h := newHarness(t)
	h.comp.zeroDowntime = []string{"web", "api"}
	h.comp.onRollout = func(context.Context, string) ([]string, error) {
		return nil, errors.New("web did not become healthy in 12s; kept the previous container")
	}
	d := h.enqueue(h.a, ModeDeploy)
	h.start()
	got := h.waitSettled(h.a, d.Number)
	if got.Status != StatusFailed || got.FailedStep != "rollout:web" || got.Reason != "web did not become healthy in 12s; kept the previous container" {
		t.Fatalf("got %+v", got)
	}
	if !reflect.DeepEqual(h.comp.rolledOut, []string{"web"}) || h.comp.marked != 0 {
		t.Fatalf("rolled out %v, marked %d", h.comp.rolledOut, h.comp.marked)
	}
	log, err := os.ReadFile(LogPath(h.comp.dirs[h.a], d.Number))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "not deployed: api\n") {
		t.Fatalf("log lacks the services left out:\n%s", log)
	}
}

// A timeout settles the Deployment as "timeout", but the log still says what
// the rollout did about it.
func TestRolloutTimeoutKeepsItsReasonInLog(t *testing.T) {
	h := newHarness(t)
	h.timeout = 50 * time.Millisecond
	h.comp.zeroDowntime = []string{"web"}
	h.comp.onRollout = func(ctx context.Context, _ string) ([]string, error) {
		<-ctx.Done()
		return nil, errors.New("web did not become healthy in 0s; could not remove the new container p-web-2: boom")
	}
	d := h.enqueue(h.a, ModeDeploy)
	h.start()
	got := h.waitSettled(h.a, d.Number)
	if got.Status != StatusFailed || got.Reason != "timeout" || got.FailedStep != "rollout:web" {
		t.Fatalf("got %s %q %q", got.Status, got.Reason, got.FailedStep)
	}
	log, err := os.ReadFile(LogPath(h.comp.dirs[h.a], d.Number))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(log), "could not remove the new container p-web-2: boom") {
		t.Fatalf("log lacks the rollout's own failure:\n%s", log)
	}
}

// A failed step keeps the warnings of the steps before it and its own.
func TestRolloutFailureKeepsWarnings(t *testing.T) {
	h := newHarness(t)
	h.comp.zeroDowntime = []string{"web"}
	h.comp.onDeploy = func(context.Context, int64, composes.DeployOptions) ([]string, error) {
		return []string{"file warning"}, nil
	}
	h.comp.onRollout = func(context.Context, string) ([]string, error) {
		return []string{"web's Traefik labels changed"}, errors.New("web did not become healthy in 12s; kept the previous container")
	}
	d := h.enqueue(h.a, ModeDeploy)
	h.start()
	got := h.waitSettled(h.a, d.Number)
	if got.Status != StatusFailed || !reflect.DeepEqual(got.Warnings, []string{"file warning", "web's Traefik labels changed"}) {
		t.Fatalf("got %s warnings %q", got.Status, got.Warnings)
	}
}

func TestCancelDuringRolloutRefused(t *testing.T) {
	h := newHarness(t)
	h.comp.zeroDowntime = []string{"web"}
	inRollout, release := make(chan struct{}), make(chan struct{})
	h.comp.onRollout = func(context.Context, string) ([]string, error) {
		close(inRollout)
		<-release
		return nil, nil
	}
	d := h.enqueue(h.a, ModeDeploy)
	h.start()
	recv(t, inRollout, "rollout to start")
	if err := h.q.Cancel(context.Background(), h.a, d.Number); !errors.Is(err, ErrNotCancelable) {
		t.Fatalf("Cancel err = %v, want ErrNotCancelable", err)
	}
	close(release)
	if got := h.waitSettled(h.a, d.Number).Status; got != StatusSucceeded {
		t.Fatalf("status = %s", got)
	}
}

func TestEnqueueRefusesZeroDowntimeProblems(t *testing.T) {
	h := newHarness(t)
	h.comp.zeroDowntime = []string{"web"}
	h.comp.problems = []zerodowntime.Problem{{Service: "web", Reasons: []string{"has no healthcheck"}}}
	h.newQueue()
	_, _, err := h.q.Enqueue(context.Background(), h.a, Request{Mode: ModeDeploy})
	var refused *ZeroDowntimeError
	if !errors.As(err, &refused) || !reflect.DeepEqual(refused.Problems, h.comp.problems) {
		t.Fatalf("err = %v, want a ZeroDowntimeError", err)
	}
	if list, _ := h.store.List(context.Background(), h.a, 10); len(list) != 0 {
		t.Fatalf("deployments = %+v, want none", list)
	}
}

func TestEnqueueFreshVolumesSkipsZeroDowntimeCheck(t *testing.T) {
	h := newHarness(t)
	h.comp.zeroDowntime = []string{"web"}
	h.comp.problems = []zerodowntime.Problem{{Service: "web", Reasons: []string{"has no healthcheck"}}}
	h.newQueue()
	if _, _, err := h.q.Enqueue(context.Background(), h.a, Request{Mode: ModeDeploy, FreshVolumes: true}); err != nil {
		t.Fatal(err)
	}
	if h.comp.checked != 0 {
		t.Fatalf("checked %d times, want 0", h.comp.checked)
	}
}

func TestCreateZeroDowntime422(t *testing.T) {
	h := newHarness(t)
	h.comp.zeroDowntime = []string{"web"}
	h.comp.problems = []zerodowntime.Problem{{Service: "web", Reasons: []string{"has no healthcheck"}}}
	rec := h.postDeployment("deploy")
	want := `{"error":"zero-downtime deploy is on for services that can't be rolled out","zeroDowntime":[{"service":"web","reasons":["has no healthcheck"]}]}`
	if rec.Code != http.StatusUnprocessableEntity || strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("create = %d %s, want 422 %s", rec.Code, rec.Body, want)
	}
}

func TestEnqueueConfigFailureIs400(t *testing.T) {
	h := newHarness(t)
	h.comp.zeroDowntime = []string{"web"}
	h.comp.problemsErr = &composes.DeployFailure{Kind: composes.FailureInvalid, Message: "compose config failed: variable X is not set"}
	rec := h.postDeployment("deploy")
	var body map[string]string
	if rec.Code != http.StatusBadRequest || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["error"] != "compose config failed: variable X is not set" {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
}
