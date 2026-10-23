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
	removed        bool
}

func (r *recordingRunner) DownWithVolumesTracked(ctx context.Context, req RunRequest) (bool, error) {
	err := r.DownWithVolumes(ctx, req)
	return r.removed, err
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

func TestFreshVolumesFailureReportsOnlyConfirmedRemoval(t *testing.T) {
	for _, tc := range []struct {
		name     string
		previous bool
		removed  bool
		downErr  error
		want     bool
	}{
		{"removed before up failed", true, true, nil, true},
		{"no named volumes", true, false, nil, false},
		{"first deploy", false, true, nil, false},
		{"down failed", true, false, errors.New("down refused"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, runner, target, _ := newDeployService(t, "services:\n  web: {image: nginx}\n")
			runner.removed, runner.downErr, runner.upErr = tc.removed, tc.downErr, errors.New("up failed")
			if tc.previous {
				if err := os.MkdirAll(ComposeDir(target.Request), 0o750); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target.Request.ComposeFilePath, []byte("services: {}\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			_, err := svc.RunDeploy(context.Background(), target, DeployOptions{FreshVolumes: true})
			var failure *DeployFailure
			if !errors.As(err, &failure) || failure.VolumesRemoved != tc.want {
				t.Fatalf("removal metadata = %+v, want %v", failure, tc.want)
			}
			if !tc.want && strings.Contains(err.Error(), "after volumes were removed") {
				t.Fatalf("unperformed removal claimed: %v", err)
			}
		})
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

const inheritedBuildNetworksYAML = "x-defaults: &defaults\n  networks:\n    backend:\n      name: custom-backend\n      internal: true\n<<: *defaults\nservices:\n  api:\n    build: .\n    networks: [backend, " + dockernet.NetworkName + "]\n"

func TestDeclareSharedNetworkPreservesEffectiveNetworks(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want map[string]any
	}{
		{
			name: "root inherited networks",
			yaml: inheritedBuildNetworksYAML,
			want: map[string]any{"backend": map[string]any{"name": "custom-backend", "internal": true}},
		},
		{
			name: "root merge sequence selects first networks mapping",
			yaml: "x-first: &first\n  networks:\n    backend:\n      internal: true\nx-second: &second\n  networks:\n    ignored: {}\n<<: [*first, *second]\nservices: {}\n",
			want: map[string]any{"backend": map[string]any{"internal": true}},
		},
		{
			name: "explicit root networks overrides inherited mapping",
			yaml: "x-defaults: &defaults\n  networks:\n    ignored: {}\n    " + dockernet.NetworkName + ":\n      name: ignored-shared\n<<: *defaults\nservices: {}\nnetworks:\n  backend:\n    name: local-backend\n",
			want: map[string]any{"backend": map[string]any{"name": "local-backend"}},
		},
		{
			name: "network mapping merge keeps declaration options",
			yaml: "x-networks: &networks\n  backend:\n    name: custom-backend\n    labels:\n      owner: operator\nservices: {}\nnetworks:\n  <<: *networks\n  local:\n    internal: true\n",
			want: map[string]any{"backend": map[string]any{"name": "custom-backend", "labels": map[string]any{"owner": "operator"}}, "local": map[string]any{"internal": true}},
		},
		{
			name: "network alias does not change its source",
			yaml: "x-networks: &networks\n  backend:\n    name: custom-backend\nservices: {}\nnetworks: *networks\nx-copy: *networks\n",
			want: map[string]any{"backend": map[string]any{"name": "custom-backend"}},
		},
		{
			name: "anchored explicit mapping does not change other aliases",
			yaml: "services: {}\nnetworks: &networks\n  backend:\n    name: custom-backend\nx-copy: *networks\n",
			want: map[string]any{"backend": map[string]any{"name": "custom-backend"}},
		},
		{
			name: "inherited anchored network value keeps a single definition",
			yaml: "x-defaults: &defaults\n  networks:\n    backend: &backend\n      name: custom-backend\n      internal: true\n<<: *defaults\nservices: {}\nx-copy: *backend\n",
			want: map[string]any{"backend": map[string]any{"name": "custom-backend", "internal": true}},
		},
		{
			name: "generated anchor avoids existing names",
			yaml: "x-anchor: &relist-build-networks {}\nx-defaults: &defaults\n  networks:\n    backend: {}\n<<: *defaults\nservices: {}\n",
			want: map[string]any{"backend": map[string]any{}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var before map[string]any
			if err := yaml.Unmarshal([]byte(c.yaml), &before); err != nil {
				t.Fatal(err)
			}
			out, err := declareSharedNetwork(c.yaml, dockernet.NetworkName)
			if err != nil {
				t.Fatal(err)
			}
			var after map[string]any
			if err := yaml.Unmarshal([]byte(out), &after); err != nil {
				t.Fatal(err)
			}
			c.want[dockernet.NetworkName] = map[string]any{"external": true}
			if !reflect.DeepEqual(after["networks"], c.want) {
				t.Fatalf("networks = %#v, want %#v", after["networks"], c.want)
			}
			delete(before, "networks")
			delete(after, "networks")
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("adding the network changed other YAML values:\nbefore: %#v\nafter: %#v", before, after)
			}
			var doc yaml.Node
			if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
				t.Fatal(err)
			}
			anchors := map[string]bool{}
			var checkAnchors func(*yaml.Node)
			checkAnchors = func(n *yaml.Node) {
				if n.Anchor != "" {
					if anchors[n.Anchor] {
						t.Fatalf("duplicate anchor %q in staging YAML", n.Anchor)
					}
					anchors[n.Anchor] = true
				}
				for _, child := range n.Content {
					checkAnchors(child)
				}
			}
			checkAnchors(&doc)
		})
	}
}

func TestDeclareSharedNetworkLeavesInheritedDeclaration(t *testing.T) {
	cases := map[string]string{
		"root inherited": "x-defaults: &defaults\n  networks:\n    " + dockernet.NetworkName + ":\n      external: true\n      name: custom-shared\n<<: *defaults\nservices: {}\n",
		"mapping merged": "x-networks: &networks\n  " + dockernet.NetworkName + ":\n    external: true\n    name: custom-shared\nservices: {}\nnetworks:\n  <<: *networks\n",
		"merge sequence": "x-first: &first\n  " + dockernet.NetworkName + ":\n    external: true\n    name: first-shared\nx-second: &second\n  " + dockernet.NetworkName + ":\n    external: true\n    name: ignored-shared\nservices: {}\nnetworks:\n  <<: [*first, *second]\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := declareSharedNetwork(content, dockernet.NetworkName)
			if err != nil {
				t.Fatal(err)
			}
			if out != content {
				t.Fatalf("existing effective declaration changed:\n%s", out)
			}
		})
	}
}

