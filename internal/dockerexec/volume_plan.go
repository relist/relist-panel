package dockerexec

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"relist-panel/internal/composes"
)

var _ composes.VolumeInspector = Runner{}

// composeProjectLabel is the label Compose puts on every volume it creates.
const composeProjectLabel = "com.docker.compose.project"

// CandidateVolumes resolves the Compose's non-external named volumes from its
// files, as DownWithVolumesTracked does, and looks up each one's owning Compose
// project. Docker failing to answer is an error, never a guess.
func (Runner) CandidateVolumes(ctx context.Context, req composes.RunRequest) ([]composes.VolumeCandidate, error) {
	content, err := run(ctx, req, nil, "config", "--format", "json")
	if err != nil {
		return nil, err
	}
	var config struct {
		Volumes map[string]struct {
			Name     string
			External bool
		}
	}
	if err := json.Unmarshal([]byte(content), &config); err != nil {
		return nil, fmt.Errorf("inspect named volumes: %w", err)
	}
	var candidates []composes.VolumeCandidate
	for logical, volume := range config.Volumes {
		if volume.External {
			continue
		}
		name := volume.Name
		if name == "" {
			name = req.ProjectName + "_" + logical
		}
		project, exists, err := volumeProject(ctx, name)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, composes.VolumeCandidate{Name: name, Exists: exists, Project: project})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Name < candidates[j].Name })
	return candidates, nil
}

// volumeProject returns the volume's com.docker.compose.project label, "" when
// it has none, and whether the volume exists.
func volumeProject(ctx context.Context, name string) (project string, exists bool, err error) {
	cmd := command(ctx, "volume", "inspect", "--format", `{{index .Labels "`+composeProjectLabel+`"}}`, name)
	cmd.WaitDelay = waitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(strings.ToLower(stderr.String()), "no such volume") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("inspect named volume: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	project = strings.TrimSpace(stdout.String())
	if project == "<no value>" {
		project = ""
	}
	return project, true, nil
}

// DownAndRemoveVolumes removes the Compose's containers (and their anonymous
// volumes), runs `down` without -v, then removes each named volume with an
// unforced `docker volume rm`, so a volume still in use by another container
// survives. It returns the names whose removal succeeded, and the failures of
// the rest joined into the error.
func (Runner) DownAndRemoveVolumes(ctx context.Context, req composes.RunRequest, names []string) ([]string, error) {
	if _, err := run(ctx, req, nil, "rm", "--stop", "--force", "--volumes"); err != nil {
		return nil, err
	}
	if _, err := run(ctx, req, nil, "down"); err != nil {
		return nil, err
	}
	var removed []string
	var errs []error
	for _, name := range names {
		cmd := command(ctx, "volume", "rm", name)
		cmd.WaitDelay = waitDelay
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			if strings.Contains(strings.ToLower(stderr.String()), "no such volume") {
				continue
			}
			errs = append(errs, fmt.Errorf("remove volume %s: %w: %s", name, err, strings.TrimSpace(stderr.String())))
			continue
		}
		removed = append(removed, name)
	}
	return removed, errors.Join(errs...)
}
