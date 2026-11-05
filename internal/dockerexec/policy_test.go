package dockerexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"relist-panel/internal/composes"
	"relist-panel/internal/policy"
)

func TestPolicyConfigArgs(t *testing.T) {
	want := []string{"--profile", "*", "config", "--format", "json", "--no-env-resolution"}
	if got := policyConfigArgs(); !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

// Each violation hidden behind a variable still fails once Compose resolves
// it; this runs the real CLI and skips without it.
func TestPolicyConfigSeesThroughVariables(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not available")
	}
	if err := exec.Command("docker", "compose", "version").Run(); err != nil {
		t.Skip("docker compose not available")
	}
	root := t.TempDir()
	dir := filepath.Join(root, ".composes", "1")
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	rules := policy.Rules{ComposeDir: dir, ProjectName: "relist-c1", PanelDomain: "panel.example.com"}
	evaluate := func(t *testing.T, yaml, env string) ([]policy.Violation, string) {
		t.Helper()
		file, envFile := filepath.Join(dir, "docker-compose.yml"), filepath.Join(dir, ".env")
		if err := os.WriteFile(file, []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(envFile, []byte(env), 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := Runner{}.PolicyConfig(context.Background(), composes.RunRequest{ProjectName: "relist-c1", ComposeFilePath: file, EnvFilePath: envFile})
		if err != nil {
			t.Fatal(err)
		}
		violations, err := policy.Check(out, yaml, rules)
		if err != nil {
			t.Fatal(err)
		}
		hash, err := policy.Hash(out)
		if err != nil {
			t.Fatal(err)
		}
		return violations, hash
	}
	ordinary := "services:\n  web:\n    image: nginx\n    environment: {TOKEN: '${TOKEN}'}\n    volumes: ['./data:/data', 'db:/db']\nvolumes:\n  db: {}\n"
	violations, first := evaluate(t, ordinary, "TOKEN=one\n")
	if len(violations) != 0 {
		t.Fatalf("ordinary: %+v", violations)
	}
	if _, second := evaluate(t, ordinary, "TOKEN=two\n"); second == first {
		t.Fatal("a variable value change kept the hash")
	}
	for name, c := range map[string]struct{ yaml, env string }{
		"bind":         {"services:\n  web:\n    image: nginx\n    volumes: ['${SRC}:/x']\n", "SRC=/var/run/docker.sock\n"},
		"privileged":   {"services:\n  web:\n    image: nginx\n    privileged: ${P}\n", "P=true\n"},
		"network mode": {"services:\n  web:\n    image: nginx\n    network_mode: ${M}\n", "M=host\n"},
		"cap_add":      {"services:\n  web:\n    image: nginx\n    cap_add: ['${C}']\n", "C=SYS_ADMIN\n"},
		"volume name":  {"services:\n  web:\n    image: nginx\n    volumes: ['db:/db']\nvolumes:\n  db: {name: '${N}'}\n", "N=relist-c2_db\n"},
		"driver_opts":  {"services:\n  web:\n    image: nginx\n    volumes: ['db:/db']\nvolumes:\n  db: {driver_opts: {type: none, o: bind, device: '${D}'}}\n", "D=/\n"},
		"profile":      {"services:\n  web:\n    image: nginx\n    profiles: [extra]\n    privileged: ${P}\n", "P=true\n"},
		"build":        {"services:\n  web:\n    build: {context: '${CTX}'}\n", "CTX=/etc\n"},
		"env_file":     {"services:\n  web:\n    image: nginx\n    env_file: ['${F}']\n", "F=/etc/hostname\n"},
	} {
		t.Run(name, func(t *testing.T) {
			if violations, _ := evaluate(t, c.yaml, c.env); len(violations) == 0 {
				t.Fatal("no violation")
			}
		})
	}
}