func TestDeclareSharedNetworkRejectsInvalidMerges(t *testing.T) {
	for name, content := range map[string]string{
		"root scalar merge":    "<<: invalid\nservices: {}\n",
		"network scalar merge": "services: {}\nnetworks:\n  <<: invalid\n",
		"recursive merge":      "services: {}\nnetworks: &networks\n  <<: *networks\n",
		"duplicate network":    "services: {}\nnetworks:\n  backend: {}\n  backend: {}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if out, err := declareSharedNetwork(content, dockernet.NetworkName); err == nil {
				t.Fatalf("expected invalid YAML error, got %s", out)
			}
		})
	}
}

func TestPrepareBuildInheritedNetworksPassComposeConfig(t *testing.T) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker not on PATH")
	}
	if out, err := exec.Command(docker, "compose", "version").CombinedOutput(); err != nil {
		t.Skipf("docker compose unavailable: %v: %s", err, out)
	}
	svc, _, target, _ := newDeployService(t, inheritedBuildNetworksYAML)
	plan, err := svc.PrepareBuild(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	out, err := exec.Command(docker, "compose", "-f", plan.Request.ComposeFilePath, "--env-file", plan.Request.EnvFilePath, "config", "-q").CombinedOutput()
	if err != nil {
		t.Fatalf("staged YAML rejected by docker compose: %v: %s", err, out)
	}
}

