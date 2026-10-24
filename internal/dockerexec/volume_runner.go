package dockerexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"relist-panel/internal/volumes"
)

// helperEntrypoint is where the panel's own image carries relist-volhelper
// (the spec's Run spec); running that image means panel and helper cannot
// skew (ADR-0010).
const helperEntrypoint = "/usr/local/bin/relist-volhelper"

// panelImageEnv is the escape hatch for resolving the panel's image when
// `docker inspect` cannot answer (ADR-0010): the operator names it directly.
const panelImageEnv = "RELIST_IMAGE"

// exitCoder is the shape a failed child leaves behind: *exec.ExitError
// carries the exit code docker propagated from the helper. It is declared as
// an interface so the mapping is scriptable without a process.
type exitCoder interface {
	ExitCode() int
}

var _ exitCoder = (*exec.ExitError)(nil)

// VolumeRunner implements volumes.Runner: every operation is its own
// `docker run --rm` of the panel's own image with the helper as entrypoint,
// and the container removes itself when the operation ends (ADR-0010).
type VolumeRunner struct {
	resolveImage func(ctx context.Context) (string, error)
	runDocker    func(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) error
}

// NewVolumeRunner constructs the real runner: the panel's image resolved the
// way ADR-0010 prescribes, and execution through the docker CLI.
func NewVolumeRunner() *VolumeRunner {
	return &VolumeRunner{resolveImage: panelImage, runDocker: execDocker}
}

// Available reports whether the Volume Explorer can work at all. The helper
// runs out of the panel's own image on the host's Docker daemon, so resolving
// that image is the check: a bare binary, a missing Docker socket or a host
// that never saw the image fails here, with the reason the UI shows, rather
// than failing on the first operation.
func (r *VolumeRunner) Available(ctx context.Context) error {
	if _, err := r.resolveImage(ctx); err != nil {
		return fmt.Errorf("volume files need a containerized panel whose image the host's Docker can see: %w", err)
	}
	return nil
}

// Run runs one operation and returns when it has ended. stdin and stdout
// carry the operation's bytes; stderr is captured into the returned error.
// A helper refusal — exit 3, before any work — comes back as a
// *volumes.RefusalError, an archive refusal — exit 4 — as a
// *volumes.ArchiveRefusedError, and an existing name — exit 5 — as a
// *volumes.ExistsError; every other failure is a plain error.
func (r *VolumeRunner) Run(ctx context.Context, req volumes.Request, stdin io.Reader, stdout io.Writer) error {
	image, err := r.resolveImage(ctx)
	if err != nil {
		return fmt.Errorf("volume %s: %w", req.Op, err)
	}

	argv := volumeArgv(image, req)
	var stderr bytes.Buffer
	if err := r.runDocker(ctx, argv, stdin, stdout, &stderr); err != nil {
		message := strings.TrimSpace(stderr.String())
		var exit exitCoder
		if errors.As(err, &exit) {
			switch exit.ExitCode() {
			case volumes.RefusalExitCode:
				// The helper refused before doing the work (the spec's
				// Caps): its message is the whole story — need versus
				// available — and the HTTP layer answers 507 rather than
				// 502.
				return &volumes.RefusalError{Message: message}
			case volumes.ArchiveRefusedExitCode:
				// The helper refused the bytes it was fed (the spec's
				// Restore): an unsafe entry, a restore cap exceeded, or a
				// body that is not a usable tar.gz. The body is the
				// client's, and the HTTP layer answers 422 rather than
				// 502.
				return &volumes.ArchiveRefusedError{Message: message}
			case volumes.ExistsExitCode:
				// A create found the name taken (the spec's v1 operation
				// set): mkdir merges nothing and create never overwrites, so
				// the HTTP layer answers 409 rather than 502.
				return &volumes.ExistsError{Message: message}
			}
		}
		// A cancelled operator request kills the docker CLI child
		// (exec.CommandContext); the helper then sees its stdin pipe close
		// and dies on EOF/EPIPE, and --rm removes the container. Nothing
		// lingers (ADR-0010).
		return fmt.Errorf("docker %s: %w: %s", strings.Join(argv, " "), err, message)
	}
	return nil
}

