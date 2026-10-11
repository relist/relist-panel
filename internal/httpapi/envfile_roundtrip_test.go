package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Compose's real parser is the contract here. Config needs the CLI, not a
// Docker daemon; deployments still use the HTTP seam's recording runner.
func TestDeployEnvironmentValuesRoundTripThroughCompose(t *testing.T) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("Docker CLI unavailable")
	}
	if err := exec.Command(docker, "compose", "version").Run(); err != nil {
		t.Skip("Docker Compose CLI unavailable")
	}
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Literal Variables", "")
	environment := defaultEnvironment(t, server, project.ID)
	values := map[string]string{
		"ENV_LITERAL_EMPTY":   "",
		"ENV_LITERAL_QUOTES":  "apostrophe's and \"double\"",
		"ENV_LITERAL_SLASH":   "C:\\files\\new\\tab\\\"quoted\\'",
		"ENV_LITERAL_LINES":   "first\nINJECTED=value\r\nlast\rreturn\ttab",
		"ENV_LITERAL_DOLLARS": "$UNSET ${HOME} $$ $1",
		"ENV_LITERAL_HASH":    "  value # literal comment  ",
		"ENV_LITERAL_UNICODE": "ภาษาไทย 🌍",
	}
	for key, value := range values {
		response := postJSON(t, server, environmentVariablesPath(project.ID, environment.ID), map[string]any{"key": key, "value": value})
		response.Body.Close()
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("create %s status = %d", key, response.StatusCode)
		}
	}
	created := postJSON(t, server, composesPath(project.ID, environment.ID), map[string]string{
		"name":        "web",
		"yamlContent": "services:\n  web:\n    image: nginx\n    environment:\n      ENV_LITERAL_INTERPOLATED: ${ENV_LITERAL_DOLLARS}\n",
	})
	var compose composeResponse
	if err := json.NewDecoder(created.Body).Decode(&compose); err != nil {
		t.Fatalf("decode compose: %v", err)
	}
	created.Body.Close()
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create compose status = %d", created.StatusCode)
	}
	server.deployOK(t, composePath(project.ID, environment.ID, compose.ID), map[string]any{"mode": "deploy"})
	var composeFile, envFile, projectName string
	for _, call := range server.runner.snapshotCalls() {
		if call.method == "up" {
			composeFile, envFile, projectName = call.request.ComposeFilePath, call.request.EnvFilePath, call.request.ProjectName
		}
	}
	if envFile == "" {
		t.Fatal("deploy never reached runner")
	}
	cmd := exec.Command(docker, "compose", "-p", projectName, "-f", composeFile, "--env-file", envFile, "config", "--format", "json")
	// Do not allow host values to override the test's .env values.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("compose config failed: %v\n%s", err, stderr.String())
	}
	var config struct {
		Services map[string]struct {
			Environment map[string]string `json:"environment"`
		} `json:"services"`
	}
	if err := json.Unmarshal(output, &config); err != nil {
		t.Fatalf("decode compose config: %v\n%s", err, output)
	}
	actual := config.Services["web"].Environment
	for key, want := range values {
		// Compose config re-escapes dollars for subsequent interpolation in
		// its rendered model; the container receives one literal dollar.
		if got, ok := actual[key]; !ok || got != strings.ReplaceAll(want, "$", "$$") {
			t.Errorf("%s = %q (present %t), want %q", key, got, ok, want)
		}
	}
	if _, injected := actual["INJECTED"]; injected {
		t.Error("a multiline value injected an extra environment variable")
	}
	if got, want := actual["ENV_LITERAL_INTERPOLATED"], strings.ReplaceAll(values["ENV_LITERAL_DOLLARS"], "$", "$$"); got != want {
		t.Errorf("Compose interpolation = %q, want %q", got, want)
	}
}

func TestEnvironmentVariableRejectsNULWithoutChangingStoredValue(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	project := createProject(t, server, "Valid Environment", "")
	environment := defaultEnvironment(t, server, project.ID)
	collection := environmentVariablesPath(project.ID, environment.ID)
	response := postJSON(t, server, collection, map[string]string{"key": "BAD", "value": "before\x00after"})
	response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("create NUL value status = %d, want 400", response.StatusCode)
	}
	created := postJSON(t, server, collection, map[string]string{"key": "GOOD", "value": "original"})
	var variable environmentVariableResponse
	if err := json.NewDecoder(created.Body).Decode(&variable); err != nil {
		t.Fatalf("decode variable: %v", err)
	}
	created.Body.Close()
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create normal value status = %d", created.StatusCode)
	}
	updated := server.request(t, http.MethodPut, variablePath(project.ID, environment.ID, variable.ID), map[string]string{"key": "GOOD", "value": "before\x00after"})
	updated.Body.Close()
	if updated.StatusCode != http.StatusBadRequest {
		t.Fatalf("update NUL value status = %d, want 400", updated.StatusCode)
	}
	listed := server.request(t, http.MethodGet, collection, nil)
	defer listed.Body.Close()
	var variables []environmentVariableResponse
	if err := json.NewDecoder(listed.Body).Decode(&variables); err != nil {
		t.Fatalf("decode variables: %v", err)
	}
	if len(variables) != 1 || variables[0].Value != "original" || variables[0].Key != "GOOD" {
		t.Fatalf("variables after rejected writes = %+v, want only the unchanged original", variables)
	}
}
