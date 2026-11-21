package httpapi

import (
	"context"
	"path/filepath"
	"testing"

	"relist-panel/internal/database"
	"relist-panel/internal/databases"
)

// idleHost answers the two reads a live Database's view makes; any other
// call panics, so a tombstone that reached the host would fail the test.
type idleHost struct{ databases.Host }

func (idleHost) Inspect(context.Context, string) (databases.ContainerState, error) {
	return databases.ContainerState{}, nil
}

func (idleHost) VolumeExists(context.Context, string) (bool, error) { return true, nil }

// The Docker page's cleanup must never offer a Database's volume or image, but
// a tombstone has no volume, and its image is free to clean.
func TestOwnDatabasesSkipsTombstones(t *testing.T) {
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := databases.NewStore(db)
	ctx := context.Background()
	live, err := store.Insert(ctx, databases.Database{Name: "Live", Slug: "live", Engine: "postgres", Version: "18", Password: "x"})
	if err != nil {
		t.Fatal(err)
	}
	gone, err := store.Insert(ctx, databases.Database{Name: "Gone", Slug: "gone", Engine: "postgres", Version: "16", Password: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Tombstone(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	svc := databases.New(databases.Deps{Store: store, Host: idleHost{}})
	images, volumes := map[string]string{}, map[string]string{}
	if err := ownDatabases(ctx, svc, images, volumes); err != nil {
		t.Fatal(err)
	}
	if volumes[databases.VolumeName(live.ID)] != databases.OwnerName || images["postgres:18"] != databases.OwnerName {
		t.Fatalf("the live Database is not owned: images %v, volumes %v", images, volumes)
	}
	if _, ok := volumes[databases.VolumeName(gone.ID)]; ok {
		t.Fatalf("a tombstone's volume is owned: %v", volumes)
	}
	if _, ok := images["postgres:16"]; ok {
		t.Fatalf("a tombstone's image is owned: %v", images)
	}
}

// Previous version data stays the Databases' until it is deleted from the
// Database's page; the Docker page must not offer it either.
func TestOwnDatabasesMarksPreviousVolumes(t *testing.T) {
	db, err := database.OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	store := databases.NewStore(db)
	ctx := context.Background()
	d, err := store.Insert(ctx, databases.Database{Name: "Live", Slug: "live", Engine: "postgres", Version: "16", Password: "x"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.InsertVersionChange(ctx, databases.VersionChange{DatabaseID: d.ID, FromVersion: "16", ToVersion: "18", Method: databases.VersionMethodDumpRestore})
	if err != nil {
		t.Fatal(err)
	}
	to := d.Volume + "-v9"
	if err := store.CommitVersionChange(ctx, databases.VersionCommit{ChangeID: c.ID, DatabaseID: d.ID, Version: "18", Volume: to, FromVolume: d.Volume}); err != nil {
		t.Fatal(err)
	}
	svc := databases.New(databases.Deps{Store: store, Host: idleHost{}})
	images, volumes := map[string]string{}, map[string]string{}
	if err := ownDatabases(ctx, svc, images, volumes); err != nil {
		t.Fatal(err)
	}
	if volumes[to] != databases.OwnerName || volumes[d.Volume] != databases.OwnerName {
		t.Fatalf("volumes = %v, want %s and %s owned", volumes, to, d.Volume)
	}
}
