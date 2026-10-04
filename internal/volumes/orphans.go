package volumes

import (
	"context"
	"errors"
	"sort"
)

const (
	// ComposeProjectLabel is the label Compose stamps on every volume it
	// creates, holding the project name.
	ComposeProjectLabel = "com.docker.compose.project"
	// ComposeVolumeLabel is the label Compose stamps on a volume it creates,
	// holding the volume's logical name — its key in the top-level `volumes:`
	// map.
	ComposeVolumeLabel = "com.docker.compose.volume"
)

// ErrNotOrphan is returned by RemoveOrphan for a name that is not, right now,
// one of the Compose's orphan volumes: the one guard between a caller-supplied
// name and `docker volume rm`.
var ErrNotOrphan = errors.New("volume is not an orphan of this compose")

// ErrVolumeInUse is returned when Docker refuses to remove a volume because a
// container still uses it. The panel never forces the removal.
var ErrVolumeInUse = errors.New("volume is in use by a container")

// ProjectVolume is one Docker volume as the CLI lists it: its real name, when
// it was created (as Docker reports it) and its labels.
type ProjectVolume struct {
	Name      string
	CreatedAt string
	Labels    map[string]string
}

// ListOrphans returns the Docker volumes Compose created for ref's project
// that the current Compose file no longer declares, sorted by name and never
// nil. The current YAML is always re-resolved, so a stale caller view cannot
// widen the answer; a CLI failure at either step is returned as is.
func (r *Resolver) ListOrphans(ctx context.Context, ref ComposeRef) ([]ProjectVolume, error) {
	// Condition 3 is judged against every profile enabled: a volume only a
	// profile-gated Service mounts is still declared, and must never be offered
	// for deletion. ref is a copy, so the caller's is not mutated.
	ref.AllProfiles = true
	resolution, err := r.Resolve(ctx, ref)
	if err != nil {
		return nil, err
	}
	listed, err := r.cli.ListProjectVolumes(ctx, ref.ProjectName)
	if err != nil {
		return nil, err
	}
	return orphansOf(ref.ProjectName, listed, resolution.Volumes), nil
}

// RemoveOrphan removes the named volume, but only if it is in a freshly
// derived ListOrphans: the caller's name is never trusted, so a name that is
// current, belongs to another project or is not a volume at all returns
// ErrNotOrphan and runs nothing. ErrVolumeInUse from the CLI passes through.
func (r *Resolver) RemoveOrphan(ctx context.Context, ref ComposeRef, name string) error {
	orphans, err := r.ListOrphans(ctx, ref)
	if err != nil {
		return err
	}
	for _, orphan := range orphans {
		if orphan.Name == name {
			return r.cli.RemoveVolume(ctx, name)
		}
	}
	return ErrNotOrphan
}

// orphansOf is the one orphan rule, deliberately conservative because it
// decides what the panel will delete. A listed volume is an orphan only when
// all of these hold: its project label is this project, its volume label is
// non-empty, its name is exactly <project>_<logical> (so a volume given an
// explicit `name:` is never matched), and it is not the Source of a named
// Volume in the current resolution. The result is name-sorted and never nil.
func orphansOf(project string, listed []ProjectVolume, current []Volume) []ProjectVolume {
	inUse := make(map[string]bool, len(current))
	for _, volume := range current {
		if volume.Kind == VolumeNamed {
			inUse[volume.Source] = true
		}
	}

	orphans := make([]ProjectVolume, 0)
	for _, volume := range listed {
		logical := volume.Labels[ComposeVolumeLabel]
		if volume.Labels[ComposeProjectLabel] != project || logical == "" {
			continue
		}
		if volume.Name != project+"_"+logical || inUse[volume.Name] {
			continue
		}
		orphans = append(orphans, volume)
	}
	sort.Slice(orphans, func(i, j int) bool { return orphans[i].Name < orphans[j].Name })
	return orphans
}
