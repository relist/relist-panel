package panelupdate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
)

// Compose labels the panel's own Container carries.
const (
	labelProject     = "com.docker.compose.project"
	labelWorkingDir  = "com.docker.compose.project.working_dir"
	labelConfigFiles = "com.docker.compose.project.config_files"
)

// Reasons an Install is unavailable. Task 4 adds the config-hash one.
const (
	reasonNotCompose  = "this panel was not started with Docker Compose"
	reasonNoInspect   = "this panel cannot inspect its own Container"
	reasonNoHelper    = "this panel cannot start a helper Container"
	reasonOutsideDir  = "an Install file lies outside the Install directory"
	reasonRelativeDir = "the Install's labels name a relative path"
)

// Install is the Compose project on the host that runs the panel, as found
// from its own Container's labels. Paths are host paths; the data dir and the
// helper Container see them at the same path.
type Install struct {
	Project string
	Dir     string
	// ConfigFiles are the -f files in the order Compose merges them: the
	// Install file, then the Install override when there is one.
	ConfigFiles []string
	// Image is the ID of the image the panel runs from, which the helper
	// Container is started from so panel and helper cannot skew.
	Image string
}

// EnvFile is the Install's .env.
func (i Install) EnvFile() string { return filepath.Join(i.Dir, ".env") }

// InstallFiles holds the bytes of an Install's files.
type InstallFiles struct {
	// Config is parallel to Install.ConfigFiles.
	Config [][]byte
	// Env is empty when the Install has no .env.
	Env []byte
}

// Self is what inspecting the panel's own Container returns.
type Self struct {
	ImageID string
	Labels  map[string]string
}

// SelfInspector inspects the panel's own Container (HOSTNAME → the Engine API).
type SelfInspector interface {
	InspectSelf(ctx context.Context) (Self, error)
}

// HelperRun is one command in a short-lived helper Container of the panel's
// image (ADR-0010). Dir is mounted read-only at the same path. Argv[0] is the
// entrypoint.
type HelperRun struct {
	Image string
	Dir   string
	Argv  []string
}

// HelperExitError is a command that ran and exited non-zero.
type HelperExitError struct {
	Code   int
	Stderr string
}

func (e *HelperExitError) Error() string {
	return fmt.Sprintf("helper exited %d: %s", e.Code, e.Stderr)
}

// Helper runs one command in a helper Container and streams its stdout.
// Reading files is built on it; Task 4 runs `docker compose config` the same
// way.
type Helper interface {
	Run(ctx context.Context, run HelperRun, stdout io.Writer) error
}

// Unavailable says why Panel update cannot work on this host.
type Unavailable struct{ Reason string }

func (u *Unavailable) Error() string { return u.Reason }

// FindInstall inspects the panel's own Container and reads the Install from
// its Compose labels. The error is an *Unavailable.
func (s *Service) FindInstall(ctx context.Context) (Install, error) {
	if s.d.Self == nil {
		return Install{}, &Unavailable{reasonNoInspect}
	}
	self, err := s.d.Self.InspectSelf(ctx)
	if err != nil {
		return Install{}, &Unavailable{reasonNoInspect}
	}
	return installFromLabels(self)
}

func installFromLabels(self Self) (Install, error) {
	project := self.Labels[labelProject]
	dir := self.Labels[labelWorkingDir]
	files := splitFiles(self.Labels[labelConfigFiles])
	if project == "" || dir == "" || len(files) == 0 {
		return Install{}, &Unavailable{reasonNotCompose}
	}
	if !filepath.IsAbs(dir) {
		return Install{}, &Unavailable{reasonRelativeDir}
	}
	dir = filepath.Clean(dir)
	for i, f := range files {
		if !filepath.IsAbs(f) {
			return Install{}, &Unavailable{reasonRelativeDir}
		}
		files[i] = filepath.Clean(f)
		// Only the Install directory is mounted into the helper.
		if !strings.HasPrefix(files[i], dir+string(filepath.Separator)) {
			return Install{}, &Unavailable{reasonOutsideDir}
		}
	}
	return Install{Project: project, Dir: dir, ConfigFiles: files, Image: self.ImageID}, nil
}

func splitFiles(list string) []string {
	var out []string
	for _, f := range strings.Split(list, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// missingExit is the exit code readScript uses for "no such file".
const missingExit = 44

// readScript prints $1, or exits missingExit when it is not a regular file.
// The path is an argument, never part of the script.
const readScript = `[ -f "$1" ] || exit 44; exec cat -- "$1"`

// ReadInstallFiles reads the Install's config files and .env through a helper
// Container. A missing config file is an error; a missing .env is empty.
func (s *Service) ReadInstallFiles(ctx context.Context, in Install) (InstallFiles, error) {
	if s.d.Helper == nil {
		return InstallFiles{}, &Unavailable{reasonNoHelper}
	}
	var out InstallFiles
	for _, path := range in.ConfigFiles {
		data, err := s.readFile(ctx, in, path)
		if err != nil {
			return InstallFiles{}, fmt.Errorf("read %s: %w", path, err)
		}
		out.Config = append(out.Config, data)
	}
	env, err := s.readFile(ctx, in, in.EnvFile())
	switch {
	case errors.Is(err, fs.ErrNotExist):
		env = nil
	case err != nil:
		return InstallFiles{}, fmt.Errorf("read %s: %w", in.EnvFile(), err)
	}
	out.Env = env
	return out, nil
}

func (s *Service) readFile(ctx context.Context, in Install, path string) ([]byte, error) {
	var stdout bytes.Buffer
	err := s.d.Helper.Run(ctx, HelperRun{
		Image: in.Image, Dir: in.Dir, Argv: []string{"sh", "-c", readScript, "sh", path},
	}, &stdout)
	var exit *HelperExitError
	if errors.As(err, &exit) && exit.Code == missingExit {
		return nil, fs.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}
