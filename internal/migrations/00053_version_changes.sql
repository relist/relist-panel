-- +goose Up
-- The volume a Database's data lives on: relist-db-<id> until a major
-- version change moves it to relist-db-<id>-v<change id> (ADR-0047).
ALTER TABLE databases ADD COLUMN volume TEXT NOT NULL DEFAULT '';
UPDATE databases SET volume = 'relist-db-' || id;

-- One row per Change version: its step log, the volume it moved to, and the
-- volume it left behind as previous version data.
CREATE TABLE database_version_changes (
    id INTEGER PRIMARY KEY,
    database_id INTEGER NOT NULL REFERENCES databases(id) ON DELETE CASCADE,
    from_version TEXT NOT NULL,
    to_version TEXT NOT NULL,
    method TEXT NOT NULL,
    status TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT '',
    log TEXT NOT NULL DEFAULT '',
    from_volume TEXT NOT NULL DEFAULT '',
    to_volume TEXT NOT NULL DEFAULT '',
    from_volume_removed_at INTEGER,
    started_at INTEGER NOT NULL,
    finished_at INTEGER
);
CREATE INDEX database_version_changes_database ON database_version_changes(database_id);

-- +goose Down
DROP TABLE database_version_changes;
ALTER TABLE databases DROP COLUMN volume;
