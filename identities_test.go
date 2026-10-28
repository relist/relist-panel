package main

import (
	"context"
	"path/filepath"
	"testing"

	"relist-panel/internal/composes"
	"relist-panel/internal/database"
	"relist-panel/internal/vulnscan"
)

func TestComposeIdentitiesMapsDeployProjectToIDs(t *testing.T) {
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.Exec(`INSERT INTO projects (id, name, created_at, updated_at) VALUES (7, 'Shop', 1, 1)`)
	db.Exec(`INSERT INTO environments (id, project_id, name, created_at) VALUES (8, 7, 'prod', 1)`)
	if _, err := db.Exec(`INSERT INTO composes (id, environment_id, name, yaml_content, created_at, updated_at) VALUES (9, 8, 'web', '', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	var c composes.Compose
	if err := db.QueryRow(`SELECT id, project_slug, env_slug, name, identity_version FROM composes WHERE id = 9`).
		Scan(&c.ID, &c.ProjectSlug, &c.EnvSlug, &c.Name, &c.IdentityVersion); err != nil {
		t.Fatal(err)
	}
	got, err := composeIdentities(db)(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := vulnscan.ImageUser{Kind: vulnscan.UserService, ProjectID: 7, EnvironmentID: 8, ComposeID: 9}
	if len(got) != 1 || got[c.DeployProjectName()] != want {
		t.Fatalf("identities = %+v", got)
	}
}
