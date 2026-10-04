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
	"os/exec"
	"strings"

	"relist-panel/internal/composes"
)

// Runner shells out to the docker compose CLI plugin.
type Runner struct{}

// New constructs a Runner.
func New() Runner {
	return Runner{}
}

// upArgs is the argv for a deploy. --remove-orphans removes containers of
// services no longer in the compose file; it never touches volumes. run has no
// injection seam, so the argv is tested through this helper.
func upArgs() []string {
	return []string{"up", "-d", "--build", "--remove-orphans"}
}

func (Runner) Up(ctx context.Context, req composes.RunRequest) error {
	_, err := run(ctx, req, upArgs()...)
	return err
}

func (Runner) Down(ctx context.Context, req composes.RunRequest) error {
	_, err := run(ctx, req, "down")
	return err
}

func (Runner) DownWithVolumes(ctx context.Context, req composes.RunRequest) error {
	_, err := run(ctx, req, "down", "-v")
	return err
}

func (Runner) Restart(ctx context.Context, req composes.RunRequest) error {
	_, err := run(ctx, req, "restart")
	return err
}

func (Runner) Status(ctx context.Context, req composes.RunRequest) (composes.ComposeStatus, error) {
	output, err := run(ctx, req, "ps", "--all", "--format", "json")
	if err != nil {
		return composes.ComposeStatus{}, err
	}
	return parseStatus(output)
}

func (Runner) Logs(ctx context.Context, req composes.RunRequest) (string, error) {
	return run(ctx, req, "logs", "--no-color", "--tail=200")
}

func run(ctx context.Context, req composes.RunRequest, args ...string) (string, error) {
	fullArgs := append([]string{
		"compose",
		"-p", req.ProjectName,
		"-f", req.ComposeFilePath,
		"--env-file", req.EnvFilePath,
	}, args...)

	cmd := exec.CommandContext(ctx, "docker", fullArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", strings.Join(fullArgs, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// parseStatus reads `docker compose ps --format json`'s newline-delimited
// JSON output (one container object per line).
func parseStatus(output string) (composes.ComposeStatus, error) {
	var status composes.ComposeStatus
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var container struct {
			State string `json:"State"`
		}
		if err := json.Unmarshal([]byte(line), &container); err != nil {
			return composes.ComposeStatus{}, fmt.Errorf("parse docker compose ps output: %w", err)
		}
		status.Total++
		if container.State == "running" {
			status.Running++
		}
	}
	return status, nil
}
