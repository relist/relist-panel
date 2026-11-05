// Package dockerexec implements the system's Docker ports: composes.ComposeRunner
// by shelling out to the `docker compose` CLI (ADR-0002), volumes.Runner by one
// `docker run --rm` per Volume operation and volumes.CLI by the CLI's
// `compose config` and `volume inspect` over the same argv-only exec
// (ADR-0010), and hostmetrics.ContainerSource by calling the stats API over the
// mounted Docker socket (ADR-0006) — one adapter package for the ports declared
// in their consumers (ADR-0003).
package dockerexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"relist-panel/internal/composes"
)

// Runner shells out to the docker compose CLI plugin.
type Runner struct{}

// New constructs a Runner.
func New() Runner {
	return Runner{}
}

// upArgs is the argv for a deploy. Build chooses --build or --no-build (a
// Source's Services are built beforehand by Build). --remove-orphans removes
// containers of services no longer in the compose file; it never touches
// volumes. Services, when set, limits up to them with --no-deps, so a Service
// being rolled out is left alone. The argv is tested through this helper.
func upArgs(opts composes.UpOptions) []string {
	args := []string{"up", "-d", buildFlag(opts.Build)}
	if len(opts.Services) > 0 {
		args = append(args, "--no-deps")
	}
	args = append(args, "--remove-orphans")
	return append(args, opts.Services...)
}

// scaleArgs starts one more Container of a Service from the current file and
// leaves the running ones as they are (ADR-0023).
func scaleArgs(opts composes.ScaleOptions) []string {
	return []string{"up", "-d", buildFlag(opts.Build), "--no-deps", "--no-recreate",
		"--scale", fmt.Sprintf("%s=%d", opts.Service, opts.Replicas), opts.Service}
}

func buildFlag(build bool) string {
	if build {
		return "--build"
	}
	return "--no-build"
}

// buildArgs is the argv for building one Service with plain progress output.
// --progress is a global compose flag; Compose v5 rejects it after build.
func buildArgs(service string, noCache bool) []string {
	args := []string{"--progress", "plain", "build"}
	if noCache {
		args = append(args, "--no-cache")
	}
	return append(args, service)
}

func (Runner) Up(ctx context.Context, req composes.RunRequest, opts composes.UpOptions) error {
	_, err := runOn(ctx, req, opts.Out, builderFor(req, opts.Build), upArgs(opts)...)
	return err
}

func (Runner) Scale(ctx context.Context, req composes.RunRequest, opts composes.ScaleOptions) error {
	_, err := runOn(ctx, req, opts.Out, builderFor(req, opts.Build), scaleArgs(opts)...)
	return err
}

func (Runner) Config(ctx context.Context, req composes.RunRequest) ([]byte, error) {
	content, err := run(ctx, req, nil, "config", "--format", "json")
	return []byte(content), err
}

// policyConfigArgs resolves the file for the Compose policy: every profile
// on, and env files left as paths so the policy can check where they are.
func policyConfigArgs() []string {
	return []string{"--profile", "*", "config", "--format", "json", "--no-env-resolution"}
}

func (Runner) PolicyConfig(ctx context.Context, req composes.RunRequest) ([]byte, error) {
	content, err := run(ctx, req, nil, policyConfigArgs()...)
	return []byte(content), err
}

func (Runner) Build(ctx context.Context, req composes.RunRequest, opts composes.BuildOptions) error {
	_, err := runOn(ctx, req, opts.Out, req.Builder, buildArgs(opts.Service, opts.NoCache)...)
	return err
}

func (Runner) Down(ctx context.Context, req composes.RunRequest) error {
	_, err := run(ctx, req, nil, "down")
	return err
}

func (Runner) DownWithVolumes(ctx context.Context, req composes.RunRequest) error {
	_, err := run(ctx, req, nil, "down", "-v")
	return err
}

func (Runner) Restart(ctx context.Context, req composes.RunRequest) error {
	_, err := run(ctx, req, nil, "restart")
	return err
}

