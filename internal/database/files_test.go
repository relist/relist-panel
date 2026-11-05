package database

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"

	"relist-panel/internal/migrations"
)

func TestOpenAndMigrateRefusesASchemaNewerThanTheBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	db, err := OpenAndMigrate(path)
	if err != nil {
		t.Fatal(err)
	}
	known, err := KnownVersion()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)", known+1); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = OpenAndMigrate(path)
	if err == nil {
		db.Close()
		t.Fatal("OpenAndMigrate() accepted a schema newer than the binary")
	}
	if !errors.Is(err, ErrSchemaAhead) {
		t.Fatalf("error = %v, want ErrSchemaAhead", err)
	}
	for _, want := range []string{"35", "34"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name version %s", err, want)
		}
	}
}

func TestOpenAndMigrateRefusesADifferentFileForAnAppliedVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	db, err := OpenAndMigrate(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE migration_files SET name = '00019_other.sql' WHERE version = 19"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = OpenAndMigrate(path)
	if err == nil {
		db.Close()
		t.Fatal("OpenAndMigrate() accepted an applied migration recorded under another file")
	}
	if !errors.Is(err, ErrMigrationMismatch) {
		t.Fatalf("error = %v, want ErrMigrationMismatch", err)
	}
	for _, want := range []string{"00019_other.sql", "00019_admin_password_from_env.sql"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

func TestOpenAndMigrateRefusesADifferentHashForAnAppliedVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.db")
	db, err := OpenAndMigrate(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE migration_files SET sha256 = 'deadbeef' WHERE version = 5"); err != nil {
		t.Fatal(err)
	}
	db.Close()

	db, err = OpenAndMigrate(path)
	if err == nil {
		db.Close()
		t.Fatal("OpenAndMigrate() accepted an applied migration with another checksum")
	}
	if !errors.Is(err, ErrMigrationMismatch) {
		t.Fatalf("error = %v, want ErrMigrationMismatch", err)
	}
}

func TestOpenAndMigrateRecordsEveryAppliedFile(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	want := map[string]string{}
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		data, err := fs.ReadFile(migrations.FS, entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		want[entry.Name()] = hex.EncodeToString(sum[:])
	}

	rows, err := db.Query("SELECT name, sha256 FROM migration_files")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var name, hash string
		if err := rows.Scan(&name, &hash); err != nil {
			t.Fatal(err)
		}
		got[name] = hash
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("migration_files has %d rows, want %d", len(got), len(want))
	}
	for name, hash := range want {
		if got[name] != hash {
			t.Errorf("migration_files[%s] = %q, want %q", name, got[name], hash)
		}
	}
}

func TestUpgradeFrom17Boots(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, ".", 17); err != nil {
		t.Fatalf("migrate to 17: %v", err)
	}
	if _, err := db.Exec("INSERT INTO projects (name, description, created_at, updated_at) VALUES ('kept', '', 1, 1)"); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(db); err != nil {
		t.Fatalf("Migrate() from 17 error = %v", err)
	}
	var name string
	if err := db.QueryRow("SELECT name FROM projects").Scan(&name); err != nil || name != "kept" {
		t.Fatalf("project after upgrade = %q, %v", name, err)
	}
	version, err := SchemaVersion(t.Context(), db)
	if err != nil || version != 34 {
		t.Fatalf("SchemaVersion() = %d, %v, want 34", version, err)
	}
	var recorded int
	if err := db.QueryRow("SELECT COUNT(*) FROM migration_files").Scan(&recorded); err != nil || recorded != 34 {
		t.Fatalf("migration_files rows = %d, %v, want 34 (backfilled)", recorded, err)
	}
}

func TestVacuumCopyBoots(t *testing.T) {
	dir := t.TempDir()
	db, err := OpenAndMigrate(filepath.Join(dir, "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	copyPath := filepath.Join(dir, "copy.db")
	if _, err := db.Exec("VACUUM INTO ?", copyPath); err != nil {
		t.Fatal(err)
	}

	copied, err := OpenAndMigrate(copyPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate(copy) error = %v", err)
	}
	copied.Close()
}

// Another branch's 00020 leaves no migration_files table, and goose skips this
// binary's 00020 because the number is already applied.
func TestOpenAndMigrateRefusesAForeignMigrationWithTheSameNumber(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.UpTo(db, ".", 19); err != nil {
		t.Fatalf("migrate to 19: %v", err)
	}
	if _, err := db.Exec("INSERT INTO goose_db_version (version_id, is_applied) VALUES (20, 1)"); err != nil {
		t.Fatal(err)
	}

	err = Migrate(db)
	if !errors.Is(err, ErrMigrationMismatch) {
		t.Fatalf("Migrate() error = %v, want ErrMigrationMismatch", err)
	}
	if !strings.Contains(err.Error(), "00020_migration_files.sql") {
		t.Errorf("error %q does not name 00020_migration_files.sql", err)
	}
}
