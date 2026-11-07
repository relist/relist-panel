package registry

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// memRegistry is an in-memory registry API. onTags runs before the n-th
// Tags call of a repository (1-based), so a test can retag between selection
// and delete.
type memRegistry struct {
	tags      map[string]map[string]TagInfo // repo → tag → info
	deleted   []string                      // repo@digest
	deleteErr error
	tagCalls  map[string]int
	onTags    func(repo string, n int)
}

func newMemRegistry() *memRegistry {
	return &memRegistry{tags: map[string]map[string]TagInfo{}, tagCalls: map[string]int{}}
}

func (m *memRegistry) put(repo, tag, digest string, hour int) {
	if m.tags[repo] == nil {
		m.tags[repo] = map[string]TagInfo{}
	}
	m.tags[repo][tag] = TagInfo{Tag: tag, Digest: digest, Created: time.Date(2026, 11, 7, hour, 0, 0, 0, time.UTC)}
}

func (m *memRegistry) Repositories(context.Context) ([]string, error) {
	var out []string
	for repo := range m.tags {
		out = append(out, repo)
	}
	slices.Sort(out)
	return out, nil
}

func (m *memRegistry) Tags(_ context.Context, repo string) ([]string, error) {
	m.tagCalls[repo]++
	if m.onTags != nil {
		m.onTags(repo, m.tagCalls[repo])
	}
	var out []string
	for tag := range m.tags[repo] {
		out = append(out, tag)
	}
	slices.Sort(out)
	return out, nil
}

func (m *memRegistry) TagInfo(_ context.Context, repo, tag string) (TagInfo, error) {
	info, ok := m.tags[repo][tag]
	if !ok {
		return TagInfo{}, errors.New("no such tag")
	}
	return info, nil
}

func (m *memRegistry) Delete(_ context.Context, repo, digest string) error {
	if m.deleteErr != nil {
		return m.deleteErr
	}
	m.deleted = append(m.deleted, repo+"@"+digest)
	for tag, info := range m.tags[repo] {
		if info.Digest == digest {
			delete(m.tags[repo], tag)
		}
	}
	return nil
}

type fakeGate struct{ busy, locked atomic.Bool }

func (g *fakeGate) Busy() bool   { return g.busy.Load() }
func (g *fakeGate) Locked() bool { return g.locked.Load() }

type cleanerFixture struct {
	*svcFixture
	reg     *memRegistry
	gate    *fakeGate
	inUse   map[string]bool
	cleaner *Cleaner
}

func newCleaner(t *testing.T) *cleanerFixture {
	t.Helper()
	f := &cleanerFixture{svcFixture: newService(t), reg: newMemRegistry(), gate: &fakeGate{}, inUse: map[string]bool{}}
	f.enable(t)
	f.cleaner = NewCleaner(CleanerDeps{
		Service:  f.svc,
		Gate:     f.gate,
		InUse:    func(context.Context) (map[string]bool, error) { return f.inUse, nil },
		Registry: func(Settings) RegistryAPI { return f.reg },
		Poll:     time.Millisecond,
		GCPoll:   time.Millisecond,
		Now:      func() time.Time { return time.Date(2026, 11, 7, 3, 0, 0, 0, time.UTC) },
	})
	ctx := context.Background()
	c, _ := f.store.CleanupSettings(ctx)
	c.Keep = 1
	_ = f.store.SaveCleanupSettings(ctx, c)
	f.host.calls = nil
	return f
}

