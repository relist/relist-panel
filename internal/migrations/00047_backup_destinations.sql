-- +goose Up
-- Phase 3 (.scratch/databases/plan-phase-3.md, ADR-0040): Backup destinations,
-- a host-wide library like External registries. secret is the destination's
-- secret access key; the API never returns it. database_backup_uploads is one
-- Backup's copy in one destination; only an 'uploaded' row with no deleted_at
-- is a live copy.
CREATE TABLE backup_destinations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    type TEXT NOT NULL,
    config TEXT NOT NULL,
    secret TEXT NOT NULL,
    last_test_at INTEGER,
    last_test_ok INTEGER NOT NULL DEFAULT 0,
    last_test_error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE database_backup_uploads (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    backup_id INTEGER NOT NULL REFERENCES database_backups(id) ON DELETE CASCADE,
    destination_id INTEGER NOT NULL REFERENCES backup_destinations(id) ON DELETE CASCADE,
    object_key TEXT NOT NULL,
    status TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT '',
    uploaded_at INTEGER,
    deleted_at INTEGER
);
CREATE INDEX database_backup_uploads_backup ON database_backup_uploads (backup_id);
CREATE INDEX database_backup_uploads_destination ON database_backup_uploads (destination_id);

ALTER TABLE database_backup_schedules ADD COLUMN destination_id INTEGER REFERENCES backup_destinations(id);

-- +goose Down
ALTER TABLE database_backup_schedules DROP COLUMN destination_id;
DROP TABLE database_backup_uploads;
DROP TABLE backup_destinations;
