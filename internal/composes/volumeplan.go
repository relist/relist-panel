package composes

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// VolumeCandidate is one non-external named volume a Compose's down would
// remove. Project is its com.docker.compose.project label, "" when it has none;
// Exists is false when Docker has no such volume yet or any more.
type VolumeCandidate struct {
	Name    string
	Exists  bool
	Project string
}

// VolumeInspector is an optional capability of a ComposeRunner. Without it a
// destructive down stays `down -v`, which removes every volume the files
// declare, including one another Compose owns.
type VolumeInspector interface {
	// CandidateVolumes resolves the Compose's non-external volumes from its files.
	CandidateVolumes(ctx context.Context, req RunRequest) ([]VolumeCandidate, error)
	// DownAndRemoveVolumes removes the Compose's containers and anonymous
	// volumes, runs `down` without -v, then removes each named volume with an
	// unforced `docker volume rm`. It returns the names that were removed.
	DownAndRemoveVolumes(ctx context.Context, req RunRequest, names []string) (removed []string, err error)
}

// KeptVolume is a volume a destructive down leaves alone, and why.
type KeptVolume struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// VolumePlan says which of a Compose's volumes a destructive down removes and
// which it keeps because another Compose owns or declares them.
type VolumePlan struct {
	Remove []string     `json:"remove"`
	Keep   []KeptVolume `json:"keep"`
}

// volumePlan decides, without side effects, which volumes a destructive down of
// c would remove. supported is false when the runner cannot inspect volumes or
// the Compose has no files on disk, so the caller falls back to plain `down -v`.
// together holds the ids of the Composes torn down in the same operation (c
// included): a volume only they declare is not "declared elsewhere".
func (s *Service) volumePlan(ctx context.Context, c Compose, req RunRequest, together []int64) (plan VolumePlan, supported bool, err error) {
	inspector, ok := s.runner.(VolumeInspector)
	if !ok {
		return VolumePlan{}, false, nil
	}
	if _, err := os.Stat(req.ComposeFilePath); errors.Is(err, os.ErrNotExist) {
		return VolumePlan{}, false, nil
	} else if err != nil {
		return VolumePlan{}, false, err
	}
	candidates, err := inspector.CandidateVolumes(ctx, req)
	if err != nil {
		return VolumePlan{}, false, err
	}
	elsewhere, err := s.volumeNamesElsewhere(ctx, append([]int64{c.ID}, together...))
	if err != nil {
		return VolumePlan{}, false, err
	}
	plan = VolumePlan{Remove: []string{}, Keep: []KeptVolume{}}
	for _, candidate := range candidates {
		if !candidate.Exists {
			continue
		}
		switch other, declared := elsewhere[candidate.Name]; {
		case candidate.Project != "" && candidate.Project != req.ProjectName:
			plan.Keep = append(plan.Keep, KeptVolume{candidate.Name, "owned by another Compose project (" + candidate.Project + ")"})
		case declared:
			plan.Keep = append(plan.Keep, KeptVolume{candidate.Name, "declared by Compose " + other})
		default:
			plan.Remove = append(plan.Remove, candidate.Name)
		}
	}
	return plan, true, nil
}

// destroyStack is the destructive down behind Delete, teardown and a
// fresh-volumes deploy. The Compose's files must be on disk. With nothing to
// keep it is today's `down -v`; otherwise it removes only plan.Remove. A
// kept volume is logged server-side. removedAny reports whether a named volume
// was confirmed gone, when the runner can tell. together is as for volumePlan.
func (s *Service) destroyStack(ctx context.Context, c Compose, req RunRequest, together []int64) (plan VolumePlan, removedAny bool, err error) {
	plan, supported, err := s.volumePlan(ctx, c, req, together)
	if err != nil {
		return VolumePlan{}, false, err
	}
	if !supported || len(plan.Keep) == 0 {
		if tracked, ok := s.runner.(interface {
			DownWithVolumesTracked(context.Context, RunRequest) (bool, error)
		}); ok {
			removedAny, err = tracked.DownWithVolumesTracked(ctx, req)
			return plan, removedAny, err
		}
		return plan, false, s.runner.DownWithVolumes(ctx, req)
	}
	for _, kept := range plan.Keep {
		log.Printf("compose %d: kept volume %s: %s", c.ID, kept.Name, kept.Reason)
	}
	removed, err := s.runner.(VolumeInspector).DownAndRemoveVolumes(ctx, req, plan.Remove)
	return plan, len(removed) > 0, err
}

// volumeComposeFields is what the top-level volumes: entries need for the
// kept-volume rule.
type volumeComposeFields struct {
	Volumes map[string]*struct {
		Name     string `yaml:"name"`
		External any    `yaml:"external"`
	} `yaml:"volumes"`
}

// declaredVolume is one non-external top-level volume with a literal name:.
type declaredVolume struct{ Key, Name string }

// declaredVolumes returns each non-external top-level volume that sets a
// literal name:, ordered by its key. A name with interpolation ("$") cannot be
// resolved without the Compose's variables, so it is skipped, as is YAML that
// does not parse.
func declaredVolumes(yamlContent string) []declaredVolume {
	var doc volumeComposeFields
	if err := yaml.Unmarshal([]byte(yamlContent), &doc); err != nil {
		return nil
	}
	var volumes []declaredVolume
	for key, volume := range doc.Volumes {
		if volume == nil || volume.Name == "" || strings.Contains(volume.Name, "$") {
			continue
		}
		switch external := volume.External.(type) {
		case nil:
		case bool:
			if external {
				continue
			}
		default:
			// `external: {name: x}` (old syntax) or a string such as "true".
			if text, isString := external.(string); !isString || strings.EqualFold(text, "true") {
				continue
			}
		}
		volumes = append(volumes, declaredVolume{Key: key, Name: volume.Name})
	}
	sort.Slice(volumes, func(i, j int) bool { return volumes[i].Key < volumes[j].Key })
	return volumes
}

// declaredVolumeNames returns the literal name: of each volume declaredVolumes
// finds, sorted.
func declaredVolumeNames(yamlContent string) []string {
	var names []string
	for _, volume := range declaredVolumes(yamlContent) {
		names = append(names, volume.Name)
	}
	sort.Strings(names)
	return names
}

// volumeNamesElsewhere maps each volume name declared by a Compose outside
// exclude to that Compose's name. Where two declare it the lowest id wins.
func (s *Service) volumeNamesElsewhere(ctx context.Context, exclude []int64) (map[string]string, error) {
	rows, err := s.repository.db.QueryContext(ctx, `SELECT id, name, yaml_content FROM composes ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("list other composes: %w", err)
	}
	defer rows.Close()
	names := make(map[string]string)
	for rows.Next() {
		var id int64
		var name, content string
		if err := rows.Scan(&id, &name, &content); err != nil {
			return nil, fmt.Errorf("list other composes: %w", err)
		}
		if slices.Contains(exclude, id) {
			continue
		}
		for _, volume := range declaredVolumeNames(content) {
			if _, taken := names[volume]; !taken {
				names[volume] = name
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list other composes: %w", err)
	}
	return names, nil
}
