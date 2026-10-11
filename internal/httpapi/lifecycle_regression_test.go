package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"relist-panel/internal/composes"
	"relist-panel/internal/httpapi"
	"relist-panel/internal/volumes"
	"strconv"
	"sync"
	"testing"
	"time"
)

func deployFixture(t *testing.T, server *testServer, projectID, environmentID int64, name string) composeResponse {
	t.Helper()
	response := postJSON(t, server, composesPath(projectID, environmentID), map[string]string{"name": name, "yamlContent": "services:\n  web:\n    image: nginx\n"})
	defer response.Body.Close()
	var c composeResponse
	decodeLifecycleResponse(t, response, &c)
	response = server.request(t, http.MethodPost, composeDeployPath(projectID, environmentID, c.ID), nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("deploy: %d", response.StatusCode)
	}
	return c
}

func TestComposeIdentitiesDoNotCollideAcrossNamesAndRenames(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	first := createProject(t, server, "a-b", "")
	firstEnv := productionEnvironment(t, server, first)
	// Use the public API to create the ambiguous parent combination.
	er := postJSON(t, server, projectEnvironmentsPath(first.ID), map[string]string{"name": "c"})
	defer er.Body.Close()
	var e environmentResponse
	decodeLifecycleResponse(t, er, &e)
	deployFixture(t, server, first.ID, e.ID, "web")
	one := server.runner.calls[len(server.runner.calls)-2].request
	second := createProject(t, server, "a", "")
	er2 := postJSON(t, server, projectEnvironmentsPath(second.ID), map[string]string{"name": "b-c"})
	defer er2.Body.Close()
	var e2 environmentResponse
	decodeLifecycleResponse(t, er2, &e2)
	deployFixture(t, server, second.ID, e2.ID, "web")
	two := server.runner.calls[len(server.runner.calls)-2].request
	if one.ProjectName == two.ProjectName {
		t.Fatal("distinct Composes share Docker identity")
	}
	deployFixture(t, server, first.ID, firstEnv.ID, "app")
	old := server.runner.calls[len(server.runner.calls)-2].request
	rr := server.request(t, http.MethodPut, "/api/projects/"+lifecycleID(first.ID), map[string]string{"name": "renamed"})
	rr.Body.Close()
	if rr.StatusCode != http.StatusOK {
		t.Fatalf("rename: %d", rr.StatusCode)
	}
	replacement := createProject(t, server, "a-b", "")
	replacementEnv := productionEnvironment(t, server, replacement)
	deployFixture(t, server, replacement.ID, replacementEnv.ID, "app")
	fresh := server.runner.calls[len(server.runner.calls)-2].request
	if old.ProjectName == fresh.ProjectName || old.ComposeFilePath == fresh.ComposeFilePath {
		t.Fatal("reused display name shares old deployment")
	}
}

func TestParentDeletionTearsDownDeployedComposes(t *testing.T) {
	for _, kind := range []string{"project", "environment"} {
		t.Run(kind, func(t *testing.T) {
			server := newTestServer(t)
			setupAndLogin(t, server)
			p := createProject(t, server, "Delete Parent", "")
			e := productionEnvironment(t, server, p)
			deployFixture(t, server, p.ID, e.ID, "web")
			dir := filepath.Dir(server.runner.calls[0].request.ComposeFilePath)
			path := environmentPath(p.ID, e.ID)
			if kind == "project" {
				path = "/api/projects/" + lifecycleID(p.ID)
			}
			response := server.request(t, http.MethodDelete, path, nil)
			defer response.Body.Close()
			if response.StatusCode != http.StatusNoContent {
				t.Fatalf("delete: %d", response.StatusCode)
			}
			found := false
			for _, call := range server.runner.calls {
				if call.method == "down-with-volumes" {
					found = true
				}
			}
			if !found {
				t.Fatal("parent deletion did not tear down its stack")
			}
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("deployment directory remains: %v", err)
			}
		})
	}
}

