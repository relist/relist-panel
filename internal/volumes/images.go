package volumes

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// Images lists the Images ref's Compose names, resolved with every profile
// enabled: images are the `image:` of every Service, sorted and deduplicated,
// and built is the name Compose gives the Image of each Service that has
// `build:` and no `image:` — `<project>-<service>` (ADR-0016). A server
// Migration moves both (ADR-0018).
func (r *Resolver) Images(ctx context.Context, ref ComposeRef) (images, built []string, err error) {
	ref.AllProfiles = true
	output, err := r.cli.ComposeConfig(ctx, ref)
	if err != nil {
		return nil, nil, err
	}
	var config struct {
		Services map[string]struct {
			Image string          `json:"image"`
			Build json.RawMessage `json:"build"`
		} `json:"services"`
	}
	if err := json.Unmarshal(output, &config); err != nil {
		return nil, nil, fmt.Errorf("parse docker compose config output: %w", err)
	}
	seen := map[string]bool{}
	for name, service := range config.Services {
		switch {
		case service.Image != "":
			if !seen[service.Image] {
				seen[service.Image] = true
				images = append(images, service.Image)
			}
		case len(service.Build) > 0 && string(service.Build) != "null":
			built = append(built, ref.ProjectName+"-"+name)
		}
	}
	sort.Strings(images)
	sort.Strings(built)
	return images, built, nil
}
