package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"relist-panel/internal/composes"
	"relist-panel/internal/httpapi"
	"relist-panel/internal/volumes"
	"strconv"
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
	server.handler = httpapi.NewHandler(server.db, runner, server.dataDir, nil, nil, server.volumeRunner, volumes.NewResolver(server.volumeCLI, server.dataDir))
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

func (r *blockingLifecycleRunner) Up(ctx context.Context, req composes.RunRequest) error {
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
