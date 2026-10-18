package migration

import (
	"context"
	"io"

	"relist-panel/internal/composes"
)

// VolumeInfo is a named Docker volume as a Migration recreates it: same name,
// driver, labels and options.
type VolumeInfo struct {
	Name    string            `json:"name"`
	Driver  string            `json:"driver"`
	Labels  map[string]string `json:"labels"`
	Options map[string]string `json:"options"`
}

// ImageInfo is one Image, keyed by its ID.
type ImageInfo struct {
	ID          string   `json:"id"`
	Tags        []string `json:"tags"`
	RepoDigests []string `json:"repoDigests"`
	Size        int64    `json:"size"`
}

// Docker is every Docker operation a Migration needs, on either host. It is
// implemented for real in dockerexec and faked in tests.
type Docker interface {
	ComposeStop(ctx context.Context, req composes.RunRequest) error
	ComposeStart(ctx context.Context, req composes.RunRequest) error
	// ProjectContainerImages lists the image IDs of a Compose project's
	// Containers, in any state.
	ProjectContainerImages(ctx context.Context, project string) ([]string, error)
	InspectVolume(ctx context.Context, name string) (VolumeInfo, bool, error)
	CreateVolume(ctx context.Context, v VolumeInfo) error
	RemoveVolume(ctx context.Context, name string) error
	InspectImage(ctx context.Context, ref string) (ImageInfo, bool, error)
	PullImage(ctx context.Context, ref string) error
	TagImage(ctx context.Context, source, target string) error
	SaveImages(ctx context.Context, refs []string, w io.Writer) error
	// LoadImages returns what `docker load` reports it loaded: tags, or
	// image IDs for an untagged Image.
	LoadImages(ctx context.Context, r io.Reader) ([]string, error)
	RemoveImage(ctx context.Context, ref string) error
	// Helper runs out of the panel's image. mountSource is a volume name or
	// an absolute host path, mounted at /data; Pack, Size and Free mount it
	// read-only, and name is relative to it.
	Pack(ctx context.Context, mountSource, name string, w io.Writer) error
	Unpack(ctx context.Context, mountSource string, r io.Reader) error
	Delete(ctx context.Context, mountSource, name string) error
	Size(ctx context.Context, mountSource, name string) (int64, error)
	Free(ctx context.Context, mountSource string) (int64, error)
	DockerRootDir(ctx context.Context) (string, error)
	// RunMaintenance replaces any running maintenance container with one
	// carrying labels; RemoveMaintenance is no error when there is none.
	RunMaintenance(ctx context.Context, labels map[string]string) error
	RemoveMaintenance(ctx context.Context) error
	MaintenanceRunning(ctx context.Context) (bool, error)
	StopTraefik(ctx context.Context) error
	StartTraefik(ctx context.Context) error
}
