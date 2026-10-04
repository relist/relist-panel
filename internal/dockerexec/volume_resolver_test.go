package dockerexec

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"relist-panel/internal/volumes"
)

// demoResolvedConfig stands in for `docker compose config --format json`
// output; the resolver only has to return it untouched.
const demoResolvedConfig = `{"services":{"db":{"volumes":[{"type":"volume","source":"pgdata","target":"/data"}]}},"volumes":{"pgdata":{"name":"demo_pgdata"}}}`

// ComposeConfig shells the spec's exact resolution call — argv only, never a
// shell string — and returns the CLI's stdout unchanged.
func TestVolumeResolverComposeConfigRunsTheResolutionArgv(t *testing.T) {
	var gotArgv []string
	resolver := &VolumeResolver{
		runDocker: func(_ context.Context, argv []string, _ io.Reader, stdout, _ io.Writer) error {
			gotArgv = argv
			_, _ = io.WriteString(stdout, demoResolvedConfig)
			return nil
		},
	}
	ref := volumes.ComposeRef{
		ProjectName:     "demo-prod",
		ComposeFilePath: "/var/lib/relist-panel/demo/prod/db/docker-compose.yml",
		EnvFilePath:     "/var/lib/relist-panel/demo/prod/db/.env",
	}

	got, err := resolver.ComposeConfig(context.Background(), ref)
	if err != nil {
		t.Fatalf("ComposeConfig() error = %v", err)
	}
	want := []string{
		"docker", "compose",
		"-p", "demo-prod",
		"-f", "/var/lib/relist-panel/demo/prod/db/docker-compose.yml",
		"--env-file", "/var/lib/relist-panel/demo/prod/db/.env",
		"config", "--format", "json",
	}
	if !reflect.DeepEqual(gotArgv, want) {
		t.Errorf("docker argv =\n\t%q\nwant\n\t%q", gotArgv, want)
	}
	if string(got) != demoResolvedConfig {
		t.Errorf("ComposeConfig() = %q, want the CLI's stdout", got)
	}
}

// A failing CLI folds its stderr into the error, which is how the operator
// sees the CLI's own `couldn't find env file` before the first deploy.
func TestVolumeResolverComposeConfigFoldsStderrIntoTheError(t *testing.T) {
	resolver := &VolumeResolver{
		runDocker: func(_ context.Context, _ []string, _ io.Reader, _, stderr io.Writer) error {
			_, _ = io.WriteString(stderr, "couldn't find env file: /var/lib/relist-panel/demo/prod/db/.env")
			return errors.New("exit status 1")
		},
	}

	_, err := resolver.ComposeConfig(context.Background(), volumes.ComposeRef{})
	if err == nil {
		t.Fatal("ComposeConfig() = nil error, want the CLI failure")
	}
	for _, want := range []string{"docker compose", "exit status 1", "couldn't find env file"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ComposeConfig() error = %v, want it to contain %q", err, want)
		}
	}
}

// VolumeExists maps the inspect call's three outcomes: found is true, a
// "no such volume" failure is false without an error, and any other failure —
// a daemon that is down, a missing CLI — stays an error.
func TestVolumeResolverVolumeExists(t *testing.T) {
	tests := []struct {
		name        string
		run         func(stderr io.Writer) error
		want        bool
		errContains string
	}{
		{
			name: "a found volume is true",
			run:  func(io.Writer) error { return nil },
			want: true,
		},
		{
			name: "a missing volume is false and not an error",
			run: func(stderr io.Writer) error {
				_, _ = io.WriteString(stderr, "[]\nError response from daemon: get demo_pgdata: no such volume\n")
				return errors.New("exit status 1")
			},
			want: false,
		},
		{
			name: "another failure stays an error",
			run: func(stderr io.Writer) error {
				_, _ = io.WriteString(stderr, "failed to connect to the docker API at unix:///var/run/docker.sock")
				return errors.New("exit status 1")
			},
			errContains: "failed to connect",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotArgv []string
			resolver := &VolumeResolver{
				runDocker: func(_ context.Context, argv []string, _ io.Reader, _, stderr io.Writer) error {
					gotArgv = argv
					return tt.run(stderr)
				},
			}

			got, err := resolver.VolumeExists(context.Background(), "demo_pgdata")
			if tt.errContains != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errContains) {
					t.Fatalf("VolumeExists() error = %v, want it to contain %q", err, tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("VolumeExists() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("VolumeExists() = %v, want %v", got, tt.want)
			}
			if want := []string{"docker", "volume", "inspect", "demo_pgdata"}; !reflect.DeepEqual(gotArgv, want) {
				t.Errorf("docker argv = %q, want %q", gotArgv, want)
			}
		})
	}
}

