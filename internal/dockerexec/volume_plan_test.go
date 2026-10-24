package dockerexec

import (
	"context"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"relist-panel/internal/composes"
)

const planConfig = `{"volumes":{"data":{},"shared":{"name":"shared-name"},"ext":{"name":"keep","external":true},"mine":{"name":"p_mine"}}}`

func TestCandidateVolumesMapsLabelsAndExistence(t *testing.T) {
	old := command
	defer func() { command = old }()
	var inspected [][]string
	command = func(ctx context.Context, args ...string) *exec.Cmd {
		if slices.Contains(args, "config") {
			return exec.CommandContext(ctx, "printf", "%s", planConfig)
		}
		inspected = append(inspected, args)
		switch args[len(args)-1] {
		case "p_data":
			return exec.CommandContext(ctx, "printf", "%s\n", "p")
		case "shared-name":
			return exec.CommandContext(ctx, "printf", "%s\n", "relist-c99")
		case "p_mine":
			return exec.CommandContext(ctx, "sh", "-c", "echo 'Error: No such volume: p_mine' >&2; exit 1")
		}
		t.Errorf("unexpected inspect: %v", args)
		return exec.CommandContext(ctx, "true")
	}
	got, err := (Runner{}).CandidateVolumes(context.Background(), composes.RunRequest{ProjectName: "p"})
	if err != nil {
		t.Fatal(err)
	}
	want := []composes.VolumeCandidate{
		{Name: "p_data", Exists: true, Project: "p"},
		{Name: "p_mine", Exists: false},
		{Name: "shared-name", Exists: true, Project: "relist-c99"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %+v, want %+v", got, want)
	}
	for _, args := range inspected {
		if !slices.Equal(args[:4], []string{"volume", "inspect", "--format", `{{index .Labels "com.docker.compose.project"}}`}) {
			t.Errorf("inspect argv = %v", args)
		}
		if slices.Contains(args, "keep") {
			t.Errorf("external volume inspected: %v", args)
		}
	}
}

func TestCandidateVolumesReportsAnUnreachableDaemon(t *testing.T) {
	old := command
	defer func() { command = old }()
	command = func(ctx context.Context, args ...string) *exec.Cmd {
		if slices.Contains(args, "config") {
			return exec.CommandContext(ctx, "printf", "%s", planConfig)
		}
		return exec.CommandContext(ctx, "sh", "-c", "echo 'cannot connect to Docker daemon' >&2; exit 1")
	}
	if got, err := (Runner{}).CandidateVolumes(context.Background(), composes.RunRequest{ProjectName: "p"}); err == nil {
		t.Fatalf("candidates = %+v, want an error rather than a guess", got)
	}
}

func TestDownAndRemoveVolumesRunsRmDownThenRemovesEachVolume(t *testing.T) {
	old := command
	defer func() { command = old }()
	var mu sync.Mutex
	var calls []string
	command = func(ctx context.Context, args ...string) *exec.Cmd {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "volume" && args[len(args)-1] == "busy" {
			return exec.CommandContext(ctx, "sh", "-c", "echo 'volume is in use' >&2; exit 1")
		}
		return exec.CommandContext(ctx, "true")
	}
	req := composes.RunRequest{ProjectName: "p", ComposeFilePath: "/c/compose.yml", EnvFilePath: "/c/.env"}
	removed, err := (Runner{}).DownAndRemoveVolumes(context.Background(), req, []string{"a", "busy", "b"})
	if err == nil || !strings.Contains(err.Error(), "busy") || !strings.Contains(err.Error(), "in use") {
		t.Fatalf("err = %v, want the busy volume named", err)
	}
	if !reflect.DeepEqual(removed, []string{"a", "b"}) {
		t.Fatalf("removed = %v, want [a b]", removed)
	}
	prefix := "compose -p p -f /c/compose.yml --env-file /c/.env "
	want := []string{
		prefix + "rm --stop --force --volumes",
		prefix + "down",
		"volume rm a",
		"volume rm busy",
		"volume rm b",
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %q, want %q", calls, want)
	}
}

func TestDownAndRemoveVolumesStopsWhenDownFails(t *testing.T) {
	old := command
	defer func() { command = old }()
	var calls []string
	command = func(ctx context.Context, args ...string) *exec.Cmd {
		calls = append(calls, args[0])
		if slices.Contains(args, "down") {
			return exec.CommandContext(ctx, "sh", "-c", "echo 'daemon unreachable' >&2; exit 1")
		}
		return exec.CommandContext(ctx, "true")
	}
	removed, err := (Runner{}).DownAndRemoveVolumes(context.Background(), composes.RunRequest{ProjectName: "p"}, []string{"a"})
	if err == nil || len(removed) != 0 {
		t.Fatalf("removed=%v err=%v, want an error and nothing removed", removed, err)
	}
	if slices.Contains(calls, "volume") {
		t.Fatalf("volumes removed after a failed down: %v", calls)
	}
}
