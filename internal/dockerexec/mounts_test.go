package dockerexec

import (
	"context"
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

func TestMountedSourcesArgv(t *testing.T) {
	old := command
	defer func() { command = old }()
	var calls [][]string
	command = func(ctx context.Context, args ...string) *exec.Cmd {
		calls = append(calls, args)
		if args[0] == "ps" {
			return exec.CommandContext(ctx, "printf", "%s\n", "c1\nc2")
		}
		return exec.CommandContext(ctx, "printf", "%s", "/data/a\n/data/b\n\n/data/a\n")
	}
	got, err := (Runner{}).MountedSources(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	wantCalls := [][]string{
		{"ps", "-aq", "--filter", "label=com.docker.compose.project=p1"},
		{"inspect", "--format", `{{range .Mounts}}{{.Source}}{{"\n"}}{{end}}`, "c1", "c2"},
	}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("argv = %v, want %v", calls, wantCalls)
	}
	if want := []string{"/data/a", "/data/b", "/data/a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}
}

func TestMountedSourcesWithoutContainersSkipsInspect(t *testing.T) {
	old := command
	defer func() { command = old }()
	command = func(ctx context.Context, args ...string) *exec.Cmd {
		if args[0] != "ps" {
			t.Errorf("unexpected call: %v", args)
		}
		return exec.CommandContext(ctx, "true")
	}
	got, err := (Runner{}).MountedSources(context.Background(), "p1")
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestMountedSourcesReportsDockerFailure(t *testing.T) {
	old := command
	defer func() { command = old }()
	for _, failing := range []string{"ps", "inspect"} {
		command = func(ctx context.Context, args ...string) *exec.Cmd {
			if args[0] == failing {
				return exec.CommandContext(ctx, "sh", "-c", "echo daemon down >&2; exit 1")
			}
			return exec.CommandContext(ctx, "printf", "%s\n", "c1")
		}
		if _, err := (Runner{}).MountedSources(context.Background(), "p1"); err == nil || !strings.Contains(err.Error(), "daemon down") {
			t.Errorf("%s failure: err = %v", failing, err)
		}
	}
}
