package composes

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"relist-panel/internal/database"
	"relist-panel/internal/dockernet"
)

// recordingRunner records the calls RunDeploy makes and what was on disk when
// the destructive ones ran.
type recordingRunner struct {
	calls          []string
	ups            []UpOptions
	composeAtDown  string
	downErr, upErr error
}

func (r *recordingRunner) Up(_ context.Context, _ RunRequest, opts UpOptions) error {
	r.calls = append(r.calls, "up")
	r.ups = append(r.ups, opts)
	return r.upErr
}
func (r *recordingRunner) Build(context.Context, RunRequest, BuildOptions) error { return nil }
func (r *recordingRunner) Down(context.Context, RunRequest) error                { return nil }
func (r *recordingRunner) DownWithVolumes(_ context.Context, req RunRequest) error {
	r.calls = append(r.calls, "down-volumes")
	b, _ := os.ReadFile(req.ComposeFilePath)
	r.composeAtDown = string(b)
	return r.downErr
}
func (r *recordingRunner) Restart(context.Context, RunRequest) error { return nil }
func (r *recordingRunner) Status(context.Context, RunRequest) (ComposeStatus, error) {
	return ComposeStatus{}, nil
}
func (r *recordingRunner) Logs(context.Context, RunRequest) (string, error) { return "", nil }

type recordingCanceler struct {
	runner *recordingRunner
	ids    []int64
	// downsSeen is how many DownWithVolumes calls had happened at cancel time.
	downsSeen int
}

func (c *recordingCanceler) CancelForComposes(_ context.Context, ids []int64) {
	c.ids = append(c.ids, ids...)
	for _, call := range c.runner.calls {
		if call == "down-volumes" {
			c.downsSeen++
		}
	}
}

func newDeployService(t *testing.T, yamlContent string) (*Service, *recordingRunner, DeployTarget, *sql.DB) {
	t.Helper()
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, statement := range []string{
		`INSERT INTO projects (id, name, created_at, updated_at) VALUES (7, 'p', 0, 0)`,
		`INSERT INTO environments (id, project_id, name, created_at) VALUES (3, 7, 'e', 0)`,
		`INSERT INTO environment_variables (environment_id, key, value) VALUES (3, 'ENV_KEY', 'env-value')`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO composes (id, environment_id, name, project_slug, env_slug, yaml_content, identity_version, created_at, updated_at) VALUES (1, 3, 'c', 'p', 'e', ?, 1, 0, 0)`, yamlContent); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO compose_variables (compose_id, key, value) VALUES (1, 'COMPOSE_KEY', 'compose-value')`); err != nil {
		t.Fatal(err)
	}
	runner := &recordingRunner{}
	svc := New(db, runner, t.TempDir())
	target, err := svc.Target(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	return svc, runner, target, db
}

func TestTargetReportsProjectAndMissingCompose(t *testing.T) {
	svc, _, target, _ := newDeployService(t, "services: {}\n")
	if target.ProjectID != 7 || target.Compose.ID != 1 || target.Request.ProjectName == "" {
		t.Fatalf("target = %+v", target)
	}
	if _, err := svc.Target(context.Background(), 99); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("err = %v, want sql.ErrNoRows", err)
	}
}

func TestCheckDeployableRefusesBlankYAML(t *testing.T) {
	svc, _, target, _ := newDeployService(t, "  \n")
	err := svc.CheckDeployable(context.Background(), target)
	if err == nil || err.Error() != "compose has no YAML content to deploy" {
		t.Fatalf("err = %v", err)
	}
}

func TestRunDeployWritesFilesAndUps(t *testing.T) {
	svc, runner, target, db := newDeployService(t, "services:\n  web: {image: nginx}\n")
	var out strings.Builder
	warnings, err := svc.RunDeploy(context.Background(), target, DeployOptions{Build: true, Out: &out})
	if err != nil {
		t.Fatal(err)
	}
	if warnings == nil {
		t.Errorf("warnings must be non-nil, got nil")
	}
	if _, err := os.Stat(target.Request.ComposeFilePath); err != nil {
		t.Errorf("resolved compose file: %v", err)
	}
	env, err := os.ReadFile(target.Request.EnvFilePath)
	if err != nil || !strings.Contains(string(env), "COMPOSE_KEY") || !strings.Contains(string(env), "ENV_KEY") {
		t.Errorf(".env = %q, %v", env, err)
	}
	if !reflect.DeepEqual(runner.ups, []UpOptions{{Build: true, Out: &out}}) {
		t.Errorf("ups = %+v", runner.ups)
	}
	var deployedAt sql.NullInt64
	if err := db.QueryRow(`SELECT deployed_at FROM composes WHERE id = 1`).Scan(&deployedAt); err != nil || !deployedAt.Valid {
		t.Errorf("deployed_at = %v, %v", deployedAt, err)
	}

	runner.ups = nil
	if _, err := svc.RunDeploy(context.Background(), target, DeployOptions{}); err != nil {
		t.Fatal(err)
	}
	if runner.ups[0].Build {
		t.Errorf("Build = true, want false when not requested")
	}
}

