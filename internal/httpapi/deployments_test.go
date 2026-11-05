package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"relist-panel/internal/composes"
	"relist-panel/internal/sources"
)

// holdRunner lets a test stop the deployment worker inside a Deploy step (Up)
// or a build step, and see how the build ended.
type holdRunner struct {
	*fakeComposeRunner
	mu           sync.Mutex
	holdUp       bool
	upStarted    chan struct{}
	upRelease    chan struct{}
	buildStarted chan struct{}
	buildEnded   chan error
}

func newHoldRunner() *holdRunner {
	return &holdRunner{
		fakeComposeRunner: newFakeComposeRunner(),
		upStarted:         make(chan struct{}, 8),
		upRelease:         make(chan struct{}),
		buildStarted:      make(chan struct{}, 8),
		buildEnded:        make(chan error, 8),
	}
}

func (r *holdRunner) hold() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.holdUp = true
}

func (r *holdRunner) Up(ctx context.Context, req composes.RunRequest, opts composes.UpOptions) error {
	r.mu.Lock()
	hold := r.holdUp
	r.mu.Unlock()
	if hold {
		r.upStarted <- struct{}{}
		<-r.upRelease
	}
	return r.fakeComposeRunner.Up(ctx, req, opts)
}

// Build blocks a Source's build step (one Service) until its context ends,
// so a test sees it canceled. The deploy step's build of every Service, which
// Cancel does not reach, returns at once.
func (r *holdRunner) Build(ctx context.Context, req composes.RunRequest, opts composes.BuildOptions) error {
	if opts.Service == "" {
		return r.fakeComposeRunner.Build(ctx, req, opts)
	}
	r.buildStarted <- struct{}{}
	<-ctx.Done()
	r.buildEnded <- ctx.Err()
	return ctx.Err()
}

func (r *holdRunner) release() {
	r.mu.Lock()
	r.holdUp = false
	r.mu.Unlock()
	close(r.upRelease)
}

type deploymentsFixture struct {
	server  *testServer
	project projectResponse
	env     environmentResponse
	compose composeResponse
}

func (f deploymentsFixture) path() string {
	return composePath(f.project.ID, f.env.ID, f.compose.ID)
}

func newDeploymentsServer(t *testing.T) *testServer {
	t.Helper()
	server := newTestServer(t)
	setupAndLogin(t, server)
	return server
}

func newDeploymentsFixture(t *testing.T, server *testServer, projectName, composeName, yaml string) deploymentsFixture {
	t.Helper()
	project := createProject(t, server, projectName, "")
	env := productionEnvironment(t, server, project)
	response := postJSON(t, server, composesPath(project.ID, env.ID), map[string]string{"name": composeName, "yamlContent": yaml})
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create compose status = %d, want %d", response.StatusCode, http.StatusCreated)
	}
	var compose composeResponse
	if err := json.NewDecoder(response.Body).Decode(&compose); err != nil {
		t.Fatalf("decode created compose: %v", err)
	}
	return deploymentsFixture{server: server, project: project, env: env, compose: compose}
}

const plainYAML = "services:\n  web:\n    image: nginx\n"

// buildYAML has a Service with build:, so a build Deployment has a step to run.
const buildYAML = "services:\n  web:\n    build: ./src\n"

func (f deploymentsFixture) addZipSource(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(f.dir(t), "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	err := sources.NewStore(f.server.db).Save(context.Background(), sources.Source{ComposeID: f.compose.ID, Kind: sources.KindZip, ZipName: "site.zip"})
	if err != nil {
		t.Fatalf("save source: %v", err)
	}
}

func (f deploymentsFixture) post(t *testing.T, body any) (int, map[string]any) {
	t.Helper()
	response := f.server.request(t, http.MethodPost, f.path()+"/deployments", body)
	defer response.Body.Close()
	var decoded map[string]any
	_ = json.NewDecoder(response.Body).Decode(&decoded)
	return response.StatusCode, decoded
}

// within runs fn on its own goroutine and fails the test unless it returns in time.
func within(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); fn() }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s did not return within %v", what, d)
	}
}

