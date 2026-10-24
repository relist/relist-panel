package composes

import (
	"context"
	"reflect"
	"testing"
)

func TestDockerOwnership(t *testing.T) {
	svc, _, _, db := newDeployService(t, "services: {}\n")
	rows := []struct {
		id       int
		name     string
		identity int
		yaml     string
	}{
		{2, "shop", 1, `services:
  web:
    image: registry.local/shop:1
  db:
    image: "postgres:16"
  dyn:
    image: ${REGISTRY}/dyn:1
  mixed:
    image: "x-$TAG"
volumes:
  data: {}
  named:
    name: shared-db
  ext:
    external: true
  ext-named:
    external: true
    name: outside
  old-ext:
    external:
      name: older
  interp:
    name: ${PREFIX}-data
`},
		{3, "legacy", 0, "volumes:\n  cache: {}\n"},
	}
	for _, r := range rows {
		if _, err := db.Exec(`INSERT INTO composes (id, environment_id, name, project_slug, env_slug, yaml_content, identity_version, created_at, updated_at) VALUES (?, 3, ?, 'p', 'e', ?, ?, 0, 0)`, r.id, r.name, r.yaml, r.identity); err != nil {
			t.Fatal(err)
		}
	}
	projects, volumes, images, err := svc.DockerOwnership(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if wantProjects := map[string]string{"relist-c1": "c", "relist-c2": "shop", "p-e-legacy": "legacy"}; !reflect.DeepEqual(projects, wantProjects) {
		t.Fatalf("projects = %v, want %v", projects, wantProjects)
	}
	wantVolumes := map[string]string{
		"relist-c2_data": "shop", "shared-db": "shop", "ext": "shop", "outside": "shop", "older": "shop",
		"p-e-legacy_cache": "legacy",
	}
	if !reflect.DeepEqual(volumes, wantVolumes) {
		t.Fatalf("volumes = %v, want %v", volumes, wantVolumes)
	}
	wantImages := map[string]string{"registry.local/shop:1": "shop", "postgres:16": "shop"}
	if !reflect.DeepEqual(images, wantImages) {
		t.Fatalf("images = %v, want %v", images, wantImages)
	}
}
