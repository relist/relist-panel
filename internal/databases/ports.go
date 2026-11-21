package databases

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// Network is the shared network every Database sits on (ADR-0014).
const Network = "relist-network"

// Labels on a Database's container and volume.
const (
	IDLabel   = "relist.database.id"
	HashLabel = "relist.database.hash"
)

const aliasSuffix = ".db.internal"

// ContainerName is the stable host name of a Database; it never changes on
// rename.
func ContainerName(id int64) string { return fmt.Sprintf("relist-db-%d", id) }

// VolumeName is the Docker volume holding a Database's data.
func VolumeName(id int64) string { return fmt.Sprintf("relist-db-%d", id) }

// Alias is the readable name on relist-network, "" when the slug is the
// fallback "x" or too long for a DNS label.
func Alias(slug string) string {
	if slug == "" || slug == "x" || len(slug) > 63 || strings.ContainsAny(slug, ". ") {
		return ""
	}
	return slug + aliasSuffix
}

// ContainerSpec is what a Database container is created from.
type ContainerSpec struct {
	Name, Volume, Image, DataPath string
	Env, Cmd, Healthcheck         []string
	Alias                         string // "" = none
	MemoryBytes, NanoCPUs         int64
	Labels                        map[string]string
}

// ContainerState is a Database container as Docker reports it.
type ContainerState struct {
	Exists, Running bool
	Health          string // "", "starting", "healthy", "unhealthy"
	Hash            string
	ImageID         string
	Aliases         []string // on relist-network
	// OnNetwork: connected to relist-network. A version change disconnects
	// a Postgres container to cut its clients off before the dumps.
	OnNetwork bool
	// Mounts: the named volume at each mount path inside the container.
	Mounts map[string]string
}

// ExecRequest is one non-interactive command run inside a Database's
// container. Passwords go in Env, never in Cmd.
type ExecRequest struct {
	Cmd, Env       []string
	Stdin          io.Reader // nil = no stdin
	Stdout, Stderr io.Writer // nil = discard
}

// Host drives Database containers and volumes on the Docker host.
type Host interface {
	// EnsureImage pulls ref when it is absent.
	EnsureImage(ctx context.Context, ref string) error
	// PullImage always pulls ref and returns its image ID.
	PullImage(ctx context.Context, ref string) (imageID string, err error)
	ImageID(ctx context.Context, ref string) (string, error)
	Inspect(ctx context.Context, name string) (ContainerState, error)
	VolumeExists(ctx context.Context, name string) (bool, error)
	// Create removes any old container of that name, creates the volume when
	// absent, then creates and starts the container.
	Create(ctx context.Context, spec ContainerSpec) error
	Start(ctx context.Context, name string) error
	Stop(ctx context.Context, name string) error
	Restart(ctx context.Context, name string) error
	// Realias disconnects the container from relist-network and connects it
	// again with alias ("" = none), without restarting it.
	Realias(ctx context.Context, name, alias string) error
	// Disconnect takes the container off relist-network, which ends every
	// client connection; docker exec keeps working. Not connected and not
	// found are nil.
	Disconnect(ctx context.Context, name string) error
	// Remove deletes the container only; not found is nil.
	Remove(ctx context.Context, name string) error
	// RemoveVolume deletes the volume; not found is nil.
	RemoveVolume(ctx context.Context, name string) error
	Logs(ctx context.Context, name string, tail int) (string, error)
	// Exec runs req.Cmd in the container, streaming stdin and output, and
	// returns the command's exit code. A write error on Stdout or Stderr is
	// returned as the error.
	Exec(ctx context.Context, name string, req ExecRequest) (exitCode int, err error)
}

// OwnerName is who the Docker page shows as the owner of a Database's volume
// and image, so cleanup never offers them.
const OwnerName = "Databases"

// targetVolume is the volume a version change moves a Database's data to.
// The change's ID makes the name unique to that one attempt, so a volume a
// failed or interrupted attempt left behind is never live data (ADR-0047).
func targetVolume(id, changeID int64) string {
	return fmt.Sprintf("relist-db-%d-v%d", id, changeID)
}
