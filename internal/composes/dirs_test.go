package composes

import (
	"context"
	"path/filepath"
	"testing"
)

func TestComposeDirsCoversBothIdentities(t *testing.T) {
	_, _, _, db := newDeployService(t, "services: {}\n")
	for _, r := range []struct {
		id       int
		name     string
		identity int
	}{{2, "Shop", 1}, {3, "Legacy One", 0}} {
		if _, err := db.Exec(`INSERT INTO composes (id, environment_id, name, project_slug, env_slug, yaml_content, identity_version, created_at, updated_at) VALUES (?, 3, ?, 'p', 'e', '', ?, 0, 0)`, r.id, r.name, r.identity); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ComposeDirs(context.Background(), db, "/data")
	if err != nil {
		t.Fatal(err)
	}
	// The fixture may hold other rows; the two added must be present.
	want := []string{filepath.Join("/data", ".composes", "2"), filepath.Join("/data", "p", "e", "legacy-one")}
	for _, w := range want {
		found := false
		for _, g := range got {
			found = found || g == w
		}
		if !found {
			t.Errorf("dirs = %v, missing %s", got, w)
		}
	}
}