func lifecycleID(id int64) string { return strconv.FormatInt(id, 10) }
func decodeLifecycleResponse(t *testing.T, r *http.Response, v any) {
	t.Helper()
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

// A second request must wait even when it arrives from another browser/API
// client; UI pending buttons cannot coordinate lifecycle mutations.
func TestLifecycleRequestsWaitForInFlightDeploy(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	p := createProject(t, server, "Concurrent", "")
	e := productionEnvironment(t, server, p)
	c := createCompose(t, server, p.ID, e.ID, "web")
	save := server.request(t, http.MethodPut, composePath(p.ID, e.ID, c.ID), map[string]string{"yamlContent": "services:\n  web:\n    image: nginx\n"})
	save.Body.Close()
	runner := &blockingLifecycleRunner{fakeComposeRunner: newFakeComposeRunner(), started: make(chan struct{}), release: make(chan struct{}), stopped: make(chan struct{})}
	server.handler = httpapi.NewHandler(server.db, runner, server.dataDir, nil, nil, server.volumeRunner, volumes.NewResolver(server.volumeCLI, server.dataDir), nil, nil)
	deployDone := make(chan *http.Response, 1)
	go func() { deployDone <- server.request(t, http.MethodPost, composeDeployPath(p.ID, e.ID, c.ID), nil) }()
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("deploy did not start")
	}
	stopDone := make(chan *http.Response, 1)
	go func() { stopDone <- server.request(t, http.MethodPost, composeStopPath(p.ID, e.ID, c.ID), nil) }()
	select {
	case <-runner.stopped:
		close(runner.release)
		t.Fatal("stop ran before deploy completed")
	case <-time.After(100 * time.Millisecond):
	}
	close(runner.release)
	for _, ch := range []chan *http.Response{deployDone, stopDone} {
		select {
		case r := <-ch:
			r.Body.Close()
			if r.StatusCode != http.StatusOK {
				t.Fatalf("lifecycle status: %d", r.StatusCode)
			}
		case <-time.After(time.Second):
			t.Fatal("lifecycle request did not complete")
		}
	}
}

type blockingLifecycleRunner struct {
	*fakeComposeRunner
	started, release, stopped chan struct{}
}

