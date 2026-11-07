package dockerexec

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"relist-panel/internal/composes"
	"relist-panel/internal/registryauth"
)

type fixedAuths []registryauth.Auth

func (f fixedAuths) Auths(context.Context) ([]registryauth.Auth, error) { return f, nil }

func envValue(env []string, name string) (string, bool) {
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); k == name {
			return v, true
		}
	}
	return "", false
}

func TestAuthEnvKeepsBuildxConfig(t *testing.T) {
	src := fixedAuths{{Host: "ghcr.io", Username: "u", Secret: "s"}}
	cases := []struct {
		environ []string
		want    string
	}{
		{[]string{"HOME=/h", "PATH=/bin"}, "/h/.docker/buildx"},
		{[]string{"HOME=/h", "DOCKER_CONFIG=/c"}, "/c/buildx"},
		{[]string{"HOME=/h", "DOCKER_CONFIG=/c", "BUILDX_CONFIG=/b"}, "/b"},
	}
	for _, c := range cases {
		env, cleanup, err := authEnv(context.Background(), src, c.environ)
		if err != nil {
			t.Fatal(err)
		}
		if got, _ := envValue(env, "BUILDX_CONFIG"); got != c.want {
			t.Errorf("%v: BUILDX_CONFIG = %q, want %q", c.environ, got, c.want)
		}
		dir, _ := envValue(env, "DOCKER_CONFIG")
		if dir == "/c" || !strings.Contains(filepath.Base(dir), "relist-auth-") {
			t.Errorf("%v: DOCKER_CONFIG = %q, want a fresh relist-auth- dir", c.environ, dir)
		}
		if n := strings.Count(strings.Join(env, "\n"), "DOCKER_CONFIG="); n != 1 {
			t.Errorf("%v: %d DOCKER_CONFIG entries in %v", c.environ, n, env)
		}
		if _, err := os.Stat(filepath.Join(dir, "config.json")); err != nil {
			t.Errorf("config.json: %v", err)
		}
		if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("dir mode = %v, %v", info.Mode(), err)
		}
		cleanup()
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Errorf("dir %s still there after cleanup: %v", dir, err)
		}
	}
}

// standIn replaces command with one that prints DOCKER_CONFIG and whether
// its config.json existed while the CLI ran.
func standIn(t *testing.T) {
	t.Helper()
	old := command
	t.Cleanup(func() { command = old })
	command = func(ctx context.Context, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "sh", "-c", `if [ -f "$DOCKER_CONFIG/config.json" ]; then echo "auth:$DOCKER_CONFIG"; else echo "none:$DOCKER_CONFIG"; fi`)
		cmd.Env = cliEnv(os.Environ())
		return cmd
	}
}

func runAndCapture(t *testing.T, f func(out *strings.Builder) error) string {
	t.Helper()
	var out strings.Builder
	if err := f(&out); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out.String())
}

func TestAuthDirRemovedAfterCall(t *testing.T) {
	standIn(t)
	r := New(fixedAuths{{Host: "ghcr.io", Username: "u", Secret: "s"}})
	req := composes.RunRequest{ProjectName: "p", ComposeFilePath: "/x/compose.yml", EnvFilePath: "/x/.env"}
	got := runAndCapture(t, func(out *strings.Builder) error {
		return r.Pull(context.Background(), req, composes.PullOptions{Out: out})
	})
	dir, ok := strings.CutPrefix(got, "auth:")
	if !ok || dir == "" {
		t.Fatalf("pull ran with %q, want a config.json", got)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("%s left behind: %v", dir, err)
	}
}

func TestCallsThatPullOrBuildGetAuth(t *testing.T) {
	standIn(t)
	r := New(fixedAuths{{Host: "ghcr.io", Username: "u", Secret: "s"}})
	req := composes.RunRequest{ProjectName: "p", ComposeFilePath: "/x/compose.yml", EnvFilePath: "/x/.env"}
	ctx := context.Background()
	calls := map[string]func(out *strings.Builder) error{
		"build":   func(out *strings.Builder) error { return r.Build(ctx, req, composes.BuildOptions{Out: out}) },
		"up pull": func(out *strings.Builder) error { return r.Up(ctx, req, composes.UpOptions{Out: out}) },
		"up build": func(out *strings.Builder) error {
			return r.Up(ctx, req, composes.UpOptions{NoPull: true, Build: true, Out: out})
		},
		"scale": func(out *strings.Builder) error {
			return r.Scale(ctx, req, composes.ScaleOptions{Service: "web", Replicas: 2, Out: out})
		},
		"up no pull": func(out *strings.Builder) error { return r.Up(ctx, req, composes.UpOptions{NoPull: true, Out: out}) },
		"scale nopull": func(out *strings.Builder) error {
			return r.Scale(ctx, req, composes.ScaleOptions{Service: "web", Replicas: 2, NoPull: true, Out: out})
		},
	}
	withAuth := []string{"build", "up pull", "up build", "scale"}
	for name, call := range calls {
		got := runAndCapture(t, call)
		if want := slices.Contains(withAuth, name); strings.HasPrefix(got, "auth:") != want {
			t.Errorf("%s: ran with %q, want auth = %v", name, got, want)
		}
	}
}

