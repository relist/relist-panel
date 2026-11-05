package dockerexec

import (
	"context"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"relist-panel/internal/composes"
)

func TestComposeCLIEnvDropsPanelVariables(t *testing.T) {
	got := cliEnv([]string{
		"PATH=/bin",
		"HOME=/root",
		"DOCKER_HOST=unix:///x",
		"RELIST_ADMIN_PASSWORD=s3cret",
		"RELIST_DATA_DIR=/data",
		"COMPOSE_PROJECT_NAME=x",
		"FOO=bar",
	})
	want := []string{"PATH=/bin", "HOME=/root", "DOCKER_HOST=unix:///x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cliEnv = %v, want %v", got, want)
	}
}

func TestRunOnPassesNoPanelVariables(t *testing.T) {
	t.Setenv("RELIST_ADMIN_PASSWORD", "s3cret")
	var captured *exec.Cmd
	original := command
	command = func(ctx context.Context, args ...string) *exec.Cmd {
		cmd := original(ctx, args...)
		cmd.Path, cmd.Args = "/bin/true", []string{"true"}
		captured = cmd
		return cmd
	}
	t.Cleanup(func() { command = original })

	req := composes.RunRequest{ProjectName: "p", ComposeFilePath: "/tmp/c.yml", EnvFilePath: "/tmp/.env"}
	for _, builder := range []string{"", "b"} {
		if _, err := runOn(context.Background(), req, nil, builder, "config"); err != nil {
			t.Fatalf("runOn(builder %q): %v", builder, err)
		}
		if captured.Env == nil {
			t.Fatalf("builder %q: cmd.Env is nil, so docker inherits the panel's environment", builder)
		}
		for _, kv := range captured.Env {
			if strings.HasPrefix(kv, "RELIST_") {
				t.Errorf("builder %q: env has %s", builder, kv)
			}
		}
		hasBuilder := false
		for _, kv := range captured.Env {
			hasBuilder = hasBuilder || kv == "BUILDX_BUILDER=b"
		}
		if hasBuilder != (builder == "b") {
			t.Errorf("builder %q: BUILDX_BUILDER present = %v", builder, hasBuilder)
		}
	}
}