func (r *blockingLifecycleRunner) Up(ctx context.Context, req composes.RunRequest, _ composes.UpOptions) error {
	close(r.started)
	select {
	case <-r.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (r *blockingLifecycleRunner) Down(ctx context.Context, req composes.RunRequest) error {
	close(r.stopped)
	return nil
}

// eventRunner records the order of lifecycle calls under a mutex and blocks
// the first Up until released, so a test can tell a request that waited for
// the in-flight one from a request that ran beside it.
type eventRunner struct {
	*fakeComposeRunner
	mu       sync.Mutex
	events   []string
	started  chan struct{}
	release  chan struct{}
	blocking bool
}

func newEventRunner() *eventRunner {
	return &eventRunner{fakeComposeRunner: newFakeComposeRunner(), started: make(chan struct{}), release: make(chan struct{})}
}

func (r *eventRunner) record(event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *eventRunner) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

// blockNextUp makes the next Up signal started and wait for release.
func (r *eventRunner) blockNextUp() {
	r.mu.Lock()
	r.blocking = true
	r.mu.Unlock()
}

func (r *eventRunner) Up(ctx context.Context, _ composes.RunRequest, _ composes.UpOptions) error {
	r.mu.Lock()
	block := r.blocking
	r.blocking = false
	r.mu.Unlock()
	if !block {
		r.record("up")
		return nil
	}
	r.record("up-blocked")
	close(r.started)
	select {
	case <-r.release:
		r.record("up-released")
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *eventRunner) Down(context.Context, composes.RunRequest) error {
	r.record("down")
	return nil
}

func (r *eventRunner) DownWithVolumes(context.Context, composes.RunRequest) error {
	r.record("down-with-volumes")
	return nil
}

// coordinationFixture is a Compose already deployed once with the plain fake
// runner, then served by an eventRunner so the next Up can be held open.
type coordinationFixture struct {
	server  *testServer
	runner  *eventRunner
	project projectResponse
	env     environmentResponse
	compose composeResponse
}

func newCoordinationFixture(t *testing.T) coordinationFixture {
	t.Helper()
	server := newTestServer(t)
	setupAndLogin(t, server)
	p := createProject(t, server, "Coordinated", "")
	e := productionEnvironment(t, server, p)
	c := deployFixture(t, server, p.ID, e.ID, "web")
	runner := newEventRunner()
	server.handler = httpapi.NewHandler(server.db, runner, server.dataDir, nil, nil, server.volumeRunner, volumes.NewResolver(server.volumeCLI, server.dataDir), nil, nil)
	return coordinationFixture{server: server, runner: runner, project: p, env: e, compose: c}
}

// holdDeploy starts a deploy that blocks inside Up and returns once it is
// running there.
func (f coordinationFixture) holdDeploy(t *testing.T) chan *http.Response {
	t.Helper()
	f.runner.blockNextUp()
	done := make(chan *http.Response, 1)
	go func() {
		done <- f.server.request(t, http.MethodPost, composeDeployPath(f.project.ID, f.env.ID, f.compose.ID), nil)
	}()
	select {
	case <-f.runner.started:
	case <-time.After(time.Second):
		t.Fatal("deploy did not start")
	}
	return done
}

func (f coordinationFixture) async(t *testing.T, method, path string, body any) chan *http.Response {
	t.Helper()
	done := make(chan *http.Response, 1)
	go func() { done <- f.server.request(t, method, path, body) }()
	return done
}

// assertWaits fails unless nothing but the held Up has happened after a
// grace period, i.e. the second request is queued behind the in-flight one.
func (f coordinationFixture) assertWaits(t *testing.T, what string, held ...string) {
	t.Helper()
	time.Sleep(100 * time.Millisecond)
	if got := f.runner.snapshot(); !reflect.DeepEqual(got, held) {
		close(f.runner.release)
		t.Fatalf("%s ran beside the in-flight request: events = %v, want only %v", what, got, held)
	}
}

func awaitStatus(t *testing.T, done chan *http.Response, want int, what string) {
	t.Helper()
	select {
	case r := <-done:
		r.Body.Close()
		if r.StatusCode != want {
			t.Fatalf("%s status = %d, want %d", what, r.StatusCode, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not complete", what)
	}
}

// deploy-fresh is destructive (down -v); it must queue behind a deploy of
// the same Project, whichever browser it came from.
func TestDeployFreshWaitsForInFlightDeploy(t *testing.T) {
	f := newCoordinationFixture(t)
	deployDone := f.holdDeploy(t)
	freshDone := f.async(t, http.MethodPost, composeDeployFreshPath(f.project.ID, f.env.ID, f.compose.ID), map[string]string{"confirmName": "web"})
	f.assertWaits(t, "deploy-fresh", "up-blocked")

	close(f.runner.release)
	awaitStatus(t, deployDone, http.StatusOK, "deploy")
	awaitStatus(t, freshDone, http.StatusOK, "deploy-fresh")
	want := []string{"up-blocked", "up-released", "down-with-volumes", "up"}
	if got := f.runner.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

// A stop must not tear the stack down under a deploy-fresh that is between
// its down -v and its up.
func TestStopWaitsForInFlightDeployFresh(t *testing.T) {
	f := newCoordinationFixture(t)
	f.runner.blockNextUp()
	freshDone := f.async(t, http.MethodPost, composeDeployFreshPath(f.project.ID, f.env.ID, f.compose.ID), map[string]string{"confirmName": "web"})
	select {
	case <-f.runner.started:
	case <-time.After(time.Second):
		t.Fatal("deploy-fresh did not reach up")
	}
	stopDone := f.async(t, http.MethodPost, composeStopPath(f.project.ID, f.env.ID, f.compose.ID), nil)
	f.assertWaits(t, "stop", "down-with-volumes", "up-blocked")

	close(f.runner.release)
	awaitStatus(t, freshDone, http.StatusOK, "deploy-fresh")
	awaitStatus(t, stopDone, http.StatusOK, "stop")
	want := []string{"down-with-volumes", "up-blocked", "up-released", "down"}
	if got := f.runner.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

// Deleting an orphan volume must queue behind a deploy of the same Project,
// so the orphan list it re-derives is not judged mid-deploy.
func TestOrphanDeleteWaitsForInFlightDeploy(t *testing.T) {
	f := newCoordinationFixture(t)
	project := "relist-c" + lifecycleID(f.compose.ID)
	f.server.volumeCLI.ComposeConfigOutput = orphanConfig(project)
	f.server.volumeCLI.ProjectVolumes = []volumes.ProjectVolume{listedVolume(project, "data"), listedVolume(project, "old")}
	f.server.volumeCLI.OnRemoveVolume = func(name string) { f.runner.record("remove-volume:" + name) }
	deployDone := f.holdDeploy(t)
	deleteDone := f.async(t, http.MethodDelete, composeVolumesPath(f.project.ID, f.env.ID, f.compose.ID)+"/orphans/"+project+"_old", nil)
	f.assertWaits(t, "orphan delete", "up-blocked")

	close(f.runner.release)
	awaitStatus(t, deployDone, http.StatusOK, "deploy")
	awaitStatus(t, deleteDone, http.StatusNoContent, "orphan delete")
	want := []string{"up-blocked", "up-released", "remove-volume:" + project + "_old"}
	if got := f.runner.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %v, want %v", got, want)
	}
}

func TestComposeResponsesExposeActualDeployIdentity(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	p := createProject(t, server, "Identity UI", "")
	e := productionEnvironment(t, server, p)
	c := createCompose(t, server, p.ID, e.ID, "web")
	response := server.request(t, http.MethodGet, composePath(p.ID, e.ID, c.ID), nil)
	defer response.Body.Close()
	var payload struct {
		DeployIdentity struct {
			ProjectName string `json:"projectName"`
			Directory   string `json:"directory"`
		} `json:"deployIdentity"`
	}
	decodeLifecycleResponse(t, response, &payload)
	if payload.DeployIdentity.ProjectName != "relist-c"+lifecycleID(c.ID) || payload.DeployIdentity.Directory != filepath.Join(server.dataDir, ".composes", lifecycleID(c.ID)) {
		t.Fatalf("displayed deploy identity = %+v, want actual ID-based Docker name and configured deployment directory", payload.DeployIdentity)
	}
}