func (Runner) Status(ctx context.Context, req composes.RunRequest) (composes.ComposeStatus, error) {
	output, err := run(ctx, req, nil, "ps", "--all", "--format", "json")
	if err != nil {
		return composes.ComposeStatus{}, err
	}
	return parseStatus(output)
}

func (Runner) Logs(ctx context.Context, req composes.RunRequest) (string, error) {
	return run(ctx, req, nil, "logs", "--no-color", "--tail=200")
}

// command builds the docker invocation; tests replace it to run a stand-in.
var command = func(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Env = cliEnv(os.Environ())
	return cmd
}

// waitDelay bounds how long Wait lingers for output pipes after the CLI exits
// or is killed.
var waitDelay = 5 * time.Second

// stderrTailSize is how much of stderr an error message keeps when output is
// streamed to out.
const stderrTailSize = 4 << 10

// tailWriter keeps the last stderrTailSize bytes written to it.
type tailWriter struct{ buf []byte }

func (w *tailWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	if len(w.buf) > stderrTailSize {
		w.buf = append(w.buf[:0], w.buf[len(w.buf)-stderrTailSize:]...)
	}
	return len(p), nil
}

// lockedWriter serializes writes to w.
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// run executes `docker compose` for req. With out set, stdout and stderr both
// stream to it and the error carries the last 4 KiB of stderr; with out nil the
// output is captured and stdout returned.
func run(ctx context.Context, req composes.RunRequest, out io.Writer, args ...string) (string, error) {
	return runOn(ctx, req, out, "", args...)
}

// builderFor is the Builder a call that may build runs on: req's when it
// builds, none otherwise.
func builderFor(req composes.RunRequest, build bool) string {
	if build {
		return req.Builder
	}
	return ""
}

// runOn is run on a buildx Builder: BUILDX_BUILDER picks it, and an empty
// builder leaves Docker default.
func runOn(ctx context.Context, req composes.RunRequest, out io.Writer, builder string, args ...string) (string, error) {
	fullArgs := append([]string{
		"compose",
		"-p", req.ProjectName,
		"-f", req.ComposeFilePath,
		"--env-file", req.EnvFilePath,
	}, args...)

	cmd := command(ctx, fullArgs...)
	// A killed docker CLI can leave a grandchild holding the output pipes;
	// without a WaitDelay, Wait would block on them and hang Cancel and Delete.
	cmd.WaitDelay = waitDelay
	if builder != "" {
		cmd.Env = append(cmd.Env, "BUILDX_BUILDER="+builder)
	}
	var stdout, stderr bytes.Buffer
	tail := &tailWriter{}
	if out != nil {
		// exec copies stdout and stderr on separate goroutines; serialize
		// their writes so out need not be safe for concurrent use.
		shared := &lockedWriter{w: out}
		cmd.Stdout = shared
		cmd.Stderr = io.MultiWriter(shared, tail)
	} else {
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
	}
	if err := cmd.Run(); err != nil {
		detail := stderr.String()
		if out != nil {
			detail = string(tail.buf)
		}
		return "", fmt.Errorf("docker %s: %w: %s", strings.Join(fullArgs, " "), err, strings.TrimSpace(detail))
	}
	return stdout.String(), nil
}

// parseStatus reads `docker compose ps --format json`'s newline-delimited
// JSON output (one container object per line) into the Compose's running and
// total Container counts plus the Service names it saw, in first-seen order.
func parseStatus(output string) (composes.ComposeStatus, error) {
	var status composes.ComposeStatus
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var container struct {
			Service string `json:"Service"`
			State   string `json:"State"`
		}
		if err := json.Unmarshal([]byte(line), &container); err != nil {
			return composes.ComposeStatus{}, fmt.Errorf("parse docker compose ps output: %w", err)
		}
		status.Total++
		if container.State == "running" {
			status.Running++
		}
		if container.Service != "" && !slices.Contains(status.Services, container.Service) {
			status.Services = append(status.Services, container.Service)
		}
	}
	return status, nil
}
