package directaccess

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/database"
)

type fakeHost struct {
	mu       sync.Mutex
	calls    []string
	started  []ProxySpec
	proxies  map[int64]bool // id -> running
	startErr error
	port     int
	extended map[int64]time.Time
	extendEr error
}

func newFakeHost() *fakeHost {
	return &fakeHost{proxies: map[int64]bool{}, port: 49153, extended: map[int64]time.Time{}}
}

func (f *fakeHost) StartProxy(_ context.Context, s ProxySpec) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "start "+ContainerName(s.AccessID))
	f.started = append(f.started, s)
	if f.startErr != nil {
		return 0, f.startErr
	}
	f.proxies[s.AccessID] = true
	if s.HostPort != 0 {
		return s.HostPort, nil
	}
	return f.port, nil
}

func (f *fakeHost) ListProxies(context.Context) ([]ProxyContainer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []ProxyContainer
	for id, run := range f.proxies {
		out = append(out, ProxyContainer{AccessID: id, Running: run})
	}
	return out, nil
}

func (f *fakeHost) RemoveProxy(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "remove "+ContainerName(id))
	delete(f.proxies, id)
	return nil
}

func (f *fakeHost) Extend(_ context.Context, id int64, t time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "extend "+ContainerName(id))
	if f.extendEr != nil {
		return f.extendEr
	}
	f.extended[id] = t
	return nil
}

func (f *fakeHost) saw(c string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.calls, c)
}

type fakeTargets struct {
	eps map[string]Endpoint // "kind:id"
}

func (f *fakeTargets) Resolve(_ context.Context, kind string, id int64) (Endpoint, error) {
	ep, ok := f.eps[kind+":"+itoa(id)]
	if !ok {
		return Endpoint{}, ErrTargetNotFound
	}
	return ep, nil
}

type rig struct {
	svc     *Service
	host    *fakeHost
	targets *fakeTargets
	store   *Store
	now     time.Time
	locked  bool
}

func newRig(t *testing.T) *rig {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	r := &rig{host: newFakeHost(), store: NewStore(db), now: time.Unix(1800000000, 0)}
	r.targets = &fakeTargets{eps: map[string]Endpoint{
		"database:7": {Name: "Shop", Network: "relist-network", Host: "relist-db-7", Port: 5432, Running: true},
	}}
	r.svc = New(Deps{Store: r.store, Host: r.host, Targets: r.targets,
		Locked: func(context.Context) bool { return r.locked },
		Now:    func() time.Time { return r.now }})
	return r
}

func (r *rig) open(t *testing.T) Access {
	t.Helper()
	a, err := r.svc.Open(context.Background(), OpenInput{TargetKind: KindDatabase, TargetID: 7, Channel: ChannelHostPort, Minutes: 60, OpenedBy: 2})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return a
}
