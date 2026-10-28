package vulnscan

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeScanner records calls and answers from its fields.
type fakeScanner struct {
	mu         sync.Mutex
	calls      []string
	ensureErr  error
	updateErr  error
	dbVersion  string // "" = no DB
	scanErr    map[string]error
	reports    map[string]Report
	scannerID  string
	onScan     func(imageID string)
	cacheBytes int64
	// blockScan makes Scan wait for its context, like a scan a shutdown interrupts.
	blockScan bool
	scanning  chan string
}

func newFakeScanner() *fakeScanner {
	return &fakeScanner{dbVersion: "db1", scanErr: map[string]error{}, reports: map[string]Report{}, scannerID: "sha256:scanner"}
}

func (f *fakeScanner) record(call string) {
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
}

func (f *fakeScanner) count(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == call {
			n++
		}
	}
	return n
}

func (f *fakeScanner) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeScanner) EnsureImage(_ context.Context, ref string) error {
	f.record("ensure " + ref)
	return f.ensureErr
}

func (f *fakeScanner) UpdateDB(context.Context, string) (DBInfo, error) {
	f.record("update")
	if f.updateErr != nil {
		return DBInfo{}, f.updateErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dbVersion == "" {
		f.dbVersion = "db1"
	}
	return DBInfo{Version: f.dbVersion}, nil
}

func (f *fakeScanner) DBInfo(context.Context, string) (DBInfo, error) {
	f.record("dbinfo")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dbVersion == "" {
		return DBInfo{}, ErrNoDB
	}
	return DBInfo{Version: f.dbVersion}, nil
}

func (f *fakeScanner) Scan(ctx context.Context, _ string, imageID string) (Report, error) {
	f.record("scan " + imageID)
	if f.blockScan {
		if f.scanning != nil {
			f.scanning <- imageID
		}
		<-ctx.Done()
		return Report{}, ctx.Err()
	}
	if f.onScan != nil {
		f.onScan(imageID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.scanErr[imageID]; err != nil {
		return Report{}, err
	}
	return f.reports[imageID], nil
}

func (f *fakeScanner) ImageID(context.Context, string) (string, error) { return f.scannerID, nil }

func (f *fakeScanner) RemoveImage(_ context.Context, ref string) error {
	f.record("remove " + ref)
	return nil
}

func (f *fakeScanner) DeleteCache(context.Context) error {
	f.record("deletecache")
	return nil
}

func (f *fakeScanner) CacheSize(context.Context) (int64, error) { return f.cacheBytes, nil }

func (f *fakeScanner) Cleanup(context.Context) error {
	f.record("cleanup")
	return nil
}

type fakeInUse struct {
	mu     sync.Mutex
	images []UsedImage
}

func (f *fakeInUse) Images(context.Context) ([]UsedImage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]UsedImage(nil), f.images...), nil
}

type fakeGate struct{ busy atomic.Bool }

func (g *fakeGate) Busy() bool { return g.busy.Load() }

var errBoom = errors.New("boom")

type harness struct {
	t       *testing.T
	store   *Store
	scanner *fakeScanner
	inUse   *fakeInUse
	gate    *fakeGate
	svc     *Service
	cancel  context.CancelFunc
	done    chan struct{}
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, store: NewStore(newDB(t)), scanner: newFakeScanner(), inUse: &fakeInUse{}, gate: &fakeGate{}}
	h.inUse.images = []UsedImage{
		{ImageID: "sha256:app", Refs: []string{"app:1"}, Users: []ImageUser{{Kind: UserService, ProjectID: 1, EnvironmentID: 2, ComposeID: 3, Service: "web"}}},
		{ImageID: "sha256:other", Refs: []string{"other:1"}, Users: []ImageUser{{Kind: UserService, ProjectID: 1, EnvironmentID: 2, ComposeID: 4, Service: "db"}}},
	}
	h.svc = New(Deps{Store: h.store, Scanner: h.scanner, InUse: h.inUse, Gate: h.gate, Now: func() time.Time { return t0 }, Tick: 5 * time.Millisecond})
	return h
}

func (h *harness) enable() {
	h.t.Helper()
	st, _ := h.store.Settings(context.Background())
	st.Enabled, st.AfterDeploy = true, true
	if err := h.store.SaveSettings(context.Background(), st); err != nil {
		h.t.Fatal(err)
	}
	h.store.SetActiveImage(context.Background(), st.EffectiveImage())
	h.store.SetDB(context.Background(), "db1", t0, "")
}

func (h *harness) start() {
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel, h.done = cancel, make(chan struct{})
	go func() {
		defer close(h.done)
		h.svc.Run(ctx)
	}()
	h.t.Cleanup(h.stop)
}

func (h *harness) stop() {
	if h.cancel == nil {
		return
	}
	h.cancel()
	select {
	case <-h.done:
	case <-time.After(5 * time.Second):
		h.t.Error("Run did not return")
	}
	h.cancel = nil
}

// eventually polls cond for up to two seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (h *harness) queueEmpty() bool {
	running, queued, err := h.store.QueueState(context.Background())
	return err == nil && running == nil && queued == 0
}
