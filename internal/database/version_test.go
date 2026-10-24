package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestKnownVersionIsHighestEmbeddedMigration(t *testing.T) {
	got, err := KnownVersion()
	if err != nil {
		t.Fatalf("KnownVersion() error = %v", err)
	}
	if got != 20 {
		t.Fatalf("KnownVersion() = %d, want 20", got)
	}
}

func TestSchemaVersionAfterOpen(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := SchemaVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("SchemaVersion() error = %v", err)
	}
	known, err := KnownVersion()
	if err != nil {
		t.Fatal(err)
	}
	if got != known {
		t.Fatalf("SchemaVersion() = %d, want %d", got, known)
	}
}