func waitSignal(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestDeploymentPostQueuesAndRunsADeploy(t *testing.T) {
	server := newDeploymentsServer(t)
	f := newDeploymentsFixture(t, server, "Deploys", "web", plainYAML)

	status, body := f.post(t, map[string]any{"mode": "deploy"})
	if status != http.StatusAccepted || body["number"] != float64(1) {
		t.Fatalf("POST = %d %v, want 202 {number:1}", status, body)
	}
	d := server.waitDeployment(t, f.path(), 1)
	if d.Status != "succeeded" || d.Mode != "deploy" || d.ComposeID != f.compose.ID {
		t.Fatalf("deployment = %+v, want a succeeded deploy", d)
	}
	if d.Steps == nil || d.Warnings == nil || d.StartedAt == nil || d.FinishedAt == nil {
		t.Fatalf("deployment = %+v, want steps and warnings as arrays and both times set", d)
	}
	calls := server.runner.snapshotCalls()
	// The deploy builds and checks its images first (nothing builds here),
	// so up starts them without building.
	if len(calls) != 1 || calls[0].method != "up" || calls[0].build {
		t.Fatalf("runner calls = %+v, want one up without build", calls)
	}
}

func TestDeploymentJSONUsesCamelCaseAndNeverNullArrays(t *testing.T) {
	server := newDeploymentsServer(t)
	f := newDeploymentsFixture(t, server, "Shape", "web", plainYAML)
	f.post(t, map[string]any{"mode": "deploy", "noCache": true})
	server.waitDeployment(t, f.path(), 1)

	response := server.request(t, http.MethodGet, f.path()+"/deployments/1", nil)
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"id", "composeId", "number", "mode", "noCache", "freshVolumes", "status", "step", "steps", "failedStep", "reason", "sourceRef", "sourceSubject", "warnings", "queuedAt", "startedAt", "finishedAt"} {
		if _, ok := fields[name]; !ok {
			t.Errorf("response lacks %q: %s", name, raw)
		}
	}
	if _, ok := fields["ahead"]; ok {
		t.Errorf("a finished deployment has no ahead: %s", raw)
	}
	if string(fields["noCache"]) != "true" || string(fields["warnings"]) != "[]" {
		t.Errorf("noCache = %s warnings = %s, want true and []", fields["noCache"], fields["warnings"])
	}
}

func TestDeploymentPostValidatesTheRequest(t *testing.T) {
	server := newDeploymentsServer(t)
	f := newDeploymentsFixture(t, server, "Validate", "web", plainYAML)
	blank := newDeploymentsFixture(t, server, "Blank", "empty", " ")

	tests := []struct {
		name string
		path string
		body any
		want int
		text string
	}{
		{"build without a source", f.path(), map[string]any{"mode": "build"}, http.StatusBadRequest, ""},
		{"skip-build without a source", f.path(), map[string]any{"mode": "skip-build"}, http.StatusBadRequest, ""},
		{"unknown mode", f.path(), map[string]any{"mode": "launch"}, http.StatusBadRequest, ""},
		{"no mode", f.path(), map[string]any{}, http.StatusBadRequest, ""},
		{"no body", f.path(), nil, http.StatusBadRequest, ""},
		{"fresh volumes without confirmName", f.path(), map[string]any{"mode": "deploy", "freshVolumes": true}, http.StatusBadRequest, "confirmName must match the compose name"},
		{"fresh volumes with another name", f.path(), freshBody("WEB"), http.StatusBadRequest, "confirmName must match the compose name"},
		{"blank yaml", blank.path(), map[string]any{"mode": "deploy"}, http.StatusBadRequest, "compose has no YAML content to deploy"},
		{"unknown compose", composePath(f.project.ID, f.env.ID, 9999), map[string]any{"mode": "deploy"}, http.StatusNotFound, ""},
		{"compose of another environment", composePath(f.project.ID, f.env.ID+100, f.compose.ID), map[string]any{"mode": "deploy"}, http.StatusNotFound, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := server.request(t, http.MethodPost, test.path+"/deployments", test.body)
			defer response.Body.Close()
			raw, _ := io.ReadAll(response.Body)
			if response.StatusCode != test.want {
				t.Fatalf("status = %d, want %d (%s)", response.StatusCode, test.want, raw)
			}
			if test.text != "" && !strings.Contains(string(raw), test.text) {
				t.Fatalf("body = %s, want it to contain %q", raw, test.text)
			}
		})
	}
	if calls := server.runner.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("runner calls = %+v, want none for refused requests", calls)
	}
}

