package dockerexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"relist-panel/internal/accesslog"
	"relist-panel/internal/traefikconfig"
)

// TraefikDocker reaches the install's Traefik for the Traefik page and the
// Access log rotation (ADR-0027).
type TraefikDocker struct {
	runDocker func(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) error
	hostname  func() string
	client    *http.Client // nil means the health client of healthzThroughTraefik
}

func NewTraefikDocker() *TraefikDocker {
	return &TraefikDocker{
		runDocker: execDocker,
		hostname:  func() string { h, _ := os.Hostname(); return h },
	}
}

var (
	_ accesslog.Traefik     = (*TraefikDocker)(nil)
	_ traefikconfig.Traefik = (*TraefikDocker)(nil)
)

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

// State reads the Traefik Container's run state, its image and its address
// on relist-network, for a Traefik apply's health watch (ADR-0030).
func (d *TraefikDocker) State(ctx context.Context) (traefikconfig.State, error) {
	id, err := d.id(ctx)
	if err != nil {
		return traefikconfig.State{}, err
	}
	out, err := d.docker(ctx, "inspect", "--format", "{{json .}}", id)
	if err != nil {
		return traefikconfig.State{}, err
	}
	var c struct {
		Image        string
		RestartCount int
		State        struct {
			Running   bool
			StartedAt time.Time
		}
		NetworkSettings struct {
			Networks map[string]struct{ IPAddress string }
		}
	}
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		return traefikconfig.State{}, fmt.Errorf("read docker inspect of %s: %w", id, err)
	}
	return traefikconfig.State{
		Running: c.State.Running, RestartCount: c.RestartCount, StartedAt: c.State.StartedAt.UTC(),
		Image: c.Image, IP: c.NetworkSettings.Networks["relist-network"].IPAddress,
	}, nil
}

// Restart restarts Traefik, giving it 10 s to finish open requests.
func (d *TraefikDocker) Restart(ctx context.Context) error {
	id, err := d.id(ctx)
	if err != nil {
		return err
	}
	_, err = d.docker(ctx, "restart", "-t", "10", id)
	return err
}

// LogsSince is Traefik's own log from since, stdout and stderr together.
func (d *TraefikDocker) LogsSince(ctx context.Context, since time.Time, tail int) (string, error) {
	id, err := d.id(ctx)
	if err != nil {
		return "", err
	}
	argv := []string{"docker", "logs", "--since", since.UTC().Format(time.RFC3339Nano), "--tail", strconv.Itoa(tail), "--timestamps", id}
	var out bytes.Buffer
	if err := d.runDocker(ctx, argv, nil, &out, &out); err != nil {
		return "", fmt.Errorf("%s: %w", strings.Join(argv, " "), err)
	}
	return out.String(), nil
}

// dryRunPrefix names the throwaway Containers of a Traefik apply's dry-run.
const dryRunPrefix = "relist-traefik-dryrun-"

// DryRun starts Traefik's image on a candidate config with no network, no
// Docker socket and an empty /letsencrypt, so it binds nothing on the host and
// asks Let's Encrypt for nothing, then reports whether it is still running
// after wait. The Container is always removed.
func (d *TraefikDocker) DryRun(ctx context.Context, id, dir, image string, wait time.Duration) (traefikconfig.DryRunResult, error) {
	name := dryRunPrefix + id
	defer func() {
		_, _ = d.docker(context.WithoutCancel(ctx), "rm", "-f", name)
	}()
	if _, err := d.docker(ctx, "run", "-d", "--name", name, "--network", "none", "--tmpfs", "/letsencrypt",
		"-v", dir+":/etc/traefik:ro", image); err != nil {
		return traefikconfig.DryRunResult{}, err
	}
	select {
	case <-time.After(wait):
	case <-ctx.Done():
		return traefikconfig.DryRunResult{}, ctx.Err()
	}
	out, err := d.docker(ctx, "inspect", "--format", "{{.State.Running}}", name)
	if err != nil {
		return traefikconfig.DryRunResult{}, err
	}
	var log bytes.Buffer
	if err := d.runDocker(ctx, []string{"docker", "logs", name}, nil, &log, &log); err != nil {
		return traefikconfig.DryRunResult{}, fmt.Errorf("docker logs %s: %w", name, err)
	}
	return traefikconfig.DryRunResult{Running: strings.TrimSpace(out) == "true", Log: log.String()}, nil
}

// RemoveDryRuns removes dry-run Containers a panel that died mid-apply left.
func (d *TraefikDocker) RemoveDryRuns(ctx context.Context) error {
	out, err := d.docker(ctx, "ps", "-aq", "--filter", "name="+dryRunPrefix)
	if err != nil {
		return err
	}
	ids := strings.Fields(out)
	if len(ids) == 0 {
		return nil
	}
	_, err = d.docker(ctx, append([]string{"rm", "-f"}, ids...)...)
	return err
}

// Healthz asks Traefik at ip for the panel's /healthz, as the Updater does.
func (d *TraefikDocker) Healthz(ctx context.Context, ip, domain string) error {
	return healthzThroughTraefik(ctx, d.client, ip, domain)
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
