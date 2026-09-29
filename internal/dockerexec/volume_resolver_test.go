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
