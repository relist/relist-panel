package migration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/composes"
	"relist-panel/internal/domains"
)

type fakeQueue struct {
	mu        sync.Mutex
	canceled  []string
	waited    int
	waitBlock chan struct{}
	events    []string // hold, cancel and release, in order
}

func (q *fakeQueue) Hold() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.events = append(q.events, "hold")
}

func (q *fakeQueue) Release() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.events = append(q.events, "release")
}

func (q *fakeQueue) eventList() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Clone(q.events)
}

func (q *fakeQueue) CancelAllQueued(_ context.Context, reason string) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.canceled = append(q.canceled, reason)
	q.events = append(q.events, "cancel")
	return 1, nil
}

func (q *fakeQueue) WaitIdle(ctx context.Context) error {
	if q.waitBlock != nil {
		select {
		case <-q.waitBlock:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	q.mu.Lock()
	q.waited++
	q.mu.Unlock()
	return nil
}

func (q *fakeQueue) Running() (int64, int, bool) { return 0, 0, false }

// statusFromDocker reports a Compose running when the fake says so.
type statusFromDocker struct{ d *FakeDocker }

func (s statusFromDocker) Status(_ context.Context, req composes.RunRequest) (composes.ComposeStatus, error) {
	s.d.mu.Lock()
	defer s.d.mu.Unlock()
	if s.d.Running[req.ProjectName] {
		return composes.ComposeStatus{Running: 1, Total: 1}, nil
	}
	return composes.ComposeStatus{Total: 1}, nil
}

type fakeDomains []domains.Domain

func (f fakeDomains) ListAll(context.Context) ([]domains.Domain, error) { return f, nil }

type sourceFixture struct {
	*manifestFixture
	store *Store
	queue *fakeQueue
	src   *OldHost
	a, b  int64
}

func newSourceFixture(t *testing.T, ds ...domains.Domain) *sourceFixture {
	t.Helper()
	m := newManifestFixture(t)
	f := &sourceFixture{manifestFixture: m, store: NewStore(m.db, newClock().now), queue: &fakeQueue{}}
	f.a = m.compose("api", "services: {}", `{"services":{"api":{"image":"nginx"}}}`, true)
	f.b = m.compose("worker", "services: {}", `{"services":{"w":{"image":"nginx"}}}`, true)
	m.docker.Running[fmt.Sprintf("relist-c%d", f.a)] = true
	f.src = NewOldHost(OldHostDeps{Store: f.store, Manifests: m.builder(), Queue: f.queue, Docker: m.docker,
		Status: statusFromDocker{m.docker}, Composes: m.targets, Domains: fakeDomains(ds)})
	return f
}

var httpsDomain = domains.Domain{ID: 3, ComposeID: 1, Host: "shop.example.com", Path: "/api", Port: 80, HTTPS: true, CertProvider: "letsencrypt"}

func TestStartRecordsRunningThenStops(t *testing.T) {
	f := newSourceFixture(t, httpsDomain)
	m, err := f.src.Start(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(m.RunningComposeIDs, []int64{f.a}) {
		t.Fatalf("running = %v, want [%d]", m.RunningComposeIDs, f.a)
	}
	if got := f.docker.CallsOf("compose-stop"); !slices.Equal(got, []string{fmt.Sprintf("compose-stop relist-c%d", f.a)}) {
		t.Fatalf("stops = %q", got)
	}
	if !f.docker.MaintenanceUp {
		t.Fatal("maintenance container not started")
	}
	step, st, _ := f.store.Lock(context.Background())
	if step != StepReady || st.Manifest == nil {
		t.Fatalf("lock = %q, manifest %v", step, st.Manifest)
	}
}

func TestStartCancelsQueuedAndWaitsForRunning(t *testing.T) {
	f := newSourceFixture(t)
	f.queue.waitBlock = make(chan struct{})
	done := make(chan error, 1)
	go func() { _, err := f.src.Start(context.Background(), nil); done <- err }()
	eventuallyTrue(t, "the lock to reach stopping", func() bool {
		step, _, _ := f.store.Lock(context.Background())
		return step == StepStopping
	})
	if len(f.docker.CallsOf("compose-stop")) != 0 {
		t.Fatal("Composes stopped before the running Deployment ended")
	}
	f.queue.mu.Lock()
	canceled := slices.Clone(f.queue.canceled)
	f.queue.mu.Unlock()
	if !slices.Equal(canceled, []string{"server migration"}) {
		t.Fatalf("canceled = %q", canceled)
	}
	close(f.queue.waitBlock)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestStartIsIdempotent(t *testing.T) {
	f := newSourceFixture(t)
	first, err := f.src.Start(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.src.Start(context.Background(), []string{"/ignored"})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(first) != fmt.Sprint(second) || len(f.docker.CallsOf("compose-stop")) != 1 {
		t.Fatalf("second Start changed things: stops %q", f.docker.CallsOf("compose-stop"))
	}
}

func TestResumeLockAtBootRecreatesMaintenance(t *testing.T) {
	f := newSourceFixture(t, httpsDomain)
	if _, err := f.src.Start(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	f.docker.MaintenanceUp = false
	f.docker.Running[fmt.Sprintf("relist-c%d", f.a)] = true // restart policy brought it back
	booted := NewOldHost(OldHostDeps{Store: f.store, Manifests: f.builder(), Queue: f.queue, Docker: f.docker,
		Status: statusFromDocker{f.docker}, Composes: f.targets, Domains: fakeDomains{httpsDomain}})
	if err := booted.ResumeLock(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.docker.MaintenanceUp {
		t.Fatal("maintenance container not recreated")
	}
	if f.docker.Running[fmt.Sprintf("relist-c%d", f.a)] {
		t.Fatal("a Compose that came back up was not stopped again")
	}
	if len(f.docker.CallsOf("compose-start")) != 0 {
		t.Fatal("ResumeLock started a Compose")
	}
}

func TestResumeLockContinuesAnInterruptedStart(t *testing.T) {
	f := newSourceFixture(t)
	if err := f.store.SetLock(context.Background(), StepLocked, LockState{}); err != nil {
		t.Fatal(err)
	}
	if err := f.src.ResumeLock(context.Background()); err != nil {
		t.Fatal(err)
	}
	if step, _, _ := f.store.Lock(context.Background()); step != StepReady {
		t.Fatalf("step = %q, want ready", step)
	}
}

func TestResumeStartsWhatWasRunningAndClears(t *testing.T) {
	f := newSourceFixture(t, httpsDomain)
	if _, err := f.src.Start(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := f.src.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.docker.CallsOf("compose-start"); !slices.Equal(got, []string{fmt.Sprintf("compose-start relist-c%d", f.a)}) {
		t.Fatalf("starts = %q", got)
	}
	if f.docker.MaintenanceUp {
		t.Fatal("maintenance container left running")
	}
	if step, _, _ := f.store.Lock(context.Background()); step != StepNone {
		t.Fatalf("step = %q, want none", step)
	}
}

func TestMiddlewareAllowlist(t *testing.T) {
	f := newSourceFixture(t)
	if err := f.store.SetLock(context.Background(), StepLocked, LockState{}); err != nil {
		t.Fatal(err)
	}
	handler := f.src.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	cases := []struct {
		method, path string
		want         int
	}{
		{"GET", "/healthz", http.StatusTeapot},
		{"GET", "/api/auth/status", http.StatusTeapot},
		{"POST", "/api/auth/login", http.StatusTeapot},
		{"POST", "/api/auth/logout", http.StatusTeapot},
		{"GET", "/api/migration/items/database", http.StatusTeapot},
		{"POST", "/api/migration/resume", http.StatusTeapot},
		{"GET", "/projects/1", http.StatusTeapot},
		{"GET", "/", http.StatusTeapot},
		{"GET", "/api/projects", http.StatusServiceUnavailable},
		{"POST", "/api/projects/1/environments/1/composes/1/deployments", http.StatusServiceUnavailable},
		{"PUT", "/api/images", http.StatusServiceUnavailable},
		{"GET", "/api", http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rec.Code, c.want)
		}
		if c.want == http.StatusServiceUnavailable && strings.TrimSpace(rec.Body.String()) != `{"error":"This server is being migrated"}` {
			t.Errorf("%s %s body = %q", c.method, c.path, rec.Body.String())
		}
	}

	if err := f.src.Finish(context.Background(), "https://new.example.com"); err == nil {
		t.Fatal("Finish before ready succeeded")
	}
	if _, err := f.src.Start(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := f.src.Finish(context.Background(), "https://new.example.com"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/api/projects", nil))
	var body map[string]string
	json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusServiceUnavailable || body["error"] != "This server was migrated to https://new.example.com" {
		t.Fatalf("migrated = %d %q", rec.Code, rec.Body.String())
	}
}

func TestMiddlewarePassesEverythingWhenUnlocked(t *testing.T) {
	f := newSourceFixture(t)
	handler := f.src.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/api/projects", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("unlocked = %d", rec.Code)
	}
}

func TestMaintenanceLabelsHTTPSDomain(t *testing.T) {
	got := MaintenanceLabels([]domains.Domain{httpsDomain})
	want := map[string]string{
		"traefik.enable":         "true",
		"traefik.docker.network": "relist-network",
		"traefik.http.services.relist-maint.loadbalancer.server.port": "8080",
		"traefik.http.routers.relist-maint-d3.rule":                   "Host(`shop.example.com`) && PathPrefix(`/api`)",
		"traefik.http.routers.relist-maint-d3.entrypoints":            "websecure",
		"traefik.http.routers.relist-maint-d3.service":                "relist-maint",
		"traefik.http.routers.relist-maint-d3.tls":                    "true",
		"traefik.http.routers.relist-maint-d3.tls.certresolver":       "letsencrypt",
		"traefik.http.routers.relist-maint-d3-http.rule":              "Host(`shop.example.com`) && PathPrefix(`/api`)",
		"traefik.http.routers.relist-maint-d3-http.entrypoints":       "web",
		"traefik.http.routers.relist-maint-d3-http.service":           "relist-maint",
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("labels =\n%v\nwant\n%v", got, want)
	}
}

func TestMaintenancePageAnswers503(t *testing.T) {
	rec := httptest.NewRecorder()
	maintenanceHandler().ServeHTTP(rec, httptest.NewRequest("GET", "/anything", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "Down for maintenance") {
		t.Fatalf("maintenance page = %d %q", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("no Retry-After")
	}
}

func TestResumeDoesNotWaitForARunningDeployment(t *testing.T) {
	f := newSourceFixture(t)
	f.queue.waitBlock = make(chan struct{}) // a Deployment that never ends
	started := make(chan error, 1)
	go func() { _, err := f.src.Start(context.Background(), nil); started <- err }()
	eventuallyTrue(t, "stopping", func() bool {
		step, _, _ := f.store.Lock(context.Background())
		return step == StepStopping
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := f.src.Resume(ctx); err != nil {
		t.Fatalf("Resume while waiting for a Deployment: %v", err)
	}
	if err := <-started; err == nil {
		t.Fatal("the interrupted Start reported success")
	}
	if step, _, _ := f.store.Lock(context.Background()); step != StepNone {
		t.Fatalf("step after Resume = %q", step)
	}
	if len(f.docker.CallsOf("compose-stop")) != 0 {
		t.Fatal("Composes were stopped after Resume")
	}
}

func TestResumeClearsTheLockEvenWhenAComposeFailsToStart(t *testing.T) {
	f := newSourceFixture(t, httpsDomain)
	if _, err := f.src.Start(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	f.docker.Fail["compose-start"] = errors.New("no container to start")
	err := f.src.Resume(context.Background())
	if err == nil || !strings.Contains(err.Error(), "api") {
		t.Fatalf("Resume() error = %v, want it to name the Compose that did not start", err)
	}
	if step, _, _ := f.store.Lock(context.Background()); step != StepNone {
		t.Fatalf("step = %q: one failed Compose wedged the lock", step)
	}
}

func TestTheLockHoldsTheQueueUntilResume(t *testing.T) {
	f := newSourceFixture(t)
	if _, err := f.src.Start(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if got := f.queue.eventList(); len(got) < 2 || got[0] != "hold" || got[1] != "cancel" {
		t.Fatalf("events = %q, want hold before cancel", got)
	}

	// After a reboot the queue is a new one: ResumeLock holds it again.
	rebooted := &fakeQueue{}
	src := NewOldHost(OldHostDeps{Store: f.store, Manifests: f.builder(), Queue: rebooted, Docker: f.docker,
		Status: statusFromDocker{f.docker}, Composes: f.targets, Domains: fakeDomains(nil)})
	if err := src.ResumeLock(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := rebooted.eventList(); !slices.Equal(got, []string{"hold"}) {
		t.Fatalf("events after reboot = %q, want [hold]", got)
	}
	if err := src.Resume(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := rebooted.eventList(); got[len(got)-1] != "release" {
		t.Fatalf("events after resume = %q, want a final release", got)
	}
}

type fakeTerminals struct{ reasons []string }

func (f *fakeTerminals) CloseAll(reason string) { f.reasons = append(f.reasons, reason) }

// Locking closes every open Terminal Session (spec, the lock's step 1): a
// shell left open would change what is being copied.
func TestStartClosesTerminalSessions(t *testing.T) {
	f := newSourceFixture(t, httpsDomain)
	terminals := &fakeTerminals{}
	f.src.d.Terminals = terminals
	if _, err := f.src.Start(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if len(terminals.reasons) != 1 || !strings.Contains(terminals.reasons[0], "migrated") {
		t.Fatalf("CloseAll calls = %q, want one naming the migration", terminals.reasons)
	}
}
