package database

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"relist-panel/internal/migrations"
)

// SchemaVersion is the highest goose migration applied to db. A Migration
// (ADR-0018) compares it with the receiving panel's KnownVersion.
func SchemaVersion(ctx context.Context, db *sql.DB) (int64, error) {
	var version int64
	err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = 1").Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

// KnownVersion is the highest migration this binary ships, from the leading
// digits of the embedded file names.
func KnownVersion() (int64, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return 0, fmt.Errorf("list migrations: %w", err)
	}
	var highest int64
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".sql") {
			continue
		}
		digits, _, _ := strings.Cut(name, "_")
		version, err := strconv.ParseInt(digits, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("migration %q has no numeric version", name)
		}
		highest = max(highest, version)
	}
	return highest, nil
}
