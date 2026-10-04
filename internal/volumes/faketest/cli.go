package faketest

import (
	"context"

	"relist-panel/internal/volumes"
)

// CLI is a scripted volumes.CLI: HTTP-seam tests install it in place of the
// real Docker CLI adapter, script what resolution and pre-flight answer, and
// assert exactly what was asked — all with no Docker in them.
type CLI struct {
	// ComposeConfigOutput is returned by every ComposeConfig call; when
	// ComposeConfigErr is set, it is returned instead.
	ComposeConfigOutput []byte
	ComposeConfigErr    error
	// ComposeConfigRefs records every ComposeConfig ref, in order.
	ComposeConfigRefs []volumes.ComposeRef

	// VolumeExistsByName answers VolumeExists; a name absent from the map —
	// including the zero-value nil map — answers false.
	VolumeExistsByName map[string]bool
	// VolumeExistsErr is returned by every VolumeExists call in place of an
	// answer, so a failed check is never read as a missing source.
	VolumeExistsErr error
	// VolumeExistsNames records every VolumeExists name, in order.
	VolumeExistsNames []string

	// ProjectVolumes is what every ListProjectVolumes call lists, whatever
	// project it is asked for — the Resolver's filter is what is under test.
	ProjectVolumes []volumes.ProjectVolume
	// ListProjectVolumesErr is returned by every ListProjectVolumes call in
	// place of the list.
	ListProjectVolumesErr error
	// ListedProjects records every ListProjectVolumes project, in order.
	ListedProjects []string

	// RemoveVolumeErr is returned by every RemoveVolume call; a failed removal
	// is not recorded in RemovedVolumes.
	RemoveVolumeErr error
	// RemovedVolumes records every volume RemoveVolume removed, in order.
	RemovedVolumes []string
}

// ComposeConfig implements volumes.CLI.
func (f *CLI) ComposeConfig(_ context.Context, ref volumes.ComposeRef) ([]byte, error) {
	f.ComposeConfigRefs = append(f.ComposeConfigRefs, ref)
	if f.ComposeConfigErr != nil {
		return nil, f.ComposeConfigErr
	}
	return f.ComposeConfigOutput, nil
}

// VolumeExists implements volumes.CLI.
func (f *CLI) VolumeExists(_ context.Context, name string) (bool, error) {
	f.VolumeExistsNames = append(f.VolumeExistsNames, name)
	if f.VolumeExistsErr != nil {
		return false, f.VolumeExistsErr
	}
	return f.VolumeExistsByName[name], nil
}

// ListProjectVolumes implements volumes.CLI.
func (f *CLI) ListProjectVolumes(_ context.Context, project string) ([]volumes.ProjectVolume, error) {
	f.ListedProjects = append(f.ListedProjects, project)
	if f.ListProjectVolumesErr != nil {
		return nil, f.ListProjectVolumesErr
	}
	return f.ProjectVolumes, nil
}

// RemoveVolume implements volumes.CLI.
func (f *CLI) RemoveVolume(_ context.Context, name string) error {
	if f.RemoveVolumeErr != nil {
		return f.RemoveVolumeErr
	}
	f.RemovedVolumes = append(f.RemovedVolumes, name)
	return nil
}

var _ volumes.CLI = (*CLI)(nil)
