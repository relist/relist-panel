package panelupdate

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// Compose labels the panel's own Container carries.
const (
	labelProject     = "com.docker.compose.project"
	labelWorkingDir  = "com.docker.compose.project.working_dir"
	labelConfigFiles = "com.docker.compose.project.config_files"
)

// Reasons an Install is unavailable.
const (
	reasonNotCompose  = "this panel was not started with Docker Compose"
	reasonNoInspect   = "this panel cannot inspect its own Container"
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
	// Image is the ID of the image the panel runs from, which the update
	// Container is started from so panel and update command cannot skew.
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

// HelperExitError is a command that ran and exited non-zero.
type HelperExitError struct {
	Code   int
	Stderr string
}

func (e *HelperExitError) Error() string {
	return fmt.Sprintf("helper exited %d: %s", e.Code, e.Stderr)
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
