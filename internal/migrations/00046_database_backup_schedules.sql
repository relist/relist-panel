-- +goose Up
-- Phase 3 (.scratch/databases/plan-phase-3.md): a schedule per Database, and
-- the two marks Rotation needs on a Backup. pinned keeps a scheduled Backup
-- from Rotation; local_removed_at is set when Rotation removed the file but a
-- copy in a Backup destination is still tracked (a "Remote only" Backup).
CREATE TABLE database_backup_schedules (
    database_id INTEGER PRIMARY KEY REFERENCES databases(id) ON DELETE CASCADE,
    enabled INTEGER NOT NULL,
    days TEXT NOT NULL,
    minute INTEGER NOT NULL,
    all_databases INTEGER NOT NULL,
    databases TEXT NOT NULL,
    local_keep_count INTEGER,
    local_keep_days INTEGER,
    remote_keep_count INTEGER,
    remote_keep_days INTEGER,
    last_run_at INTEGER,
    last_error TEXT NOT NULL DEFAULT ''
);

ALTER TABLE database_backups ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0;
ALTER TABLE database_backups ADD COLUMN local_removed_at INTEGER;

-- +goose Down
ALTER TABLE database_backups DROP COLUMN local_removed_at;
ALTER TABLE database_backups DROP COLUMN pinned;
DROP TABLE database_backup_schedules;
