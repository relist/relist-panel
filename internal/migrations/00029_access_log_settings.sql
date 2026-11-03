-- +goose Up
-- How long the panel keeps rotated Access log files (ADR-0027), and how its
-- last rotation run went. One row.
CREATE TABLE access_log_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    retention_days INTEGER NOT NULL DEFAULT 90 CHECK (retention_days >= 1),
    keep_forever INTEGER NOT NULL DEFAULT 0,
    last_rotated_at INTEGER,
    last_error TEXT NOT NULL DEFAULT ''
);
INSERT INTO access_log_settings (id) VALUES (1);

-- +goose Down
DROP TABLE access_log_settings;