func TestDeploymentListIsNewestFirst(t *testing.T) {
	server := newDeploymentsServer(t)
	f := newDeploymentsFixture(t, server, "List", "web", plainYAML)
	for i := 0; i < 3; i++ {
		server.deployOK(t, f.path(), nil)
	}
	response := server.request(t, http.MethodGet, f.path()+"/deployments", nil)
	defer response.Body.Close()
	var list []deploymentResponse
	if err := json.NewDecoder(response.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	var numbers []int
	for _, d := range list {
		numbers = append(numbers, d.Number)
	}
	if len(numbers) != 3 || numbers[0] != 3 || numbers[1] != 2 || numbers[2] != 1 {
		t.Fatalf("numbers = %v, want [3 2 1]", numbers)
	}
}

func TestDeploymentGetUnknownIs404(t *testing.T) {
	server := newDeploymentsServer(t)
	f := newDeploymentsFixture(t, server, "Missing", "web", plainYAML)
	for _, path := range []string{"/deployments/7", "/deployments/7/log", "/deployments/abc"} {
		response := server.request(t, http.MethodGet, f.path()+path, nil)
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, response.StatusCode)
		}
	}
	response := server.request(t, http.MethodPost, f.path()+"/deployments/7/cancel", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Errorf("cancel of an unknown number = %d, want 404", response.StatusCode)
	}
}

type logBody struct {
	Text   string `json:"text"`
	Offset int64  `json:"offset"`
	Done   bool   `json:"done"`
}

func (f deploymentsFixture) log(t *testing.T, number int, offset int64) logBody {
	t.Helper()
	response := f.server.request(t, http.MethodGet, f.path()+"/deployments/"+strconv.Itoa(number)+"/log?offset="+strconv.FormatInt(offset, 10), nil)
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("log status = %d, want 200", response.StatusCode)
	}
	var body logBody
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}

func TestDeploymentLogReturnsTextOffsetAndDone(t *testing.T) {
	server := newDeploymentsServer(t)
	f := newDeploymentsFixture(t, server, "Logs", "web", plainYAML)
	server.deployOK(t, f.path(), nil)

	first := f.log(t, 1, 0)
	if !strings.Contains(first.Text, "==> deploy") || first.Offset != int64(len(first.Text)) || !first.Done {
		t.Fatalf("log = %+v, want the deploy step, offset at the end and done", first)
	}
	next := f.log(t, 1, first.Offset)
	if next.Text != "" || next.Offset != first.Offset || !next.Done {
		t.Fatalf("log past the end = %+v, want empty at the same offset and done", next)
	}
	partial := f.log(t, 1, 4)
	if partial.Text != first.Text[4:] {
		t.Fatalf("log from offset 4 = %q, want %q", partial.Text, first.Text[4:])
	}
	response := server.request(t, http.MethodGet, f.path()+"/deployments/1/log?offset=-1", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("negative offset = %d, want 400", response.StatusCode)
	}
}

