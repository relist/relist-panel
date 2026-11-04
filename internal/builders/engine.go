package builders

import (
	"context"
	"regexp"
	"strconv"
)

// DefaultBuildKitImage is the BuildKit image Builders run while the operator
// has not chosen one. A release that bumps it moves every Builder to it on the
// Builder's next use.
const DefaultBuildKitImage = "moby/buildkit:v0.34.2@sha256:c5f8862f52499773af85bc07695b2379ef08e8002352b0df2c4864c91d78dabe"

// cpuPeriod is the CFS period a Builder's CPU quota is measured against.
const cpuPeriod = 100000

var imageRefPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/:-]*(:[A-Za-z0-9._-]+)?@sha256:[0-9a-f]{64}$`)

// ValidImageRef reports whether ref names an image by digest, the only form a
// Builder runs.
func ValidImageRef(ref string) bool {
	return imageRefPattern.MatchString(ref)
}

// EffectiveImage is the BuildKit image to run for the setting (empty = default).
func EffectiveImage(setting string) string {
	if setting == "" {
		return DefaultBuildKitImage
	}
	return setting
}

// HostSpec is a Builder as the host sees it: the buildx builder name, the
// BuildKit image and the limits on its container.
type HostSpec struct {
	Name, Image                      string
	CPUQuota, CPUPeriod, MemoryBytes int64
	MaxParallelism                   int // 0 = BuildKit's default
}

// HostName is the buildx builder name of Builder id. It never changes on a
// rename, so the container and its cache stay found.
func HostName(id int64) string {
	return "relist-builder-" + strconv.FormatInt(id, 10)
}

func SpecFor(b Builder, image string) HostSpec {
	return HostSpec{
		Name:           HostName(b.ID),
		Image:          image,
		CPUQuota:       int64(b.CPUsMilli) * cpuPeriod / 1000,
		CPUPeriod:      cpuPeriod,
		MemoryBytes:    int64(b.MemoryMB) << 20,
		MaxParallelism: b.MaxParallelism,
	}
}

type ContainerState string

const (
	StateMissing ContainerState = "missing"
	StateRunning ContainerState = "running"
	StateStopped ContainerState = "stopped"
)

// Inspection is a Builder container's state and image (Config.Image), which is
// empty when the container is missing.
type Inspection struct {
	State ContainerState
	Image string
}

// Engine is the host side of Builders, implemented in dockerexec with the
// buildx CLI and the Engine API.
type Engine interface {
	// Ensure creates or attaches to the builder, starts it and brings its
	// container's limits to spec. It never changes the image of an existing
	// container; Recreate does.
	Ensure(ctx context.Context, spec HostSpec) error
	// Recreate removes the builder but keeps its cache, then Ensures it.
	Recreate(ctx context.Context, spec HostSpec) error
	// Remove deletes the builder and its cache; a missing one is not an error.
	Remove(ctx context.Context, name string) error
	Inspect(ctx context.Context, name string) (Inspection, error)
	Pull(ctx context.Context, ref string) error
	// DiskUsage is the size of the builder's state volume, 0 when it has none.
	DiskUsage(ctx context.Context, name string) (int64, error)
	Prune(ctx context.Context, name string) error
	Capacity(ctx context.Context) (cpus int, memoryBytes int64, err error)
	ImageExists(ctx context.Context, ref string) (bool, error)
}
