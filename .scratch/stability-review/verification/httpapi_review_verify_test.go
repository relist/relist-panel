package httpapi_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"relist-panel/internal/dockerhost"
	"relist-panel/internal/httpapi"
	"relist-panel/internal/volumes"
)

// blockingVolumeRunner streams a few bytes and then waits, like an archive
// download to a slow client.
type blockingVolumeRunner struct {
	started chan struct{}
	release chan struct{}
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

func deploymentStatus(t *testing.T, server *testServer, path string, number int) string {
	t.Helper()
	response := server.request(t, http.MethodGet, path+"/deployments", nil)
	defer response.Body.Close()
	var list []map[string]any
	decodeJSON(t, response, &list)
	for _, d := range list {
		if int(d["number"].(float64)) == number {
			return d["status"].(string) + "/" + d["step"].(string)
		}
	}
	return "missing"
}

// C2: an archive download in Project A holds A's gate; a Deployment of A waits
// for it in its Deploy step, and every Deployment queued behind it on the host
// waits too.
func TestReviewC2ArchiveDownloadStallsTheQueue(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	explorer := setupExplorerCompose(t, server)
	runner := newHoldRunner()
	blocking := &blockingVolumeRunner{started: make(chan struct{}), release: make(chan struct{})}
	server.stopWorker()
	server.handler, server.stopWorker = startHandler(t, server.db, runner, server.dataDir, nil, nil, blocking, volumes.NewResolver(server.volumeCLI, server.dataDir), nil, nil)

	// A1: a deployable Compose in the archive's Project.
	created := postJSON(t, server, composesPath(explorer.project.ID, explorer.production.ID), map[string]string{"name": "api", "yamlContent": plainYAML})
	var a1 composeResponse
	decodeJSON(t, created, &a1)
	a1Path := composePath(explorer.project.ID, explorer.production.ID, a1.ID)
	x := newDeploymentsFixture(t, server, "Busy", "x", plainYAML)
	b := newDeploymentsFixture(t, server, "Other", "b", plainYAML)

	runner.hold()
	if s, _ := x.post(t, map[string]any{"mode": "deploy"}); s != http.StatusAccepted {
		t.Fatalf("x %d", s)
	}
	waitSignal(t, runner.upStarted, "x deploy step")
	if r := server.request(t, http.MethodPost, a1Path+"/deployments", map[string]any{"mode": "deploy"}); r.StatusCode != http.StatusAccepted {
		t.Fatalf("a1 %d", r.StatusCode)
	}

	// The archive download starts (and stays open).
	q := url.Values{"source": {"demoapp_pgdata"}, "service": {"db"}, "rootConfirmed": {"1"}, "bindConfirmed": {"1"}}
	req := httptest.NewRequest(http.MethodGet, "http://relist-panel.test"+composeVolumesPath(explorer.project.ID, explorer.production.ID, explorer.compose.ID)+"/archive?"+q.Encode(), nil)
	for _, c := range server.cookies {
		req.AddCookie(c)
	}
	go server.handler.ServeHTTP(httptest.NewRecorder(), req)
	waitSignal(t, blocking.started, "archive stream")

	if s, _ := b.post(t, map[string]any{"mode": "deploy"}); s != http.StatusAccepted {
		t.Fatalf("b %d", s)
	}
	runner.release() // x finishes; the worker moves on to A1
	time.Sleep(3 * time.Second)
	t.Logf("C2 3s after x finished, archive still open: A1=%s, B (other Project)=%s", deploymentStatus(t, server, a1Path, 1), deploymentStatus(t, server, b.path(), 1))
	close(blocking.release)
	time.Sleep(500 * time.Millisecond)
	t.Logf("C2 archive closed: A1=%s, B=%s", deploymentStatus(t, server, a1Path, 1), deploymentStatus(t, server, b.path(), 1))
}

func decodeJSON(t *testing.T, response *http.Response, v any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(v); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

type uncertainEngine struct{ hostEngine }

func (u *uncertainEngine) Prune(context.Context, dockerhost.Kind) (dockerhost.PruneResult, error) {
	return dockerhost.PruneResult{}, dockerhost.ErrPruneUncertain
}

// C5.1: after an uncertain prune blocks the guard, queuing a Deployment answers
// a generic 500 with no reason.
func TestReviewC5BlockedGuardGivesGeneric500(t *testing.T) {
	s := newTestServer(t)
	s.stopWorker()
	ctx, cancel := context.WithCancel(context.Background())
	handler, wait := httpapi.NewHandler(ctx, httpapi.Deps{DB: s.db, Runner: s.runner, DataDir: s.dataDir, Images: s.images, Docker: &uncertainEngine{}})
	t.Cleanup(func() { cancel(); wait() })
	s.handler = handler
	setupAndLogin(t, s)
	f := newDeploymentsFixture(t, s, "Guarded", "web", plainYAML)
	prune := s.request(t, http.MethodPost, "/api/docker/prune", map[string]string{"kind": "containers", "confirm": "PRUNE"})
	t.Logf("C5 uncertain prune -> %d", prune.StatusCode)
	status, body := f.post(t, map[string]any{"mode": "deploy"})
	t.Logf("C5 POST deployment while blocked -> %d %v", status, body)
	stop := s.request(t, http.MethodPost, f.path()+"/stop", nil)
	t.Logf("C5 POST stop while blocked -> %d %s", stop.StatusCode, readBody(t, stop))
}