// volumeArgv builds the spec's exact command line for one operation, with
// argv[0] included so tests can assert the whole invocation. Exactly one
// mount is ever added — the declared source — never the Docker socket, never
// another host path; and `--` before the path keeps a filename starting with
// `-` from being read as a flag.
func volumeArgv(image string, req volumes.Request) []string {
	mount := req.Source + ":/data"
	if req.ReadOnly {
		// Read operations get the `:ro` mount flag: the kernel refuses any
		// write, whatever a later layer believes. Write operations omit the
		// suffix and stay Docker's default read-write, exactly the spec's
		// `[:ro]`.
		mount += ":ro"
	}

	argv := []string{
		"docker",
		"run",
		"--rm",
		"-i",
		"--network", "none",
		"--user", req.User,
		"--read-only",
		"--tmpfs", "/tmp",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		// The helper is bounded so one bad Volume cannot take the host's
		// memory: the hard cap is generous because pack, archive and
		// recursive remove still list whole directories, and GOMEMLIMIT sits
		// under it so the GC works before the kernel's OOM killer does.
		"--memory", "512m",
		"-e", "GOMEMLIMIT=400MiB",
		"-v", mount,
		"--entrypoint", helperEntrypoint,
		image,
		string(req.Op),
	}
	switch {
	case req.Op == volumes.OpArchive:
		// archive's pinned format, before the path.
		argv = append(argv, "--format", "tar.gz")
	case req.Op == volumes.OpList && req.After != "":
		// list resumes after a name cursor, before the path.
		argv = append(argv, "--after", req.After)
	case req.Op == volumes.OpWrite && req.DeclaredBytes > 0:
		// write accepts the upload's declared size, so the helper can refuse
		// it before reading a byte of stdin (the spec's Caps).
		argv = append(argv, "--expect", strconv.FormatInt(req.DeclaredBytes, 10))
	}
	return append(argv, "--", req.Path)
}

// execDocker runs the docker CLI with argv passed directly, never through a
// shell: no part of a Request can be interpreted by anything but docker and
// the helper. exec.CommandContext kills the CLI child when ctx is cancelled.
func execDocker(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// panelImage resolves the panel's own image name. Docker sets a container's
// hostname to its ID by default, so `docker inspect <hostname>` names the
// image the panel is running from (ADR-0010) — no registry, no runtime pull.
// A bare-binary run, a missing socket, or unexpected inspect output all fall
// back to RELIST_IMAGE.
func panelImage(ctx context.Context) (string, error) {
	hostname, _ := os.Hostname()
	inspect := func(name string) ([]byte, error) {
		return exec.CommandContext(ctx, "docker", "inspect", name).Output()
	}
	return resolveImage(hostname, inspect, os.Getenv)
}

// resolveImage is panelImage's decision with the hostname, the inspect call
// and the environment injected, so both the inspect path and the
// RELIST_IMAGE fallback are unit-testable without a Docker daemon: inspect's
// `.Config.Image` wins, RELIST_IMAGE answers for every inspect failure, and
// with neither there is no image to run.
func resolveImage(hostname string, inspect func(name string) ([]byte, error), getenv func(string) string) (string, error) {
	if hostname != "" && inspect != nil {
		if output, err := inspect(hostname); err == nil {
			if image, err := parseInspectImage(output); err == nil {
				return image, nil
			}
		}
	}
	if image := getenv(panelImageEnv); image != "" {
		return image, nil
	}
	return "", fmt.Errorf("could not resolve the panel image: docker inspect failed and %s is unset", panelImageEnv)
}

// parseInspectImage reads .Config.Image from `docker inspect <hostname>`'s
// one-element JSON array — the image a container was created from.
func parseInspectImage(output []byte) (string, error) {
	var inspected []struct {
		Config struct {
			Image string `json:"Image"`
		} `json:"Config"`
	}
	if err := json.Unmarshal(output, &inspected); err != nil {
		return "", fmt.Errorf("parse docker inspect output: %w", err)
	}
	if len(inspected) == 0 || inspected[0].Config.Image == "" {
		return "", errors.New("docker inspect output names no image")
	}
	return inspected[0].Config.Image, nil
}

var _ volumes.Runner = (*VolumeRunner)(nil)