func TestQueuedDeploymentHasAheadAndAnEmptyLog(t *testing.T) {
	server := newDeploymentsServer(t)
	runner := newHoldRunner()
	server.useRunner(t, runner)
	runner.hold()
	running := newDeploymentsFixture(t, server, "Running", "web", plainYAML)
	queued := newDeploymentsFixture(t, server, "Queued", "api", plainYAML)
	t.Cleanup(runner.release)

	running.post(t, map[string]any{"mode": "deploy"})
	waitSignal(t, runner.upStarted, "the first deploy to reach Up")
	status, body := queued.post(t, map[string]any{"mode": "deploy"})
	if status != http.StatusAccepted || body["number"] != float64(1) {
		t.Fatalf("POST = %d %v, want 202 {number:1}", status, body)
	}

	response := server.request(t, http.MethodGet, queued.path()+"/deployments/1", nil)
	defer response.Body.Close()
	var d deploymentResponse
	if err := json.NewDecoder(response.Body).Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d.Status != "queued" || d.Ahead == nil || *d.Ahead != 1 {
		t.Fatalf("deployment = %+v ahead %v, want queued with 1 ahead", d, d.Ahead)
	}
	listResponse := server.request(t, http.MethodGet, queued.path()+"/deployments", nil)
	defer listResponse.Body.Close()
	var list []deploymentResponse
	if err := json.NewDecoder(listResponse.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Ahead == nil || *list[0].Ahead != 1 {
		t.Fatalf("list = %+v, want the queued row with 1 ahead", list)
	}
	if log := queued.log(t, 1, 0); log.Text != "" || log.Offset != 0 || log.Done {
		t.Fatalf("log of a queued deployment = %+v, want empty, offset 0, not done", log)
	}
}

func TestSecondPostWhileQueuedReturnsTheSameNumber(t *testing.T) {
	server := newDeploymentsServer(t)
	runner := newHoldRunner()
	server.useRunner(t, runner)
	runner.hold()
	running := newDeploymentsFixture(t, server, "Running", "web", plainYAML)
	queued := newDeploymentsFixture(t, server, "Queued", "api", plainYAML)
	t.Cleanup(runner.release)

	running.post(t, map[string]any{"mode": "deploy"})
	waitSignal(t, runner.upStarted, "the first deploy to reach Up")
	first, firstBody := queued.post(t, map[string]any{"mode": "deploy"})
	second, secondBody := queued.post(t, map[string]any{"mode": "deploy"})
	if first != http.StatusAccepted || second != http.StatusOK {
		t.Fatalf("statuses = %d then %d, want 202 then 200", first, second)
	}
	if firstBody["number"] != secondBody["number"] {
		t.Fatalf("numbers = %v and %v, want the same", firstBody["number"], secondBody["number"])
	}
}

func TestDeploymentPostWithDifferentOptionsIs409(t *testing.T) {
	server := newDeploymentsServer(t)
	runner := newHoldRunner()
	server.useRunner(t, runner)
	runner.hold()
	running := newDeploymentsFixture(t, server, "Running", "web", plainYAML)
	t.Cleanup(runner.release)
	running.post(t, map[string]any{"mode": "deploy"})
	waitSignal(t, runner.upStarted, "the first deploy to reach Up")

	fresh := map[string]any{"mode": "deploy", "freshVolumes": true, "confirmName": "api"}
	plain := map[string]any{"mode": "deploy"}
	cases := []struct {
		name          string
		first, second map[string]any
		wantOptions   string
	}{
		{"fresh then plain", fresh, plain, "mode=deploy, noCache=false, freshVolumes=true"},
		{"plain then fresh", plain, fresh, "mode=deploy, noCache=false, freshVolumes=false"},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			queued := newDeploymentsFixture(t, server, fmt.Sprintf("Queued %d", i), "api", plainYAML)
			status, first := queued.post(t, c.first)
			if status != http.StatusAccepted {
				t.Fatalf("first status = %d, want 202", status)
			}
			status, second := queued.post(t, c.second)
			if status != http.StatusConflict {
				t.Fatalf("second status = %d, want 409", status)
			}
			number := first["number"]
			if second["number"] != number {
				t.Fatalf("number = %v, want %v", second["number"], number)
			}
			want := fmt.Sprintf("deployment #%v is already queued with %s; cancel it first", number, c.wantOptions)
			if second["error"] != want {
				t.Fatalf("error = %v, want %q", second["error"], want)
			}
			// The same request still answers 200 with the same number.
			status, again := queued.post(t, c.first)
			if status != http.StatusOK || again["number"] != number {
				t.Fatalf("repeat = %d %v, want 200 %v", status, again, number)
			}
		})
	}
}

func TestCancelDuringDeployIs409AndNothingTheGateHoldsBlocksReads(t *testing.T) {
	server := newDeploymentsServer(t)
	runner := newHoldRunner()
	server.useRunner(t, runner)
	runner.hold()
	f := newDeploymentsFixture(t, server, "Gate", "web", plainYAML)

	f.post(t, map[string]any{"mode": "deploy"})
	waitSignal(t, runner.upStarted, "the deploy to reach Up")
	defer runner.release()

	// The Deploy step holds the Project gate. Cancel and the reads must not wait for it.
	within(t, 2*time.Second, "cancel", func() {
		response := server.request(t, http.MethodPost, f.path()+"/deployments/1/cancel", nil)
		response.Body.Close()
		if response.StatusCode != http.StatusConflict {
			t.Errorf("cancel during Deploy = %d, want 409", response.StatusCode)
		}
	})
	within(t, 2*time.Second, "log read", func() {
		response := server.request(t, http.MethodGet, f.path()+"/deployments/1/log?offset=0", nil)
		response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Errorf("log during Deploy = %d, want 200", response.StatusCode)
		}
	})
	within(t, 2*time.Second, "queue read", func() {
		response := server.request(t, http.MethodGet, "/api/deployments/queue", nil)
		defer response.Body.Close()
		var queue []struct {
			ComposeID   int64  `json:"composeId"`
			ComposeName string `json:"composeName"`
			Number      int    `json:"number"`
			Status      string `json:"status"`
			Step        string `json:"step"`
		}
		if err := json.NewDecoder(response.Body).Decode(&queue); err != nil {
			t.Errorf("decode queue: %v", err)
			return
		}
		if len(queue) != 1 || queue[0].ComposeName != "web" || queue[0].ComposeID != f.compose.ID || queue[0].Number != 1 || queue[0].Status != "running" || queue[0].Step != "deploy" {
			t.Errorf("queue = %+v, want the running deploy of web", queue)
		}
	})
}

func TestQueueEndpointIsAnEmptyArrayWhenIdle(t *testing.T) {
	server := newDeploymentsServer(t)
	response := server.request(t, http.MethodGet, "/api/deployments/queue", nil)
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || strings.TrimSpace(string(raw)) != "[]" {
		t.Fatalf("queue = %d %s, want 200 []", response.StatusCode, raw)
	}
}

