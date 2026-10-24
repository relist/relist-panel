package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"relist-panel/internal/volumes"
)

// blockingVolumeRunner streams a few bytes and then waits, like an archive
// download to a slow client.
type blockingVolumeRunner struct {
	started chan struct{}
	release chan struct{}
}

func newBlockingVolumeRunner() *blockingVolumeRunner {
	return &blockingVolumeRunner{started: make(chan struct{}), release: make(chan struct{})}
}

func (r *blockingVolumeRunner) Available(context.Context) error { return nil }
func (r *blockingVolumeRunner) Run(ctx context.Context, req volumes.Request, _ io.Reader, stdout io.Writer) error {
	if req.Op != volumes.OpArchive {
		return nil
	}
	stdout.Write([]byte("partial-tar"))
	close(r.started)
	select {
	case <-r.release:
	case <-ctx.Done():
	}
	return nil
}

// archiveFixture is a Project with an Explorer Compose whose archive download
// stays open until open.release is closed.
type archiveFixture struct {
	server   *testServer
	explorer explorerCompose
	runner   *holdRunner
	archive  *blockingVolumeRunner
	closed   atomic.Bool
}

func newArchiveFixture(t *testing.T) *archiveFixture {
	t.Helper()
	server := newTestServer(t)
	setupAndLogin(t, server)
	explorer := setupExplorerCompose(t, server)
	runner := newHoldRunner()
	archive := newBlockingVolumeRunner()
	server.stopWorker()
	server.handler, server.stopWorker = startHandler(t, server.db, runner, server.dataDir, nil, nil, archive, volumes.NewResolver(server.volumeCLI, server.dataDir), nil, nil)
	f := &archiveFixture{server: server, explorer: explorer, runner: runner, archive: archive}
	t.Cleanup(f.closeArchive)
	return f
}

func (f *archiveFixture) closeArchive() {
	if f.closed.CompareAndSwap(false, true) {
		close(f.archive.release)
	}
}

// openArchive starts the download and returns once the helper is streaming.
func (f *archiveFixture) openArchive(t *testing.T) {
	t.Helper()
	q := url.Values{"source": {"demoapp_pgdata"}, "service": {"db"}, "rootConfirmed": {"1"}, "bindConfirmed": {"1"}}
	req := httptest.NewRequest(http.MethodGet, "http://relist-panel.test"+composeVolumesPath(f.explorer.project.ID, f.explorer.production.ID, f.explorer.compose.ID)+"/archive?"+q.Encode(), nil)
	for _, c := range f.server.cookies {
		req.AddCookie(c)
	}
	go f.server.handler.ServeHTTP(httptest.NewRecorder(), req)
	waitSignal(t, f.archive.started, "archive stream")
}

// holdQueue parks the host's one worker in another Project's Deploy step, so
// Deployments enqueued next stay queued until the returned func runs. A Volume
// read opened in between then meets them in their own Deploy step, as in the
// stability review's reproduction.
func (f *archiveFixture) holdQueue(t *testing.T) (release func()) {
	t.Helper()
	busy := newDeploymentsFixture(t, f.server, "Busy", "x", plainYAML)
	f.runner.hold()
	if status, _ := busy.post(t, map[string]any{"mode": "deploy"}); status != http.StatusAccepted {
		t.Fatalf("enqueue busy = %d", status)
	}
	waitSignal(t, f.runner.upStarted, "the busy Deploy step")
	return f.runner.release
}

func (f *archiveFixture) composeInProject(t *testing.T, name string) string {
	t.Helper()
	created := postJSON(t, f.server, composesPath(f.explorer.project.ID, f.explorer.production.ID), map[string]string{"name": name, "yamlContent": plainYAML})
	var c composeResponse
	decodeJSON(t, created, &c)
	return composePath(f.explorer.project.ID, f.explorer.production.ID, c.ID)
}

func deploymentState(t *testing.T, server *testServer, path string, number int) string {
	t.Helper()
	response := server.request(t, http.MethodGet, path+"/deployments/"+strconv.Itoa(number), nil)
	var d deploymentResponse
	decodeJSON(t, response, &d)
	return d.Status + "/" + d.Step
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// An archive download in Project A shares the Project's volume lock instead of
// holding its mutation gate, so neither A's own Deployment nor a Deployment of
// another Project (queued behind it on the host) waits for the download.
func TestArchiveDownloadDoesNotBlockDeployments(t *testing.T) {
	f := newArchiveFixture(t)
	a1 := f.composeInProject(t, "api")
	other := newDeploymentsFixture(t, f.server, "Other", "b", plainYAML)

	releaseQueue := f.holdQueue(t)
	if status, _ := f.server.enqueue(t, a1, map[string]any{"mode": "deploy"}); status != http.StatusAccepted {
		t.Fatalf("enqueue A1 = %d", status)
	}
	if status, _ := other.post(t, map[string]any{"mode": "deploy"}); status != http.StatusAccepted {
		t.Fatalf("enqueue B = %d", status)
	}
	f.openArchive(t)
	releaseQueue()
	if d := f.server.waitDeployment(t, a1, 1); d.Status != "succeeded" {
		t.Fatalf("A1 = %s (step %q, reason %q) with the archive open, want succeeded", d.Status, d.Step, d.Reason)
	}
	if d := f.server.waitDeployment(t, other.path(), 1); d.Status != "succeeded" {
		t.Fatalf("B = %s (step %q, reason %q) with the archive open, want succeeded", d.Status, d.Step, d.Reason)
	}
}

// Deleting a Compose removes volumes an open archive may be reading, so the
// DELETE waits for the download to end.
func TestComposeDeleteWaitsForArchive(t *testing.T) {
	f := newArchiveFixture(t)
	f.openArchive(t)

	returned := make(chan int, 1)
	go func() {
		response := f.server.request(t, http.MethodDelete, composePath(f.explorer.project.ID, f.explorer.production.ID, f.explorer.compose.ID), nil)
		response.Body.Close()
		returned <- response.StatusCode
	}()
	select {
	case status := <-returned:
		t.Fatalf("DELETE returned %d while the archive was open", status)
	case <-time.After(500 * time.Millisecond):
	}
	f.closeArchive()
	select {
	case status := <-returned:
		if status != http.StatusNoContent {
			t.Fatalf("DELETE = %d, want %d", status, http.StatusNoContent)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DELETE did not return after the archive closed")
	}
}

// A fresh-volumes Deployment removes the Compose's volumes in its Deploy step,
// so it waits for an open archive although a plain Deploy does not.
func TestFreshDeployWaitsForArchive(t *testing.T) {
	f := newArchiveFixture(t)
	path := composePath(f.explorer.project.ID, f.explorer.production.ID, f.explorer.compose.ID)

	releaseQueue := f.holdQueue(t)
	status, number := f.server.enqueue(t, path, map[string]any{"mode": "deploy", "freshVolumes": true, "confirmName": "web"})
	if status != http.StatusAccepted {
		t.Fatalf("enqueue fresh deploy = %d", status)
	}
	f.openArchive(t)
	releaseQueue()
	eventually(t, "the fresh Deployment to reach its Deploy step", func() bool {
		return deploymentState(t, f.server, path, number) == "running/deploy"
	})
	time.Sleep(500 * time.Millisecond)
	if got := deploymentState(t, f.server, path, number); got != "running/deploy" {
		t.Fatalf("fresh Deployment = %s while the archive was open, want running/deploy", got)
	}
	f.closeArchive()
	f.server.waitDeployment(t, path, number)
}

func decodeJSON(t *testing.T, response *http.Response, v any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}
