package registry

import "context"

// DefaultImage is the registry the panel runs: Distribution 3.1.2, pinned by
// its index digest (probed 2026-11-06). It is the fourth image the panel
// pulls itself (ADR-0033).
const DefaultImage = "registry:3@sha256:38907510915c3f1604c7e83d6ab602ab3020d092f73bdaed8696773693ff768c"

// Host names on the Docker host.
const (
	ContainerName   = "relist-registry"
	GCContainerName = "relist-registry-gc"
	VolumeName      = "relist-registry"
	HashLabel       = "relist.registry.hash"
	Network         = "relist-network"
	Port            = 5000
)

// ContainerSpec is what the registry container is created from.
type ContainerSpec struct {
	Image    string
	Hostname string
	TLS      string // TLSLetsEncrypt or TLSCertificate
	Hash     string // the relist.registry.hash label
	FilesDir string // <data dir>/.registry, mounted at /etc/distribution
}

// ContainerState is the registry container as Docker reports it.
type ContainerState struct {
	Exists, Running bool
	Image           string // the reference it was created from
	Hash            string // its relist.registry.hash label
}

// GCState is the garbage-collection container as Docker reports it.
type GCState struct {
	Exists, Running bool
	ExitCode        int
	Logs            string
}

// Host drives the registry's containers and volume on the Docker host.
type Host interface {
	EnsureImage(ctx context.Context, ref string) error
	Inspect(ctx context.Context) (ContainerState, error)
	// Create removes any relist-registry container, then creates and starts
	// one from spec.
	Create(ctx context.Context, spec ContainerSpec) error
	Restart(ctx context.Context) error
	Remove(ctx context.Context) error
	RemoveVolume(ctx context.Context) error
	VolumeBytes(ctx context.Context) (int64, error)
	// RunGC starts relist-registry-gc detached, so it outlives the panel.
	RunGC(ctx context.Context, image, filesDir string) error
	GCState(ctx context.Context) (GCState, error)
	RemoveGC(ctx context.Context) error
}
