-- +goose Up
-- Backups and restores of panel-managed Databases (.scratch/databases/spec.md,
-- ADR-0039). A Backup is one gzipped file under <data dir>/.databases/<id>/
-- backups/; scope is JSON ({"all":true} or {"databases":["a"]}). A restore is
-- recorded so a failed one has a place for its log. deleted_at marks a
-- Database that was deleted while keeping its Backups (a tombstone).
CREATE TABLE database_backups (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    database_id INTEGER NOT NULL REFERENCES databases(id) ON DELETE CASCADE,
    trigger TEXT NOT NULL,
    scope TEXT NOT NULL,
    status TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT '',
    log TEXT NOT NULL DEFAULT '',
    file_name TEXT NOT NULL DEFAULT '',
    size INTEGER NOT NULL DEFAULT 0,
    sha256 TEXT NOT NULL DEFAULT '',
    started_at INTEGER NOT NULL,
    finished_at INTEGER
);
CREATE INDEX database_backups_database ON database_backups (database_id, started_at);

CREATE TABLE database_restores (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    database_id INTEGER NOT NULL REFERENCES databases(id) ON DELETE CASCADE,
    source TEXT NOT NULL,
    backup_id INTEGER,
    pre_backup_id INTEGER,
    upload_name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT '',
    log TEXT NOT NULL DEFAULT '',
    started_at INTEGER NOT NULL,
    finished_at INTEGER
);
CREATE INDEX database_restores_database ON database_restores (database_id, started_at);

ALTER TABLE databases ADD COLUMN deleted_at INTEGER;

-- +goose Down
ALTER TABLE databases DROP COLUMN deleted_at;
DROP TABLE database_restores;
DROP TABLE database_backups;
