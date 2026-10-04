package dockerexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"relist-panel/internal/volumes"
)

// VolumeResolver implements volumes.CLI: the Docker CLI answers which sources
// a Compose resolves to, whether a named volume exists, which volumes a
// project owns and removes one, all over argv, never a shell.
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
// pruning are all resolved by the CLI, the reference implementation. With
// ref.AllProfiles, `--profile '*'` is added before `config` so profile-gated
// Services resolve too. The
// CLI's stderr is folded into the error, which is how the operator sees its
// own `couldn't find env file` before the first deploy.
func (r *VolumeResolver) ComposeConfig(ctx context.Context, ref volumes.ComposeRef) ([]byte, error) {
	argv := []string{
		"docker",
		"compose",
		"-p", ref.ProjectName,
		"-f", ref.ComposeFilePath,
		"--env-file", ref.EnvFilePath,
	}
	if ref.AllProfiles {
		// Global flags: they must precede the config subcommand.
		argv = append(argv, "--profile", "*")
	}
	argv = append(argv, "config", "--format", "json")
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

// ListProjectVolumes lists the volumes labelled with project, in two calls:
//
//	docker volume ls -q --filter label=com.docker.compose.project=<project>
//	docker volume inspect <names…>
//
// The inspect JSON array supplies Name, CreatedAt and Labels. When ls finds
// nothing the inspect is skipped (it would fail with no names) and the result
// is an empty non-nil slice. Label filtering is only a narrowing here: the
// volumes package re-checks every label before treating a volume as an orphan.
func (r *VolumeResolver) ListProjectVolumes(ctx context.Context, project string) ([]volumes.ProjectVolume, error) {
	lsArgv := []string{"docker", "volume", "ls", "-q", "--filter", "label=" + volumes.ComposeProjectLabel + "=" + project}
	var lsOut bytes.Buffer
	if err := r.run(ctx, lsArgv, &lsOut); err != nil {
		return nil, err
	}
	names := strings.Fields(lsOut.String())
	if len(names) == 0 {
		return []volumes.ProjectVolume{}, nil
	}

	var inspectOut bytes.Buffer
	if err := r.run(ctx, append([]string{"docker", "volume", "inspect"}, names...), &inspectOut); err != nil {
		return nil, err
	}
	var inspected []struct {
		Name      string
		CreatedAt string
		Labels    map[string]string
	}
	if err := json.Unmarshal(inspectOut.Bytes(), &inspected); err != nil {
		return nil, fmt.Errorf("parse docker volume inspect output: %w", err)
	}
	listed := make([]volumes.ProjectVolume, 0, len(inspected))
	for _, volume := range inspected {
		listed = append(listed, volumes.ProjectVolume{Name: volume.Name, CreatedAt: volume.CreatedAt, Labels: volume.Labels})
	}
	return listed, nil
}

// RemoveVolume runs `docker volume rm <name>` — never `-f`, so Docker itself
// refuses a volume a container still uses. That refusal ("volume is in use")
// maps to volumes.ErrVolumeInUse wrapping Docker's message; any other failure
// is returned with stderr folded in.
func (r *VolumeResolver) RemoveVolume(ctx context.Context, name string) error {
	argv := []string{"docker", "volume", "rm", name}
	var stderr bytes.Buffer
	err := r.runDocker(ctx, argv, nil, io.Discard, &stderr)
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(stderr.String())
	if strings.Contains(strings.ToLower(message), "volume is in use") {
		return fmt.Errorf("%w: %s", volumes.ErrVolumeInUse, message)
	}
	return fmt.Errorf("docker %s: %w: %s", strings.Join(argv, " "), err, message)
}

// run executes argv, capturing stdout into stdout and folding stderr into the
// error.
func (r *VolumeResolver) run(ctx context.Context, argv []string, stdout *bytes.Buffer) error {
	var stderr bytes.Buffer
	if err := r.runDocker(ctx, argv, nil, stdout, &stderr); err != nil {
		return fmt.Errorf("docker %s: %w: %s", strings.Join(argv, " "), err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

var _ volumes.CLI = (*VolumeResolver)(nil)
