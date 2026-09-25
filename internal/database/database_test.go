package database

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"

	"relist-panel/internal/migrations"
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
	if version != 10 {
		t.Fatalf("migration version = %d, want 10", version)
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

// A Compose that existed before the deployed_at column (migration 00009) must
// read as never deployed afterwards: the column backfills to NULL, not to a
// defaulted timestamp.
func TestDeployTimestampsMigrationBackfillsExistingRowsToNull(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("configure goose SQLite dialect: %v", err)
	}
	if err := goose.UpTo(db, ".", 8); err != nil {
		t.Fatalf("migrate to version 8: %v", err)
	}

	if _, err := db.Exec(`
		INSERT INTO projects (name, description, created_at, updated_at) VALUES ('Legacy', '', 1, 1);
		INSERT INTO environments (project_id, name, created_at) VALUES (1, 'production', 1);
		INSERT INTO composes (environment_id, name, project_slug, env_slug, yaml_content, created_at, updated_at)
			VALUES (1, 'web', 'legacy', 'production', 'services: {}', 1, 1);
	`); err != nil {
		t.Fatalf("seed pre-migration compose row: %v", err)
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("migrate to latest: %v", err)
	}

	var deployedAt sql.NullInt64
	if err := db.QueryRow("SELECT deployed_at FROM composes WHERE id = 1").Scan(&deployedAt); err != nil {
		t.Fatalf("read deployed_at: %v", err)
	}
	if deployedAt.Valid {
		t.Fatalf("deployed_at = %+v, want NULL (never deployed stays never deployed)", deployedAt)
	}

	var version int
	if err := db.QueryRow("SELECT version_id FROM goose_db_version WHERE is_applied = 1 ORDER BY id DESC LIMIT 1").Scan(&version); err != nil {
		t.Fatalf("read goose version: %v", err)
	}
	if version != 9 {
		t.Fatalf("migration version = %d, want 9", version)
	}
}
