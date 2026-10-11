package dockerexec

import (
	"bytes"
	"context"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"relist-panel/internal/composes"
)

func TestUpArgsRemoveOrphanContainersButNeverVolumes(t *testing.T) {
	got := upArgs(true)
	want := []string{"up", "-d", "--build", "--remove-orphans"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("upArgs(true) = %v, want %v", got, want)
	}
	for _, arg := range got {
		if arg == "-v" || arg == "--volumes" {
			t.Fatalf("upArgs(true) contains %q; deploy must never remove volumes", arg)
		}
	}
}

func TestUpArgs(t *testing.T) {
	if got, want := upArgs(true), []string{"up", "-d", "--build", "--remove-orphans"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("upArgs(true) = %v, want %v", got, want)
	}
	if got, want := upArgs(false), []string{"up", "-d", "--no-build", "--remove-orphans"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("upArgs(false) = %v, want %v", got, want)
	}
}

func TestBuildArgs(t *testing.T) {
	if got, want := buildArgs("api", false), []string{"build", "--progress", "plain", "api"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("buildArgs(api, false) = %v, want %v", got, want)
	}
	if got, want := buildArgs("api", true), []string{"build", "--progress", "plain", "--no-cache", "api"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("buildArgs(api, true) = %v, want %v", got, want)
	}
}

func TestRunStreamsToOut(t *testing.T) {
	old := command
	command = func(ctx context.Context, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "echo out; echo err >&2; exit 3")
	}
	t.Cleanup(func() { command = old })

	var out bytes.Buffer
	_, err := run(context.Background(), composes.RunRequest{}, &out, "up")
	if err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("err = %v, want exit status 3", err)
	}
	if !strings.Contains(out.String(), "out\n") || !strings.Contains(out.String(), "err\n") {
		t.Fatalf("Out = %q, want both stdout and stderr lines", out.String())
	}
	if !strings.Contains(err.Error(), "err") {
		t.Fatalf("err = %v, want stderr tail in the error text", err)
	}
}

// A Compose's Service names ride its status for the terminal listing, in
// first-seen order and without duplicates across a Service's Containers.
func TestParseStatusCollectsServiceNames(t *testing.T) {
	output := `{"Service":"app","State":"running"}
{"Service":"worker","State":"exited"}
{"Service":"app","State":"running"}`
	status, err := parseStatus(output)
	if err != nil {
		t.Fatalf("parseStatus() error = %v", err)
	}
	if status.Running != 2 || status.Total != 3 {
		t.Fatalf("status counts = %+v, want running 2 of 3", status)
	}
	if want := []string{"app", "worker"}; !reflect.DeepEqual(status.Services, want) {
		t.Fatalf("Services = %v, want %v", status.Services, want)
	}
}