func (f *cleanerFixture) runOnce(t *testing.T) Run {
	t.Helper()
	ctx := context.Background()
	id, err := f.cleaner.RunNow(ctx, "user:admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.cleaner.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	runs, _ := f.store.Runs(ctx, 10)
	for _, r := range runs {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("run %d not recorded", id)
	return Run{}
}

func TestCleanupDeletesThenGCsReadOnly(t *testing.T) {
	f := newCleaner(t)
	f.reg.put("app", "v1", "d1", 1)
	f.reg.put("app", "v2", "d2", 2)
	f.host.volume, f.host.volumeAfterGC = 5000, 3000
	r := f.runOnce(t)
	if r.Result != RunOK || !slices.Equal(r.Deleted, []string{"app:v1"}) || r.FreedBytes != 2000 {
		t.Fatalf("run = %+v", r)
	}
	if !f.host.readOnlyAtGC {
		t.Fatal("GC ran while the registry was writable")
	}
	if ro, _ := f.files.ReadOnly(); ro {
		t.Fatal("registry left read-only")
	}
	if id, _ := f.store.GCRunning(context.Background()); id != 0 {
		t.Fatalf("gc_running = %d", id)
	}
}

func TestCleanupSkipsDigestRetaggedSinceSelection(t *testing.T) {
	f := newCleaner(t)
	f.reg.put("app", "v1", "d1", 1)
	f.reg.put("app", "v2", "d2", 2)
	f.reg.onTags = func(repo string, n int) {
		if n == 2 { // the re-list before delete: CI just pushed "hotfix" onto d1
			f.reg.put("app", "hotfix", "d1", 0)
		}
	}
	r := f.runOnce(t)
	if len(f.reg.deleted) != 0 || len(r.Deleted) != 0 {
		t.Fatalf("deleted %v (run %+v), want d1 skipped", f.reg.deleted, r)
	}
	if !strings.Contains(r.Log, "hotfix") {
		t.Fatalf("log = %q, want the skip explained", r.Log)
	}
}

func TestCleanupKeepsInUseTags(t *testing.T) {
	f := newCleaner(t)
	f.reg.put("app", "v1", "d1", 1)
	f.reg.put("app", "v2", "d2", 2)
	f.inUse["registry.example.com/app:v1"] = true
	r := f.runOnce(t)
	if len(f.reg.deleted) != 0 || r.Result != RunOK {
		t.Fatalf("deleted %v, run %+v", f.reg.deleted, r)
	}
}

func TestRetentionErrorSkipsGC(t *testing.T) {
	f := newCleaner(t)
	f.reg.put("app", "v1", "d1", 1)
	f.reg.put("app", "v2", "d2", 2)
	f.reg.deleteErr = errors.New("boom")
	r := f.runOnce(t)
	if r.Result != RunFailed || !strings.Contains(r.Log, "boom") || slices.Contains(f.host.calls, "RunGC") {
		t.Fatalf("run = %+v, calls = %v", r, f.host.calls)
	}
}

func TestGCFailureStillRestoresReadWrite(t *testing.T) {
	f := newCleaner(t)
	f.reg.put("app", "v1", "d1", 1)
	f.host.gcExit, f.host.gcLogs = 2, "panic: disk"
	r := f.runOnce(t)
	if r.Result != RunFailed || !strings.Contains(r.Log, "panic: disk") {
		t.Fatalf("run = %+v", r)
	}
	if ro, _ := f.files.ReadOnly(); ro {
		t.Fatal("registry left read-only after a failed GC")
	}
	if !slices.Contains(f.host.calls, "RemoveGC") {
		t.Fatalf("calls = %v, want RemoveGC", f.host.calls)
	}
}

func TestGCOnEmptyRegistryIsNothingToCollect(t *testing.T) {
	f := newCleaner(t)
	f.host.gcExit = 1
	f.host.gcLogs = "failed to mark: filesystem: Path not found: /docker/registry/v2/repositories"
	if r := f.runOnce(t); r.Result != RunOK {
		t.Fatalf("run = %+v, want ok", r)
	}
}

func interruptedRun(t *testing.T, f *cleanerFixture) int64 {
	t.Helper()
	ctx := context.Background()
	id, _ := f.store.StartRun(ctx, time.Now(), "schedule")
	_ = f.store.SetGCRunning(ctx, id)
	_ = f.files.Write(RenderConfig(true), []byte(""))
	return id
}

func TestStartupFinishesInterruptedGC(t *testing.T) {
	f := newCleaner(t)
	id := interruptedRun(t, f)
	f.host.gc = GCState{Exists: true, ExitCode: 0, Logs: "marked 3 blobs"}
	if err := f.cleaner.Startup(context.Background()); err != nil {
		t.Fatal(err)
	}
	runs, _ := f.store.Runs(context.Background(), 10)
	if runs[0].ID != id || runs[0].Result != RunInterrupted || runs[0].FinishedAt == nil {
		t.Fatalf("run = %+v", runs[0])
	}
	if ro, _ := f.files.ReadOnly(); ro {
		t.Fatal("still read-only")
	}
	if g, _ := f.store.GCRunning(context.Background()); g != 0 || !slices.Contains(f.host.calls, "Restart") || !slices.Contains(f.host.calls, "RemoveGC") {
		t.Fatalf("gc_running = %d, calls = %v", g, f.host.calls)
	}
}

func TestStartupWaitsForRunningGCContainer(t *testing.T) {
	f := newCleaner(t)
	interruptedRun(t, f)
	f.host.gc = GCState{Exists: true, Running: true}
	f.host.gcPolls = 3
	if err := f.cleaner.Startup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.host.gcPolls != 0 {
		t.Fatalf("restarted with the GC container still running (%d polls left)", f.host.gcPolls)
	}
	restart := slices.Index(f.host.calls, "Restart")
	if restart < 0 {
		t.Fatalf("calls = %v", f.host.calls)
	}
}

func TestRunNowBusy(t *testing.T) {
	f := newCleaner(t)
	f.gate.busy.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := f.cleaner.RunNow(ctx, "user:admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.cleaner.RunNow(ctx, "user:admin"); !errors.Is(err, ErrCleanupBusy) {
		t.Fatalf("second RunNow = %v, want ErrCleanupBusy", err)
	}
	f.gate.busy.Store(false)
	if err := f.cleaner.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWaitsWhileDeploymentBusy(t *testing.T) {
	f := newCleaner(t)
	f.reg.put("app", "v1", "d1", 1)
	f.reg.put("app", "v2", "d2", 2)
	f.gate.busy.Store(true)
	ctx := context.Background()
	if _, err := f.cleaner.RunNow(ctx, "user:admin"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if f.reg.tagCalls["app"] != 0 {
		t.Fatal("cleanup started while a Deployment runs")
	}
	f.gate.busy.Store(false)
	if err := f.cleaner.WaitIdle(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.reg.deleted) != 1 {
		t.Fatalf("deleted = %v after the Deployment finished", f.reg.deleted)
	}
}