// dockerStep is one scripted docker call's stdout, stderr and error.
type dockerStep struct {
	stdout string
	stderr string
	err    error
}

// scriptedDocker answers each docker call in order with the matching step and
// records every argv; a call past the script fails.
func scriptedDocker(steps []dockerStep, gotArgvs *[][]string) func(context.Context, []string, io.Reader, io.Writer, io.Writer) error {
	return func(_ context.Context, argv []string, _ io.Reader, stdout, stderr io.Writer) error {
		i := len(*gotArgvs)
		*gotArgvs = append(*gotArgvs, argv)
		if i >= len(steps) {
			return errors.New("unexpected docker call")
		}
		_, _ = io.WriteString(stdout, steps[i].stdout)
		_, _ = io.WriteString(stderr, steps[i].stderr)
		return steps[i].err
	}
}

// ListProjectVolumes lists names by project label, then inspects them all in
// one call; the inspect JSON array supplies Name, CreatedAt and Labels.
func TestVolumeResolverListProjectVolumesRunsLsThenInspect(t *testing.T) {
	var gotArgvs [][]string
	resolver := &VolumeResolver{runDocker: scriptedDocker([]dockerStep{
		{stdout: "app_data\napp_old\n"},
		{stdout: `[
			{"Name":"app_data","CreatedAt":"2026-01-02T03:04:05Z","Driver":"local","Labels":{"com.docker.compose.project":"app","com.docker.compose.volume":"data"}},
			{"Name":"app_old","CreatedAt":"2026-02-03T04:05:06Z","Driver":"local","Labels":null}
		]`},
	}, &gotArgvs)}

	got, err := resolver.ListProjectVolumes(context.Background(), "app")
	if err != nil {
		t.Fatalf("ListProjectVolumes() error = %v", err)
	}
	wantArgvs := [][]string{
		{"docker", "volume", "ls", "-q", "--filter", "label=com.docker.compose.project=app"},
		{"docker", "volume", "inspect", "app_data", "app_old"},
	}
	if !reflect.DeepEqual(gotArgvs, wantArgvs) {
		t.Errorf("docker argvs =\n\t%q\nwant\n\t%q", gotArgvs, wantArgvs)
	}
	want := []volumes.ProjectVolume{
		{Name: "app_data", CreatedAt: "2026-01-02T03:04:05Z", Labels: map[string]string{
			volumes.ComposeProjectLabel: "app",
			volumes.ComposeVolumeLabel:  "data",
		}},
		{Name: "app_old", CreatedAt: "2026-02-03T04:05:06Z"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListProjectVolumes() = %#v, want %#v", got, want)
	}
}

// With no volume matching the label there is nothing to inspect, and the
// answer is an empty non-nil slice.
func TestVolumeResolverListProjectVolumesSkipsInspectWhenNothingMatches(t *testing.T) {
	var gotArgvs [][]string
	resolver := &VolumeResolver{runDocker: scriptedDocker([]dockerStep{{stdout: "\n"}}, &gotArgvs)}

	got, err := resolver.ListProjectVolumes(context.Background(), "app")
	if err != nil {
		t.Fatalf("ListProjectVolumes() error = %v", err)
	}
	if len(gotArgvs) != 1 {
		t.Errorf("docker calls = %q, want only the ls", gotArgvs)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("ListProjectVolumes() = %#v, want an empty non-nil slice", got)
	}
}

func TestVolumeResolverListProjectVolumesFoldsStderrIntoTheError(t *testing.T) {
	var gotArgvs [][]string
	resolver := &VolumeResolver{runDocker: scriptedDocker([]dockerStep{
		{stderr: "Cannot connect to the Docker daemon", err: errors.New("exit status 1")},
	}, &gotArgvs)}

	_, err := resolver.ListProjectVolumes(context.Background(), "app")
	if err == nil {
		t.Fatal("ListProjectVolumes() = nil error, want the CLI failure")
	}
	for _, want := range []string{"docker volume ls", "exit status 1", "Cannot connect"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ListProjectVolumes() error = %v, want it to contain %q", err, want)
		}
	}
}

// RemoveVolume runs a plain `docker volume rm <name>` — never `-f`.
func TestVolumeResolverRemoveVolumeRunsTheExactArgv(t *testing.T) {
	var gotArgvs [][]string
	resolver := &VolumeResolver{runDocker: scriptedDocker([]dockerStep{{stdout: "app_old\n"}}, &gotArgvs)}

	if err := resolver.RemoveVolume(context.Background(), "app_old"); err != nil {
		t.Fatalf("RemoveVolume() error = %v", err)
	}
	if want := [][]string{{"docker", "volume", "rm", "app_old"}}; !reflect.DeepEqual(gotArgvs, want) {
		t.Errorf("docker argvs = %q, want %q", gotArgvs, want)
	}
}

