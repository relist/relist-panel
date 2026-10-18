package dockerexec

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"relist-panel/internal/composes"
)

// DownWithVolumesTracked confirms actual named-volume removal. The old deployed
// files resolve the candidates; an external volume is never a removal candidate.
// Unknown inspection results never become an assertion that data was deleted.
func (r Runner) DownWithVolumesTracked(ctx context.Context, req composes.RunRequest) (bool, error) {
	content, err := run(ctx, req, nil, "config", "--format", "json")
	if err != nil {
		return false, err
	}
	var config struct {
		Volumes map[string]struct {
			Name     string
			External bool
		}
	}
	if err := json.Unmarshal([]byte(content), &config); err != nil {
		return false, fmt.Errorf("inspect named volumes: %w", err)
	}
	var existing []string
	for logical, volume := range config.Volumes {
		if volume.External {
			continue
		}
		name := volume.Name
		if name == "" {
			name = req.ProjectName + "_" + logical
		}
		present, err := namedVolumeExists(ctx, name)
		if err != nil {
			return false, err
		}
		if present {
			existing = append(existing, name)
		}
	}
	downErr := r.DownWithVolumes(ctx, req)
	inspectCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	removed := false
	var inspectErr error
	for _, name := range existing {
		present, err := namedVolumeExists(inspectCtx, name)
		if err != nil {
			inspectErr = err
			continue
		}
		removed = removed || !present
	}
	if downErr != nil {
		return removed, downErr
	}
	return removed, inspectErr
}

func namedVolumeExists(ctx context.Context, name string) (bool, error) {
	cmd := command(ctx, "volume", "inspect", name)
	cmd.WaitDelay = waitDelay
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if strings.Contains(strings.ToLower(stderr.String()), "no such volume") {
			return false, nil
		}
		return false, fmt.Errorf("inspect named volume: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return true, nil
}
