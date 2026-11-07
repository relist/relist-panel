-- +goose Up
-- Registry cleanup (.scratch/private-registry/spec.md, PR 3; ADR-0033).
-- Settings live on the one registry_settings row: on/off, how many tags per
-- repository to keep, the days ("daily" or weekdays, comma-separated), the
-- minute of the day in host time, when it last ran, and the run whose GC is
-- in progress (0 when none) so start-up can finish an interrupted one.
-- registry_cleanup_runs keeps the last 10 runs.
ALTER TABLE registry_settings ADD COLUMN cleanup_enabled INTEGER NOT NULL DEFAULT 1;
ALTER TABLE registry_settings ADD COLUMN cleanup_keep INTEGER NOT NULL DEFAULT 10 CHECK (cleanup_keep >= 1);
ALTER TABLE registry_settings ADD COLUMN cleanup_days TEXT NOT NULL DEFAULT 'daily';
ALTER TABLE registry_settings ADD COLUMN cleanup_minute INTEGER NOT NULL DEFAULT 180 CHECK (cleanup_minute BETWEEN 0 AND 1439);
ALTER TABLE registry_settings ADD COLUMN cleanup_last_run_at INTEGER;
ALTER TABLE registry_settings ADD COLUMN gc_running INTEGER NOT NULL DEFAULT 0;

CREATE TABLE registry_cleanup_runs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at INTEGER NOT NULL,
    finished_at INTEGER,
    trigger TEXT NOT NULL,
    deleted TEXT NOT NULL DEFAULT '[]',
    freed_bytes INTEGER NOT NULL DEFAULT 0,
    result TEXT NOT NULL DEFAULT 'running' CHECK (result IN ('running', 'ok', 'failed', 'interrupted')),
    log TEXT NOT NULL DEFAULT ''
);

-- +goose Down
DROP TABLE registry_cleanup_runs;
ALTER TABLE registry_settings DROP COLUMN gc_running;
ALTER TABLE registry_settings DROP COLUMN cleanup_last_run_at;
ALTER TABLE registry_settings DROP COLUMN cleanup_minute;
ALTER TABLE registry_settings DROP COLUMN cleanup_days;
ALTER TABLE registry_settings DROP COLUMN cleanup_keep;
ALTER TABLE registry_settings DROP COLUMN cleanup_enabled;