func TestStatusGetsNoAuthEnv(t *testing.T) {
	old := command
	t.Cleanup(func() { command = old })
	t.Setenv("DOCKER_CONFIG", "")
	var env []string
	command = func(ctx context.Context, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "echo", "[]")
		cmd.Env = cliEnv(os.Environ())
		return cmd
	}
	r := New(fixedAuths{{Host: "ghcr.io", Username: "u", Secret: "s"}})
	req := composes.RunRequest{ProjectName: "p", ComposeFilePath: "/x/compose.yml", EnvFilePath: "/x/.env"}
	old2 := command
	command = func(ctx context.Context, args ...string) *exec.Cmd {
		cmd := old2(ctx, args...)
		env = cmd.Env
		return cmd
	}
	if _, err := r.Logs(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if v, _ := envValue(env, "DOCKER_CONFIG"); strings.Contains(v, "relist-auth-") {
		t.Fatalf("logs ran with DOCKER_CONFIG=%s", v)
	}
}

func TestNilSourceInjectsNothing(t *testing.T) {
	standIn(t)
	t.Setenv("DOCKER_CONFIG", "")
	req := composes.RunRequest{ProjectName: "p", ComposeFilePath: "/x/compose.yml", EnvFilePath: "/x/.env"}
	got := runAndCapture(t, func(out *strings.Builder) error {
		return New(nil).Pull(context.Background(), req, composes.PullOptions{Out: out})
	})
	if strings.Contains(got, "relist-auth-") {
		t.Fatalf("nil source ran with %q", got)
	}
}

// The per-call config keeps what the original one says besides logins, so a
// native run with a context, proxies or plugins behaves the same.
func TestAuthEnvKeepsTheOriginalConfig(t *testing.T) {
	home := t.TempDir()
	orig := filepath.Join(home, ".docker")
	for _, d := range []string{"contexts/meta", "cli-plugins"} {
		if err := os.MkdirAll(filepath.Join(orig, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(orig, "config.json"), []byte(`{"currentContext":"remote","proxies":{"default":{"httpProxy":"http://p:3128"}},
		"credsStore":"desktop","credHelpers":{"ghcr.io":"gh","quay.io":"q"},"auths":{"old.example.com":{"auth":"eDp5"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	env, cleanup, err := authEnv(context.Background(), fixedAuths{{Host: "ghcr.io", Username: "u", Secret: "s"}}, []string{"HOME=" + home})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	dir, _ := envValue(env, "DOCKER_CONFIG")
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	auths, _ := cfg["auths"].(map[string]any)
	helpers, _ := cfg["credHelpers"].(map[string]any)
	if cfg["currentContext"] != "remote" || cfg["proxies"] == nil || cfg["credsStore"] != nil ||
		auths["ghcr.io"] == nil || auths["old.example.com"] == nil || helpers["ghcr.io"] != nil || helpers["quay.io"] != "q" {
		t.Fatalf("config = %s", raw)
	}
	for _, d := range []string{"contexts", "cli-plugins"} {
		if target, err := os.Readlink(filepath.Join(dir, d)); err != nil || target != filepath.Join(orig, d) {
			t.Errorf("%s link = %q, %v", d, target, err)
		}
	}
}

func TestAuthEnvIgnoresAnUnreadableOriginalConfig(t *testing.T) {
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".docker"), 0o700)
	_ = os.WriteFile(filepath.Join(home, ".docker", "config.json"), []byte("\x00\x00"), 0o600)
	env, cleanup, err := authEnv(context.Background(), fixedAuths{{Host: "ghcr.io", Username: "u", Secret: "s"}}, []string{"HOME=" + home})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	dir, _ := envValue(env, "DOCKER_CONFIG")
	raw, _ := os.ReadFile(filepath.Join(dir, "config.json"))
	if !strings.Contains(string(raw), "ghcr.io") {
		t.Fatalf("config = %s", raw)
	}
}
