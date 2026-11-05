package dockerexec

import (
	"bytes"
	"context"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"relist-panel/internal/composes"
)

func TestUpArgsRemoveOrphanContainersButNeverVolumes(t *testing.T) {
	got := upArgs(composes.UpOptions{Build: true})
	want := []string{"up", "-d", "--build", "--remove-orphans"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("upArgs(build) = %v, want %v", got, want)
	}
	for _, arg := range got {
		if arg == "-v" || arg == "--volumes" {
			t.Fatalf("upArgs(build) contains %q; deploy must never remove volumes", arg)
		}
	}
}

func TestUpArgs(t *testing.T) {
	if got, want := upArgs(composes.UpOptions{Build: true}), []string{"up", "-d", "--build", "--remove-orphans"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("upArgs(build) = %v, want %v", got, want)
	}
	if got, want := upArgs(composes.UpOptions{}), []string{"up", "-d", "--no-build", "--remove-orphans"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("upArgs() = %v, want %v", got, want)
	}
	got := upArgs(composes.UpOptions{Services: []string{"worker", "db"}})
	if want := []string{"up", "-d", "--no-build", "--no-deps", "--remove-orphans", "worker", "db"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("upArgs(services) = %v, want %v", got, want)
	}
}

func TestScaleArgs(t *testing.T) {
	got := scaleArgs(composes.ScaleOptions{Service: "web", Replicas: 2})
	if want := []string{"up", "-d", "--no-build", "--no-deps", "--no-recreate", "--scale", "web=2", "web"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scaleArgs = %v, want %v", got, want)
	}
	got = scaleArgs(composes.ScaleOptions{Service: "web", Replicas: 3, Build: true})
	if want := []string{"up", "-d", "--build", "--no-deps", "--no-recreate", "--scale", "web=3", "web"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scaleArgs(build) = %v, want %v", got, want)
	}
}

func TestBuildArgs(t *testing.T) {
	if got, want := buildArgs("api", false), []string{"--progress", "plain", "build", "api"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("buildArgs(api, false) = %v, want %v", got, want)
	}
	if got, want := buildArgs("api", true), []string{"--progress", "plain", "build", "--no-cache", "api"}; !reflect.DeepEqual(got, want) {
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

// A killed CLI whose grandchild keeps the output pipes open must not hang the
// caller: WaitDelay lets Wait give up on the pipes.
func TestRunReturnsWhenAGrandchildHoldsThePipes(t *testing.T) {
	oldCommand, oldDelay := command, waitDelay
	command = func(ctx context.Context, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "sleep 30 & wait")
	}
	waitDelay = 100 * time.Millisecond
	t.Cleanup(func() { command, waitDelay = oldCommand, oldDelay })

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := run(ctx, composes.RunRequest{}, &bytes.Buffer{}, "up")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("run succeeded, want an error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run hung on the grandchild's pipes")
	}
}

// BUILDX_BUILDER picks the Builder only for calls that can build; nothing
// else may ever run against a Builder by accident.
func TestBuilderEnvOnlyOnBuildCapableCalls(t *testing.T) {
	var cmds []*exec.Cmd
	old := command
	command = func(ctx context.Context, _ ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "true")
		cmds = append(cmds, cmd)
		return cmd
	}
	t.Cleanup(func() { command = old })
	hasBuilder := func() bool {
		for _, kv := range cmds[len(cmds)-1].Env {
			if kv == "BUILDX_BUILDER=relist-builder-7" {
				return true
			}
		}
		return false
	}
	ctx, r := context.Background(), Runner{}
	req := composes.RunRequest{Builder: "relist-builder-7"}
	for name, tc := range map[string]struct {
		call func() error
		want bool
	}{
		"build":      {func() error { return r.Build(ctx, req, composes.BuildOptions{Service: "web"}) }, true},
		"up --build": {func() error { return r.Up(ctx, req, composes.UpOptions{Build: true}) }, true},
		"scale --build": {func() error {
			return r.Scale(ctx, req, composes.ScaleOptions{Service: "web", Replicas: 2, Build: true})
		}, true},
		"up --no-build":    {func() error { return r.Up(ctx, req, composes.UpOptions{}) }, false},
		"scale --no-build": {func() error { return r.Scale(ctx, req, composes.ScaleOptions{Service: "web", Replicas: 2}) }, false},
		"status":           {func() error { _, err := r.Status(ctx, req); return err }, false},
		"docker default": {func() error {
			return r.Build(ctx, composes.RunRequest{}, composes.BuildOptions{Service: "web"})
		}, false},
	} {
		if err := tc.call(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := hasBuilder(); got != tc.want {
			t.Errorf("%s: BUILDX_BUILDER set = %v, want %v", name, got, tc.want)
		}
	}
}

// A deploy whose images were checked never pulls again: a registry could
// answer a second pull with another image.
func TestUpAndScaleArgsWithoutPulling(t *testing.T) {
	if got, want := upArgs(composes.UpOptions{NoPull: true}), []string{"up", "-d", "--no-build", "--pull", "never", "--remove-orphans"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("upArgs(no pull) = %v, want %v", got, want)
	}
	got := scaleArgs(composes.ScaleOptions{Service: "web", Replicas: 2, NoPull: true})
	if want := []string{"up", "-d", "--no-build", "--pull", "never", "--no-deps", "--no-recreate", "--scale", "web=2", "web"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("scaleArgs(no pull) = %v, want %v", got, want)
	}
}

func TestBuildArgsForEveryService(t *testing.T) {
	if got, want := buildArgs("", false), []string{"--progress", "plain", "build"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("buildArgs(all) = %v, want %v", got, want)
	}
}

func TestComposePullArgs(t *testing.T) {
	if got, want := composePullArgs(composes.PullOptions{Missing: true}), []string{"pull", "--ignore-buildable", "--policy", "missing"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("composePullArgs(missing) = %v, want %v", got, want)
	}
	if got, want := composePullArgs(composes.PullOptions{Services: []string{"web", "db"}}), []string{"pull", "--ignore-buildable", "web", "db"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("composePullArgs(services) = %v, want %v", got, want)
	}
}

func TestImageLabels(t *testing.T) {
	var argv []string
	old := command
	t.Cleanup(func() { command = old })
	output := `{"traefik.enable":"true"}`
	command = func(ctx context.Context, args ...string) *exec.Cmd {
		argv = args
		return exec.CommandContext(ctx, "echo", output)
	}
	labels, err := Runner{}.ImageLabels(context.Background(), "app:1", "")
	if err != nil || labels["traefik.enable"] != "true" {
		t.Fatalf("labels = %v, err = %v", labels, err)
	}
	if want := []string{"image", "inspect", "--format", "{{json .Config.Labels}}", "app:1"}; !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	if _, err := (Runner{}).ImageLabels(context.Background(), "app:1", "linux/386"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"image", "inspect", "--platform", "linux/386", "--format", "{{json .Config.Labels}}", "app:1"}; !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv with platform = %v, want %v", argv, want)
	}
	output = "null"
	if labels, err := (Runner{}).ImageLabels(context.Background(), "app:1", ""); err != nil || len(labels) != 0 {
		t.Fatalf("no labels = %v, err = %v", labels, err)
	}
}
