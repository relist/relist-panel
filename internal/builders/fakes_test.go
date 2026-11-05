package builders

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fakeEngine records every call as "Method arg" and answers from its fields.
type fakeEngine struct {
	mu          sync.Mutex
	calls       []string
	specs       []HostSpec
	inspection  Inspection
	ensureErr   error
	pullErr     error
	cpus        int
	memory      int64
	capacityErr error
	images      map[string]bool
	diskUsage   int64
	recreateHit chan struct{} // when set, Recreate blocks until it is closed
	ensureHit   chan struct{} // when set, Ensure blocks until it is closed
	pruneErr    error
	removeErr   error
}

func newFakeEngine() *fakeEngine {
	return &fakeEngine{inspection: Inspection{State: StateMissing}, cpus: 16, memory: 31900 << 20, images: map[string]bool{}}
}

func (f *fakeEngine) record(call string, spec *HostSpec) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	if spec != nil {
		f.specs = append(f.specs, *spec)
	}
}

func (f *fakeEngine) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeEngine) count(method string) int {
	n := 0
	for _, c := range f.Calls() {
		if c == method || len(c) > len(method) && c[:len(method)+1] == method+" " {
			n++
		}
	}
	return n
}

func (f *fakeEngine) lastSpec() HostSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.specs[len(f.specs)-1]
}

func (f *fakeEngine) Ensure(_ context.Context, spec HostSpec) error {
	f.record("Ensure "+spec.Name, &spec)
	if f.ensureHit != nil {
		<-f.ensureHit
	}
	return f.ensureErr
}

func (f *fakeEngine) Recreate(_ context.Context, spec HostSpec) error {
	f.record("Recreate "+spec.Name, &spec)
	if f.recreateHit != nil {
		<-f.recreateHit
	}
	return f.ensureErr
}

func (f *fakeEngine) Remove(_ context.Context, name string) error {
	f.record("Remove "+name, nil)
	return f.removeErr
}

func (f *fakeEngine) Inspect(_ context.Context, name string) (Inspection, error) {
	f.record("Inspect "+name, nil)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.inspection, nil
}

func (f *fakeEngine) Pull(_ context.Context, ref string) error {
	f.record("Pull "+ref, nil)
	return f.pullErr
}

func (f *fakeEngine) DiskUsage(_ context.Context, name string) (int64, error) {
	f.record("DiskUsage "+name, nil)
	return f.diskUsage, nil
}

func (f *fakeEngine) Prune(_ context.Context, name string) error {
	f.record("Prune "+name, nil)
	return f.pruneErr
}

func (f *fakeEngine) Capacity(context.Context) (int, int64, error) {
	return f.cpus, f.memory, f.capacityErr
}

func (f *fakeEngine) ImageExists(_ context.Context, ref string) (bool, error) {
	f.record("ImageExists "+ref, nil)
	return f.images[ref], nil
}

type fakeDeployments struct{ uses map[int64]bool }

func (f *fakeDeployments) UsesBuilder(_ context.Context, id int64) (bool, error) {
	return f.uses[id], nil
}

type serviceFixture struct {
	*fixture
	svc    *Service
	engine *fakeEngine
	deps   *fakeDeployments
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	f := newFixture(t)
	e := newFakeEngine()
	d := &fakeDeployments{uses: map[int64]bool{}}
	return &serviceFixture{fixture: f, engine: e, deps: d, svc: New(Deps{Store: f.store, Engine: e, Deployments: d, Now: func() time.Time { return t0 }})}
}
