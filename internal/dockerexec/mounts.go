package dockerexec

import (
	"bytes"
	"context"
	"fmt"
	"strings"
)

// MountedSources lists the host source of every mount of the Compose project's
// containers, stopped ones included: a stopped container still references its
// bind paths and can be started again.
func (Runner) MountedSources(ctx context.Context, projectName string) ([]string, error) {
	ids, err := dockerOutput(ctx, "ps", "-aq", "--filter", "label="+composeProjectLabel+"="+projectName)
	if err != nil {
		return nil, err
	}
	containers := strings.Fields(ids)
	if len(containers) == 0 {
		return nil, nil
	}
	args := append([]string{"inspect", "--format", `{{range .Mounts}}{{.Source}}{{"\n"}}{{end}}`}, containers...)
	out, err := dockerOutput(ctx, args...)
	if err != nil {
		return nil, err
	}
	var sources []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			sources = append(sources, line)
		}
	}
	return sources, nil
}

// dockerOutput runs a plain docker command and returns its stdout.
func dockerOutput(ctx context.Context, args ...string) (string, error) {
	cmd := command(ctx, args...)
	cmd.WaitDelay = waitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("docker %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