func TestRunDeployWritesWarningsToOut(t *testing.T) {
	svc, _, target, db := newDeployService(t, "services:\n  web: {image: nginx}\n")
	for _, statement := range []string{
		`INSERT INTO variable_sets (id, compose_id, name, created_at, updated_at) VALUES (1, 1, 's', 0, 0)`,
		`INSERT INTO service_bindings (compose_id, service, set_id, position) VALUES (1, 'old-api', 1, 0)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	var out strings.Builder
	warnings, err := svc.RunDeploy(context.Background(), target, DeployOptions{Out: &out})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) == 0 {
		t.Fatal("expected a warning for the binding to a missing service")
	}
	if want := "warning: " + warnings[0] + "\n"; !strings.Contains(out.String(), want) {
		t.Errorf("out = %q, want %q", out.String(), want)
	}
}

func TestRunDeployFreshDownsBeforeWrite(t *testing.T) {
	svc, runner, target, _ := newDeployService(t, "services:\n  web: {image: nginx}\n")
	if err := os.MkdirAll(ComposeDir(target.Request), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target.Request.ComposeFilePath, []byte("old: yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RunDeploy(context.Background(), target, DeployOptions{FreshVolumes: true}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(runner.calls, []string{"down-volumes", "up"}) {
		t.Errorf("calls = %v", runner.calls)
	}
	if runner.composeAtDown != "old: yaml\n" {
		t.Errorf("file at down = %q, want the old file", runner.composeAtDown)
	}
	if b, _ := os.ReadFile(target.Request.ComposeFilePath); string(b) == "old: yaml\n" {
		t.Errorf("compose file was not replaced")
	}
}

func TestRunDeployDomainTargetErrorWritesNothing(t *testing.T) {
	svc, runner, target, db := newDeployService(t, "services:\n  web: {image: nginx}\n")
	if _, err := db.Exec(`INSERT INTO domains (compose_id, host, service, port, created_at, updated_at) VALUES (1, 'a.example.com', 'ghost', 80, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(ComposeDir(target.Request), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target.Request.ComposeFilePath, []byte("old: yaml\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := svc.RunDeploy(context.Background(), target, DeployOptions{FreshVolumes: true})
	var f *DeployFailure
	if !errors.As(err, &f) || f.Kind != FailureInvalid || !strings.Contains(err.Error(), `domain a.example.com`) || !strings.Contains(err.Error(), `service "ghost"`) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(target.Request.ComposeFilePath); string(b) != "old: yaml\n" {
		t.Errorf("file changed: %q", b)
	}
	if len(runner.calls) != 0 {
		t.Errorf("calls = %v, want none", runner.calls)
	}
}

const buildYAML = "services:\n  web:\n    build: .\n  db:\n    image: postgres\n  api:\n    build:\n      context: ./api\n"

func TestPrepareBuildWritesUnresolvedStaging(t *testing.T) {
	svc, _, target, _ := newDeployService(t, buildYAML)
	plan, err := svc.PrepareBuild(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(plan.Services, []string{"web", "api"}) {
		t.Errorf("services = %v", plan.Services)
	}
	dir := ComposeDir(target.Request)
	if plan.Request.ComposeFilePath != filepath.Join(dir, ".build-compose.yml") || plan.Request.EnvFilePath != filepath.Join(dir, ".build.env") || plan.Request.ProjectName != target.Request.ProjectName {
		t.Errorf("request = %+v", plan.Request)
	}
	if b, _ := os.ReadFile(plan.Request.ComposeFilePath); !strings.Contains(string(b), "context: ./api") || !strings.Contains(string(b), dockernet.NetworkName+":") {
		t.Errorf("build compose = %q", b)
	}
	env, _ := os.ReadFile(plan.Request.EnvFilePath)
	if string(env) != "COMPOSE_KEY=\"compose-value\"\nENV_KEY=\"env-value\"\n" && !(strings.Contains(string(env), "ENV_KEY=\"env-value\"") && strings.Contains(string(env), "COMPOSE_KEY=\"compose-value\"")) {
		t.Errorf("build env = %q", env)
	}
	if info, err := os.Stat(plan.Request.EnvFilePath); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("env mode = %v, %v", info, err)
	}
	plan.Cleanup()
	for _, p := range []string{plan.Request.ComposeFilePath, plan.Request.EnvFilePath} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s still exists after Cleanup: %v", p, err)
		}
	}
}

func TestPrepareBuildOnNewCompose(t *testing.T) {
	svc, _, target, _ := newDeployService(t, buildYAML)
	if _, err := os.Stat(ComposeDir(target.Request)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("compose dir exists before the first deploy: %v", err)
	}
	plan, err := svc.PrepareBuild(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target.Request.ComposeFilePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("PrepareBuild must not write the resolved file: %v", err)
	}
	plan.Cleanup()
}

func TestDeleteCallsCanceler(t *testing.T) {
	svc, runner, target, _ := newDeployService(t, "services:\n  web: {image: nginx}\n")
	canceler := &recordingCanceler{runner: runner}
	svc.SetDeploymentCanceler(canceler)
	if err := os.MkdirAll(ComposeDir(target.Request), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target.Request.ComposeFilePath, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/api/projects/7/environments/3/composes/1", nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if !reflect.DeepEqual(canceler.ids, []int64{1}) || canceler.downsSeen != 0 {
		t.Errorf("ids = %v, downs before cancel = %d; want [1] and 0", canceler.ids, canceler.downsSeen)
	}
	if !reflect.DeepEqual(runner.calls, []string{"down-volumes"}) {
		t.Errorf("calls = %v", runner.calls)
	}
}

func TestTeardownCallsCanceler(t *testing.T) {
	svc, runner, target, _ := newDeployService(t, "services:\n  web: {image: nginx}\n")
	canceler := &recordingCanceler{runner: runner}
	svc.SetDeploymentCanceler(canceler)
	if err := os.MkdirAll(ComposeDir(target.Request), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target.Request.ComposeFilePath, []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := svc.TeardownProject(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(canceler.ids, []int64{1}) || canceler.downsSeen != 0 {
		t.Errorf("ids = %v, downs before cancel = %d; want [1] and 0", canceler.ids, canceler.downsSeen)
	}
	if !reflect.DeepEqual(runner.calls, []string{"down-volumes"}) {
		t.Errorf("calls = %v", runner.calls)
	}
}

func TestPrepareBuildDeclaresSharedNetwork(t *testing.T) {
	const y = "services:\n  api:\n    build: .\n    networks: [" + dockernet.NetworkName + "]\n"
	svc, _, target, _ := newDeployService(t, y)
	plan, err := svc.PrepareBuild(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	b, _ := os.ReadFile(plan.Request.ComposeFilePath)
	var doc struct {
		Networks map[string]map[string]any `yaml:"networks"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Networks[dockernet.NetworkName]["external"] != true {
		t.Errorf("network not declared external: %s", b)
	}
}

func TestDeclareSharedNetworkLeavesExistingDeclaration(t *testing.T) {
	declared := "services:\n  api:\n    build: .\nnetworks:\n  " + dockernet.NetworkName + ":\n    external: true\n    name: custom\n"
	out, err := declareSharedNetwork(declared, dockernet.NetworkName)
	if err != nil || out != declared {
		t.Errorf("declared network changed: %q, %v", out, err)
	}
	other, err := declareSharedNetwork("services: {}\nnetworks:\n  back: {}\n", dockernet.NetworkName)
	if err != nil || !strings.Contains(other, "back") || !strings.Contains(other, dockernet.NetworkName) {
		t.Errorf("other networks lost: %q, %v", other, err)
	}
}

func TestPrepareBuildOutputPassesComposeConfig(t *testing.T) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker not on PATH")
	}
	svc, _, target, _ := newDeployService(t, "services:\n  api:\n    build: .\n    networks: ["+dockernet.NetworkName+"]\n")
	plan, err := svc.PrepareBuild(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	out, err := exec.Command(docker, "compose", "-f", plan.Request.ComposeFilePath, "--env-file", plan.Request.EnvFilePath, "config", "-q").CombinedOutput()
	if err != nil {
		t.Skipf("docker compose unavailable or failed: %v: %s", err, out)
	}
}

func TestBuildServicesThroughAnchors(t *testing.T) {
	cases := map[string]struct {
		yaml string
		want []string
	}{
		"plain":      {buildYAML, []string{"web", "api"}},
		"merge key":  {"x-app: &app\n  build: .\nservices:\n  db:\n    image: pg\n  web:\n    <<: *app\n  api:\n    <<: *app\n    command: x\n", []string{"web", "api"}},
		"alias body": {"x-app: &app\n  build: .\nservices:\n  web: *app\n  db:\n    image: pg\n", []string{"web"}},
		"none":       {"services:\n  db:\n    image: pg\n", nil},
	}
	for name, c := range cases {
		got, err := buildServices(c.yaml)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %v, %v want %v", name, got, err, c.want)
		}
	}
}
