package composes

import (
	"reflect"
	"strings"
	"testing"
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