// A volume a container still uses maps to volumes.ErrVolumeInUse, with
// Docker's own message kept for the operator.
func TestVolumeResolverRemoveVolumeMapsInUseToErrVolumeInUse(t *testing.T) {
	const message = "Error response from daemon: remove app_old: volume is in use - [abc123]"
	var gotArgvs [][]string
	resolver := &VolumeResolver{runDocker: scriptedDocker([]dockerStep{
		{stderr: message, err: errors.New("exit status 1")},
	}, &gotArgvs)}

	err := resolver.RemoveVolume(context.Background(), "app_old")
	if !errors.Is(err, volumes.ErrVolumeInUse) {
		t.Fatalf("RemoveVolume() error = %v, want ErrVolumeInUse", err)
	}
	if !strings.Contains(err.Error(), message) {
		t.Errorf("RemoveVolume() error = %v, want Docker's message %q", err, message)
	}
}

// With AllProfiles the resolver lists every declared profile with
// `config --profiles` and passes each one explicitly as a global --profile
// before `config`, so volumes of profile-gated services are resolved too.
// `*` is never relied on: a Compose that read it literally would drop the
// gated Services and report their volumes as orphans.
func TestVolumeResolverComposeConfigAllProfilesPassesEveryDeclaredProfile(t *testing.T) {
	var gotArgvs [][]string
	resolver := &VolumeResolver{runDocker: scriptedDocker([]dockerStep{
		{stdout: "extra\nother\n"},
		{stdout: demoResolvedConfig},
	}, &gotArgvs)}
	ref := volumes.ComposeRef{ProjectName: "demo-prod", ComposeFilePath: "/c/docker-compose.yml", EnvFilePath: "/c/.env", AllProfiles: true}

	got, err := resolver.ComposeConfig(context.Background(), ref)
	if err != nil {
		t.Fatalf("ComposeConfig() error = %v", err)
	}
	if string(got) != demoResolvedConfig {
		t.Errorf("ComposeConfig() = %q, want the config call's stdout", got)
	}
	base := []string{"docker", "compose", "-p", "demo-prod", "-f", "/c/docker-compose.yml", "--env-file", "/c/.env"}
	want := [][]string{
		append(append([]string{}, base...), "config", "--profiles"),
		append(append([]string{}, base...), "--profile", "extra", "--profile", "other", "config", "--format", "json"),
	}
	if !reflect.DeepEqual(gotArgvs, want) {
		t.Errorf("docker argvs =\n\t%q\nwant\n\t%q", gotArgvs, want)
	}
}

// A Compose that declares no profiles resolves with no --profile flag.
func TestVolumeResolverComposeConfigAllProfilesWithNoneAddsNoFlag(t *testing.T) {
	var gotArgvs [][]string
	resolver := &VolumeResolver{runDocker: scriptedDocker([]dockerStep{
		{stdout: ""},
		{stdout: demoResolvedConfig},
	}, &gotArgvs)}
	ref := volumes.ComposeRef{ProjectName: "demo-prod", ComposeFilePath: "/c/docker-compose.yml", EnvFilePath: "/c/.env", AllProfiles: true}

	if _, err := resolver.ComposeConfig(context.Background(), ref); err != nil {
		t.Fatalf("ComposeConfig() error = %v", err)
	}
	base := []string{"docker", "compose", "-p", "demo-prod", "-f", "/c/docker-compose.yml", "--env-file", "/c/.env"}
	want := [][]string{
		append(append([]string{}, base...), "config", "--profiles"),
		append(append([]string{}, base...), "config", "--format", "json"),
	}
	if !reflect.DeepEqual(gotArgvs, want) {
		t.Errorf("docker argvs =\n\t%q\nwant\n\t%q", gotArgvs, want)
	}
}

// A failing `config --profiles` is returned as an error and the resolution
// call never runs: orphan detection fails closed.
func TestVolumeResolverComposeConfigAllProfilesFailsClosedWhenListingFails(t *testing.T) {
	var gotArgvs [][]string
	resolver := &VolumeResolver{runDocker: scriptedDocker([]dockerStep{
		{stderr: "boom", err: errors.New("exit status 1")},
	}, &gotArgvs)}
	ref := volumes.ComposeRef{ProjectName: "demo-prod", ComposeFilePath: "/c/docker-compose.yml", EnvFilePath: "/c/.env", AllProfiles: true}

	got, err := resolver.ComposeConfig(context.Background(), ref)
	if err == nil {
		t.Fatalf("ComposeConfig() = %q, nil; want an error", got)
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want the CLI's stderr", err)
	}
	if len(gotArgvs) != 1 {
		t.Errorf("docker calls = %d, want 1 (no config call after a failed --profiles)", len(gotArgvs))
	}
}
