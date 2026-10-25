-- +goose Up
-- A host-wide library of SSH keys. A Git Source picks one instead of owning a Deploy key.
CREATE TABLE ssh_keys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL COLLATE NOCASE UNIQUE,
    private_key TEXT NOT NULL,
    public_key TEXT NOT NULL,
    created_at INTEGER NOT NULL
);

ALTER TABLE compose_sources ADD COLUMN ssh_key_id INTEGER REFERENCES ssh_keys(id) ON DELETE SET NULL;

-- compose_sources.ssh_private_key and ssh_public_key are retired: nothing reads
-- them any more, and dropping them would make SQLite rebuild the table. Their
-- contents are discarded here, and so is the test result that used them.
UPDATE compose_sources
SET ssh_private_key = '', ssh_public_key = '', last_test_at = NULL, last_test_ok = NULL;

-- +goose Down
-- SQLite cannot drop a column that is used in a foreign key, so ssh_key_id
-- stays on compose_sources (cleared) and only the table goes.
UPDATE compose_sources SET ssh_key_id = NULL;
DROP TABLE ssh_keys;
