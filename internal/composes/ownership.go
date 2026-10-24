package composes

import (
	"context"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ownershipFields is what DockerOwnership reads from a Compose's YAML: the
// literal image: of each service and every top-level volume, external or not.
type ownershipFields struct {
	Services map[string]*struct {
		Image string `yaml:"image"`
	} `yaml:"services"`
	Volumes map[string]*struct {
		Name     string `yaml:"name"`
		External any    `yaml:"external"`
	} `yaml:"volumes"`
}

// ownedVolumeNames returns the Docker volume names a Compose's YAML refers to,
// external ones included. A volume without a literal name: is called
// <project>_<key> unless it is external, where the key itself is the name. A
// name with interpolation ("$") cannot be resolved here and is skipped.
func ownedVolumeNames(doc ownershipFields, project string) []string {
	var names []string
	for key, volume := range doc.Volumes {
		var name string
		external := false
		if volume != nil {
			name = volume.Name
			switch value := volume.External.(type) {
			case bool:
				external = value
			case string:
				external = strings.EqualFold(value, "true")
			case map[string]any:
				// Old syntax: external: {name: x}.
				external = true
				if old, ok := value["name"].(string); ok && name == "" {
					name = old
				}
			}
		}
		switch {
		case name == "" && external:
			name = key
		case name == "":
			name = project + "_" + key
		}
		if strings.Contains(name, "$") {
			continue
		}
		names = append(names, name)
	}
	return names
}

// DockerOwnership says which Docker resources the panel's Composes own or name,
// each mapped to the Compose that does: the deploy project name of every
// Compose (the com.docker.compose.project label of what it creates), the volume
// names its YAML refers to (labelled or not, external ones included), and the
// literal image: references. Pruning skips all of them unless the operator
// opts in.
func (s *Service) DockerOwnership(ctx context.Context) (projects, volumes, images map[string]string, err error) {
	rows, err := s.repository.db.QueryContext(ctx, `SELECT id, name, project_slug, env_slug, identity_version, yaml_content FROM composes ORDER BY id`)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("list composes: %w", err)
	}
	defer rows.Close()
	projects, volumes, images = map[string]string{}, map[string]string{}, map[string]string{}
	for rows.Next() {
		var c Compose
		var content string
		if err := rows.Scan(&c.ID, &c.Name, &c.ProjectSlug, &c.EnvSlug, &c.IdentityVersion, &content); err != nil {
			return nil, nil, nil, fmt.Errorf("list composes: %w", err)
		}
		project := c.DeployProjectName()
		if _, taken := projects[project]; !taken {
			projects[project] = c.Name
		}
		var doc ownershipFields
		if yaml.Unmarshal([]byte(content), &doc) != nil {
			continue
		}
		for _, name := range ownedVolumeNames(doc, project) {
			if _, taken := volumes[name]; !taken {
				volumes[name] = c.Name
			}
		}
		for _, service := range doc.Services {
			if service == nil || service.Image == "" || strings.Contains(service.Image, "$") {
				continue
			}
			if _, taken := images[service.Image]; !taken {
				images[service.Image] = c.Name
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("list composes: %w", err)
	}
	return projects, volumes, images, nil
}
