package dockerexec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"relist-panel/internal/accesslog"
)

// TraefikDocker reaches the install's Traefik for the Traefik page and the
// Access log rotation (ADR-0026).
type TraefikDocker struct {
	runDocker func(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) error
	hostname  func() string
}

func NewTraefikDocker() *TraefikDocker {
	return &TraefikDocker{
		runDocker: execDocker,
		hostname:  func() string { h, _ := os.Hostname(); return h },
	}
}

var _ accesslog.Traefik = (*TraefikDocker)(nil)

func (d *TraefikDocker) docker(ctx context.Context, args ...string) (string, error) {
	argv := append([]string{"docker"}, args...)
	var out, errOut bytes.Buffer
	if err := d.runDocker(ctx, argv, nil, &out, &errOut); err != nil {
		return "", fmt.Errorf("%s: %w: %s", strings.Join(argv, " "), err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
}

func (d *TraefikDocker) id(ctx context.Context) (string, error) {
	return installTraefik(ctx, d.docker, d.hostname())
}

func (d *TraefikDocker) Reopen(ctx context.Context) error {
	id, err := d.id(ctx)
	if err != nil {
		return err
	}
	_, err = d.docker(ctx, "kill", "--signal=USR1", id)
	return err
}

// Logs returns the last tail lines of Traefik's own log. Docker keeps stdout
// and stderr apart, so both go to one buffer, interleaved as written.
func (d *TraefikDocker) Logs(ctx context.Context, tail int) (string, error) {
	id, err := d.id(ctx)
	if err != nil {
		return "", err
	}
	argv := []string{"docker", "logs", "--tail", strconv.Itoa(tail), "--timestamps", id}
	var out bytes.Buffer
	if err := d.runDocker(ctx, argv, nil, &out, &out); err != nil {
		return "", fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	return out.String(), nil
}

// Args returns the arguments Traefik was started with: its static config.
func (d *TraefikDocker) Args(ctx context.Context) ([]string, error) {
	id, err := d.id(ctx)
	if err != nil {
		return nil, err
	}
	out, err := d.docker(ctx, "inspect", "--format", "{{range .Args}}{{println .}}{{end}}", id)
	if err != nil {
		return nil, err
	}
	var args []string
	for _, line := range strings.Split(out, "\n") {
		if line != "" {
			args = append(args, line)
		}
	}
	return args, nil
}

// installTraefik finds the install stack's Traefik: the container of service
// "traefik" in the panel container's own Compose project. Every way of not
// finding it wraps accesslog.ErrNoTraefik.
func installTraefik(ctx context.Context, docker func(ctx context.Context, args ...string) (string, error), hostname string) (string, error) {
	out, err := docker(ctx, "inspect", "--format", `{{index .Config.Labels "com.docker.compose.project"}}`, hostname)
	if err != nil {
		return "", fmt.Errorf("find the install's Compose project: %w: %w", accesslog.ErrNoTraefik, err)
	}
	project := strings.TrimSpace(out)
	if project == "" {
		return "", fmt.Errorf("the panel container has no Compose project label, so the install's Traefik cannot be found: %w", accesslog.ErrNoTraefik)
	}
	out, err = docker(ctx, "ps", "-aq", "--filter", "label=com.docker.compose.project="+project, "--filter", "label=com.docker.compose.service=traefik")
	if err != nil {
		return "", err
	}
	ids := strings.Fields(out)
	if len(ids) == 0 {
		return "", fmt.Errorf("no traefik service in the install project %q: %w", project, accesslog.ErrNoTraefik)
	}
	return ids[0], nil
}
