package composes

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"relist-panel/internal/database"
)

func TestMergeVariablesLayersAndKeepsEnvFileOrder(t *testing.T) {
	env := []environmentVariable{{Key: "A", Value: "env-a"}, {Key: "B", Value: "env-b"}}
	compose := []environmentVariable{{Key: "C", Value: "c"}, {Key: "A", Value: "compose-a"}}
	sets := []boundSet{
		{ID: 1, Name: "one", Entries: []environmentVariable{{Key: "A", Value: "one-a"}, {Key: "S", Value: "one-s"}}},
		{ID: 2, Name: "two", Entries: []environmentVariable{{Key: "S", Value: "two-s", Secret: true}}},
	}

	merged := mergeVariables(env, compose, sets)

	wantEnv := []environmentVariable{{Key: "A", Value: "compose-a"}, {Key: "B", Value: "env-b"}, {Key: "C", Value: "c"}}
	if !reflect.DeepEqual(merged.Interpolated, wantEnv) {
		t.Fatalf("interpolated = %+v, want %+v", merged.Interpolated, wantEnv)
	}
	if got := renderEnvFile(merged.Interpolated); got != "A=\"compose-a\"\nB=\"env-b\"\nC=\"c\"\n" {
		t.Fatalf(".env = %q", got)
	}
	got := map[string]effectiveVariable{}
	for _, v := range merged.Effective {
		got[v.Key] = v
	}
	if len(merged.Effective) != 4 {
		t.Fatalf("effective = %+v, want 4 distinct keys", merged.Effective)
	}
	if a := got["A"]; a.Value != "one-a" || a.Source.SetName != "one" || len(a.Overrides) != 2 || a.Overrides[0].Kind != "environment" || a.Overrides[1].Kind != "compose" {
		t.Errorf("A = %+v", a)
	}
	if s := got["S"]; s.Value != "two-s" || !s.Secret || s.Source.SetID != 2 || len(s.Overrides) != 1 || s.Overrides[0].SetID != 1 {
		t.Errorf("S = %+v", s)
	}
	if b := got["B"]; b.Source.Kind != "environment" || len(b.Overrides) != 0 {
		t.Errorf("B = %+v", b)
	}
}

func TestResolveComposeMissingServiceBindingWarnsWithoutInjecting(t *testing.T) {
	vars := deployVariables{Bindings: []setBinding{{Service: "old-api", Set: boundSet{ID: 4, Name: "db-creds"}}}}
	resolved, used, warnings, err := resolveCompose("services:\n  web: {image: nginx}\n", testNetwork, "/data/.env", 12, nil, testIdentity, vars)
	if err != nil {
		t.Fatal(err)
	}
	if len(used) != 0 || strings.Contains(resolved, "sets/") {
		t.Fatalf("used = %v, resolved = %s; a missing service must not reference its set file", used, resolved)
	}
	want := `variable set "db-creds" is bound to service "old-api", which the compose does not declare`
	if !reflect.DeepEqual(warnings, []string{want}) {
		t.Fatalf("warnings = %q, want %q", warnings, want)
	}
}

func TestResolveComposeWithoutBindingsIsUnchanged(t *testing.T) {
	const yamlContent = "services:\n  web:\n    image: nginx\n    env_file: ./own.env\n"
	plain, _, warnings, err := resolveCompose(yamlContent, testNetwork, "/data/.env", 12, nil, testIdentity, deployVariables{})
	if err != nil || len(warnings) != 0 {
		t.Fatalf("resolve = %v, %q", err, warnings)
	}
	withUnused := deployVariables{Environment: []environmentVariable{{Key: "A"}}, Compose: []environmentVariable{{Key: "B"}}}
	again, _, _, _ := resolveCompose(yamlContent, testNetwork, "/data/.env", 12, nil, testIdentity, withUnused)
	if again != plain || strings.Index(plain, "./own.env") > strings.Index(plain, "/data/.env") {
		t.Fatalf("resolved differs or lost its own env_file first:\n%s\nvs\n%s", plain, again)
	}
}

