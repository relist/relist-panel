package composes

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Reloading variables or routing during either step would change these files.
func TestCapturedFilesSurviveEditsDuringBuild(t *testing.T) {
	svc, _, target, db := newDeployService(t, "services:\n  web:\n    image: nginx\n    build: ./src\n    environment: [ENV_KEY]\n")
	for _, statement := range []string{
		`INSERT INTO variable_sets (id, compose_id, name, created_at, updated_at) VALUES (1, 1, 'runtime', 0, 0)`,
		`INSERT INTO variable_set_entries (set_id, key, value, secret) VALUES (1, 'TOKEN', 'original-secret', 1)`,
		`INSERT INTO service_bindings (compose_id, service, set_id, position) VALUES (1, 'web', 1, 0)`,
		`INSERT INTO domains (compose_id, host, service, port, created_at, updated_at) VALUES (1, 'original.example.com', 'web', 80, 0, 0)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	captured, err := svc.Capture(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE environment_variables SET value = 'edited-value'`,
		`UPDATE variable_set_entries SET value = 'edited-secret'`,
		`UPDATE domains SET host = 'edited.example.com'`,
		`UPDATE composes SET yaml_content = 'services: {}'`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := svc.PrepareBuild(context.Background(), captured)
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Cleanup()
	buildEnv, _ := os.ReadFile(plan.Request.EnvFilePath)
	buildYAML, _ := os.ReadFile(plan.Request.ComposeFilePath)
	if !strings.Contains(string(buildEnv), `ENV_KEY="env-value"`) || !strings.Contains(string(buildYAML), "original.example.com") {
		t.Fatalf("build used mutable inputs: %s %s", buildEnv, buildYAML)
	}
	if _, err := svc.RunDeploy(context.Background(), captured, DeployOptions{}); err != nil {
		t.Fatal(err)
	}
	deployEnv, _ := os.ReadFile(target.Request.EnvFilePath)
	deployYAML, _ := os.ReadFile(target.Request.ComposeFilePath)
	setEnv, _ := os.ReadFile(setEnvPath(target.Request.EnvFilePath, 1))
	if string(deployEnv) != string(buildEnv) || !strings.Contains(string(deployYAML), "original.example.com") || strings.Contains(string(deployYAML), "edited.example.com") || !strings.Contains(string(setEnv), "original-secret") {
		t.Fatalf("deploy used mutable inputs: %s %s %s", deployEnv, deployYAML, setEnv)
	}
}

func TestSnapshotSourcePathsRespectNamedVolumesAndInterpolation(t *testing.T) {
	svc, _, target, db := newDeployService(t, "services:\n  web:\n    build:\n      context: ${SOURCE_PATH:-./src}\n      additional_contexts: {assets: ./src/assets}\n    volumes: ['src:/named', '${SOURCE_PATH:-./src}:/code']\nvolumes: {src: {}}\n")
	if _, err := db.Exec(`INSERT INTO compose_variables (compose_id,key,value) VALUES (1,'SOURCE_PATH','./src')`); err != nil {
		t.Fatal(err)
	}
	target, err := svc.Capture(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	target, err = target.WithSourceDirectory("/frozen/job/src")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(target.Snapshot.YAML, "${SOURCE_PATH") || !strings.Contains(target.Snapshot.YAML, "/frozen/job/src/assets") || !strings.Contains(target.Snapshot.YAML, "src:/named") || !strings.Contains(target.Snapshot.YAML, "/frozen/job/src:/code") {
		t.Fatalf("incorrect Source paths: %s", target.Snapshot.YAML)
	}
}

func TestSourceSnapshotKeepsVariableNamedVolumesAndRelativeRootBuild(t *testing.T) {
	svc, _, target, db := newDeployService(t, "services:\n  web:\n    build: {context: '.', dockerfile: './src/Dockerfile'}\n    volumes: ['${VOLUME_NAME:-src}:/data']\nvolumes: {src: {}}\n")
	if _, err := db.Exec(`INSERT INTO compose_variables (compose_id,key,value) VALUES (1,'VOLUME_NAME','src')`); err != nil {
		t.Fatal(err)
	}
	target, err := svc.Capture(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	target, err = target.WithSourceDirectory("/frozen/job/src")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(target.Snapshot.YAML, "context: /frozen/job") || !strings.Contains(target.Snapshot.YAML, "${VOLUME_NAME:-src}:/data") || !strings.Contains(target.Snapshot.YAML, "dockerfile: ./src/Dockerfile") {
		t.Fatalf("incorrect root context/named volume: %s", target.Snapshot.YAML)
	}
}

func TestSourceSnapshotDefaultsMissingBuildContextToPrivateRoot(t *testing.T) {
	svc, _, target, _ := newDeployService(t, "services:\n  web:\n    build: {dockerfile: ./src/Dockerfile}\n")
	target, err := svc.Capture(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if !target.NeedsComposeBuildContext() {
		t.Fatal("default context wasn't captured")
	}
	target, err = target.WithSourceDirectory("/frozen/job/src")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(target.Snapshot.YAML, "context: /frozen/job") || !strings.Contains(target.Snapshot.YAML, "dockerfile: ./src/Dockerfile") {
		t.Fatalf("default context uses mutable root:%s", target.Snapshot.YAML)
	}
}
