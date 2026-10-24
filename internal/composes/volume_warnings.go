package composes

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// serviceMounts is what the ./src warning reads from each service.
type serviceMounts struct {
	Services map[string]struct {
		Volumes []any `yaml:"volumes"`
	} `yaml:"services"`
}

// volumeWarnings names the two YAML patterns that lose data without a sign:
// a writable bind mount under ./src when the Compose has a Source (each
// Deployment runs from its own snapshot of src/, so what the container writes
// there is not carried over), and a named volume with an explicit name: that
// is not external (deleting the Compose removes it). elsewhere maps volume
// names other Composes declare to the Compose's name. YAML that does not parse
// gets no warnings: resolveCompose reports that.
func volumeWarnings(yamlContent, composeDir string, hasSource bool, elsewhere map[string]string) []string {
	var warnings []string
	if hasSource {
		var doc serviceMounts
		if err := yaml.Unmarshal([]byte(yamlContent), &doc); err != nil {
			return nil
		}
		names := make([]string, 0, len(doc.Services))
		for name := range doc.Services {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			for _, mount := range doc.Services[name].Volumes {
				source, target, readOnly, ok := bindMount(mount)
				if !ok || readOnly || !underSrc(source, composeDir) {
					continue
				}
				warnings = append(warnings, fmt.Sprintf("Service %q mounts %s writable at %s: files the container writes there are not kept between deployments. Use a named volume for data, or mount it :ro.", name, source, target))
			}
		}
	}
	for _, volume := range declaredVolumes(yamlContent) {
		warning := fmt.Sprintf("Volume %q sets name: %s without external: true, so deleting this Compose removes it. Declare it external: true if other stacks use it.", volume.Key, volume.Name)
		if other, declared := elsewhere[volume.Name]; declared {
			warning += fmt.Sprintf(" Compose %q also declares it.", other)
		}
		warnings = append(warnings, warning)
	}
	return warnings
}

// bindMount reads one service volumes: entry, in short or long syntax. ok is
// false for anything that is not a host-path bind: a named volume, a tmpfs or
// an entry it cannot read.
func bindMount(entry any) (source, target string, readOnly, ok bool) {
	switch entry := entry.(type) {
	case string:
		source, rest, found := shortMountParts(entry)
		if !found || !(strings.HasPrefix(source, ".") || filepath.IsAbs(source)) {
			return "", "", false, false
		}
		target, modes, _ := strings.Cut(rest, ":")
		for _, mode := range strings.Split(modes, ",") {
			if mode == "ro" {
				readOnly = true
			}
		}
		return source, target, readOnly, true
	case map[string]any:
		if entry["type"] != "bind" {
			return "", "", false, false
		}
		source, _ = entry["source"].(string)
		target, _ = entry["target"].(string)
		switch value := entry["read_only"].(type) {
		case bool:
			readOnly = value
		case string:
			readOnly = strings.EqualFold(value, "true")
		}
		return source, target, readOnly, source != ""
	}
	return "", "", false, false
}

// underSrc reports whether a bind source is src/ of the Compose or inside it,
// written as ./src, ./src/... or the absolute path under composeDir. A path
// with interpolation cannot be resolved here and is left alone.
func underSrc(source, composeDir string) bool {
	if strings.Contains(source, "$") {
		return false
	}
	path := filepath.Clean(source)
	if filepath.IsAbs(path) {
		relative, err := filepath.Rel(composeDir, path)
		if err != nil {
			return false
		}
		path = relative
	}
	return path == "src" || strings.HasPrefix(path, "src/")
}

// hasSource reports whether the Compose has a Source, so its src/ is the
// extracted tree Deployments snapshot.
func (r *repository) hasSource(ctx context.Context, composeID int64) (bool, error) {
	var found bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM compose_sources WHERE compose_id = ?)`, composeID).Scan(&found)
	return found, err
}
