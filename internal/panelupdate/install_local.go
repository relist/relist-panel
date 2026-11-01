package panelupdate

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ReadLocalFiles reads the Install's config files and .env from the local
// filesystem: the Updater Container mounts the Install directory at its host
// path. A symlink or anything but a regular file is refused, since a symlink's
// target may lie outside the mounted directory. A missing .env is empty; a
// missing config file is an error.
func ReadLocalFiles(in Install) (InstallFiles, error) {
	var out InstallFiles
	for _, path := range in.ConfigFiles {
		data, err := readRegular(path)
		if err != nil {
			return InstallFiles{}, fmt.Errorf("read %s: %w", path, err)
		}
		out.Config = append(out.Config, data)
	}
	env, err := readRegular(in.EnvFile())
	switch {
	case errors.Is(err, fs.ErrNotExist):
		env = nil
	case err != nil:
		return InstallFiles{}, fmt.Errorf("read %s: %w", in.EnvFile(), err)
	}
	out.Env = env
	return out, nil
}

func readRegular(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, &Unavailable{fmt.Sprintf("%s is a symlink or not a regular file; Panel update needs regular files in the Install directory", path)}
	}
	return os.ReadFile(path)
}

// ProjectFinder lists the Containers of a Compose project directory.
type ProjectFinder interface {
	// ContainersInDir returns every Container whose
	// com.docker.compose.project.working_dir label equals dir.
	ContainersInDir(ctx context.Context, dir string) ([]Self, error)
}

// ComposeRunner runs a `docker compose …` argv in this process's environment.
// A non-zero exit is a *HelperExitError.
type ComposeRunner interface {
	Run(ctx context.Context, argv []string) ([]byte, error)
}

// InstallArgs is what the update command's flags say about the Install.
type InstallArgs struct {
	Project, Dir string
	Files        []string
}

var projectNameInvalid = regexp.MustCompile(`[^a-z0-9_-]`)

// ResolveInstall finds the Install an update command works on. Explicit flags
// win; with only a directory, the Compose labels of a Container started from it
// are used; with none running, the project name comes from .env or the
// directory's name, and the files are the Install file plus the override.
func ResolveInstall(ctx context.Context, a InstallArgs, f ProjectFinder) (Install, error) {
	if a.Dir == "" || !filepath.IsAbs(a.Dir) {
		return Install{}, &Unavailable{reasonRelativeDir}
	}
	if a.Project != "" && len(a.Files) > 0 {
		return installFromLabels(Self{Labels: map[string]string{
			labelProject:     a.Project,
			labelWorkingDir:  a.Dir,
			labelConfigFiles: strings.Join(a.Files, ","),
		}})
	}
	dir := filepath.Clean(a.Dir)
	if f != nil {
		selves, err := f.ContainersInDir(ctx, dir)
		if err != nil {
			return Install{}, fmt.Errorf("find the install's containers: %w", err)
		}
		if len(selves) > 0 {
			return installFromLabels(selves[0])
		}
	}
	project := a.Project
	if project == "" {
		env, err := readRegular(filepath.Join(dir, ".env"))
		if err == nil {
			project = parseEnv(env)["COMPOSE_PROJECT_NAME"]
		}
	}
	if project == "" {
		project = projectNameInvalid.ReplaceAllString(strings.ToLower(filepath.Base(dir)), "")
	}
	files := a.Files
	if len(files) == 0 {
		files = []string{filepath.Join(dir, "docker-compose.yml")}
		override := filepath.Join(dir, "docker-compose.override.yml")
		if _, err := os.Lstat(override); err == nil {
			files = append(files, override)
		}
	}
	return installFromLabels(Self{Labels: map[string]string{
		labelProject:     project,
		labelWorkingDir:  dir,
		labelConfigFiles: strings.Join(files, ","),
	}})
}
