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

var _ volumes.CLI = (*CLI)(nil)
