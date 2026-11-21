package composes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"relist-panel/internal/database"
)

// listEngine is a ContainerEngine for the status list: it counts the calls
// and can block ComposeContainers until gate closes.
type listEngine struct {
	*fakeEngine
	all          []EngineContainer
	err          error
	gate         chan struct{}
	started      chan struct{}
	composeCalls atomic.Int32
	projectCalls atomic.Int32
}

func (e *listEngine) ComposeContainers(ctx context.Context) ([]EngineContainer, error) {
	if e.composeCalls.Add(1) == 1 && e.started != nil {
		close(e.started)
	}
	if e.gate != nil {
		select {
		case <-e.gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.err != nil {
		return nil, e.err
	}
	return append([]EngineContainer(nil), e.all...), nil
}

func (e *listEngine) ProjectContainers(_ context.Context, project string) ([]EngineContainer, error) {
	e.projectCalls.Add(1)
	if e.err != nil {
		return nil, e.err
	}
	var out []EngineContainer
	for _, c := range e.all {
		if c.Project == project {
			out = append(out, c)
		}
	}
	return out, nil
}

// statusFixture has Composes with IDs 1..n in Environment 3, each deployed
// (its compose file on disk) except those named in undeployed.
type statusFixture struct {
	svc     *Service
	engine  *listEngine
	clock   *fakeClock
	dataDir string
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newStatusFixture(t *testing.T, ids []int64, undeployed ...int64) *statusFixture {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, statement := range []string{
		`INSERT INTO projects (id, name, created_at, updated_at) VALUES (7, 'p', 0, 0)`,
		`INSERT INTO environments (id, project_id, name, created_at) VALUES (3, 7, 'e', 0)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	dataDir := t.TempDir()
	for _, id := range ids {
		name := "c" + strconv.FormatInt(id, 10)
		if _, err := db.Exec(`INSERT INTO composes (id, environment_id, name, project_slug, env_slug, yaml_content, identity_version, created_at, updated_at) VALUES (?, 3, ?, 'p', 'e', 'services: {}', 1, 0, 0)`, id, name); err != nil {
			t.Fatal(err)
		}
		skip := false
		for _, u := range undeployed {
			skip = skip || u == id
		}
		if skip {
			continue
		}
		dir := filepath.Join(dataDir, ".composes", strconv.FormatInt(id, 10))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, composeFileName), []byte("services: {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	engine := &listEngine{fakeEngine: &fakeEngine{}}
	svc := New(db, &recordingRunner{}, dataDir).WithEngine(engine)
	clock := &fakeClock{t: time.Unix(1000, 0)}
	svc.status.now = clock.now
	return &statusFixture{svc: svc, engine: engine, clock: clock, dataDir: dataDir}
}

func (f *statusFixture) composes(t *testing.T) []Compose {
	t.Helper()
	composes, err := f.svc.repository.list(context.Background(), 3)
	if err != nil {
		t.Fatal(err)
	}
	return composes
}

func ec(project, service string, running bool) EngineContainer {
	return EngineContainer{ID: project + service, Project: project, Service: service, Running: running}
}

func TestStatusOf(t *testing.T) {
	got := statusOf([]EngineContainer{ec("p", "web", true), ec("p", "web", false), ec("p", "db", true)})
	if want := (ComposeStatus{Running: 2, Total: 3, Services: []string{"db", "web"}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("statusOf = %+v, want %+v", got, want)
	}
	if got := statusOf(nil); !reflect.DeepEqual(got, ComposeStatus{}) || got.Services != nil {
		t.Fatalf("statusOf(nil) = %+v, want the zero value", got)
	}
}

func TestStatusesGroupByExactProject(t *testing.T) {
	f := newStatusFixture(t, []int64{1, 19})
	f.engine.all = []EngineContainer{
		ec("relist-c19", "web", true), ec("relist-c19", "db", true),
		ec("relist-c1", "web", false),
	}
	got := f.svc.withStatuses(context.Background(), f.composes(t))
	if got[0].ID != 1 || got[0].Status.Total != 1 || got[0].Status.Running != 0 {
		t.Fatalf("relist-c1 status = %+v, want 0 of 1", got[0].Status)
	}
	if got[1].ID != 19 || got[1].Status.Total != 2 || got[1].Status.Running != 2 {
		t.Fatalf("relist-c19 status = %+v, want 2 of 2", got[1].Status)
	}
}

func TestStatusesOneEngineCall(t *testing.T) {
	f := newStatusFixture(t, []int64{1, 2, 3, 4, 5})
	f.engine.all = []EngineContainer{ec("relist-c3", "web", true)}
	got := f.svc.withStatuses(context.Background(), f.composes(t))
	if f.engine.composeCalls.Load() != 1 || f.engine.projectCalls.Load() != 0 {
		t.Fatalf("calls = %d compose, %d project, want 1 and 0", f.engine.composeCalls.Load(), f.engine.projectCalls.Load())
	}
	if got[2].Status.Running != 1 || got[0].Status.Total != 0 {
		t.Fatalf("statuses = %+v", got)
	}
}

func TestSnapshotSharedAcrossConcurrentLists(t *testing.T) {
	f := newStatusFixture(t, []int64{1, 2})
	f.engine.gate = make(chan struct{})
	f.engine.started = make(chan struct{})
	f.engine.all = []EngineContainer{ec("relist-c1", "web", true), ec("relist-c2", "web", true), ec("relist-c2", "db", false)}
	const lists = 20
	results := make([][]Compose, lists)
	var wg sync.WaitGroup
	for i := range lists {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = f.svc.withStatuses(context.Background(), f.composes(t))
		}()
	}
	<-f.engine.started
	time.Sleep(50 * time.Millisecond) // let the others reach the shared call
	close(f.engine.gate)
	wg.Wait()
	if n := f.engine.composeCalls.Load(); n != 1 {
		t.Fatalf("ComposeContainers calls = %d, want 1", n)
	}
	for i, got := range results {
		if got[0].Status.Total != 1 || got[1].Status.Total != 2 || got[1].Status.Running != 1 {
			t.Fatalf("list %d statuses = %+v / %+v", i, got[0].Status, got[1].Status)
		}
	}
}

func TestSnapshotExpires(t *testing.T) {
	f := newStatusFixture(t, []int64{1})
	list := func() { f.svc.withStatuses(context.Background(), f.composes(t)) }
	list()
	f.clock.advance(1900 * time.Millisecond)
	list()
	if n := f.engine.composeCalls.Load(); n != 1 {
		t.Fatalf("calls at 1.9 s = %d, want 1", n)
	}
	f.clock.advance(200 * time.Millisecond)
	list()
	if n := f.engine.composeCalls.Load(); n != 2 {
		t.Fatalf("calls at 2.1 s = %d, want 2", n)
	}
}

func TestSnapshotErrorNotCached(t *testing.T) {
	f := newStatusFixture(t, []int64{1})
	f.engine.err = errors.New("docker down")
	f.svc.withStatuses(context.Background(), f.composes(t))
	f.engine.err = nil
	f.engine.all = []EngineContainer{ec("relist-c1", "web", true)}
	got := f.svc.withStatuses(context.Background(), f.composes(t))
	if n := f.engine.composeCalls.Load(); n != 2 {
		t.Fatalf("calls = %d, want a retry after the error", n)
	}
	if got[0].Status.Running != 1 {
		t.Fatalf("status = %+v, want the retried result", got[0].Status)
	}
}

func TestSnapshotCallerCancel(t *testing.T) {
	f := newStatusFixture(t, []int64{1})
	f.engine.gate = make(chan struct{})
	f.engine.started = make(chan struct{})
	f.engine.all = []EngineContainer{ec("relist-c1", "web", true)}

	ctxA, cancelA := context.WithCancel(context.Background())
	errA := make(chan error, 1)
	go func() {
		_, err := f.svc.status.get(ctxA, f.engine)
		errA <- err
	}()
	<-f.engine.started
	type result struct {
		by  map[string][]EngineContainer
		err error
	}
	resB := make(chan result, 1)
	go func() {
		by, err := f.svc.status.get(context.Background(), f.engine)
		resB <- result{by, err}
	}()
	time.Sleep(50 * time.Millisecond) // B is waiting on A's call
	cancelA()
	select {
	case err := <-errA:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("A err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("A did not return when its ctx ended")
	}
	close(f.engine.gate)
	select {
	case r := <-resB:
		if r.err != nil || len(r.by["relist-c1"]) != 1 {
			t.Fatalf("B = %+v, want the result", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("B did not get the shared result")
	}
	if n := f.engine.composeCalls.Load(); n != 1 {
		t.Fatalf("calls = %d, want 1", n)
	}
}

func TestRefreshedStatusDropsSnapshot(t *testing.T) {
	f := newStatusFixture(t, []int64{1})
	list := func() { f.svc.withStatuses(context.Background(), f.composes(t)) }
	list()
	compose := f.composes(t)[0]
	f.svc.withRefreshedStatus(context.Background(), runRequestFor(f.dataDir, compose), compose)
	list()
	if n := f.engine.composeCalls.Load(); n != 2 {
		t.Fatalf("calls = %d, want a second one after the action", n)
	}
}

func TestStatusesUndeployedNoCall(t *testing.T) {
	f := newStatusFixture(t, []int64{1, 2}, 1, 2)
	got := f.svc.withStatuses(context.Background(), f.composes(t))
	if f.engine.composeCalls.Load() != 0 || f.engine.projectCalls.Load() != 0 {
		t.Fatal("an all-undeployed list called the Engine")
	}
	if got[0].Status.Total != 0 || got[1].Status.Total != 0 {
		t.Fatalf("statuses = %+v", got)
	}
}

func TestStatusesEngineDownIsZero(t *testing.T) {
	f := newStatusFixture(t, []int64{1, 2, 3})
	f.engine.err = errors.New("cannot connect to the docker daemon")
	mux := http.NewServeMux()
	f.svc.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/projects/7/environments/3/composes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if strings.Count(rec.Body.String(), `"running":0,"total":0`) != 3 {
		t.Fatalf("body = %s, want three zero statuses", rec.Body)
	}
	if n := f.engine.composeCalls.Load(); n != 1 {
		t.Fatalf("calls = %d, want 1", n)
	}
}

func TestServiceNamesFromEngine(t *testing.T) {
	f := newStatusFixture(t, []int64{1})
	f.engine.all = []EngineContainer{ec("relist-c1", "web", true), ec("relist-c1", "db", false), ec("relist-c2", "other", true)}
	req := runRequestFor(f.dataDir, f.composes(t)[0])
	names, err := f.svc.ServiceNames(context.Background(), req)
	if err != nil || !reflect.DeepEqual(names, []string{"db", "web"}) {
		t.Fatalf("ServiceNames = %v, %v, want [db web]", names, err)
	}
	f.engine.err = errors.New("boom")
	if _, err := f.svc.ServiceNames(context.Background(), req); !errors.Is(err, f.engine.err) {
		t.Fatalf("err = %v, want the Engine's error", err)
	}
}

func TestDeployDropsSnapshot(t *testing.T) {
	svc, _, target, _ := newDeployService(t, "services:\n  web: {image: nginx}\n")
	engine := &listEngine{fakeEngine: &fakeEngine{}}
	svc.WithEngine(engine)
	if _, err := svc.status.get(context.Background(), engine); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RunDeploy(context.Background(), target, DeployOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.status.get(context.Background(), engine); err != nil {
		t.Fatal(err)
	}
	if n := engine.composeCalls.Load(); n != 2 {
		t.Fatalf("calls = %d, want a second one after the deploy", n)
	}
}
