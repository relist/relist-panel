package deployments

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

	"relist-panel/internal/composes"
	"relist-panel/internal/sources"
	"relist-panel/internal/sshkeys"
)

type fakeMounts struct {
	mu       sync.Mutex
	paths    func() []string
	err      error
	projects []string
}

func (m *fakeMounts) MountedSources(_ context.Context, project string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.projects = append(m.projects, project)
	if m.err != nil {
		return nil, m.err
	}
	if m.paths == nil {
		return nil, nil
	}
	return m.paths(), nil
}

// hookRunner runs onUp at the start of every Up.
type hookRunner struct {
	*snapshotRunner
	onUp func()
}

func (r *hookRunner) Up(ctx context.Context, req composes.RunRequest, opts composes.UpOptions) error {
	if r.onUp != nil {
		r.onUp()
	}
	return r.snapshotRunner.Up(ctx, req, opts)
}

type gcFixture struct {
	t       *testing.T
	db      *sql.DB
	id      int64
	runner  *hookRunner
	mounts  *fakeMounts
	gate    *fakeGate
	q       *Queue
	dir     string
	sources *sources.Store
	svc     *composes.Service
}

// newGCFixture is a Compose with a ZIP Source whose ./src is bind mounted,
// run through the real composes.Service.
func newGCFixture(t *testing.T) *gcFixture {
	t.Helper()
	db, id, _ := newDB(t)
	f := &gcFixture{t: t, db: db, id: id, runner: &hookRunner{snapshotRunner: &snapshotRunner{}}, mounts: &fakeMounts{}, gate: &fakeGate{rec: &recorder{}}}
	f.mounts.paths = f.latestMount
	f.svc = composes.New(db, f.runner, t.TempDir())
	content := "services:\n  web:\n    image: sample\n    volumes: ['./src:/app']\n"
	if _, err := db.Exec(`UPDATE composes SET yaml_content=?, identity_version=1 WHERE id=?`, content, id); err != nil {
		t.Fatal(err)
	}
	target, err := f.svc.Target(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	f.dir = composes.ComposeDir(target.Request)
	if err := os.MkdirAll(filepath.Join(f.dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, "src", "app.txt"), []byte("code"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.sources = sources.NewStore(db)
	if err := f.sources.Save(context.Background(), sources.Source{ComposeID: id, Kind: sources.KindZip, ZipName: "source.zip"}); err != nil {
		t.Fatal(err)
	}
	f.q = NewQueue(Deps{Store: NewStore(db), Sources: f.sources, Composes: f.svc, Fetcher: &countedGit{}, Builder: f.runner, Gate: f.gate, Mounts: f.mounts})
	return f
}

// latestMount is what the newest container mounts: the last path Up saw.
func (f *gcFixture) latestMount() []string {
	if n := len(f.runner.mountedPaths); n > 0 {
		return []string{f.runner.mountedPaths[n-1]}
	}
	return nil
}

func (f *gcFixture) deploy(mode Mode) {
	f.t.Helper()
	d, _, err := f.q.Enqueue(context.Background(), f.id, Request{Mode: mode})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.q.process(context.Background(), d); err != nil {
		f.t.Fatal(err)
	}
}

func (f *gcFixture) snapshotDirs() []string {
	f.t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.dir, sourceSnapshotsDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		f.t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func (f *gcFixture) leftover(name string) string {
	f.t.Helper()
	path := filepath.Join(f.dir, sourceSnapshotsDir, name)
	if err := os.MkdirAll(filepath.Join(path, "src"), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "src", "app.txt"), []byte("old"), 0o644); err != nil {
		f.t.Fatal(err)
	}
	return path
}

func TestSnapshotGCKeepsOnlyReferencedDirs(t *testing.T) {
	f := newGCFixture(t)
	for i := 1; i <= 6; i++ {
		f.deploy(ModeSkipBuild)
		dirs := f.snapshotDirs()
		if len(dirs) != 1 {
			t.Fatalf("after deploy %d: %v, want exactly the mounted snapshot", i, dirs)
		}
		mounted := f.runner.mountedPaths[len(f.runner.mountedPaths)-1]
		if want := filepath.Join(f.dir, sourceSnapshotsDir, dirs[0], "src"); mounted != want {
			t.Fatalf("after deploy %d kept %s, mounted %s", i, want, mounted)
		}
		if _, err := os.Stat(filepath.Join(mounted, "app.txt")); err != nil {
			t.Fatalf("mounted tree damaged: %v", err)
		}
	}
	target, _ := f.svc.Target(context.Background(), f.id)
	if got := f.mounts.projects; len(got) == 0 || got[0] != target.Request.ProjectName {
		t.Fatalf("mounts asked for projects %v, want %q", got, target.Request.ProjectName)
	}
}

func TestSnapshotGCKeepsQueuedSnapshot(t *testing.T) {
	f := newGCFixture(t)
	f.mounts.paths = nil
	var queued Deployment
	f.runner.onUp = func() {
		f.runner.onUp = nil
		var err error
		if queued, _, err = f.q.Enqueue(context.Background(), f.id, Request{Mode: ModeSkipBuild}); err != nil {
			t.Error(err)
		}
	}
	f.deploy(ModeSkipBuild)
	if queued.ID == 0 {
		t.Fatal("no Deployment was queued while the first ran")
	}
	snapshot, err := f.q.d.Store.snapshot(context.Background(), queued.ID)
	if err != nil || snapshot == nil {
		t.Fatalf("snapshot = %v, %v", snapshot, err)
	}
	dirs := f.snapshotDirs()
	if len(dirs) != 1 || filepath.Join(f.dir, sourceSnapshotsDir, dirs[0]) != snapshot.SourceDir {
		t.Fatalf("dirs = %v, want only the queued Deployment's %s", dirs, snapshot.SourceDir)
	}
	// It runs next, with its files intact.
	if err := f.q.process(context.Background(), queued); err != nil {
		t.Fatal(err)
	}
	if len(f.runner.contents) != 2 || f.runner.contents[1] != "code" {
		t.Fatalf("queued Deployment ran with %v", f.runner.contents)
	}
}

func TestSnapshotGCKeepsMaterializedTree(t *testing.T) {
	f := newGCFixture(t)
	work := t.TempDir()
	localGit(t, work, "init")
	if err := os.WriteFile(filepath.Join(work, "app.txt"), []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	localGit(t, work, "add", ".")
	localGit(t, work, "commit", "-m", "first")
	key, err := sshkeys.NewStore(f.db).Create(context.Background(), "gc")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.sources.Save(context.Background(), sources.Source{ComposeID: f.id, Kind: sources.KindGit, RepoURL: "file://" + work, Branch: "main", SSHKeyID: &key.ID}); err != nil {
		t.Fatal(err)
	}
	f.q.d.Fetcher = &countedGit{}
	f.mounts.paths = nil
	f.deploy(ModeBuild)
	built, err := f.sources.Get(context.Background(), f.id)
	if err != nil || built.TreeDirectory == "" {
		t.Fatalf("no materialization recorded: %+v %v", built, err)
	}
	if dirs := f.snapshotDirs(); len(dirs) != 1 || filepath.Join(f.dir, sourceSnapshotsDir, dirs[0]) != filepath.Dir(built.TreeDirectory) {
		t.Fatalf("dirs = %v, want only the one holding %s", dirs, built.TreeDirectory)
	}
	// A skip-build Deployment copies from it, and both survive while
	// the new one is what runs.
	f.mounts.paths = f.latestMount
	f.deploy(ModeSkipBuild)
	if dirs := f.snapshotDirs(); len(dirs) != 2 {
		t.Fatalf("dirs = %v, want the materialized tree and the mounted copy", dirs)
	}
	if content, err := os.ReadFile(filepath.Join(built.TreeDirectory, "app.txt")); err != nil || string(content) != "first" {
		t.Fatalf("materialized tree = %q, %v", content, err)
	}
}

func TestSnapshotGCSkipsOnMountError(t *testing.T) {
	f := newGCFixture(t)
	f.leftover("source-old1")
	f.leftover("source-old2")
	f.mounts.err = errors.New("docker unreachable")
	f.q.collectSnapshots(context.Background(), f.id)
	if dirs := f.snapshotDirs(); len(dirs) != 2 {
		t.Fatalf("dirs = %v, want nothing deleted", dirs)
	}
	f.mounts.err = nil
	f.q.collectSnapshots(context.Background(), f.id)
	if dirs := f.snapshotDirs(); len(dirs) != 0 {
		t.Fatalf("dirs = %v, want both removed once Docker answers", dirs)
	}
}

func TestSnapshotGCGivesUpOnAHungDocker(t *testing.T) {
	old := snapshotLookupTimeout
	snapshotLookupTimeout = 50 * time.Millisecond
	defer func() { snapshotLookupTimeout = old }()
	f := newGCFixture(t)
	f.leftover("source-old")
	f.q.d.Mounts = hungMounts{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.q.collectSnapshots(context.Background(), f.id)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("collectSnapshots hung with Docker")
	}
	if dirs := f.snapshotDirs(); len(dirs) != 1 {
		t.Fatalf("dirs = %v, want nothing deleted", dirs)
	}
}

type hungMounts struct{}

func (hungMounts) MountedSources(ctx context.Context, _ string) ([]string, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestSnapshotGCKeepsDirsContainingAMountedSource(t *testing.T) {
	f := newGCFixture(t)
	root := filepath.Join(f.dir, sourceSnapshotsDir)
	mounted := f.leftover("source-mounted")
	deeper := f.leftover("source-deeper")
	f.leftover("source-stale")
	f.mounts.paths = func() []string {
		return []string{filepath.Join(mounted, "src"), filepath.Join(deeper, "src", "app.txt") + "/", "/var/lib/docker/volumes/x/_data"}
	}
	f.q.collectSnapshots(context.Background(), f.id)
	got := map[string]bool{}
	for _, d := range f.snapshotDirs() {
		got[d] = true
	}
	if len(got) != 2 || !got["source-mounted"] || !got["source-deeper"] {
		t.Fatalf("dirs under %s = %v", root, got)
	}
}

func TestSnapshotGCSkipsOnDatabaseError(t *testing.T) {
	f := newGCFixture(t)
	f.leftover("source-old")
	if _, err := f.db.Exec(`INSERT INTO deployments (compose_id, number, mode, status, queued_at) VALUES (?, 1, 'skip-build', 'queued', 1)`, f.id); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := f.db.QueryRow(`SELECT id FROM deployments WHERE compose_id=?`, f.id).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO deployment_snapshots (deployment_id, snapshot) VALUES (?, 'not json')`, id); err != nil {
		t.Fatal(err)
	}
	f.q.collectSnapshots(context.Background(), f.id)
	if dirs := f.snapshotDirs(); len(dirs) != 1 {
		t.Fatalf("dirs = %v, want nothing deleted when a queued snapshot cannot be read", dirs)
	}
}

func TestSnapshotGCOnlyRemovesSnapshotDirectories(t *testing.T) {
	f := newGCFixture(t)
	root := filepath.Join(f.dir, sourceSnapshotsDir)
	f.leftover("source-old")
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "precious"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "source-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "other"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source-file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.mounts.paths = nil
	f.q.collectSnapshots(context.Background(), f.id)
	names := strings.Join(f.snapshotDirs(), ",")
	if names != "other,source-file,source-link" {
		t.Fatalf("entries = %s", names)
	}
	if _, err := os.Stat(filepath.Join(outside, "precious")); err != nil {
		t.Fatalf("GC followed a symlink: %v", err)
	}
}

func TestSnapshotGCIgnoresAComposeWithoutSnapshots(t *testing.T) {
	f := newGCFixture(t)
	f.q.collectSnapshots(context.Background(), f.id)
	f.q.collectSnapshots(context.Background(), 9999)
	if _, err := os.Stat(filepath.Join(f.dir, sourceSnapshotsDir)); !os.IsNotExist(err) {
		t.Fatalf("GC created %s: %v", sourceSnapshotsDir, err)
	}
}

func TestSnapshotGCWaitsForTheProjectGate(t *testing.T) {
	f := newGCFixture(t)
	f.leftover("source-old")
	held := make(chan struct{})
	entered := make(chan struct{}, 1)
	f.gate.onAcquire = func(ctx context.Context) error {
		entered <- struct{}{}
		select {
		case <-held:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.q.collectSnapshots(context.Background(), f.id)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("collectSnapshots never asked for the Project gate")
	}
	select {
	case <-done:
		t.Fatal("collectSnapshots finished while the gate was held")
	case <-time.After(100 * time.Millisecond):
	}
	if dirs := f.snapshotDirs(); len(dirs) != 1 {
		t.Fatalf("dirs = %v, want nothing deleted while the gate is held", dirs)
	}
	close(held)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("collectSnapshots did not run after the gate was released")
	}
	if dirs := f.snapshotDirs(); len(dirs) != 0 {
		t.Fatalf("dirs = %v, want the leftover removed", dirs)
	}
}

func TestSnapshotGCWaitsForTheProjectVolumes(t *testing.T) {
	f := newGCFixture(t)
	f.leftover("source-old")
	held := make(chan struct{})
	entered := make(chan struct{}, 1)
	f.gate.onAcquireVolumes = func(ctx context.Context) error {
		entered <- struct{}{}
		select {
		case <-held:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.q.collectSnapshots(context.Background(), f.id)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("collectSnapshots never asked for the Project's volumes")
	}
	select {
	case <-done:
		t.Fatal("collectSnapshots finished while a Volume read held the volumes")
	case <-time.After(100 * time.Millisecond):
	}
	if dirs := f.snapshotDirs(); len(dirs) != 1 {
		t.Fatalf("dirs = %v, want nothing deleted while the volumes are held", dirs)
	}
	close(held)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("collectSnapshots did not run after the volumes were released")
	}
	if dirs := f.snapshotDirs(); len(dirs) != 0 {
		t.Fatalf("dirs = %v, want the leftover removed", dirs)
	}
	// Gate first, then volumes; both released, the volumes first.
	events := strings.Join(f.gate.rec.all(), ",")
	if !strings.Contains(events, "gate+,volumes+,volumes-,gate-") {
		t.Fatalf("events = %s, want the gate taken before the volumes and released after", events)
	}
}

func TestSnapshotGCSkipsWhenTheVolumesTimeOut(t *testing.T) {
	old := snapshotLookupTimeout
	snapshotLookupTimeout = 50 * time.Millisecond
	defer func() { snapshotLookupTimeout = old }()
	f := newGCFixture(t)
	f.leftover("source-old")
	f.gate.onAcquireVolumes = func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}
	f.q.collectSnapshots(context.Background(), f.id)
	if dirs := f.snapshotDirs(); len(dirs) != 1 {
		t.Fatalf("dirs = %v, want nothing deleted", dirs)
	}
	events := f.gate.rec.all()
	if len(events) == 0 || events[len(events)-1] != "gate-" {
		t.Fatalf("events = %v, want the gate released", events)
	}
}

func TestSnapshotGCSkipsWhenTheGateFails(t *testing.T) {
	f := newGCFixture(t)
	f.leftover("source-old")
	f.gate.onAcquire = func(context.Context) error { return errors.New("gate unavailable") }
	f.q.collectSnapshots(context.Background(), f.id)
	if dirs := f.snapshotDirs(); len(dirs) != 1 {
		t.Fatalf("dirs = %v, want nothing deleted", dirs)
	}
}

func TestRunCollectsSnapshotsAtStart(t *testing.T) {
	f := newGCFixture(t)
	f.mounts.paths = nil
	f.leftover("source-previous-run")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.q.Run(ctx)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(f.snapshotDirs()) != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if dirs := f.snapshotDirs(); len(dirs) != 0 {
		t.Fatalf("dirs = %v, want the previous run's leftovers removed", dirs)
	}
}

// firstCallBlocksMounts blocks its first MountedSources call until released or
// canceled, as a hung Docker daemon would; later calls return at once.
type firstCallBlocksMounts struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (m *firstCallBlocksMounts) MountedSources(ctx context.Context, _ string) ([]string, error) {
	block := false
	m.once.Do(func() { block = true })
	if !block {
		return nil, nil
	}
	close(m.entered)
	select {
	case <-m.release:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestBootCollectionDoesNotDelayFirstDeployment(t *testing.T) {
	f := newGCFixture(t)
	f.leftover("source-previous-run")
	mounts := &firstCallBlocksMounts{entered: make(chan struct{}), release: make(chan struct{})}
	f.q.d.Mounts = mounts
	d, _, err := f.q.Enqueue(context.Background(), f.id, Request{Mode: ModeDeploy})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.q.Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()
	select {
	case <-mounts.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("boot collection never asked for the mounts")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := f.q.d.Store.Get(context.Background(), f.id, d.Number)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == StatusSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("deployment = %s, want it to run while boot collection waits on Docker", got.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(mounts.release)
}
