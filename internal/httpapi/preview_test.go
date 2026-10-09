package httpapi_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"relist-panel/internal/httpapi"
	"relist-panel/internal/volumes"
)

type previewBody struct {
	YAML     string   `json:"yaml"`
	Warnings []string `json:"warnings"`
	Error    string   `json:"error"`
}

func (f domainsFixture) preview(t *testing.T, yaml string) (int, previewBody, []byte) {
	t.Helper()
	response, raw := f.send(t, http.MethodPost, "/preview", map[string]any{"yamlContent": yaml})
	var body previewBody
	_ = json.Unmarshal(raw, &body)
	return response.StatusCode, body, raw
}

func TestPreviewResolvesTheDraftWithoutDeploying(t *testing.T) {
	f := newDeployDomainsFixture(t)
	domain := f.createDomain(t, "api.example.com", "/")
	environment := productionEnvironment(t, f.server, f.project)
	secret := "s3cr3t-preview-value"
	created := postJSON(t, f.server, environmentVariablesPath(f.project.ID, environment.ID), map[string]any{"key": "API_TOKEN", "value": secret})
	created.Body.Close()
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create variable status = %d", created.StatusCode)
	}

	// The draft differs from the saved YAML: it adds a worker the saved file lacks.
	draft := "services:\n  web:\n    image: nginx:1.27\n    environment:\n      TOKEN: ${API_TOKEN}\n  worker:\n    image: busybox\n"
	status, body, raw := f.preview(t, draft)
	if status != http.StatusOK {
		t.Fatalf("preview status = %d, body %s", status, raw)
	}
	router := "relist-c" + strconv.FormatInt(f.composeID, 10) + "-d" + strconv.FormatInt(domain.ID, 10)
	envFile := filepath.Join(f.server.dataDir, ".composes", strconv.FormatInt(f.composeID, 10), ".env")
	for _, want := range []string{"worker:", "relist-network", envFile, "traefik.http.routers." + router + ".rule=Host(`api.example.com`)", "${API_TOKEN}",
		"web.relist-c" + strconv.FormatInt(f.composeID, 10) + ".internal", "web.web.production.deploy-domains.internal"} {
		if !strings.Contains(body.YAML, want) {
			t.Fatalf("preview yaml = %s, want it to contain %q", body.YAML, want)
		}
	}
	if strings.Contains(body.YAML, "worker.relist-c") {
		t.Fatalf("preview yaml = %s, want worker (no domain, no traefik labels) off relist-network", body.YAML)
	}
	if body.Warnings == nil || len(body.Warnings) != 0 || !strings.Contains(string(raw), `"warnings":[]`) {
		t.Fatalf("body = %s, want warnings:[]", raw)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("preview leaked a variable value: %s", raw)
	}
	if len(f.server.runner.calls) != 0 {
		t.Fatalf("runner calls = %v, want none", f.server.runner.calls)
	}
	if _, err := os.Stat(filepath.Dir(envFile)); !os.IsNotExist(err) {
		t.Fatalf("compose dir stat err = %v, want it absent", err)
	}
	response, saved := f.send(t, http.MethodGet, "", nil)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(saved), `"yamlContent":"`+strings.ReplaceAll(domainsYAML, `"`, `\"`)+`"`) {
		t.Fatalf("saved compose = %s, want the original yaml untouched", saved)
	}
}

func TestPreviewReturnsDeployWarnings(t *testing.T) {
	f := newDeployDomainsFixture(t)
	f.createDomain(t, "api.example.com", "/")
	status, body, raw := f.preview(t, "services:\n  web:\n    image: nginx\n    labels:\n      - traefik.enable=false\n")
	if status != http.StatusOK {
		t.Fatalf("status = %d, body %s", status, raw)
	}
	if len(body.Warnings) != 1 || !strings.Contains(body.Warnings[0], "traefik.enable=false was overridden") {
		t.Fatalf("warnings = %v, want the enable warning", body.Warnings)
	}
}

func TestPreviewRefusesWhatADeployWouldRefuse(t *testing.T) {
	f := newDeployDomainsFixture(t)
	f.createDomain(t, "api.example.com", "/")
	cases := []struct {
		name, yaml, want string
	}{
		{"blank", "  \n", "compose has no YAML content to deploy"},
		{"invalid", "services: [", "compose yaml is invalid: "},
		{"service removed", "services: {db: {image: postgres:16}}", `domain api.example.com/ points at service "web", which is not in the compose file`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, body, raw := f.preview(t, c.yaml)
			if status != http.StatusBadRequest || !strings.HasPrefix(body.Error, c.want) {
				t.Fatalf("status = %d, body %s, want 400 %q", status, raw, c.want)
			}
		})
	}
	if response, raw := f.send(t, http.MethodPost, "/preview", map[string]any{}); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing yamlContent status = %d, body %s", response.StatusCode, raw)
	}
	missing := strings.TrimSuffix(f.base, strconv.FormatInt(f.composeID, 10)) + "999"
	response := f.server.request(t, http.MethodPost, missing+"/preview", map[string]any{"yamlContent": domainsYAML})
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown compose status = %d", response.StatusCode)
	}
}

// A preview only reads the database, so it must answer while a deploy of the
// same Project is still running.
func TestPreviewDoesNotWaitForInFlightDeploy(t *testing.T) {
	f := newDeployDomainsFixture(t)
	server := f.server
	runner := &blockingLifecycleRunner{fakeComposeRunner: newFakeComposeRunner(), started: make(chan struct{}), release: make(chan struct{}), stopped: make(chan struct{})}
	server.handler = httpapi.NewHandler(server.db, runner, server.dataDir, nil, nil, server.volumeRunner, volumes.NewResolver(server.volumeCLI, server.dataDir), nil, nil)
	deployDone := make(chan *http.Response, 1)
	go func() { deployDone <- server.request(t, http.MethodPost, f.base+"/deploy", nil) }()
	select {
	case <-runner.started:
	case <-time.After(time.Second):
		t.Fatal("deploy did not start")
	}
	defer func() {
		close(runner.release)
		select {
		case r := <-deployDone:
			r.Body.Close()
		case <-time.After(time.Second):
			t.Fatal("deploy did not complete")
		}
	}()

	previewDone := make(chan int, 1)
	go func() {
		response := server.request(t, http.MethodPost, f.base+"/preview", map[string]any{"yamlContent": domainsYAML})
		response.Body.Close()
		previewDone <- response.StatusCode
	}()
	select {
	case status := <-previewDone:
		if status != http.StatusOK {
			t.Fatalf("preview status = %d", status)
		}
	case <-time.After(time.Second):
		t.Fatal("preview waited for the in-flight deploy")
	}
}
