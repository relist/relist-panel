package httpapi_test

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
)

type composeVariableResponse struct {
	ID        int64  `json:"id"`
	ComposeID int64  `json:"composeId"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	Secret    bool   `json:"secret"`
}

type composeVariablesFixture struct {
	server        *testServer
	projectID     int64
	environmentID int64
	composeID     int64
}

func newComposeVariablesFixture(t *testing.T, server *testServer, yaml string) composeVariablesFixture {
	t.Helper()
	project := createProject(t, server, "Shop", "")
	environment := defaultEnvironment(t, server, project.ID)
	created := postJSON(t, server, composesPath(project.ID, environment.ID), map[string]string{"name": "web", "yamlContent": yaml})
	defer created.Body.Close()
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create compose status = %d", created.StatusCode)
	}
	var compose composeResponse
	if err := json.NewDecoder(created.Body).Decode(&compose); err != nil {
		t.Fatal(err)
	}
	return composeVariablesFixture{server: server, projectID: project.ID, environmentID: environment.ID, composeID: compose.ID}
}

func (f composeVariablesFixture) path(suffix string) string {
	return composePath(f.projectID, f.environmentID, f.composeID) + "/variables" + suffix
}

func (f composeVariablesFixture) send(t *testing.T, method, suffix string, body any) (*http.Response, []byte) {
	t.Helper()
	response := f.server.request(t, method, f.path(suffix), body)
	defer response.Body.Close()
	var raw bytes.Buffer
	_, _ = raw.ReadFrom(response.Body)
	return response, raw.Bytes()
}

func (f composeVariablesFixture) create(t *testing.T, key, value string, secret bool) composeVariableResponse {
	t.Helper()
	response, raw := f.send(t, http.MethodPost, "", map[string]any{"key": key, "value": value, "secret": secret})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create %s status = %d, body %s", key, response.StatusCode, raw)
	}
	var variable composeVariableResponse
	if err := json.Unmarshal(raw, &variable); err != nil {
		t.Fatal(err)
	}
	return variable
}

func (f composeVariablesFixture) variablesChangedAt(t *testing.T) *string {
	t.Helper()
	response := f.server.request(t, http.MethodGet, composePath(f.projectID, f.environmentID, f.composeID), nil)
	defer response.Body.Close()
	var compose struct {
		VariablesChangedAt *string `json:"variablesChangedAt"`
	}
	if err := json.NewDecoder(response.Body).Decode(&compose); err != nil {
		t.Fatal(err)
	}
	return compose.VariablesChangedAt
}

// deployEnvFile deploys the Compose and returns the .env the runner was handed.
func (f composeVariablesFixture) deployEnvFile(t *testing.T) (envFile, composeFile, projectName string) {
	t.Helper()
	f.server.runner.calls = nil
	response := f.server.request(t, http.MethodPost, composeDeployPath(f.projectID, f.environmentID, f.composeID), nil)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("deploy status = %d", response.StatusCode)
	}
	for _, call := range f.server.runner.calls {
		if call.method == "up" {
			content, err := os.ReadFile(call.request.EnvFilePath)
			if err != nil {
				t.Fatal(err)
			}
			return string(content), call.request.ComposeFilePath, call.request.ProjectName
		}
	}
	t.Fatal("deploy never reached runner")
	return "", "", ""
}

func (f composeVariablesFixture) createEnvironmentVariable(t *testing.T, key, value string) {
	t.Helper()
	response := postJSON(t, f.server, environmentVariablesPath(f.projectID, f.environmentID), map[string]any{"key": key, "value": value})
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create environment variable %s status = %d", key, response.StatusCode)
	}
}

func TestComposeVariablesCRUD(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	f := newComposeVariablesFixture(t, server, "services: {web: {image: nginx}}")

	response, raw := f.send(t, http.MethodGet, "", nil)
	if response.StatusCode != http.StatusOK || strings.TrimSpace(string(raw)) != "[]" {
		t.Fatalf("empty list = %d %s", response.StatusCode, raw)
	}

	created := f.create(t, "LOG_LEVEL", "debug", false)
	if created.ID == 0 || created.ComposeID != f.composeID || created.Key != "LOG_LEVEL" || created.Value != "debug" || created.Secret {
		t.Fatalf("created = %+v", created)
	}
	secret := f.create(t, "API_KEY", "s3cret", true)
	if !secret.Secret {
		t.Fatalf("secret flag not stored: %+v", secret)
	}

	response, raw = f.send(t, http.MethodPut, "/"+idString(created.ID), map[string]any{"key": "LOG_LEVEL", "value": "info", "secret": true})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, body %s", response.StatusCode, raw)
	}
	var updated composeVariableResponse
	_ = json.Unmarshal(raw, &updated)
	if updated.ID != created.ID || updated.Value != "info" || !updated.Secret {
		t.Fatalf("updated = %+v", updated)
	}

	_, raw = f.send(t, http.MethodGet, "", nil)
	var list []composeVariableResponse
	if err := json.Unmarshal(raw, &list); err != nil || len(list) != 2 || list[0].ID != created.ID || list[1].ID != secret.ID || list[0].Value != "info" {
		t.Fatalf("list = %s (%v)", raw, err)
	}

	if response, _ = f.send(t, http.MethodDelete, "/"+idString(created.ID), nil); response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", response.StatusCode)
	}
	if response, _ = f.send(t, http.MethodDelete, "/"+idString(created.ID), nil); response.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete status = %d, want 404", response.StatusCode)
	}
	if response, _ = f.send(t, http.MethodPut, "/"+idString(created.ID), map[string]any{"key": "A", "value": "b"}); response.StatusCode != http.StatusNotFound {
		t.Fatalf("update of deleted status = %d, want 404", response.StatusCode)
	}
}

func TestComposeVariablesValidation(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	f := newComposeVariablesFixture(t, server, "services: {web: {image: nginx}}")
	f.create(t, "LOG_LEVEL", "debug", false)
	second := f.create(t, "OTHER", "x", false)

	for name, body := range map[string]map[string]any{
		"dash in key":   {"key": "MY-KEY", "value": "v"},
		"empty key":     {"key": " ", "value": "v"},
		"missing value": {"key": "NOVALUE"},
		"unknown field": {"key": "A", "value": "v", "extra": 1},
	} {
		if response, raw := f.send(t, http.MethodPost, "", body); response.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (%s)", name, response.StatusCode, raw)
		}
	}
	if response, _ := f.send(t, http.MethodPost, "", map[string]any{"key": "LOG_LEVEL", "value": "other"}); response.StatusCode != http.StatusConflict {
		t.Errorf("duplicate create status = %d, want 409", response.StatusCode)
	}
	if response, _ := f.send(t, http.MethodPut, "/"+idString(second.ID), map[string]any{"key": "LOG_LEVEL", "value": "x"}); response.StatusCode != http.StatusConflict {
		t.Errorf("duplicate update status = %d, want 409", response.StatusCode)
	}

	// The same key in a different Compose is fine: uniqueness is per Compose.
	other := createCompose(t, server, f.projectID, f.environmentID, "other")
	response := postJSON(t, server, composePath(f.projectID, f.environmentID, other.ID)+"/variables", map[string]any{"key": "LOG_LEVEL", "value": "v"})
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Errorf("same key in another compose status = %d, want 201", response.StatusCode)
	}
}

func TestComposeVariablesRouteScoping(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	f := newComposeVariablesFixture(t, server, "services: {web: {image: nginx}}")
	variable := f.create(t, "A", "1", false)

	otherProject := createProject(t, server, "Blog", "")
	otherEnvironment := defaultEnvironment(t, server, otherProject.ID)
	crossPath := composePath(otherProject.ID, otherEnvironment.ID, f.composeID) + "/variables"
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, crossPath},
		{http.MethodPost, crossPath},
		{http.MethodPut, crossPath + "/" + idString(variable.ID)},
		{http.MethodDelete, crossPath + "/" + idString(variable.ID)},
		{http.MethodGet, composePath(f.projectID, f.environmentID, 9999) + "/variables"},
	} {
		response := server.request(t, c.method, c.path, map[string]any{"key": "B", "value": "2"})
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s status = %d, want 404", c.method, c.path, response.StatusCode)
		}
	}
	// Nothing leaked through the wrong route.
	if _, raw := f.send(t, http.MethodGet, "", nil); !strings.Contains(string(raw), `"A"`) || strings.Contains(string(raw), `"B"`) {
		t.Errorf("list after cross-route attempts = %s", raw)
	}
}

func TestComposeVariablesCascadeWithCompose(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	f := newComposeVariablesFixture(t, server, "services: {web: {image: nginx}}")
	f.create(t, "A", "1", false)

	response := server.request(t, http.MethodDelete, composePath(f.projectID, f.environmentID, f.composeID), nil)
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("delete compose status = %d", response.StatusCode)
	}
	var count int
	if err := server.db.QueryRow("SELECT COUNT(*) FROM compose_variables WHERE compose_id = ?", f.composeID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("compose variables left after compose delete = %d", count)
	}
}

func TestVariablesChangedAtMovesOnWritesOnly(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	f := newComposeVariablesFixture(t, server, "services: {web: {image: nginx}}")

	const past = "1970-01-01T00:00:01Z"
	backdate := func() {
		t.Helper()
		if _, err := server.db.Exec("UPDATE composes SET variables_changed_at = 1 WHERE id = ?", f.composeID); err != nil {
			t.Fatal(err)
		}
		if got := f.variablesChangedAt(t); got == nil || *got != past {
			t.Fatalf("backdated variablesChangedAt = %v, want %s", got, past)
		}
	}
	moved := func(what string) {
		t.Helper()
		if got := f.variablesChangedAt(t); got == nil || *got == past {
			t.Errorf("variablesChangedAt after %s = %v, want it moved", what, got)
		}
	}

	if got := f.variablesChangedAt(t); got != nil {
		t.Fatalf("initial variablesChangedAt = %v, want null", *got)
	}
	variable := f.create(t, "A", "1", false)
	moved("create")

	backdate()
	f.send(t, http.MethodGet, "", nil)
	f.deployEnvFile(t)
	if got := f.variablesChangedAt(t); got == nil || *got != past {
		t.Errorf("variablesChangedAt after read and deploy = %v, want unchanged", got)
	}

	// A rejected write (409) must not stamp: the stamp shares the write's transaction.
	if response, _ := f.send(t, http.MethodPost, "", map[string]any{"key": "A", "value": "2"}); response.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate create status = %d", response.StatusCode)
	}
	if got := f.variablesChangedAt(t); got == nil || *got != past {
		t.Errorf("variablesChangedAt after rejected create = %v, want unchanged", got)
	}

	f.send(t, http.MethodPut, "/"+idString(variable.ID), map[string]any{"key": "A", "value": "2"})
	moved("update")
	backdate()
	f.send(t, http.MethodDelete, "/"+idString(variable.ID), nil)
	moved("delete")
}

func TestDeployMergesComposeVariablesOverEnvironmentVariables(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	f := newComposeVariablesFixture(t, server, "services: {web: {image: nginx}}")
	f.createEnvironmentVariable(t, "FIRST", "env-1")
	f.createEnvironmentVariable(t, "SHARED", "env-shared")
	f.createEnvironmentVariable(t, "LAST", "env-last")
	// Created before the override so ID order differs from Environment order.
	f.create(t, "ONLY_B", "b", false)
	f.create(t, "SHARED", "compose-shared", false)
	f.create(t, "ONLY_A", "a", false)

	envFile, _, _ := f.deployEnvFile(t)
	want := "FIRST=\"env-1\"\nSHARED=\"compose-shared\"\nLAST=\"env-last\"\nONLY_B=\"b\"\nONLY_A=\"a\"\n"
	if envFile != want {
		t.Fatalf(".env =\n%s\nwant\n%s", envFile, want)
	}
}

func TestDeployWithoutComposeVariablesKeepsEnvFileUnchanged(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	f := newComposeVariablesFixture(t, server, "services: {web: {image: nginx}}")
	f.createEnvironmentVariable(t, "A", "1")
	f.createEnvironmentVariable(t, "B", "two words $x")

	envFile, _, _ := f.deployEnvFile(t)
	if want := "A=\"1\"\nB=\"two words $$x\"\n"; envFile != want {
		t.Fatalf(".env = %q, want %q", envFile, want)
	}

	// Adding then removing a Compose Variable leaves the file as it was.
	variable := f.create(t, "C", "3", false)
	f.send(t, http.MethodDelete, "/"+idString(variable.ID), nil)
	if again, _, _ := f.deployEnvFile(t); again != envFile {
		t.Fatalf(".env after add+delete = %q, want %q", again, envFile)
	}
}

func TestComposeVariableIsInterpolatedByCompose(t *testing.T) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("Docker CLI unavailable")
	}
	if err := exec.Command(docker, "compose", "version").Run(); err != nil {
		t.Skip("Docker Compose CLI unavailable")
	}
	server := newTestServer(t)
	setupAndLogin(t, server)
	f := newComposeVariablesFixture(t, server, "services:\n  web:\n    image: nginx:${TAG}\n")
	f.createEnvironmentVariable(t, "TAG", "from-environment")
	f.create(t, "TAG", "from-compose", false)

	f.deployEnvFile(t)
	var composeFile, envFile, projectName string
	for _, call := range server.runner.calls {
		if call.method == "up" {
			composeFile, envFile, projectName = call.request.ComposeFilePath, call.request.EnvFilePath, call.request.ProjectName
		}
	}
	cmd := exec.Command(docker, "compose", "-p", projectName, "-f", composeFile, "--env-file", envFile, "config", "--format", "json")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("compose config failed: %v\n%s", err, stderr.String())
	}
	var config struct {
		Services map[string]struct {
			Image string `json:"image"`
		} `json:"services"`
	}
	if err := json.Unmarshal(output, &config); err != nil {
		t.Fatal(err)
	}
	if got := config.Services["web"].Image; got != "nginx:from-compose" {
		t.Fatalf("interpolated image = %q, want nginx:from-compose", got)
	}
}

func TestComposeVariablesAPIDoesNotLogValues(t *testing.T) {
	server := newTestServer(t)
	setupAndLogin(t, server)
	f := newComposeVariablesFixture(t, server, "services: {web: {image: nginx}}")

	var captured bytes.Buffer
	log.SetOutput(&captured)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	sentinel := "sentinel-hunter-2-supersecret"
	created := f.create(t, "JWT_SECRET", sentinel, true)
	f.send(t, http.MethodGet, "", nil)
	f.send(t, http.MethodPut, "/"+idString(created.ID), map[string]any{"key": "JWT_SECRET", "value": sentinel + "-rotated", "secret": true})
	f.send(t, http.MethodPost, "", map[string]any{"key": "MY-KEY", "value": sentinel})
	f.send(t, http.MethodPost, "", map[string]any{"key": "JWT_SECRET", "value": sentinel})
	f.deployEnvFile(t)
	f.send(t, http.MethodDelete, "/"+idString(created.ID), nil)
	response := server.request(t, http.MethodPost, composePath(f.projectID, f.environmentID, 9999)+"/variables", map[string]any{"key": "A", "value": sentinel})
	response.Body.Close()

	if strings.Contains(captured.String(), sentinel) {
		t.Fatalf("log output contains a variable value:\n%s", captured.String())
	}
}
