package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/composes"
)

// disconnectRunner holds the first destructive or lifecycle Docker call open
// until the test has canceled the request, then records whether the context it
// was given survived the cancel.
type disconnectRunner struct {
	*fakeComposeRunner
	started chan struct{}
	proceed chan struct{}
	once    sync.Once

	mu          sync.Mutex
	ctxErr      error
	hasDeadline bool
	called      bool
}

func newDisconnectRunner() *disconnectRunner {
	return &disconnectRunner{fakeComposeRunner: newFakeComposeRunner(), started: make(chan struct{}), proceed: make(chan struct{})}
}

func (r *disconnectRunner) observe(ctx context.Context) {
	r.once.Do(func() { close(r.started) })
	select {
	case <-r.proceed:
	case <-time.After(5 * time.Second):
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.called = true
	r.ctxErr = ctx.Err()
	_, r.hasDeadline = ctx.Deadline()
}

func (r *disconnectRunner) Down(ctx context.Context, req composes.RunRequest) error {
	r.observe(ctx)
	return r.fakeComposeRunner.Down(context.Background(), req)
}

func (r *disconnectRunner) Restart(ctx context.Context, req composes.RunRequest) error {
	r.observe(ctx)
	return r.fakeComposeRunner.Restart(context.Background(), req)
}

func (r *disconnectRunner) DownWithVolumes(ctx context.Context, req composes.RunRequest) error {
	r.observe(ctx)
	return r.fakeComposeRunner.DownWithVolumes(context.Background(), req)
}

// DownWithVolumesTracked is what a delete calls on a runner that has it; the
// embedded one would call the fake's own DownWithVolumes and skip the probe.
func (r *disconnectRunner) DownWithVolumesTracked(ctx context.Context, req composes.RunRequest) (bool, error) {
	err := r.DownWithVolumes(ctx, req)
	return false, err
}

// assertSurvived fails unless the probed call ran, with a live context that
// carries the lifecycle timeout.
func (r *disconnectRunner) assertSurvived(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.called {
		t.Fatal("the runner was never called")
	}
	if r.ctxErr != nil {
		t.Fatalf("runner context was canceled with the client: %v", r.ctxErr)
	}
	if !r.hasDeadline {
		t.Fatal("runner context has no deadline, want the lifecycle timeout")
	}
}

// disconnectingRequest sends the request, cancels its context once the runner
// is mid-call, lets the runner finish and returns the handler's response.
func disconnectingRequest(t *testing.T, server *testServer, runner *disconnectRunner, method, path string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request := httptest.NewRequest(method, "http://relist-panel.test"+path, nil).WithContext(ctx)
	done := make(chan *http.Response, 1)
	go func() { done <- server.serve(request) }()
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the runner was never reached")
	}
	cancel()
	close(runner.proceed)
	select {
	case response := <-done:
		return response
	case <-time.After(10 * time.Second):
		t.Fatal("the request never finished")
		return nil
	}
}

// deployedWithProbe deploys a Compose through the default fake, then swaps in
// a disconnectRunner so only the call under test is probed.
func deployedWithProbe(t *testing.T) (*testServer, *disconnectRunner, projectResponse, environmentResponse, composeResponse) {
	t.Helper()
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "demo", "")
	environment := productionEnvironment(t, server, project)
	compose := deployFixture(t, server, project.ID, environment.ID, "web")
	runner := newDisconnectRunner()
	server.useRunner(t, runner)
	return server, runner, project, environment, compose
}

func assertGone(t *testing.T, server *testServer, path string) {
	t.Helper()
	response := server.request(t, http.MethodGet, path, nil)
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("GET %s = %d, want %d", path, response.StatusCode, http.StatusNotFound)
	}
}

func TestDeleteFinishesAfterClientDisconnects(t *testing.T) {
	server, runner, project, environment, compose := deployedWithProbe(t)
	response := disconnectingRequest(t, server, runner, http.MethodDelete, composePath(project.ID, environment.ID, compose.ID))
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}
	runner.assertSurvived(t)
	assertGone(t, server, composePath(project.ID, environment.ID, compose.ID))
}

func TestStopFinishesAfterClientDisconnects(t *testing.T) {
	server, runner, project, environment, compose := deployedWithProbe(t)
	response := disconnectingRequest(t, server, runner, http.MethodPost, composeStopPath(project.ID, environment.ID, compose.ID))
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("stop status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	runner.assertSurvived(t)
}

func TestRestartFinishesAfterClientDisconnects(t *testing.T) {
	server, runner, project, environment, compose := deployedWithProbe(t)
	response := disconnectingRequest(t, server, runner, http.MethodPost, composeRestartPath(project.ID, environment.ID, compose.ID))
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("restart status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	runner.assertSurvived(t)
}

func TestProjectDeleteFinishesAfterClientDisconnects(t *testing.T) {
	server, runner, project, _, _ := deployedWithProbe(t)
	path := "/api/projects/" + lifecycleID(project.ID)
	response := disconnectingRequest(t, server, runner, http.MethodDelete, path)
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("project delete status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}
	runner.assertSurvived(t)
	assertGone(t, server, path)
}

func TestEnvironmentDeleteFinishesAfterClientDisconnects(t *testing.T) {
	server, runner, project, environment, _ := deployedWithProbe(t)
	path := projectEnvironmentsPath(project.ID) + "/" + lifecycleID(environment.ID)
	response := disconnectingRequest(t, server, runner, http.MethodDelete, path)
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("environment delete status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}
	runner.assertSurvived(t)
	assertGone(t, server, path)
}