func TestCancelQueuedDeploymentIs202(t *testing.T) {
	server := newDeploymentsServer(t)
	runner := newHoldRunner()
	server.useRunner(t, runner)
	runner.hold()
	running := newDeploymentsFixture(t, server, "Running", "web", plainYAML)
	queued := newDeploymentsFixture(t, server, "Queued", "api", plainYAML)
	t.Cleanup(runner.release)

	running.post(t, map[string]any{"mode": "deploy"})
	waitSignal(t, runner.upStarted, "the first deploy to reach Up")
	queued.post(t, map[string]any{"mode": "deploy"})

	response := server.request(t, http.MethodPost, queued.path()+"/deployments/1/cancel", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("cancel of a queued deployment = %d, want 202", response.StatusCode)
	}
	if d := server.waitDeployment(t, queued.path(), 1); d.Status != "canceled" {
		t.Fatalf("deployment = %s, want canceled", d.Status)
	}
	response = server.request(t, http.MethodPost, queued.path()+"/deployments/1/cancel", nil)
	response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("cancel of a finished deployment = %d, want 409", response.StatusCode)
	}
}

func TestDeletingAComposeCancelsItsBuildingDeployment(t *testing.T) {
	server := newDeploymentsServer(t)
	runner := newHoldRunner()
	server.useRunner(t, runner)
	f := newDeploymentsFixture(t, server, "Building", "web", buildYAML)
	f.addZipSource(t)

	if status, body := f.post(t, map[string]any{"mode": "build"}); status != http.StatusAccepted {
		t.Fatalf("POST = %d %v, want 202", status, body)
	}
	waitSignal(t, runner.buildStarted, "the build to start")

	within(t, 5*time.Second, "delete", func() {
		response := server.request(t, http.MethodDelete, f.path(), nil)
		response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Errorf("delete = %d, want 204", response.StatusCode)
		}
	})
	select {
	case err := <-runner.buildEnded:
		if err == nil {
			t.Fatal("the build ended without its context being canceled")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the build never saw its context canceled")
	}
}

func TestDeletingAComposeWaitsForItsDeployStep(t *testing.T) {
	server := newDeploymentsServer(t)
	runner := newHoldRunner()
	server.useRunner(t, runner)
	runner.hold()
	f := newDeploymentsFixture(t, server, "Deploying", "web", plainYAML)

	f.post(t, map[string]any{"mode": "deploy"})
	waitSignal(t, runner.upStarted, "the deploy to reach Up")

	deleted := make(chan int, 1)
	go func() {
		response := server.request(t, http.MethodDelete, f.path(), nil)
		response.Body.Close()
		deleted <- response.StatusCode
	}()
	select {
	case status := <-deleted:
		t.Fatalf("delete answered %d while the Deploy step was running", status)
	case <-time.After(200 * time.Millisecond):
	}
	runner.release()
	select {
	case status := <-deleted:
		if status != http.StatusNoContent {
			t.Fatalf("delete = %d, want 204", status)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("delete never finished after the Deploy step did")
	}
}

func TestBuildOnAGitSourceWithoutABranchIs400ButSkipBuildQueues(t *testing.T) {
	server := newDeploymentsServer(t)
	f := newDeploymentsFixture(t, server, "NoBranch", "web", plainYAML)
	err := sources.NewStore(server.db).Save(context.Background(), sources.Source{ComposeID: f.compose.ID, Kind: sources.KindGit, RepoURL: "git@example.com:a/b.git", Branch: ""})
	if err != nil {
		t.Fatalf("save source: %v", err)
	}

	if status, body := f.post(t, map[string]any{"mode": "build"}); status != http.StatusBadRequest {
		t.Fatalf("build without a branch = %d %v, want 400", status, body)
	}
	if status, body := f.post(t, map[string]any{"mode": "skip-build"}); status != http.StatusAccepted {
		t.Fatalf("skip-build without a branch = %d %v, want 202", status, body)
	}
}

func TestDeploymentPostDeployModeWithASourceIs400(t *testing.T) {
	server := newDeploymentsServer(t)
	f := newDeploymentsFixture(t, server, "WithSource", "web", plainYAML)
	f.addZipSource(t)
	status, body := f.post(t, map[string]any{"mode": "deploy"})
	if status != http.StatusBadRequest || body["error"] != "this compose has a source; use build or skip-build" {
		t.Fatalf("POST = %d %v", status, body)
	}
	if calls := server.runner.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("runner calls = %+v, want none", calls)
	}
}