func TestWriteFilesFailedSetWriteKeepsEveryReferencedPath(t *testing.T) {
	dir := t.TempDir()
	req := RunRequest{ComposeFilePath: filepath.Join(dir, "compose.yaml"), EnvFilePath: filepath.Join(dir, ".env")}
	yamlFor := func(ids ...int64) string {
		var b strings.Builder
		b.WriteString("services:\n  api:\n    env_file:\n")
		for _, id := range ids {
			b.WriteString("      - " + setEnvPath(req.EnvFilePath, id) + "\n")
		}
		return b.String()
	}
	if message, err := writeFiles(req, deployFiles{composeYAML: yamlFor(1), setFiles: map[int64]string{1: "A=\"1\"\n"}}); err != nil {
		t.Fatalf("first write: %s: %v", message, err)
	}

	// Set 2 cannot be written: a directory sits where its file belongs.
	if err := os.Mkdir(setEnvPath(req.EnvFilePath, 2), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := writeFiles(req, deployFiles{composeYAML: yamlFor(2), setFiles: map[int64]string{2: "B=\"2\"\n"}}); err == nil {
		t.Fatal("write succeeded, want the set write to fail")
	}
	written, err := os.ReadFile(req.ComposeFilePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(written), "\n") {
		if path, ok := strings.CutPrefix(strings.TrimSpace(line), "- "); ok {
			if _, err := os.Stat(path); err != nil {
				t.Errorf("YAML references %s: %v", path, err)
			}
		}
	}
}

func TestWriteFilesRemovesStaleSetFilesAndEmptyDir(t *testing.T) {
	dir := t.TempDir()
	req := RunRequest{ComposeFilePath: filepath.Join(dir, "compose.yaml"), EnvFilePath: filepath.Join(dir, ".env")}
	if _, err := writeFiles(req, deployFiles{setFiles: map[int64]string{1: "A=1\n", 2: "B=2\n"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := writeFiles(req, deployFiles{setFiles: map[int64]string{2: "B=2\n"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(setEnvPath(req.EnvFilePath, 1)); !os.IsNotExist(err) {
		t.Errorf("stale set file stat err = %v, want removed", err)
	}
	if _, err := os.Stat(setEnvPath(req.EnvFilePath, 2)); err != nil {
		t.Errorf("kept set file: %v", err)
	}
	if _, err := writeFiles(req, deployFiles{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(setsDir(req.EnvFilePath)); !os.IsNotExist(err) {
		t.Errorf("sets dir stat err = %v, want removed when nothing is bound", err)
	}
}

func TestVariableWarningsBareEnvironmentKey(t *testing.T) {
	bound := func(entries ...string) deployVariables {
		var set boundSet
		set.ID, set.Name = 1, "stripe"
		for _, key := range entries {
			set.Entries = append(set.Entries, environmentVariable{Key: key})
		}
		return deployVariables{Bindings: []setBinding{{Service: "api", Set: set}}}
	}
	run := func(environment string, vars deployVariables) []string {
		yamlContent := "services:\n  api:\n    image: x\n    environment:" + environment
		var doc struct {
			Services map[string]any `yaml:"services"`
		}
		if err := yaml.Unmarshal([]byte(yamlContent), &doc); err != nil {
			t.Fatal(err)
		}
		return variableWarnings(yamlContent, doc.Services, vars)
	}
	want := `service "api" lists STRIPE_KEY in environment: without a value, so Compose resolves it from the .env and the value from variable set "stripe" is lost`

	for name, environment := range map[string]string{
		"list": "\n      - STRIPE_KEY\n",
		"map":  "\n      STRIPE_KEY:\n",
	} {
		if got := run(environment, bound("STRIPE_KEY")); !reflect.DeepEqual(got, []string{want}) {
			t.Errorf("%s form warnings = %q, want %q", name, got, want)
		}
	}
	for name, tc := range map[string]struct {
		environment string
		vars        deployVariables
	}{
		"list with a value":  {"\n      - STRIPE_KEY=x\n", bound("STRIPE_KEY")},
		"map with a value":   {"\n      STRIPE_KEY: x\n", bound("STRIPE_KEY")},
		"key not in the set": {"\n      - OTHER\n", bound("STRIPE_KEY")},
		"also an environment variable": {"\n      - STRIPE_KEY\n", func() deployVariables {
			v := bound("STRIPE_KEY")
			v.Environment = []environmentVariable{{Key: "STRIPE_KEY"}}
			return v
		}()},
		"also a compose variable": {"\n      STRIPE_KEY:\n", func() deployVariables {
			v := bound("STRIPE_KEY")
			v.Compose = []environmentVariable{{Key: "STRIPE_KEY"}}
			return v
		}()},
	} {
		if got := run(tc.environment, tc.vars); len(got) != 0 {
			t.Errorf("%s: warnings = %q, want none", name, got)
		}
	}
}

func TestLoadDeployVariablesWithoutValuesReadsNoValue(t *testing.T) {
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, statement := range []string{
		`INSERT INTO projects (id, name, created_at, updated_at) VALUES (1, 'p', 0, 0)`,
		`INSERT INTO environments (id, project_id, name, created_at) VALUES (1, 1, 'e', 0)`,
		`INSERT INTO composes (id, environment_id, name, yaml_content, created_at, updated_at) VALUES (1, 1, 'c', '', 0, 0)`,
		`INSERT INTO environment_variables (environment_id, key, value) VALUES (1, 'ENV_KEY', 'env-secret')`,
		`INSERT INTO compose_variables (compose_id, key, value) VALUES (1, 'COMPOSE_KEY', 'compose-secret')`,
		`INSERT INTO variable_sets (id, compose_id, name, created_at, updated_at) VALUES (1, 1, 's', 0, 0)`,
		`INSERT INTO variable_set_entries (set_id, key, value) VALUES (1, 'SET_KEY', 'set-secret')`,
		`INSERT INTO service_bindings (compose_id, service, set_id, position) VALUES (1, 'api', 1, 0)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	repo := &repository{db: db}
	compose := Compose{ID: 1, EnvironmentID: 1}

	for withValues, wantValue := range map[bool]bool{false: false, true: true} {
		vars, err := repo.loadDeployVariables(context.Background(), compose, withValues)
		if err != nil {
			t.Fatal(err)
		}
		all := append(append([]environmentVariable{}, vars.Environment...), vars.Compose...)
		for _, binding := range vars.Bindings {
			all = append(all, binding.Set.Entries...)
		}
		if len(all) != 3 {
			t.Fatalf("withValues=%v: %d variables, want 3 (keys must still load)", withValues, len(all))
		}
		for _, variable := range all {
			if (variable.Value != "") != wantValue {
				t.Errorf("withValues=%v: %s value = %q", withValues, variable.Key, variable.Value)
			}
		}
	}
}
