package dockerexec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"relist-panel/internal/volumes"
)

// VolumeResolver implements volumes.CLI: the Docker CLI answers which sources
// a Compose resolves to and whether a named volume exists, both over argv,
// never a shell.
type VolumeResolver struct {
	runDocker func(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) error
}

// NewVolumeResolver constructs the real resolver, executing through the
// docker CLI.
func NewVolumeResolver() *VolumeResolver {
	return &VolumeResolver{runDocker: execDocker}
}

// ComposeConfig shells the spec's resolution call exactly:
//
//	docker compose -p <project> -f <file> --env-file <env> config --format json
//
// so named volumes, relative and absolute binds, `${VAR}` interpolation and
// pruning are all resolved by the CLI, the reference implementation. The
// CLI's stderr is folded into the error, which is how the operator sees its
// own `couldn't find env file` before the first deploy.
func (r *VolumeResolver) ComposeConfig(ctx context.Context, ref volumes.ComposeRef) ([]byte, error) {
	argv := []string{
		"docker",
		"compose",
		"-p", ref.ProjectName,
		"-f", ref.ComposeFilePath,
		"--env-file", ref.EnvFilePath,
		"config",
		"--format", "json",
	}
	var stdout, stderr bytes.Buffer
	if err := r.runDocker(ctx, argv, nil, &stdout, &stderr); err != nil {
		return nil, fmt.Errorf("docker %s: %w: %s", strings.Join(argv, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// VolumeExists reduces `docker volume inspect <name>` to a boolean. Docker
// reports a volume that does not exist as an error carrying "no such volume";
// that one maps to false, while every other failure stays an error so a
// daemon that is down is never read as "the volume is gone".
func (r *VolumeResolver) VolumeExists(ctx context.Context, name string) (bool, error) {
	argv := []string{"docker", "volume", "inspect", name}
	var stdout, stderr bytes.Buffer
	err := r.runDocker(ctx, argv, nil, &stdout, &stderr)
	if err == nil {
		return true, nil
	}
	if strings.Contains(strings.ToLower(stderr.String()), "no such volume") {
		return false, nil
	}
	return false, fmt.Errorf("docker %s: %w: %s", strings.Join(argv, " "), err, strings.TrimSpace(stderr.String()))
}

var _ volumes.CLI = (*VolumeResolver)(nil)
