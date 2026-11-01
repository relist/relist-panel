package panelupdate

import "context"

// UpdateSpec is one `relist-panel update` Container of the current panel image,
// run outside the Install with no Compose labels (spec "The update command").
type UpdateSpec struct {
	// Name is the Container's name for a detached run; a preview is anonymous
	// and removed when it exits.
	Name string
	// Image is the current panel image, by ID.
	Image string
	// Args follow `update`.
	Args []string
	// InstallDir and DataDir are mounted read-write at their own paths, and
	// DataDir is the command's RELIST_DATA_DIR.
	InstallDir string
	DataDir    string
	Network    string
	// Env are extra NAME=value pairs, such as RELIST_UPDATE_MANIFEST_URL.
	Env []string
}

// UpdateRunner starts the update Container; dockerexec implements it.
type UpdateRunner interface {
	// Preflight runs the Container with --rm, waits for it and returns its
	// stdout and exit code. The error is for "could not run it at all".
	Preflight(ctx context.Context, spec UpdateSpec) (stdout []byte, exitCode int, err error)
	// StartUpdater starts the Container detached under spec.Name and returns
	// once it runs.
	StartUpdater(ctx context.Context, spec UpdateSpec) error
	// StopUpdater runs `docker stop` on the named Updater Container.
	StopUpdater(ctx context.Context, name string) error
}
