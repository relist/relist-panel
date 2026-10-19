package dockerexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"relist-panel/internal/composes"
	"relist-panel/internal/dockernet"
	"relist-panel/internal/migration"
)

const (
	maintenanceName  = "relist-maintenance"
	maintenanceLabel = "relist.maintenance=1"
)

// MigrationDocker implements migration.Docker with the docker CLI over the
// mounted socket (ADR-0018). Every call is one argv, never a shell.
type MigrationDocker struct {
	runDocker    func(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) error
	resolveImage func(ctx context.Context) (string, error)
	hostname     func() string
}

// NewMigrationDocker constructs the real adapter.
func NewMigrationDocker() *MigrationDocker {
	return &MigrationDocker{
		runDocker:    execDocker,
		resolveImage: panelImage,
		hostname:     func() string { h, _ := os.Hostname(); return h },
	}
}

var _ migration.Docker = (*MigrationDocker)(nil)

// docker runs argv (without the leading "docker") and returns its stdout. A
// failure carries the command and its stderr.
func (d *MigrationDocker) docker(ctx context.Context, stdin io.Reader, stdout io.Writer, args ...string) (string, error) {
	argv := append([]string{"docker"}, args...)
	var out, errOut bytes.Buffer
	if stdout == nil {
		stdout = &out
	}
	if err := d.runDocker(ctx, argv, stdin, stdout, &errOut); err != nil {
		return "", fmt.Errorf("%s: %w: %s", strings.Join(argv, " "), err, strings.TrimSpace(errOut.String()))
	}
	return out.String(), nil
}

func isNotFound(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such volume") || strings.Contains(msg, "no such image") ||
		strings.Contains(msg, "no such container") || strings.Contains(msg, "no such object")
}

func composeArgs(req composes.RunRequest, args ...string) []string {
	return append([]string{"compose", "-p", req.ProjectName, "-f", req.ComposeFilePath, "--env-file", req.EnvFilePath}, args...)
}

func (d *MigrationDocker) ComposeStop(ctx context.Context, req composes.RunRequest) error {
	_, err := d.docker(ctx, nil, nil, composeArgs(req, "stop")...)
	return err
}

func (d *MigrationDocker) ComposeStart(ctx context.Context, req composes.RunRequest) error {
	_, err := d.docker(ctx, nil, nil, composeArgs(req, "start")...)
	return err
}

func (d *MigrationDocker) ProjectContainerImages(ctx context.Context, project string) ([]string, error) {
	out, err := d.docker(ctx, nil, nil, "ps", "-aq", "--filter", "label=com.docker.compose.project="+project)
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(out)
	if len(ids) == 0 {
		return nil, nil
	}
	out, err = d.docker(ctx, nil, nil, append([]string{"container", "inspect", "--format", "{{.Image}}"}, ids...)...)
	if err != nil {
		return nil, err
	}
	var images []string
	for _, id := range strings.Fields(out) {
		if !slices.Contains(images, id) {
			images = append(images, id)
		}
	}
	return images, nil
}

