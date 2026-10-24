package database

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"relist-panel/internal/migrations"
)

func OpenAndMigrate(databasePath string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(databasePath), 0o750); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}

	// foreign_keys is a per-connection setting, so it lives in the DSN: database/sql
	// replaces a connection whose query was interrupted, and a pragma run once
	// would be lost with it.
	db, err := sql.Open("sqlite", "file:"+databasePath+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}

	// SQLite serializes writes. A single connection keeps pragmas and goose's
	// migration transaction behavior predictable for this small application.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping SQLite database: %w", err)
	}

	if err := Migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func Migrate(db *sql.DB) error {
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		return fmt.Errorf("enable SQLite foreign keys: %w", err)
	}
	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("configure goose SQLite dialect: %w", err)
	}
	if err := goose.Up(db, "."); err != nil {
		return fmt.Errorf("run goose migrations: %w", err)
	}
	return checkSchema(db)
}

// ForeignKeyViolations counts the rows that reference a parent that no longer
// exists (PRAGMA foreign_key_check).
func ForeignKeyViolations(ctx context.Context, db *sql.DB) (int, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return 0, fmt.Errorf("check SQLite foreign keys: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("check SQLite foreign keys: %w", err)
	}
	return count, nil
}
