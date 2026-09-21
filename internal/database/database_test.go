package database

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestOpenAndMigrateCreatesDatabaseAndRunsEmbeddedMigrations(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "nested", "panel.db")
	db, err := OpenAndMigrate(databasePath)
	if err != nil {
		t.Fatalf("OpenAndMigrate() error = %v", err)
	}
	defer db.Close()

	var version int
	if err := db.QueryRow("SELECT version_id FROM goose_db_version WHERE is_applied = 1 ORDER BY id DESC LIMIT 1").Scan(&version); err != nil {
		t.Fatalf("read goose version: %v", err)
	}
	if version != 7 {
		t.Fatalf("migration version = %d, want 7", version)
	}

	var one int
	if err := db.QueryRow("SELECT 1").Scan(&one); err != nil {
		t.Fatalf("query migrated database: %v", err)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	if err := Migrate(db); err != nil {
		t.Fatalf("first Migrate() error = %v", err)
	}
	if err := Migrate(db); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}
}
