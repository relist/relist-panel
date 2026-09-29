package dockerexec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"relist-panel/internal/volumes"
)

const testImage = "relist-panel:latest"

// The spec's argv, op by op: one run per operation, the read ops mounted
// `:ro`, and the write ops at Docker's default read-write. archive carries
// `--format tar.gz`, and a write with a declared size carries `--expect <n>`;
// both sit before `--`, where parse expects them.
func TestVolumeArgvForEveryOp(t *testing.T) {
	tests := []struct {
		name          string
		op            volumes.Op
		readOnly      bool
		declaredBytes int64
		wantMount     string
		wantOpArgs    []string
	}{
		{name: "list", op: volumes.OpList, readOnly: true, wantMount: "demo_pgdata:/data:ro", wantOpArgs: []string{"list"}},
		{name: "stat", op: volumes.OpStat, readOnly: true, wantMount: "demo_pgdata:/data:ro", wantOpArgs: []string{"stat"}},
		{name: "read", op: volumes.OpRead, readOnly: true, wantMount: "demo_pgdata:/data:ro", wantOpArgs: []string{"read"}},
		{name: "archive", op: volumes.OpArchive, readOnly: true, wantMount: "demo_pgdata:/data:ro", wantOpArgs: []string{"archive", "--format", "tar.gz"}},
		{name: "write", op: volumes.OpWrite, wantMount: "demo_pgdata:/data", wantOpArgs: []string{"write"}},
		{name: "write with a declared size", op: volumes.OpWrite, declaredBytes: 1048576, wantMount: "demo_pgdata:/data", wantOpArgs: []string{"write", "--expect", "1048576"}},
		{name: "delete", op: volumes.OpDelete, wantMount: "demo_pgdata:/data", wantOpArgs: []string{"delete"}},
		{name: "extract", op: volumes.OpExtract, wantMount: "demo_pgdata:/data", wantOpArgs: []string{"extract"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := volumes.Request{
				Source:        "demo_pgdata",
				User:          "1000:1000",
				Op:            tt.op,
				Path:          "uploads/notes.txt",
				DeclaredBytes: tt.declaredBytes,
				ReadOnly:      tt.readOnly,
			}

			got := volumeArgv(testImage, req)
			want := []string{
				"docker",
				"run",
				"--rm",
				"-i",
				"--network", "none",
				"--user", "1000:1000",
				"--read-only",
				"--tmpfs", "/tmp",
				"--cap-drop", "ALL",
				"--security-opt", "no-new-privileges",
				"-v", tt.wantMount,
				"--entrypoint", "/usr/local/bin/relist-volhelper",
				testImage,
			}
			want = append(want, tt.wantOpArgs...)
			want = append(want, "--", req.Path)

			if !reflect.DeepEqual(got, want) {
				t.Errorf("volumeArgv() =\n\t%q\nwant\n\t%q", got, want)
			}
		})
	}
}

// The parts the op table does not vary: a bind source is mounted verbatim, an
// explicit --user passes through unchanged, and a path starting with `-` is
// still the path because `--` precedes it.
func TestVolumeArgvPassesSourceUserAndLeadingDashPathVerbatim(t *testing.T) {
	req := volumes.Request{
		Source:   "/srv/app/data",
		User:     "999:998",
		Op:       volumes.OpRead,
		Path:     "-leading.txt",
		ReadOnly: true,
	}

	got := volumeArgv(testImage, req)
	want := []string{
		"docker",
		"run",
		"--rm",
		"-i",
		"--network", "none",
		"--user", "999:998",
		"--read-only",
		"--tmpfs", "/tmp",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"-v", "/srv/app/data:/data:ro",
		"--entrypoint", "/usr/local/bin/relist-volhelper",
		testImage,
		"read",
		"--",
		"-leading.txt",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("volumeArgv() =\n\t%q\nwant\n\t%q", got, want)
	}
}