func TestPrepareBuildOutputPassesComposeConfig(t *testing.T) {
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker not on PATH")
	}
	if out, err := exec.Command(docker, "compose", "version").CombinedOutput(); err != nil {
		t.Skipf("docker compose unavailable: %v: %s", err, out)
	}
	svc, _, target, _ := newDeployService(t, "services:\n  api:\n    build: .\n    networks: ["+dockernet.NetworkName+"]\n")
	plan, err := svc.PrepareBuild(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	out, err := exec.Command(docker, "compose", "-f", plan.Request.ComposeFilePath, "--env-file", plan.Request.EnvFilePath, "config", "-q").CombinedOutput()
	if err != nil {
		t.Fatalf("build staging compose is invalid: %v: %s", err, out)
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

func TestBuildServicesThroughMappingMerges(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want []string
	}{
		{
			name: "services merge preserves surrounding order",
			yaml: "x-services: &services\n  api:\n    build: .\n  worker:\n    build: .\nservices:\n  before:\n    build: .\n  <<: *services\n  after:\n    build: .\n",
			want: []string{"before", "api", "worker", "after"},
		},
		{
			name: "root merge supplies services",
			yaml: "x-defaults: &defaults\n  services:\n    api:\n      build: .\n    worker:\n      build: .\n<<: *defaults\n",
			want: []string{"api", "worker"},
		},
		{
			name: "services alias supplies mapping",
			yaml: "x-services: &services\n  api:\n    build: .\n  worker:\n    build: .\nservices: *services\n",
			want: []string{"api", "worker"},
		},
		{
			name: "merge sequence uses first map and explicit overrides",
			yaml: "x-first: &first\n  api:\n    build: .\n  image-only:\n    image: busybox\n  overridden:\n    build: .\nx-second: &second\n  api:\n    build: ./other\n  image-only:\n    build: .\n  worker:\n    build: .\nservices:\n  before:\n    build: .\n  <<: [*first, *second]\n  overridden:\n    image: busybox\n  after:\n    build: .\n",
			want: []string{"before", "api", "worker", "after"},
		},
		{
			name: "explicit service uses its own order before or after merge",
			yaml: "x-services: &services\n  earlier:\n    image: busybox\n  inherited:\n    build: .\n  later:\n    image: busybox\nservices:\n  earlier:\n    build: .\n  <<: *services\n  later:\n    build: .\n",
			want: []string{"earlier", "inherited", "later"},
		},
		{
			name: "root merge sequence uses first services mapping",
			yaml: "x-first: &first\n  services:\n    api:\n      build: .\nx-second: &second\n  services:\n    ignored:\n      build: .\n<<: [*first, *second]\n",
			want: []string{"api"},
		},
		{
			name: "explicit root services replaces inherited mapping",
			yaml: "x-defaults: &defaults\n  services:\n    ignored:\n      build: .\n<<: *defaults\nservices:\n  api:\n    build: .\n",
			want: []string{"api"},
		},
		{
			name: "nested mapping merges",
			yaml: "x-base: &base\n  api:\n    build: .\nx-services: &services\n  <<: *base\n  worker:\n    build: .\nx-defaults: &defaults\n  services:\n    <<: *services\n<<: *defaults\n",
			want: []string{"api", "worker"},
		},
		{
			name: "quoted merge-looking key is an ordinary service",
			yaml: "services:\n  '<<':\n    build: .\n  api:\n    build: .\n",
			want: []string{"<<", "api"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := buildServices(c.yaml)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("build services = %v, want %v", got, c.want)
			}
		})
	}
}

func TestBuildServicesRejectsInvalidMerges(t *testing.T) {
	cases := map[string]string{
		"root scalar merge":     "<<: invalid\nservices:\n  api:\n    build: .\n",
		"service scalar merge":  "services:\n  <<: invalid\n",
		"service sequence item": "services:\n  <<: [{api: {build: .}}, invalid]\n",
		"recursive merge":       "services: &services\n  <<: *services\n",
		"recursive service":     "x-app: &app\n  <<: *app\nservices:\n  api: *app\n",
		"duplicate service":     "services:\n  api:\n    build: .\n  api:\n    build: ./other\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if services, err := buildServices(content); err == nil {
				t.Fatalf("expected invalid YAML error, got services %v", services)
			}
		})
	}
}
