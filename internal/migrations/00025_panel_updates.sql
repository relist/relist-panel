-- +goose Up
-- Panel update (ADR-0024). One settings row for the update check, and one row
-- per update run. Later schema migrations add columns as the run grows.
CREATE TABLE panel_update_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    check_enabled INTEGER NOT NULL DEFAULT 1,
    last_check_at INTEGER,
    last_error TEXT NOT NULL DEFAULT '',
    -- The newest verified Update manifest and its signature, byte for byte, so
    -- they are verified again on read. NULL = nothing newer is known.
    available_manifest BLOB,
    available_sig BLOB
);
INSERT INTO panel_update_settings (id) VALUES (1);

CREATE TABLE panel_updates (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    from_version TEXT NOT NULL,
    to_version TEXT NOT NULL,
    state TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT '',
    started_at INTEGER NOT NULL,
    finished_at INTEGER
);

-- +goose Down
DROP TABLE panel_updates;
DROP TABLE panel_update_settings;
