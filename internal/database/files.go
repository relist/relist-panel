package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"relist-panel/internal/migrations"
)

var (
	// ErrSchemaAhead means the database was migrated by a newer binary.
	ErrSchemaAhead = errors.New("database schema is newer than this binary")
	// ErrMigrationMismatch means an applied version came from a different file
	// than the one this binary ships under that number.
	ErrMigrationMismatch = errors.New("applied migration differs from this binary's")
)

// migrationFilesMigration creates the migration_files table.
const migrationFilesMigration = "00020_migration_files.sql"

type embeddedMigration struct {
	version int64
	name    string
	sha256  string
}

// checkSchema refuses a database this binary does not match. goose treats a
// database ahead of every embedded migration as up to date, and records only
// version numbers, so it cannot see a version applied from another branch's
// file. Versions applied before migration_files existed are backfilled from the
// embedded files.
func checkSchema(db *sql.DB) error {
	ctx := context.Background()

	schema, err := SchemaVersion(ctx, db)
	if err != nil {
		return err
	}
	known, err := KnownVersion()
	if err != nil {
		return err
	}
	if schema > known {
		return fmt.Errorf("%w: database schema %d is newer than this binary (%d); run the binary that applied it or restore a backup", ErrSchemaAhead, schema, known)
	}

	embedded, err := embeddedMigrations()
	if err != nil {
		return err
	}

	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return err
	}
	// goose ran nothing for the migration that creates migration_files, so its
	// number was already applied from another file.
	var tables int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'migration_files'").Scan(&tables); err != nil {
		return fmt.Errorf("look for migration_files: %w", err)
	}
	if tables == 0 {
		for _, migration := range embedded {
			if migration.name == migrationFilesMigration {
				return fmt.Errorf("%w: migration %d was applied from another file; this binary ships %s; run the binary that applied it or restore a backup", ErrMigrationMismatch, migration.version, migration.name)
			}
		}
		return fmt.Errorf("%w: migration_files is missing and %s is not embedded", ErrMigrationMismatch, migrationFilesMigration)
	}
	recorded, err := recordedFiles(ctx, db)
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("record migration files: %w", err)
	}
	defer tx.Rollback()
	for _, migration := range embedded {
		if !applied[migration.version] {
			continue
		}
		have, ok := recorded[migration.version]
		if !ok {
			if _, err := tx.ExecContext(ctx, "INSERT INTO migration_files (version, name, sha256) VALUES (?, ?, ?)", migration.version, migration.name, migration.sha256); err != nil {
				return fmt.Errorf("record migration %d: %w", migration.version, err)
			}
			continue
		}
		if have.name != migration.name || have.sha256 != migration.sha256 {
			recordedName := have.name
			if have.name == migration.name {
				recordedName += " (different contents)"
			}
			return fmt.Errorf("%w: migration %d was applied from %s but this binary ships %s; run the binary that applied it or restore a backup", ErrMigrationMismatch, migration.version, recordedName, migration.name)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("record migration files: %w", err)
	}
	return nil
}

func embeddedMigrations() ([]embeddedMigration, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	var result []embeddedMigration
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		digits, _, _ := strings.Cut(name, "_")
		version, err := strconv.ParseInt(digits, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("migration %q has no numeric version", name)
		}
		data, err := fs.ReadFile(migrations.FS, name)
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		sum := sha256.Sum256(data)
		result = append(result, embeddedMigration{version: version, name: name, sha256: hex.EncodeToString(sum[:])})
	}
	return result, nil
}

// appliedVersions are the migration files goose has applied; its version 0 row
// marks the table's creation, not a file.
func appliedVersions(ctx context.Context, db *sql.DB) (map[int64]bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT version_id FROM goose_db_version WHERE is_applied = 1 AND version_id > 0")
	if err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	defer rows.Close()
	applied := map[int64]bool{}
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			return nil, fmt.Errorf("read applied migrations: %w", err)
		}
		applied[version] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	return applied, nil
}

func recordedFiles(ctx context.Context, db *sql.DB) (map[int64]embeddedMigration, error) {
	rows, err := db.QueryContext(ctx, "SELECT version, name, sha256 FROM migration_files")
	if err != nil {
		return nil, fmt.Errorf("read migration files: %w", err)
	}
	defer rows.Close()
	recorded := map[int64]embeddedMigration{}
	for rows.Next() {
		var m embeddedMigration
		if err := rows.Scan(&m.version, &m.name, &m.sha256); err != nil {
			return nil, fmt.Errorf("read migration files: %w", err)
		}
		recorded[m.version] = m
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read migration files: %w", err)
	}
	return recorded, nil
}