func (d *MigrationDocker) InspectVolume(ctx context.Context, name string) (migration.VolumeInfo, bool, error) {
	out, err := d.docker(ctx, nil, nil, "volume", "inspect", "--format", "{{json .}}", "--", name)
	if err != nil {
		if isNotFound(err) {
			return migration.VolumeInfo{}, false, nil
		}
		return migration.VolumeInfo{}, false, err
	}
	var v struct {
		Name    string            `json:"Name"`
		Driver  string            `json:"Driver"`
		Labels  map[string]string `json:"Labels"`
		Options map[string]string `json:"Options"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return migration.VolumeInfo{}, false, fmt.Errorf("parse volume inspect: %w", err)
	}
	return migration.VolumeInfo{Name: v.Name, Driver: v.Driver, Labels: v.Labels, Options: v.Options}, true, nil
}

func (d *MigrationDocker) CreateVolume(ctx context.Context, v migration.VolumeInfo) error {
	args := []string{"volume", "create"}
	if v.Driver != "" {
		args = append(args, "--driver", v.Driver)
	}
	for _, k := range sortedKeys(v.Labels) {
		args = append(args, "--label", k+"="+v.Labels[k])
	}
	for _, k := range sortedKeys(v.Options) {
		args = append(args, "--opt", k+"="+v.Options[k])
	}
	_, err := d.docker(ctx, nil, nil, append(args, "--", v.Name)...)
	return err
}

func (d *MigrationDocker) RemoveVolume(ctx context.Context, name string) error {
	_, err := d.docker(ctx, nil, nil, "volume", "rm", "--", name)
	if err != nil && isNotFound(err) {
		return nil
	}
	return err
}

func (d *MigrationDocker) InspectImage(ctx context.Context, ref string) (migration.ImageInfo, bool, error) {
	out, err := d.docker(ctx, nil, nil, "image", "inspect", "--format", "{{json .}}", ref)
	if err != nil {
		if isNotFound(err) {
			return migration.ImageInfo{}, false, nil
		}
		return migration.ImageInfo{}, false, err
	}
	var img struct {
		ID          string   `json:"Id"`
		RepoTags    []string `json:"RepoTags"`
		RepoDigests []string `json:"RepoDigests"`
		Size        int64    `json:"Size"`
	}
	if err := json.Unmarshal([]byte(out), &img); err != nil {
		return migration.ImageInfo{}, false, fmt.Errorf("parse image inspect: %w", err)
	}
	return migration.ImageInfo{ID: img.ID, Tags: nonNil(img.RepoTags), RepoDigests: nonNil(img.RepoDigests), Size: img.Size}, true, nil
}

func (d *MigrationDocker) PullImage(ctx context.Context, ref string) error {
	_, err := d.docker(ctx, nil, nil, "pull", "--quiet", ref)
	return err
}

func (d *MigrationDocker) TagImage(ctx context.Context, source, target string) error {
	_, err := d.docker(ctx, nil, nil, "tag", source, target)
	return err
}

func (d *MigrationDocker) SaveImages(ctx context.Context, refs []string, w io.Writer) error {
	_, err := d.docker(ctx, nil, w, append([]string{"image", "save"}, refs...)...)
	return err
}

func (d *MigrationDocker) LoadImages(ctx context.Context, r io.Reader) ([]string, error) {
	out, err := d.docker(ctx, r, nil, "image", "load", "--quiet")
	if err != nil {
		return nil, err
	}
	return parseLoaded(out), nil
}

// parseLoaded reads `docker load`'s "Loaded image: <tag>" and
// "Loaded image ID: <id>" lines.
func parseLoaded(out string) []string {
	var loaded []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if ref, ok := strings.CutPrefix(line, "Loaded image ID: "); ok {
			loaded = append(loaded, ref)
		} else if ref, ok := strings.CutPrefix(line, "Loaded image: "); ok {
			loaded = append(loaded, ref)
		}
	}
	return loaded
}

func (d *MigrationDocker) RemoveImage(ctx context.Context, ref string) error {
	_, err := d.docker(ctx, nil, nil, "image", "rm", ref)
	if err != nil && isNotFound(err) {
		return nil
	}
	return err
}

// helper runs the Volume helper as root with only the capabilities op needs:
// reading every file for pack, size and free; setting owners and modes for
// unpack and delete.
func (d *MigrationDocker) helper(ctx context.Context, mountSource, op, name string, stdin io.Reader, stdout io.Writer) (string, error) {
	image, err := d.resolveImage(ctx)
	if err != nil {
		return "", err
	}
	write := op == "unpack" || op == "delete"
	// --log-driver none: a 50 GB pack must not also land in the container log.
	args := []string{"run", "--rm", "-i", "--log-driver", "none", "--network", "none", "--user", "0:0", "--read-only", "--tmpfs", "/tmp", "--cap-drop", "ALL"}
	mount := mountSource + ":/data"
	if write {
		args = append(args, "--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "--cap-add", "FOWNER")
	} else {
		args = append(args, "--cap-add", "DAC_READ_SEARCH")
		mount += ":ro"
	}
	args = append(args, "--security-opt", "no-new-privileges", "-v", mount, "--entrypoint", helperEntrypoint, image, op, "--", name)
	return d.docker(ctx, stdin, stdout, args...)
}

func (d *MigrationDocker) Pack(ctx context.Context, mountSource, name string, w io.Writer) error {
	_, err := d.helper(ctx, mountSource, "pack", name, nil, w)
	return err
}

func (d *MigrationDocker) Unpack(ctx context.Context, mountSource, name string, r io.Reader) error {
	_, err := d.helper(ctx, mountSource, "unpack", name, r, nil)
	return err
}

func (d *MigrationDocker) Delete(ctx context.Context, mountSource, name string) error {
	_, err := d.helper(ctx, mountSource, "delete", name, nil, nil)
	return err
}

func (d *MigrationDocker) bytesOf(ctx context.Context, mountSource, op, name string) (int64, error) {
	out, err := d.helper(ctx, mountSource, op, name, nil, nil)
	if err != nil {
		return 0, err
	}
	var answer struct {
		Bytes int64 `json:"bytes"`
	}
	if err := json.Unmarshal([]byte(out), &answer); err != nil {
		return 0, fmt.Errorf("parse helper %s: %w", op, err)
	}
	return answer.Bytes, nil
}

func (d *MigrationDocker) Size(ctx context.Context, mountSource, name string) (int64, error) {
	return d.bytesOf(ctx, mountSource, "size", name)
}

func (d *MigrationDocker) Probe(ctx context.Context, mountSource, name string) (migration.PathState, error) {
	out, err := d.helper(ctx, mountSource, "probe", name, nil, nil)
	if err != nil {
		return migration.PathMissing, err
	}
	var answer struct {
		Exists bool `json:"exists"`
		Empty  bool `json:"empty"`
	}
	if err := json.Unmarshal([]byte(out), &answer); err != nil {
		return migration.PathMissing, fmt.Errorf("parse helper probe: %w", err)
	}
	switch {
	case !answer.Exists:
		return migration.PathMissing, nil
	case answer.Empty:
		return migration.PathEmpty, nil
	}
	return migration.PathOccupied, nil
}

func (d *MigrationDocker) Free(ctx context.Context, mountSource string) (int64, error) {
	return d.bytesOf(ctx, mountSource, "free", ".")
}

func (d *MigrationDocker) DockerRootDir(ctx context.Context) (string, error) {
	out, err := d.docker(ctx, nil, nil, "info", "--format", "{{.DockerRootDir}}")
	return strings.TrimSpace(out), err
}

func (d *MigrationDocker) RunMaintenance(ctx context.Context, labels map[string]string) error {
	if err := d.RemoveMaintenance(ctx); err != nil {
		return err
	}
	image, err := d.resolveImage(ctx)
	if err != nil {
		return err
	}
	args := []string{"run", "-d", "--name", maintenanceName, "--restart", "unless-stopped",
		"--network", dockernet.NetworkName, "--label", maintenanceLabel}
	for _, k := range sortedKeys(labels) {
		args = append(args, "--label", k+"="+labels[k])
	}
	_, err = d.docker(ctx, nil, nil, append(args, "--entrypoint", "relist-panel", image, "maintenance-page")...)
	return err
}

func (d *MigrationDocker) RemoveMaintenance(ctx context.Context) error {
	_, err := d.docker(ctx, nil, nil, "rm", "-f", maintenanceName)
	if err != nil && isNotFound(err) {
		return nil
	}
	return err
}

func (d *MigrationDocker) MaintenanceRunning(ctx context.Context) (bool, error) {
	out, err := d.docker(ctx, nil, nil, "ps", "-q", "--filter", "label="+maintenanceLabel, "--filter", "status=running")
	return strings.TrimSpace(out) != "", err
}

// traefik finds the install stack's Traefik: the container of service
// "traefik" in the panel container's own Compose project.
func (d *MigrationDocker) traefik(ctx context.Context) (string, error) {
	out, err := d.docker(ctx, nil, nil, "inspect", "--format", `{{index .Config.Labels "com.docker.compose.project"}}`, d.hostname())
	if err != nil {
		return "", fmt.Errorf("find the install's Compose project: %w", err)
	}
	project := strings.TrimSpace(out)
	if project == "" {
		return "", errors.New("the panel container has no Compose project label, so the install's Traefik cannot be found")
	}
	out, err = d.docker(ctx, nil, nil, "ps", "-aq", "--filter", "label=com.docker.compose.project="+project, "--filter", "label=com.docker.compose.service=traefik")
	if err != nil {
		return "", err
	}
	ids := strings.Fields(out)
	if len(ids) == 0 {
		return "", fmt.Errorf("no traefik service in the install project %q", project)
	}
	return ids[0], nil
}

func (d *MigrationDocker) StopTraefik(ctx context.Context) error {
	id, err := d.traefik(ctx)
	if err != nil {
		return err
	}
	_, err = d.docker(ctx, nil, nil, "stop", id)
	return err
}

func (d *MigrationDocker) StartTraefik(ctx context.Context) error {
	id, err := d.traefik(ctx)
	if err != nil {
		return err
	}
	_, err = d.docker(ctx, nil, nil, "start", id)
	return err
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