func TestResolveImage(t *testing.T) {
	inspected := []byte(`[{"Id":"abc123","Config":{"Image":"relist-panel:latest"}}]`)

	tests := []struct {
		name     string
		hostname string
		inspect  func(name string) ([]byte, error)
		env      string
		want     string
		wantErr  bool
	}{
		{
			name:     "inspect names the image",
			hostname: "abc123",
			inspect: func(name string) ([]byte, error) {
				if name != "abc123" {
					t.Errorf("inspect(%q), want the container hostname abc123", name)
				}
				return inspected, nil
			},
			env:  "relist-panel:from-env",
			want: "relist-panel:latest",
		},
		{
			name:     "inspect fails and RELIST_IMAGE answers",
			hostname: "abc123",
			inspect:  func(string) ([]byte, error) { return nil, errors.New("exec: docker: not found") },
			env:      "relist-panel:from-env",
			want:     "relist-panel:from-env",
		},
		{
			name:     "malformed inspect output falls back to RELIST_IMAGE",
			hostname: "abc123",
			inspect:  func(string) ([]byte, error) { return []byte("not json"), nil },
			env:      "relist-panel:from-env",
			want:     "relist-panel:from-env",
		},
		{
			name:     "an empty inspect array falls back to RELIST_IMAGE",
			hostname: "abc123",
			inspect:  func(string) ([]byte, error) { return []byte(`[]`), nil },
			env:      "relist-panel:from-env",
			want:     "relist-panel:from-env",
		},
		{
			name:     "an inspect output naming no image falls back to RELIST_IMAGE",
			hostname: "abc123",
			inspect:  func(string) ([]byte, error) { return []byte(`[{"Config":{"Image":""}}]`), nil },
			env:      "relist-panel:from-env",
			want:     "relist-panel:from-env",
		},
		{
			name:     "a missing hostname skips inspect",
			hostname: "",
			inspect: func(string) ([]byte, error) {
				t.Error("inspect was called without a hostname")
				return nil, nil
			},
			env:  "relist-panel:from-env",
			want: "relist-panel:from-env",
		},
		{
			name:     "neither inspect nor RELIST_IMAGE answers",
			hostname: "abc123",
			inspect:  func(string) ([]byte, error) { return nil, errors.New("exec: docker: not found") },
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(key string) string {
				if key != panelImageEnv {
					t.Errorf("getenv(%q), want %q", key, panelImageEnv)
				}
				return tt.env
			}

			got, err := resolveImage(tt.hostname, tt.inspect, getenv)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolveImage() = %q, nil; want an error", got)
				}
				if !strings.Contains(err.Error(), panelImageEnv) {
					t.Errorf("resolveImage() error = %v, want it to name %s", err, panelImageEnv)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveImage() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("resolveImage() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Run is the wiring around the pure argv builder: it resolves the image,
// hands the built argv to the docker seam, and passes stdin/stdout through
// untouched.
func TestVolumeRunnerRunStreamsThroughTheDockerSeam(t *testing.T) {
	var gotArgv []string
	var gotStdin string
	runner := &VolumeRunner{
		resolveImage: func(context.Context) (string, error) { return testImage, nil },
		runDocker: func(_ context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) error {
			gotArgv = argv
			data, err := io.ReadAll(stdin)
			if err != nil {
				t.Fatalf("read stdin: %v", err)
			}
			gotStdin = string(data)
			_, _ = io.WriteString(stdout, "written")
			_, _ = io.WriteString(stderr, "warning: harmless")
			return nil
		},
	}
	req := volumes.Request{
		Source: "demo_pgdata",
		User:   "999:998",
		Op:     volumes.OpWrite,
		Path:   "notes.txt",
	}

	var stdout bytes.Buffer
	if err := runner.Run(context.Background(), req, strings.NewReader("bytes"), &stdout); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if want := volumeArgv(testImage, req); !reflect.DeepEqual(gotArgv, want) {
		t.Errorf("docker argv =\n\t%q\nwant\n\t%q", gotArgv, want)
	}
	if gotStdin != "bytes" {
		t.Errorf("stdin reached docker as %q, want %q", gotStdin, "bytes")
	}
	if stdout.String() != "written" {
		t.Errorf("stdout reached the caller as %q, want %q", stdout.String(), "written")
	}
}

func TestVolumeRunnerRunFoldsStderrIntoTheError(t *testing.T) {
	runner := &VolumeRunner{
		resolveImage: func(context.Context) (string, error) { return testImage, nil },
		runDocker: func(_ context.Context, _ []string, _ io.Reader, _, stderr io.Writer) error {
			_, _ = io.WriteString(stderr, "docker: Error response from daemon: no such volume")
			return errors.New("exit status 125")
		},
	}

	err := runner.Run(context.Background(), volumes.Request{Op: volumes.OpList, Path: ".", ReadOnly: true}, nil, io.Discard)
	if err == nil {
		t.Fatal("Run() = nil error, want the docker failure")
	}
	for _, want := range []string{"exit status 125", "no such volume", "docker run"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Run() error = %v, want it to contain %q", err, want)
		}
	}
}

func TestVolumeRunnerRunSurfacesCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	runner := &VolumeRunner{
		resolveImage: func(context.Context) (string, error) { return testImage, nil },
		runDocker: func(ctx context.Context, _ []string, _ io.Reader, _, _ io.Writer) error {
			return ctx.Err()
		},
	}

	err := runner.Run(ctx, volumes.Request{Op: volumes.OpRead, Path: "notes.txt", ReadOnly: true}, nil, io.Discard)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Run() error = %v, want context.Canceled", err)
	}
}

func TestVolumeRunnerRunStopsWhenTheImageCannotBeResolved(t *testing.T) {
	runner := &VolumeRunner{
		resolveImage: func(context.Context) (string, error) { return "", errors.New("no image") },
		runDocker: func(context.Context, []string, io.Reader, io.Writer, io.Writer) error {
			t.Error("docker ran without a resolved image")
			return nil
		},
	}

	err := runner.Run(context.Background(), volumes.Request{Op: volumes.OpList, Path: "."}, nil, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "no image") {
		t.Errorf("Run() error = %v, want the image resolution failure", err)
	}
}

// Available is the feature-availability probe: it resolves the panel image,
// the one thing every helper run needs before it can start.
func TestVolumeRunnerAvailableResolvesThePanelImage(t *testing.T) {
	resolved := false
	runner := &VolumeRunner{
		resolveImage: func(context.Context) (string, error) {
			resolved = true
			return testImage, nil
		},
	}

	if err := runner.Available(context.Background()); err != nil {
		t.Fatalf("Available() error = %v", err)
	}
	if !resolved {
		t.Error("Available() did not resolve the image")
	}
}

// A native binary run or a missing Docker socket must say the feature needs a
// containerized panel, in words the UI can show, rather than failing on the
// first operation.
func TestVolumeRunnerAvailableNeedsAContainerizedPanel(t *testing.T) {
	runner := &VolumeRunner{
		resolveImage: func(context.Context) (string, error) {
			return "", errors.New("could not resolve the panel image: docker inspect failed and RELIST_IMAGE is unset")
		},
	}

	err := runner.Available(context.Background())
	if err == nil {
		t.Fatal("Available() = nil error, want the image resolution failure")
	}
	for _, want := range []string{"containerized panel", "RELIST_IMAGE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Available() error = %v, want it to contain %q", err, want)
		}
	}
}
